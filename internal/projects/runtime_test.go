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
