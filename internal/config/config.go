package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the top-level application configuration.
type Config struct {
	RepoPath    string      `yaml:"repo_path"`
	DatabaseURL string      `yaml:"database_url"`
	OpenAIKey   string      `yaml:"openai_api_key"`
	Index       IndexConfig `yaml:"index"`
}

// IndexConfig holds settings that control which files are indexed.
type IndexConfig struct {
	IncludePatterns  []string `yaml:"include_patterns"`
	ExcludePatterns  []string `yaml:"exclude_patterns"`
	GeneratedMarkers []string `yaml:"generated_markers"`
}

// Load reads a YAML config file from path and returns a Config.
// Environment variables DATABASE_URL, OPENAI_API_KEY, and REPO_PATH
// override the corresponding fields when set.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Environment variable overrides.
	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		cfg.OpenAIKey = v
	}
	if v := os.Getenv("REPO_PATH"); v != "" {
		cfg.RepoPath = v
	}

	return &cfg, nil
}
