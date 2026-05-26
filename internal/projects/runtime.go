package projects

import (
	"context"
	"fmt"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/storage"
)

// Runtime is everything a CLI command or MCP handler needs to operate on
// one project: the resolved config, the project-scoped DB pool, and the
// identity fields used for logs and surfaces.
type Runtime struct {
	Slug          string
	StorageSchema string
	RepoPath      string
	Config        *config.Config
	DB            *storage.DB
}

// Close releases the project's DB pool.
func (r *Runtime) Close() {
	if r != nil && r.DB != nil {
		r.DB.Close()
	}
}

// LoadProjectConfig loads the optional per-project config file and overlays
// the registry's RepoPath. It also injects the registry's databaseURL.
// Project config supplies indexing/provider settings; identity (repo_path,
// storage_schema) always comes from the registry.
func LoadProjectConfig(p Project, databaseURL string) (*config.Config, error) {
	var cfg *config.Config
	if p.ConfigPath != "" {
		c, err := config.Load(p.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("load project config %s: %w", p.ConfigPath, err)
		}
		cfg = c
	} else {
		cfg = config.NewWithDefaults()
	}
	cfg.RepoPath = p.RepoPath
	cfg.DatabaseURL = databaseURL
	return cfg, nil
}

// Resolve opens a project-scoped pool and returns a ready Runtime. The
// caller MUST call Runtime.Close.
func Resolve(ctx context.Context, reg *Registry, slug string) (*Runtime, error) {
	p, err := reg.Find(slug)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadProjectConfig(p, reg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	db, err := storage.ConnectScoped(ctx, reg.DatabaseURL, p.StorageSchema)
	if err != nil {
		return nil, fmt.Errorf("project %q: %w", slug, err)
	}
	return &Runtime{
		Slug:          p.Slug,
		StorageSchema: p.StorageSchema,
		RepoPath:      p.RepoPath,
		Config:        cfg,
		DB:            db,
	}, nil
}
