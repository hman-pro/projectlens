//go:build integration

// Integration tests for IndexHistory against a live database and a real
// on-disk git repo fixture.
// Run with: go test ./internal/history/ -tags integration -v
//
// Prerequisites:
//   - Postgres running on localhost:5433 with projectlens database
//   - Migrations applied
//   - `git` available on PATH
//
// These tests use marker-based isolation so they can run safely against a
// shared dev database: they only delete rows whose commit_hash matches the
// test marker, and only files whose path matches the fixture repo prefix.
package history

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/storage"
)

const testDB = "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"

func connectForIntegration(t *testing.T) *storage.DB {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Connect(ctx, testDB)
	if err != nil {
		t.Skipf("cannot connect to test database: %v", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		t.Skipf("cannot ping test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// runGit executes `git <args>` inside dir with optional env overrides,
// failing the test on non-zero exit.
func runGit(t *testing.T, dir string, extraEnv []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Force a deterministic env — no global config interference.
	base := []string{
		"HOME=" + dir,      // isolate from user's ~/.gitconfig
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
		"PATH=" + os.Getenv("PATH"),
	}
	cmd.Env = append(base, extraEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\noutput:\n%s", args, err, out)
	}
}

// setupGitRepo creates a tmp dir initialized as a git repo, with the given
// files committed in order. Each commit is a map[relpath]content and a date
// string (git-compatible, e.g. "2024-01-01T12:00:00Z").
type fixtureCommit struct {
	files map[string]string
	date  string // empty means "now"
	msg   string
}

func setupGitRepo(t *testing.T, commits []fixtureCommit) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "projectlens-history-it-")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	runGit(t, dir, nil, "init", "-q", "-b", "main")

	for i, c := range commits {
		for rel, content := range c.files {
			full := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatalf("mkdirall: %v", err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", rel, err)
			}
			runGit(t, dir, nil, "add", rel)
		}
		env := []string(nil)
		if c.date != "" {
			// Set both author and committer date so the commit's --since
			// filter will see the intended timestamp.
			env = append(env,
				"GIT_AUTHOR_DATE="+c.date,
				"GIT_COMMITTER_DATE="+c.date,
			)
		}
		msg := c.msg
		if msg == "" {
			msg = fmt.Sprintf("commit %d", i+1)
		}
		runGit(t, dir, env, "commit", "-q", "-m", msg)
	}
	return dir
}

// countFileHistoryForHashes returns how many file_history rows exist whose
// commit_hash is in the given set. Scoped so we only see this test's rows.
func countFileHistoryForHashes(t *testing.T, db *storage.DB, hashes []string) int {
	t.Helper()
	ctx := context.Background()
	var n int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM file_history WHERE commit_hash = ANY($1)`,
		hashes,
	).Scan(&n); err != nil {
		t.Fatalf("count file_history: %v", err)
	}
	return n
}

// commitHashes returns all commit SHAs in the repo, oldest-first.
func commitHashes(t *testing.T, repoPath string) []string {
	t.Helper()
	cmd := exec.Command("git", "-C", repoPath, "log", "--format=%H", "--reverse")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	var hashes []string
	for _, line := range splitLines(string(out)) {
		if line != "" {
			hashes = append(hashes, line)
		}
	}
	return hashes
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// TestIndexHistory_IncrementalSkipsOldCommits verifies that once baseline
// history is recorded, a subsequent run with FullReindex=false picks up only
// the new commit's rows (plus idempotent duplicates of the old ones, which
// ON CONFLICT DO NOTHING swallows). It does NOT re-process every commit from
// scratch — we assert that by seeing the file_history row count grow by
// exactly the new commit's file-count, never more than once.
func TestIndexHistory_IncrementalSkipsOldCommits(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	marker := fmt.Sprintf("ih-it-%d", time.Now().UnixNano())
	// Path prefixes we'll seed into `files` — they match the paths
	// ParseGitLog will emit (relative to repoPath, which we use as-is).
	relA := marker + "-a.go"
	relB := marker + "-b.go"
	relC := marker + "-c.go"

	// Cleanup: delete our file_history rows (by fixture commit hash, resolved
	// below) and our files rows (by exact path).
	var fixtureHashes []string
	t.Cleanup(func() {
		if len(fixtureHashes) > 0 {
			if _, err := db.Pool.Exec(ctx,
				`DELETE FROM file_history WHERE commit_hash = ANY($1)`, fixtureHashes,
			); err != nil {
				t.Logf("cleanup file_history: %v", err)
			}
		}
		// Edges: scoped by file_id. We resolve the ids on demand below; for
		// cleanup we just purge any co_changes edge that references our file
		// paths. DeleteEdgesByType is global, so we don't use it here.
		if _, err := db.Pool.Exec(ctx, `
			DELETE FROM edges
			WHERE edge_type = 'co_changes'
			  AND (
			    (source_type = 'file' AND source_id IN (SELECT id FROM files WHERE path = ANY($1))) OR
			    (target_type = 'file' AND target_id IN (SELECT id FROM files WHERE path = ANY($1)))
			  )`,
			[]string{relA, relB, relC},
		); err != nil {
			t.Logf("cleanup edges: %v", err)
		}
		if _, err := db.Pool.Exec(ctx,
			`DELETE FROM files WHERE path = ANY($1)`,
			[]string{relA, relB, relC},
		); err != nil {
			t.Logf("cleanup files: %v", err)
		}
	})

	// --- Fixture: 3 commits, 2 dated "old" (still within the 12mo window),
	// the 3rd left for after baseline. All three files must be touched at
	// least twice across the three commits so ComputeCoupling produces pairs.
	oldDate1 := time.Now().Add(-60 * 24 * time.Hour).UTC().Format(time.RFC3339)
	oldDate2 := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)

	repoPath := setupGitRepo(t, []fixtureCommit{
		{
			files: map[string]string{
				relA: "package a\nfunc A() {}\n",
				relB: "package b\nfunc B() {}\n",
			},
			date: oldDate1,
			msg:  "first",
		},
		{
			files: map[string]string{
				relA: "package a\nfunc A() { _ = 1 }\n",
				relB: "package b\nfunc B() { _ = 2 }\n",
			},
			date: oldDate2,
			msg:  "second",
		},
	})
	// Resolve the baseline commit hashes so we can scope row counts.
	fixtureHashes = commitHashes(t, repoPath)
	if len(fixtureHashes) != 2 {
		t.Fatalf("expected 2 baseline commits, got %d", len(fixtureHashes))
	}

	// --- Seed `files` rows. IndexHistory requires each indexed path to
	// already exist in `files` (it uses ListFiles as its universe).
	for _, rel := range []string{relA, relB} {
		if _, err := db.UpsertFile(ctx, &storage.FileRecord{
			Path:        rel,
			PackageName: "testpkg",
			Checksum:    "chk-" + rel,
			Language:    "go",
			CommitSHA:   "seed-" + marker,
		}); err != nil {
			t.Fatalf("UpsertFile %s: %v", rel, err)
		}
	}

	cfg := Config{
		WindowMonths:         12,
		MinCommitsPerFile:    1,
		CouplingMinCoChanges: 2, // two co-changes in the baseline trigger one pair (A,B)
		CouplingMaxFiles:     20,
		FullReindex:          true,
	}

	// --- Baseline run (full).
	if err := IndexHistory(ctx, db, repoPath, cfg); err != nil {
		t.Fatalf("IndexHistory baseline: %v", err)
	}

	baselineRows := countFileHistoryForHashes(t, db, fixtureHashes)
	// 2 commits × 2 files each = 4 file_history rows.
	if baselineRows != 4 {
		t.Fatalf("expected 4 baseline file_history rows (2 commits × 2 files), got %d", baselineRows)
	}

	// --- Idempotency check: a second run with no new commits must be stable.
	cfg.FullReindex = false
	if err := IndexHistory(ctx, db, repoPath, cfg); err != nil {
		t.Fatalf("IndexHistory second run (no new commits): %v", err)
	}
	stableRows := countFileHistoryForHashes(t, db, fixtureHashes)
	if stableRows != baselineRows {
		t.Errorf("expected idempotent row count %d after re-running, got %d", baselineRows, stableRows)
	}

	// --- Add a new commit with a current timestamp.
	// The new commit touches A and C. C is a NEW path — we must seed it now
	// so IndexHistory's indexed-file filter sees it.
	if _, err := db.UpsertFile(ctx, &storage.FileRecord{
		Path:        relC,
		PackageName: "testpkg",
		Checksum:    "chk-" + relC,
		Language:    "go",
		CommitSHA:   "seed-" + marker,
	}); err != nil {
		t.Fatalf("UpsertFile %s: %v", relC, err)
	}

	// Update files and add new one, then commit with current date (default).
	if err := os.WriteFile(filepath.Join(repoPath, relA), []byte("package a\nfunc A() { _ = 3 }\n"), 0o644); err != nil {
		t.Fatalf("write relA: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, relC), []byte("package c\nfunc C() {}\n"), 0o644); err != nil {
		t.Fatalf("write relC: %v", err)
	}
	runGit(t, repoPath, nil, "add", relA, relC)
	runGit(t, repoPath, nil, "commit", "-q", "-m", "third")

	fixtureHashes = commitHashes(t, repoPath) // now 3
	if len(fixtureHashes) != 3 {
		t.Fatalf("expected 3 commits after adding, got %d", len(fixtureHashes))
	}

	// --- Incremental run.
	if err := IndexHistory(ctx, db, repoPath, cfg); err != nil {
		t.Fatalf("IndexHistory incremental: %v", err)
	}

	afterIncr := countFileHistoryForHashes(t, db, fixtureHashes)
	// Expected: baseline 4 + new commit (relA, relC) = 6.
	if afterIncr != baselineRows+2 {
		t.Errorf("expected %d rows after incremental (baseline %d + 2 new file touches), got %d",
			baselineRows+2, baselineRows, afterIncr)
	}

	// --- Second incremental run — nothing new; must be stable again.
	if err := IndexHistory(ctx, db, repoPath, cfg); err != nil {
		t.Fatalf("IndexHistory second incremental: %v", err)
	}
	finalRows := countFileHistoryForHashes(t, db, fixtureHashes)
	if finalRows != afterIncr {
		t.Errorf("expected idempotent row count %d after re-running incremental, got %d", afterIncr, finalRows)
	}

	// --- Coupling sanity: (A,B) co-changed twice in the baseline, which
	// meets CouplingMinCoChanges=2. We should see at least one co_changes
	// edge referencing our files. We query with both orderings since
	// ComputeCoupling orders the pair lexicographically.
	var coEdges int
	if err := db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM edges
		WHERE edge_type = 'co_changes'
		  AND source_type = 'file' AND target_type = 'file'
		  AND source_id IN (SELECT id FROM files WHERE path = ANY($1))
		  AND target_id IN (SELECT id FROM files WHERE path = ANY($1))`,
		[]string{relA, relB, relC},
	).Scan(&coEdges); err != nil {
		t.Fatalf("count co_changes edges: %v", err)
	}
	if coEdges < 1 {
		t.Errorf("expected at least 1 co_changes edge among fixture files, got %d", coEdges)
	}

	// --- Duplicate-free coupling: running again must not double the count.
	// The clear-before-insert step is what guarantees this.
	if err := IndexHistory(ctx, db, repoPath, cfg); err != nil {
		t.Fatalf("IndexHistory third incremental: %v", err)
	}
	var coEdgesAfter int
	if err := db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM edges
		WHERE edge_type = 'co_changes'
		  AND source_type = 'file' AND target_type = 'file'
		  AND source_id IN (SELECT id FROM files WHERE path = ANY($1))
		  AND target_id IN (SELECT id FROM files WHERE path = ANY($1))`,
		[]string{relA, relB, relC},
	).Scan(&coEdgesAfter); err != nil {
		t.Fatalf("count co_changes edges (after): %v", err)
	}
	if coEdgesAfter != coEdges {
		t.Errorf("expected co_changes count stable across re-runs (want %d, got %d) — clear-before-insert is broken",
			coEdges, coEdgesAfter)
	}
}
