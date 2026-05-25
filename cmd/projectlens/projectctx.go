package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/projects"
	"github.com/hman-pro/projectlens/internal/storage"
)

// CmdStorage is the resolved storage handle for one CLI command invocation.
// Exactly one of Runtime (project path) or LegacyDB (public path) is non-nil.
type CmdStorage struct {
	Runtime *projects.Runtime

	LegacyDB   *storage.DB
	LegacyCfg  *config.Config
	LegacyRepo string

	cleanup func()
}

// DB returns the active DB regardless of project/legacy path.
func (c *CmdStorage) DB() *storage.DB {
	if c.Runtime != nil {
		return c.Runtime.DB
	}
	return c.LegacyDB
}

// Config returns the active config regardless of project/legacy path.
func (c *CmdStorage) Config() *config.Config {
	if c.Runtime != nil {
		return c.Runtime.Config
	}
	return c.LegacyCfg
}

// RepoPath returns the active repo path regardless of project/legacy path.
func (c *CmdStorage) RepoPath() string {
	if c.Runtime != nil {
		return c.Runtime.RepoPath
	}
	return c.LegacyRepo
}

// Slug returns the active project slug, or empty when on the legacy path.
func (c *CmdStorage) Slug() string {
	if c.Runtime != nil {
		return c.Runtime.Slug
	}
	return ""
}

// StorageSchema returns the active storage schema. Legacy path returns "public".
func (c *CmdStorage) StorageSchema() string {
	if c.Runtime != nil {
		return c.Runtime.StorageSchema
	}
	return "public"
}

// Close releases resources.
func (c *CmdStorage) Close() {
	if c == nil {
		return
	}
	if c.cleanup != nil {
		c.cleanup()
		return
	}
	if c.Runtime != nil {
		c.Runtime.Close()
		return
	}
	if c.LegacyDB != nil {
		c.LegacyDB.Close()
	}
}

// validateMutex enforces that --project and --repo cannot both be set.
// Returns true when --project was passed (project mode).
func validateMutex(cmd *cobra.Command) (bool, error) {
	slug, _ := cmd.Flags().GetString("project")
	repo, _ := cmd.Flags().GetString("repo")
	if slug != "" && repo != "" {
		return false, fmt.Errorf("--project and --repo are mutually exclusive; remove one")
	}
	return slug != "", nil
}

// openCmdStorage is the SINGLE entry point every storage-opening CLI command
// MUST use to obtain a DB handle. It enforces the --project/--repo mutex,
// resolves project runtime via the registry, or falls back to the legacy
// single-project path.
func openCmdStorage(ctx context.Context, cmd *cobra.Command) (*CmdStorage, error) {
	projectMode, err := validateMutex(cmd)
	if err != nil {
		return nil, err
	}
	if projectMode {
		rt, err := resolveProjectRuntime(ctx, cmd)
		if err != nil {
			return nil, err
		}
		return &CmdStorage{Runtime: rt}, nil
	}
	cfg, repoPath, err := loadCmdConfig(cmd)
	if err != nil {
		return nil, err
	}
	db, err := storage.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	return &CmdStorage{LegacyDB: db, LegacyCfg: cfg, LegacyRepo: repoPath}, nil
}

// resolveProjectRuntime opens a scoped runtime for the --project. Callers
// must route through openCmdStorage or migrateProjectSchemaFromFlags so
// validateMutex runs first.
func resolveProjectRuntime(ctx context.Context, cmd *cobra.Command) (*projects.Runtime, error) {
	slug, _ := cmd.Flags().GetString("project")
	if slug == "" {
		return nil, fmt.Errorf("internal: resolveProjectRuntime called without --project")
	}
	regPath, _ := cmd.Flags().GetString("projects")
	reg, err := projects.LoadRegistry(regPath)
	if err != nil {
		return nil, err
	}
	if dbURL, _ := cmd.Flags().GetString("db"); dbURL != "" {
		reg.DatabaseURL = dbURL
	}
	return projects.Resolve(ctx, reg, slug)
}

// migrateProjectSchemaFromFlags runs MigrateInSchema for --project. Callers
// must run validateMutex first; this function trusts that gate.
func migrateProjectSchemaFromFlags(ctx context.Context, cmd *cobra.Command) error {
	if _, err := validateMutex(cmd); err != nil {
		return err
	}
	regPath, _ := cmd.Flags().GetString("projects")
	slug, _ := cmd.Flags().GetString("project")
	reg, err := projects.LoadRegistry(regPath)
	if err != nil {
		return err
	}
	if dbURL, _ := cmd.Flags().GetString("db"); dbURL != "" {
		reg.DatabaseURL = dbURL
	}
	p, err := reg.Find(slug)
	if err != nil {
		return err
	}
	root, err := storage.Connect(ctx, reg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer root.Close()
	return root.MigrateInSchema(ctx, findMigrationsDir(), p.StorageSchema)
}
