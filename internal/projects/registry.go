package projects

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Project is a single registry entry.
type Project struct {
	Slug          string `yaml:"slug"`
	StorageSchema string `yaml:"storage_schema"`
	RepoPath      string `yaml:"repo_path"`
	ConfigPath    string `yaml:"config_path,omitempty"`
}

// Registry is the parsed projects.yaml.
type Registry struct {
	DatabaseURL    string    `yaml:"database_url"`
	DefaultProject string    `yaml:"default_project,omitempty"`
	Projects       []Project `yaml:"projects"`
}

// LoadRegistry reads and validates a YAML project registry from path.
func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read registry %s: %w", path, err)
	}
	var reg Registry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse registry %s: %w", path, err)
	}
	if err := reg.Validate(); err != nil {
		return nil, fmt.Errorf("registry %s: %w", path, err)
	}
	return &reg, nil
}

// Validate runs all registry-level invariants.
func (r *Registry) Validate() error {
	if r.DatabaseURL == "" {
		return fmt.Errorf("database_url is required")
	}
	if len(r.Projects) == 0 {
		return fmt.Errorf("at least one project is required")
	}
	slugs := map[string]bool{}
	schemas := map[string]bool{}
	for i, p := range r.Projects {
		if err := ValidateSlug(p.Slug); err != nil {
			return fmt.Errorf("projects[%d]: %w", i, err)
		}
		if err := ValidateStorageSchema(p.StorageSchema); err != nil {
			return fmt.Errorf("projects[%d] (%s): %w", i, p.Slug, err)
		}
		if p.RepoPath == "" {
			return fmt.Errorf("projects[%d] (%s): repo_path is required", i, p.Slug)
		}
		if slugs[p.Slug] {
			return fmt.Errorf("duplicate slug %q", p.Slug)
		}
		if schemas[p.StorageSchema] {
			return fmt.Errorf("duplicate storage_schema %q", p.StorageSchema)
		}
		slugs[p.Slug] = true
		schemas[p.StorageSchema] = true
	}
	if r.DefaultProject != "" {
		if !slugs[r.DefaultProject] {
			return fmt.Errorf("default_project %q is not a configured project", r.DefaultProject)
		}
	}
	return nil
}

// Find returns the project with the given slug.
func (r *Registry) Find(slug string) (Project, error) {
	for _, p := range r.Projects {
		if p.Slug == slug {
			return p, nil
		}
	}
	return Project{}, fmt.Errorf("unknown project %q", slug)
}
