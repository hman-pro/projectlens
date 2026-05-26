package projects

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectConfigOverlay(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "p.yaml")
	if err := writeFile(cfgPath, `
database_url: ignored-by-overlay
repo_path: should-be-overridden
embeddings:
  provider: ollama
  model: mxbai-embed-large
`); err != nil {
		t.Fatal(err)
	}

	p := Project{
		Slug:          "demo",
		StorageSchema: "demo",
		RepoPath:      "/canonical/path",
		ConfigPath:    cfgPath,
	}
	cfg, err := LoadProjectConfig(p, "ignored-db-url")
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	if cfg.RepoPath != "/canonical/path" {
		t.Errorf("RepoPath overlay: got %q want /canonical/path", cfg.RepoPath)
	}
	if cfg.Embeddings.Provider != "ollama" {
		t.Errorf("Embeddings.Provider: got %q", cfg.Embeddings.Provider)
	}
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}

// TestLoadProjectConfigFilelessAppliesDefaults guards the regression
// where a registry entry without config_path produced a zero-valued
// Config (empty embeddings provider) and broke index-* commands.
func TestLoadProjectConfigFilelessAppliesDefaults(t *testing.T) {
	t.Setenv("OLLAMA_ENDPOINT", "")
	p := Project{
		Slug:          "noconfig",
		StorageSchema: "noconfig",
		RepoPath:      "/repo",
	}
	cfg, err := LoadProjectConfig(p, "db-url")
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	if cfg.Embeddings.Provider != "ollama" {
		t.Errorf("Embeddings.Provider: got %q want ollama", cfg.Embeddings.Provider)
	}
	if cfg.Embeddings.Endpoint != "http://localhost:11434" {
		t.Errorf("Embeddings.Endpoint default: got %q", cfg.Embeddings.Endpoint)
	}
	if cfg.Summarization.Provider != "anthropic" {
		t.Errorf("Summarization.Provider: got %q want anthropic", cfg.Summarization.Provider)
	}
	if cfg.RepoPath != "/repo" {
		t.Errorf("RepoPath: got %q want /repo", cfg.RepoPath)
	}
	if cfg.DatabaseURL != "db-url" {
		t.Errorf("DatabaseURL: got %q want db-url", cfg.DatabaseURL)
	}
}

// TestLoadProjectConfigFilelessPreservesOllamaEndpoint pins the env-vs-
// defaults ordering: when no config file exists, OLLAMA_ENDPOINT must
// still win over the built-in default. Regression for the earlier order
// bug where the default block clobbered the env override.
func TestLoadProjectConfigFilelessPreservesOllamaEndpoint(t *testing.T) {
	t.Setenv("OLLAMA_ENDPOINT", "http://ollama.internal:11434")
	p := Project{
		Slug:          "noconfig-env",
		StorageSchema: "noconfig_env",
		RepoPath:      "/repo",
	}
	cfg, err := LoadProjectConfig(p, "db-url")
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	if cfg.Embeddings.Endpoint != "http://ollama.internal:11434" {
		t.Errorf("Embeddings.Endpoint: got %q want OLLAMA_ENDPOINT value", cfg.Embeddings.Endpoint)
	}
}
