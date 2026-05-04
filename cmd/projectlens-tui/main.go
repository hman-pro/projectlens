package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/joho/godotenv/autoload"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/tui/app"
	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/sections"
	cfgsec "github.com/hman-pro/projectlens/internal/tui/sections/config"
	"github.com/hman-pro/projectlens/internal/tui/sections/health"
	jobssec "github.com/hman-pro/projectlens/internal/tui/sections/jobs"
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
		cfgsec.New(ctx, s),
		jobssec.New(ctx),
	}

	// Resolve projectlens binary for the runner. Empty path is fine —
	// the app shows a toast on action keys when the binary is missing.
	binPath, berr := jobs.ResolveBinary()
	if berr != nil {
		log.Printf("projectlens binary not resolvable: %v", berr)
	}
	target := jobs.RunnerTarget{
		BinaryPath:  binPath,
		ConfigPath:  cfgPath,
		DatabaseURL: cfg.DatabaseURL,
		RepoPath:    cfg.RepoPath,
	}
	runner := jobs.NewRunner(target, nil)
	registry := jobs.DefaultRegistry(cfg)

	m := app.New(ctx, secs).WithJobs(s, runner, registry, target)
	app.InitLogger()
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	// Wire program Send into the runner now that prog exists. Order
	// is fine: no jobs message dispatched until prog.Run() executes.
	runner.SetSend(prog.Send)

	_, err = prog.Run()
	return err
}

func getEnvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
