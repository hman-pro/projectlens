package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/joho/godotenv/autoload"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/tui/app"
	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/sections/health"
	"github.com/hman-pro/projectlens/internal/tui/sections/pipeline"
	"github.com/hman-pro/projectlens/internal/tui/sections/runs"
	"github.com/hman-pro/projectlens/internal/tui/sections/storage"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfgPath := getEnvOr("CONFIG_PATH", "configs/index.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required (set in .env or config)")
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()

	s := store.NewPG(pool, cfg, cfg.RepoPath)

	secs := []sections.Section{
		health.New(ctx, s),
		pipeline.New(ctx, s),
		storage.New(ctx, s),
		runs.New(ctx, s),
		// config section plugs in here as it lands.
	}

	m := app.New(ctx, secs)
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err = prog.Run()
	return err
}

func getEnvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
