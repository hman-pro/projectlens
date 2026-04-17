package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the top-level application configuration.
type Config struct {
	RepoPath      string              `yaml:"repo_path"`
	DatabaseURL   string              `yaml:"database_url"`
	OpenAIKey     string              `yaml:"openai_api_key"`
	Index         IndexConfig         `yaml:"index"`
	Embeddings    EmbeddingsConfig    `yaml:"embeddings"`
	Summarization SummarizationConfig `yaml:"summarization"`
	Datastore     DatastoreConfig     `yaml:"datastore"`
}

// DatastoreConfig controls datastore indexing: migration discovery and SQL scanning.
type DatastoreConfig struct {
	Engines      []DatastoreEngine `yaml:"engines"`
	SQLScanPaths []string          `yaml:"sql_scan_paths"`
}

// DatastoreEngine defines migration paths for a database engine.
type DatastoreEngine struct {
	Name           string   `yaml:"name"`
	MigrationPaths []string `yaml:"migration_paths"`
}

// EmbeddingsConfig controls which provider and model are used for generating
// vector embeddings during indexing.
type EmbeddingsConfig struct {
	Provider   string `yaml:"provider"`   // "ollama" or "openai"
	Model      string `yaml:"model"`      // e.g., "mxbai-embed-large"
	Dimensions int    `yaml:"dimensions"` // e.g., 1024
	Endpoint   string `yaml:"endpoint"`   // for ollama, e.g., "http://localhost:11434"
}

// SummarizationConfig controls which provider and model are used for
// generating package summaries during indexing.
type SummarizationConfig struct {
	Provider string `yaml:"provider"` // "anthropic" or "openai"
	Model    string `yaml:"model"`    // e.g., "claude-sonnet-4-6"
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
	if v := os.Getenv("OLLAMA_ENDPOINT"); v != "" {
		cfg.Embeddings.Endpoint = v
	}

	// Set defaults for new config sections.
	if cfg.Embeddings.Provider == "" {
		cfg.Embeddings.Provider = "ollama"
		cfg.Embeddings.Model = "mxbai-embed-large"
		cfg.Embeddings.Dimensions = 1024
		cfg.Embeddings.Endpoint = "http://localhost:11434"
	}
	if cfg.Summarization.Provider == "" {
		cfg.Summarization.Provider = "anthropic"
		cfg.Summarization.Model = "claude-sonnet-4-6"
	}

	return &cfg, nil
}
