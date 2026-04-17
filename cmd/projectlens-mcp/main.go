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
	"github.com/hman-pro/projectlens/internal/providers/ollama"
	"github.com/hman-pro/projectlens/internal/providers/openai"
	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
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
			embedder = openai.NewClient(cfg.OpenAIKey)
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
	srv := mcpserver.New(db, router, port)
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
