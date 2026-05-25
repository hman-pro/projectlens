package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/mcpserver"
	"github.com/hman-pro/projectlens/internal/projects"
	"github.com/hman-pro/projectlens/internal/providers/anthropic"
	"github.com/hman-pro/projectlens/internal/providers/ollama"
	"github.com/hman-pro/projectlens/internal/providers/openai"
	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
	_ "github.com/joho/godotenv/autoload"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	projectsPath := envOr("PROJECTS_PATH", "configs/projects.yaml")
	port := portFromEnv()

	if _, err := os.Stat(projectsPath); err == nil {
		return runMultiProject(ctx, projectsPath, port)
	}
	return runLegacySingle(ctx, port)
}

// runMultiProject loads a projects registry and mounts each project's MCP
// handler under /{slug}/mcp on a single HTTP listener. Resolve failures for
// an individual project are logged and skipped so the rest of the registry
// can still come up.
func runMultiProject(ctx context.Context, projectsPath string, port int) error {
	reg, err := projects.LoadRegistry(projectsPath)
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}

	mux := http.NewServeMux()
	cleanups := []func(){}

	for _, p := range reg.Projects {
		rt, err := projects.Resolve(ctx, reg, p.Slug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: project %q not ready: %v\n", p.Slug, err)
			continue
		}
		cleanups = append(cleanups, rt.Close)

		srv := buildProjectServer(rt, port)
		mount := "/" + p.Slug + "/mcp"
		handler := srv.Handler()
		mux.Handle(mount, http.StripPrefix(mount, handler))
		mux.Handle(mount+"/", http.StripPrefix(mount, handler))
		log.Printf("mounted %s -> storage_schema=%s repo=%s", mount, p.StorageSchema, p.RepoPath)
	}

	defer func() {
		for _, c := range cleanups {
			c()
		}
	}()

	addr := fmt.Sprintf(":%d", port)
	log.Printf("projectlens MCP server listening on %s", addr)
	httpSrv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		log.Println("shutting down MCP server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// buildProjectServer constructs a per-project *mcpserver.Server from a
// resolved Runtime. The port is shared across mounts (used only for log
// messages and tool metadata).
func buildProjectServer(rt *projects.Runtime, port int) *mcpserver.Server {
	cfg := rt.Config
	var embedder retrieval.QueryEmbedder
	switch cfg.Embeddings.Provider {
	case "ollama":
		embedder = ollama.NewClient(cfg.Embeddings.Endpoint, cfg.Embeddings.Model)
	case "openai":
		if cfg.OpenAIKey != "" {
			if cfg.Embeddings.Dimensions > 0 {
				embedder = openai.NewClientWithDims(cfg.OpenAIKey, cfg.Embeddings.Dimensions)
			} else {
				embedder = openai.NewClient(cfg.OpenAIKey)
			}
		}
	}
	router := retrieval.NewRouter(rt.DB, embedder)
	return mcpserver.New(rt.DB, router, port, rt.RepoPath).
		WithSummarizer(newSummarizerProber(cfg))
}

// runLegacySingle preserves the original single-project behavior used when
// configs/projects.yaml is absent. It loads configs/index.yaml (or
// CONFIG_PATH) and serves one MCP endpoint at /mcp on the configured port.
func runLegacySingle(ctx context.Context, port int) error {
	cfgPath := envOr("CONFIG_PATH", "configs/index.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if cfg.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required (set via env or config)")
	}

	db, err := storage.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		return fmt.Errorf("pinging database: %w", err)
	}
	log.Println("database connection established")

	var embedder retrieval.QueryEmbedder
	switch cfg.Embeddings.Provider {
	case "ollama":
		embedder = ollama.NewClient(cfg.Embeddings.Endpoint, cfg.Embeddings.Model)
	case "openai":
		if cfg.OpenAIKey != "" {
			if cfg.Embeddings.Dimensions > 0 {
				embedder = openai.NewClientWithDims(cfg.OpenAIKey, cfg.Embeddings.Dimensions)
			} else {
				embedder = openai.NewClient(cfg.OpenAIKey)
			}
		}
	}
	router := retrieval.NewRouter(db, embedder)
	srv := mcpserver.New(db, router, port, cfg.RepoPath).
		WithSummarizer(newSummarizerProber(cfg))
	return srv.Start(ctx)
}

// portFromEnv returns the MCP listen port, honoring MCP_PORT when set.
func portFromEnv() int {
	port := 8484
	if v := os.Getenv("MCP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			port = p
		}
	}
	return port
}

// envOr returns the value of the environment variable named by key, or
// defaultVal if the variable is not set.
func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// summarizerProberFunc adapts the configured summarization provider to
// the mcpserver.SummarizerProber interface. configured is mandatory;
// ping is optional. When ping is nil and configured returns true the
// prober reports "configured" (credentials present, no probe run) —
// suitable for providers where probing costs tokens (e.g. Anthropic).
type summarizerProberFunc struct {
	name       string
	configured func() bool
	ping       func(ctx context.Context) error
}

func (f summarizerProberFunc) ProbeSummarizer(ctx context.Context) (string, string, error) {
	if !f.configured() {
		return f.name, "not_configured", nil
	}
	if f.ping == nil {
		// Credentials present but no probe available.
		return f.name, "configured", nil
	}
	if err := f.ping(ctx); err != nil {
		return f.name, "error", err
	}
	return f.name, "reachable", nil
}

// newSummarizerProber returns an mcpserver.SummarizerProber backed by
// the provider named in cfg.Summarization.Provider. Returns nil when
// no provider is configured.
func newSummarizerProber(cfg *config.Config) mcpserver.SummarizerProber {
	switch cfg.Summarization.Provider {
	case "anthropic":
		client := anthropic.NewClient(cfg.Summarization.Model)
		return summarizerProberFunc{
			name:       "anthropic",
			configured: client.Configured,
		}
	case "openai":
		if cfg.OpenAIKey == "" {
			// Provider is known but unkeyed: surface "openai/not_configured"
			// instead of dropping to the default (nil prober) so index_status
			// can distinguish "provider chosen but key missing" from "no
			// summarization provider configured at all".
			return summarizerProberFunc{
				name:       "openai",
				configured: func() bool { return false },
			}
		}
		client := openai.NewClient(cfg.OpenAIKey)
		return summarizerProberFunc{
			name:       "openai",
			configured: func() bool { return true },
			ping:       client.Ping,
		}
	default:
		return nil
	}
}
