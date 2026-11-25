//go:build smoke

package smoketest

import (
	"os/exec"
	"strings"
	"testing"
)

func TestFixtureRepo_ThreeCommits(t *testing.T) {
	dir := buildFixtureRepo(t)
	count, err := exec.Command("git", "-C", dir, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if got := strings.TrimSpace(string(count)); got != "3" {
		t.Fatalf("commit count = %q, want 3", got)
	}
	status, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if s := strings.TrimSpace(string(status)); s != "" {
		t.Fatalf("work tree not clean: %q", s)
	}
}

func TestSmoke_IndexesFixture(t *testing.T) {
	ollama := NewFakeOllama(1024)
	defer ollama.Close()
	db := freshDatabase(t)
	repo := buildFixtureRepo(t)
	cfg := writeConfig(t, ollama.URL, repo)
	env := subprocEnv()

	runLens(t, env, "migrate", "--config", cfg, "--db", db)
	out := runLens(t, env, "index-all", "--config", cfg, "--db", db, "--repo", repo)
	for _, bad := range []string{"embedding failed", "datastore indexing failed", "history indexing failed"} {
		if strings.Contains(out, bad) {
			t.Errorf("index-all output contains %q:\n%s", bad, out)
		}
	}
}
