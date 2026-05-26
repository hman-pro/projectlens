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
	"github.com/hman-pro/projectlens/internal/projects"
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
	projectsPath := getEnvOr("PROJECTS_PATH", "configs/projects.yaml")
	slugEnv := os.Getenv("PROJECT")

	var (
		pool        *pgxpool.Pool
		cfg         *config.Config
		repoPath    string
		dbURL       string
		projectSlug string
		regPathUsed string
		runtimeDone = func() {}
	)

	if _, err := os.Stat(projectsPath); err == nil {
		reg, rerr := projects.LoadRegistry(projectsPath)
		if rerr != nil {
			return fmt.Errorf("load registry: %w", rerr)
		}
		slug, serr := pickActiveProject(reg, slugEnv)
		if serr != nil {
			return serr
		}
		rt, rerr := projects.Resolve(ctx, reg, slug)
		if rerr != nil {
			return fmt.Errorf("resolve project %q: %w", slug, rerr)
		}
		runtimeDone = rt.Close
		pool = rt.DB.Pool
		cfg = rt.Config
		repoPath = rt.RepoPath
		dbURL = reg.DatabaseURL
		projectSlug = rt.Slug
		regPathUsed = projectsPath
	} else {
		legacyCfg, lerr := config.Load(cfgPath)
		if lerr != nil {
			return fmt.Errorf("load config: %w", lerr)
		}
		if legacyCfg.DatabaseURL == "" {
			return fmt.Errorf("DATABASE_URL is required (set in .env or config)")
		}
		p, perr := pgxpool.New(ctx, legacyCfg.DatabaseURL)
		if perr != nil {
			return fmt.Errorf("connect db: %w", perr)
		}
		runtimeDone = p.Close
		pool = p
		cfg = legacyCfg
		repoPath = legacyCfg.RepoPath
		dbURL = legacyCfg.DatabaseURL
	}
	defer runtimeDone()

	s := store.NewPG(pool, cfg, repoPath)

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
		BinaryPath:   binPath,
		ConfigPath:   cfgPath,
		DatabaseURL:  dbURL,
		RepoPath:     repoPath,
		ProjectSlug:  projectSlug,
		ProjectsPath: regPathUsed,
	}
	runner := jobs.NewRunner(target, nil)
	registry := jobs.DefaultRegistry(cfg)

	m := app.New(ctx, secs).WithJobs(s, runner, registry, target)
	app.InitLogger()
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	// Wire program Send into the runner now that prog exists. Order
	// is fine: no jobs message dispatched until prog.Run() executes.
	runner.SetSend(prog.Send)

	_, err := prog.Run()
	return err
}

func pickActiveProject(reg *projects.Registry, explicit string) (string, error) {
	if explicit != "" {
		if _, err := reg.Find(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	if reg.DefaultProject != "" {
		return reg.DefaultProject, nil
	}
	if len(reg.Projects) == 1 {
		return reg.Projects[0].Slug, nil
	}
	return "", fmt.Errorf("multiple projects configured; set PROJECT or set default_project in projects.yaml")
}

func getEnvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
