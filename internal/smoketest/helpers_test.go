//go:build smoke

package smoketest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	"github.com/hman-pro/projectlens/internal/agentsetup"
)

// repoRoot returns the directory containing go.mod, walking up from the cwd.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// binPath returns <repoRoot>/bin/<name>, failing if it has not been built.
func binPath(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(repoRoot(t), "bin", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("%s not found: run make build first", p)
	}
	return p
}

// adminURL returns the connection URL used to create throwaway databases.
func adminURL(t *testing.T) string {
	t.Helper()
	for _, k := range []string{"PROJECTLENS_SMOKE_DATABASE_URL", "PROJECTLENS_DATABASE_URL"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	t.Fatalf("set PROJECTLENS_DATABASE_URL (e.g. make docker-up)")
	return ""
}

// freshDatabase creates smoke_<hex>, drops it on cleanup, and returns its URL.
func freshDatabase(t *testing.T) string {
	t.Helper()
	admin := adminURL(t)
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("random: %v", err)
	}
	name := "smoke_" + hex.EncodeToString(b[:])
	quoted := pgx.Identifier{name}.Sanitize()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect admin database: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			t.Errorf("cleanup connect: %v", err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quoted+" WITH (FORCE)"); err != nil {
			t.Errorf("drop database %s: %v", name, err)
		}
	})

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("parse admin url: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// subprocEnv is the environment for child processes: the parent env without
// PROJECTLENS_* and repo-local git vars, plus extra.
func subprocEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PROJECTLENS_") {
			continue
		}
		env = append(env, kv)
	}
	env = agentsetup.ScrubGitEnv(env)
	return append(env, extra...)
}

func git(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// buildFixtureRepo copies testdata/smoke-repo into a temp git repo with 3 commits.
func buildFixtureRepo(t *testing.T) string {
	t.Helper()
	src := filepath.Join(repoRoot(t), "testdata", "smoke-repo")
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture: %v", err)
	}

	env := subprocEnv(
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=smoke", "GIT_AUTHOR_EMAIL=smoke@example.com",
		"GIT_COMMITTER_NAME=smoke", "GIT_COMMITTER_EMAIL=smoke@example.com",
	)
	git(t, dst, env, "init", "-b", "main")
	git(t, dst, env, "add", ".")
	git(t, dst, env, "commit", "-m", "add widget store")
	for _, rev := range []string{"rev 2", "rev 3"} {
		for _, f := range []string{"widget/widget.go", "main.go"} {
			fh, err := os.OpenFile(filepath.Join(dst, f), os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatalf("open %s: %v", f, err)
			}
			if _, err := fmt.Fprintf(fh, "// %s\n", rev); err != nil {
				fh.Close()
				t.Fatalf("append %s: %v", f, err)
			}
			if err := fh.Close(); err != nil {
				t.Fatalf("close %s: %v", f, err)
			}
		}
		git(t, dst, env, "commit", "-am", rev)
	}
	return dst
}

// writeConfig writes a smoke index.yaml derived from configs/index.yaml.
func writeConfig(t *testing.T, ollamaURL, repoPath string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "configs", "index.yaml"))
	if err != nil {
		t.Fatalf("read template config: %v", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse template config: %v", err)
	}
	section := func(name string) map[string]any {
		m, ok := cfg[name].(map[string]any)
		if !ok {
			t.Fatalf("template config has no %q section", name)
		}
		return m
	}
	cfg["repo_path"] = repoPath
	section("embeddings")["endpoint"] = ollamaURL
	section("summarization")["enabled"] = false
	hist := section("history")
	hist["min_commits_per_file"] = 1
	hist["coupling_min_cochanges"] = 2
	idx := section("index")
	var excludes []any
	for _, e := range idx["exclude_patterns"].([]any) {
		if e != "**/testdata/**" {
			excludes = append(excludes, e)
		}
	}
	idx["exclude_patterns"] = excludes

	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	p := filepath.Join(t.TempDir(), "index.yaml")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

// runLens runs bin/lens from the repo root and returns combined output.
func runLens(t *testing.T, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binPath(t, "lens"), args...)
	cmd.Dir = repoRoot(t)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lens %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
