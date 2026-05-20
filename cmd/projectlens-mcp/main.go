package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/mcpserver"
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

	// Load config from file, with env var overrides.
	cfgPath := envOr("CONFIG_PATH", "configs/index.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if cfg.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required (set via env or config)")
	}

	// Connect to database.
	db, err := storage.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		return fmt.Errorf("pinging database: %w", err)
	}
	log.Println("database connection established")

	// Create embedder for query-time semantic search based on config.
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

	// Determine port.
	port := 8484
	if v := os.Getenv("MCP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			port = p
		}
	}

	// Create and start MCP server.
	srv := mcpserver.New(db, router, port, cfg.RepoPath).
		WithSummarizer(newSummarizerProber(cfg))
	return srv.Start(ctx)
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
