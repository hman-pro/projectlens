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
	History       HistoryConfig       `yaml:"history"`
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

// HistoryConfig controls git history indexing parameters.
type HistoryConfig struct {
	WindowMonths         int `yaml:"window_months"`
	MinCommitsPerFile    int `yaml:"min_commits_per_file"`
	CouplingMinCoChanges int `yaml:"coupling_min_cochanges"`
	CouplingMaxFiles     int `yaml:"coupling_exclude_max_files"`
}

// EmbeddingsConfig controls which provider and model are used for generating
// vector embeddings during indexing.
type EmbeddingsConfig struct {
	Provider   string `yaml:"provider"`   // "ollama" or "openai"
	Model      string `yaml:"model"`      // e.g., "mxbai-embed-large"
	Dimensions int    `yaml:"dimensions"` // e.g., 1024
	Endpoint   string `yaml:"endpoint"`   // for ollama, e.g., "http://localhost:11434"
}

// SummarizationConfig controls package summarization.
// Disabled by default for public quick start.
type SummarizationConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Provider string `yaml:"provider"` // "ollama" only in public alpha
	Model    string `yaml:"model"`    // e.g. "qwen3-coder:30b"
	Endpoint string `yaml:"endpoint"` // e.g. "http://localhost:11434"
}

// IndexConfig holds settings that control which files are indexed.
type IndexConfig struct {
	IncludePatterns  []string `yaml:"include_patterns"`
	ExcludePatterns  []string `yaml:"exclude_patterns"`
	GeneratedMarkers []string `yaml:"generated_markers"`
}

// Load reads a YAML config file from path and returns a Config.
// Environment variables DATABASE_URL and REPO_PATH override the
// corresponding fields when set.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	applyEnvAndDefaults(&cfg)
	return &cfg, nil
}

// NewWithDefaults returns a Config populated only from environment
// variables and built-in defaults. Used when a project registry entry
// omits config_path — the file-less path must still yield a runnable
// provider configuration.
func NewWithDefaults() *Config {
	cfg := &Config{}
	applyEnvAndDefaults(cfg)
	return cfg
}

func applyEnvAndDefaults(cfg *Config) {
	// Defaults first — env overrides win last so OLLAMA_ENDPOINT is not
	// clobbered by the embeddings-provider default block.
	if cfg.Embeddings.Provider == "" {
		cfg.Embeddings.Provider = "ollama"
		cfg.Embeddings.Model = "qwen3-embedding:0.6b"
		cfg.Embeddings.Dimensions = 1024
	}
	if cfg.Embeddings.Endpoint == "" {
		cfg.Embeddings.Endpoint = "http://localhost:11434"
	}
	// Summarization has no default provider; users opt in by editing
	// the config file (e.g. ollama + qwen3-coder:30b).

	if cfg.History.WindowMonths == 0 {
		cfg.History.WindowMonths = 12
	}
	if cfg.History.MinCommitsPerFile == 0 {
		cfg.History.MinCommitsPerFile = 5
	}
	if cfg.History.CouplingMinCoChanges == 0 {
		cfg.History.CouplingMinCoChanges = 5
	}
	if cfg.History.CouplingMaxFiles == 0 {
		cfg.History.CouplingMaxFiles = 20
	}

	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
	}
	if v := os.Getenv("REPO_PATH"); v != "" {
		cfg.RepoPath = v
	}
	if v := os.Getenv("OLLAMA_ENDPOINT"); v != "" {
		cfg.Embeddings.Endpoint = v
	}
}
