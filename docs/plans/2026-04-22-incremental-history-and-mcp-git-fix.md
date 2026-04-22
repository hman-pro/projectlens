# Incremental Git History + MCP Symbol-History Fix Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make `index-history` fast for routine reindexing (only parse commits since the last indexed one) and fix the broken `get_change_history` MCP tool for symbol queries.

**Architecture:**
- Incremental: derive the "since" timestamp from `MAX(committed_at)` in `file_history` — no new columns, self-repairing. Coupling is always recomputed from the full 12-month window by reconstructing commits from `file_history` rows (GROUP BY commit_hash). This keeps coupling deterministic regardless of incremental vs full run.
- MCP fix: install `git` in the runtime Docker image so `git log -p` actually runs; tighten the fallback messaging when `repoPath` is empty so users aren't misled.

**Tech Stack:** Go 1.26, pgx, cobra, charmbracelet/log, Docker (alpine runtime), git.

---

## Scope notes

- Working directory: `/Users/hamed.zohrehvand/source/projectlens`.
- Assumes the Ingest DB is populated from last week's full index; a 4-day-delta incremental run should be the first real validation.
- No migrations required — `file_history` already has the data we need.
- Tasks 1 and 2 are independent of tasks 3-7 and can be landed in any order; everything after task 2 is sequential.

---

### Task 1: Install `git` in the runtime Docker image

**Files:**
- Modify: `docker/Dockerfile:19`

**Step 1: Inspect the current runtime stage**

Current state at `docker/Dockerfile:17-23`:
```dockerfile
FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=builder /bin/projectlens      /bin/projectlens
COPY --from=builder /bin/projectlens-mcp  /bin/projectlens-mcp
```

The MCP server (`internal/history/symbol_evolution.go:34`) shells out to `git`, but the runtime image has no git binary. Any call to `get_change_history` for a symbol returns a Go `exec: "git": executable file not found` error.

**Step 2: Add git to the runtime apk install**

Edit `docker/Dockerfile` line 19 from:
```dockerfile
RUN apk add --no-cache ca-certificates
```
to:
```dockerfile
RUN apk add --no-cache ca-certificates git
```

**Step 3: Build the image and verify**

Run:
```bash
docker build -f docker/Dockerfile -t projectlens:test .
docker run --rm --entrypoint git projectlens:test --version
```
Expected: `git version 2.x.x` prints and exits 0.

**Step 4: Commit**

```bash
git add docker/Dockerfile
git commit -m "fix(docker): install git in runtime image for symbol history"
```

---

### Task 2: Tighten the `get_change_history` symbol fallback message

**Files:**
- Modify: `internal/mcpserver/handlers.go:437-444`

**Background:** When `s.repoPath == ""`, the handler falls back to DB-based symbol history (`GetSymbolHistory`). But `InsertSymbolHistory` is never called anywhere in the codebase — the `symbol_history` table is always empty. The current message `"Run 'projectlens index-history' to index git history."` is misleading because `index-history` only populates `file_history`.

**Step 1: Write the failing test**

Add to `internal/mcpserver/handlers_integration_test.go` (a new test that constructs a server with `repoPath=""` and a symbol in the DB). Look at existing tests there for constructor conventions.

```go
func TestGetChangeHistory_SymbolWithoutRepoPath(t *testing.T) {
    db := setupTestDB(t) // existing helper
    defer db.Close()

    // Insert a file + symbol so LexicalSearch returns a hit.
    seedFileAndSymbol(t, db, "svc/foo.go", "DoThing")

    srv := mcpserver.New(db, retrieval.NewRouter(db, nil), 0, "") // no repoPath

    req := mcp.CallToolRequest{ /* name: "DoThing", limit: 10 */ }
    res, err := srv.HandleGetChangeHistory(context.Background(), req)
    if err != nil { t.Fatal(err) }

    text := textOf(res)
    if !strings.Contains(text, "repoPath") {
        t.Errorf("expected message to mention repoPath configuration, got: %s", text)
    }
    if strings.Contains(text, "index-history") {
        t.Errorf("message should not suggest index-history (it does not populate symbol_history), got: %s", text)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ -run TestGetChangeHistory_SymbolWithoutRepoPath -v -tags=integration`
Expected: FAIL on the message assertion.

**Step 3: Update the handler**

At `internal/mcpserver/handlers.go:442-443`, replace:
```go
if len(records) == 0 {
    return mcp.NewToolResultText(fmt.Sprintf("No change history found for symbol %q. Run 'projectlens index-history' to index git history.", name)), nil
}
```
with:
```go
if len(records) == 0 {
    if s.repoPath == "" {
        return mcp.NewToolResultText(fmt.Sprintf("Symbol-level change history requires repoPath configured on the MCP server. Set REPO_PATH env or repo_path in configs/index.yaml, then restart. (File-level history via get_change_history on a file path works without it.)")), nil
    }
    return mcp.NewToolResultText(fmt.Sprintf("No change history found for symbol %q in %s.", target.SymbolName, target.FilePath)), nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/mcpserver/ -run TestGetChangeHistory_SymbolWithoutRepoPath -v -tags=integration`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/mcpserver/handlers.go internal/mcpserver/handlers_integration_test.go
git commit -m "fix(mcp): clearer message when repoPath missing for symbol history"
```

---

### Task 3: Storage — `GetLatestFileHistoryTimestamp`

**Files:**
- Modify: `internal/storage/history.go` (append new method near existing file_history methods, around line 130)
- Test: `internal/storage/history_test.go` (or new `history_integration_test.go` if integration-tagged tests live separately)

**Step 1: Write the failing test**

```go
func TestGetLatestFileHistoryTimestamp(t *testing.T) {
    db := setupTestDB(t)
    defer db.Close()

    // Empty table: expect found=false, no error.
    _, found, err := db.GetLatestFileHistoryTimestamp(context.Background())
    if err != nil { t.Fatal(err) }
    if found { t.Error("expected found=false on empty table") }

    // Seed a file + two history rows.
    fileID := seedFile(t, db, "svc/foo.go")
    t1 := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
    t2 := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
    mustInsertHistory(t, db, fileID, "aaa111", t1)
    mustInsertHistory(t, db, fileID, "bbb222", t2)

    got, found, err := db.GetLatestFileHistoryTimestamp(context.Background())
    if err != nil { t.Fatal(err) }
    if !found { t.Fatal("expected found=true") }
    if !got.Equal(t2) {
        t.Errorf("got %v, want %v", got, t2)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/ -run TestGetLatestFileHistoryTimestamp -v -tags=integration`
Expected: FAIL — `db.GetLatestFileHistoryTimestamp undefined`.

**Step 3: Implement**

Add to `internal/storage/history.go`:
```go
// GetLatestFileHistoryTimestamp returns the most recent committed_at across
// all file_history rows. Returns (zero, false, nil) if the table is empty.
func (db *DB) GetLatestFileHistoryTimestamp(ctx context.Context) (time.Time, bool, error) {
    const query = `SELECT MAX(committed_at) FROM file_history`
    var ts *time.Time
    if err := db.Pool.QueryRow(ctx, query).Scan(&ts); err != nil {
        return time.Time{}, false, fmt.Errorf("storage: latest file_history timestamp: %w", err)
    }
    if ts == nil {
        return time.Time{}, false, nil
    }
    return *ts, true, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/storage/ -run TestGetLatestFileHistoryTimestamp -v -tags=integration`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/storage/history.go internal/storage/history_test.go
git commit -m "feat(storage): add GetLatestFileHistoryTimestamp for incremental history"
```

---

### Task 4: Storage — `ListCommitsInWindow` for coupling reconstruction

**Files:**
- Modify: `internal/storage/history.go`
- Test: `internal/storage/history_test.go`

**Rationale:** Coupling needs the full 12-month window, not just the incremental delta. We reconstruct it from `file_history` so it's consistent regardless of how/when rows arrived.

**Step 1: Write the failing test**

```go
func TestListCommitsInWindow(t *testing.T) {
    db := setupTestDB(t)
    defer db.Close()

    fileA := seedFile(t, db, "svc/a.go")
    fileB := seedFile(t, db, "svc/b.go")

    inside := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
    outside := time.Now().Add(-400 * 24 * time.Hour).UTC().Truncate(time.Second)

    mustInsertHistory(t, db, fileA, "c1", inside)
    mustInsertHistory(t, db, fileB, "c1", inside) // same commit, two files
    mustInsertHistory(t, db, fileA, "c_old", outside)

    commits, err := db.ListCommitsInWindow(context.Background(), 12)
    if err != nil { t.Fatal(err) }

    if len(commits) != 1 {
        t.Fatalf("expected 1 commit in window, got %d", len(commits))
    }
    if commits[0].Hash != "c1" {
        t.Errorf("got hash %s, want c1", commits[0].Hash)
    }
    if len(commits[0].Files) != 2 {
        t.Errorf("expected 2 files for c1, got %d: %v", len(commits[0].Files), commits[0].Files)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/ -run TestListCommitsInWindow -v -tags=integration`
Expected: FAIL — undefined.

**Step 3: Implement**

Return type needs `Hash`, `Timestamp`, and `Files` — matching what `ComputeCoupling` consumes. Define a lean return shape in storage to avoid importing the history package:

```go
// CommitFiles is the shape ComputeCoupling consumes: commit hash + timestamp + files touched.
type CommitFiles struct {
    Hash      string
    Timestamp time.Time
    Files     []string
}

// ListCommitsInWindow returns commits recorded in file_history within the last
// `months` months, one row per commit with its touched file paths aggregated.
func (db *DB) ListCommitsInWindow(ctx context.Context, months int) ([]CommitFiles, error) {
    const query = `
        SELECT fh.commit_hash,
               MIN(fh.committed_at)         AS ts,
               ARRAY_AGG(f.path ORDER BY f.path) AS files
        FROM file_history fh
        JOIN files f ON f.id = fh.file_id
        WHERE fh.committed_at >= NOW() - ($1 || ' months')::interval
        GROUP BY fh.commit_hash
    `
    rows, err := db.Pool.Query(ctx, query, fmt.Sprintf("%d", months))
    if err != nil {
        return nil, fmt.Errorf("storage: list commits in window: %w", err)
    }
    defer rows.Close()

    var out []CommitFiles
    for rows.Next() {
        var c CommitFiles
        if err := rows.Scan(&c.Hash, &c.Timestamp, &c.Files); err != nil {
            return nil, fmt.Errorf("storage: scan commit: %w", err)
        }
        out = append(out, c)
    }
    return out, rows.Err()
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/storage/ -run TestListCommitsInWindow -v -tags=integration`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/storage/history.go internal/storage/history_test.go
git commit -m "feat(storage): add ListCommitsInWindow for DB-derived coupling"
```

---

### Task 5: Incremental path in `IndexHistory`

**Files:**
- Modify: `internal/history/indexer.go` (the `IndexHistory` function + `Config`)
- Test: `internal/history/indexer_test.go` (create if absent) — integration test against DB.

**Design:**
- Add `FullReindex bool` to `history.Config`.
- At start of `IndexHistory`:
  - If `FullReindex`, use `window = "<WindowMonths> months"` (current behavior).
  - Else, call `GetLatestFileHistoryTimestamp`:
    - If found, set `window = last.Format(time.RFC3339)` and log `"incremental since <ts>"`.
    - If not found, fall back to the full window and log `"no prior history, running full window"`.
- **Change coupling source:** after inserting new `file_history`, instead of calling `ComputeCoupling(filteredCommits, …)`, call `db.ListCommitsInWindow(ctx, WindowMonths)` and adapt its `[]CommitFiles` to `[]Commit` (only `Hash`, `Timestamp`, `Files` are read by ComputeCoupling).

**Step 1: Write the failing test**

```go
//go:build integration

func TestIndexHistory_IncrementalSkipsOldCommits(t *testing.T) {
    db := setupTestDB(t)
    defer db.Close()

    repo := initGitRepoWithCommits(t, [][]string{
        {"svc/a.go"},              // old commit
        {"svc/a.go", "svc/b.go"},  // recent commit
    })
    seedIndexedFile(t, db, "svc/a.go")
    seedIndexedFile(t, db, "svc/b.go")

    // First run (full).
    if err := history.IndexHistory(ctx, db, repo, history.Config{
        WindowMonths: 12, FullReindex: true,
        CouplingMinCoChanges: 1, CouplingMaxFiles: 20,
    }); err != nil {
        t.Fatal(err)
    }
    firstCount := mustCountFileHistory(t, db)

    // Add a new commit.
    addGitCommit(t, repo, []string{"svc/a.go"})

    // Second run (incremental) — should only process the one new commit.
    logs := captureLogs(t, func() {
        if err := history.IndexHistory(ctx, db, repo, history.Config{
            WindowMonths: 12, FullReindex: false,
            CouplingMinCoChanges: 1, CouplingMaxFiles: 20,
        }); err != nil {
            t.Fatal(err)
        }
    })

    if !strings.Contains(logs, "incremental since") {
        t.Errorf("expected incremental log line, got: %s", logs)
    }
    secondCount := mustCountFileHistory(t, db)
    if secondCount != firstCount + 1 {
        t.Errorf("expected 1 new row, got delta %d", secondCount-firstCount)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/history/ -run TestIndexHistory_IncrementalSkipsOldCommits -v -tags=integration`
Expected: FAIL — `FullReindex` field undefined, or log line missing.

**Step 3: Implement**

Edit `internal/history/indexer.go`:

Add field to `Config`:
```go
type Config struct {
    WindowMonths         int  `yaml:"window_months"`
    MinCommitsPerFile    int  `yaml:"min_commits_per_file"`
    CouplingMinCoChanges int  `yaml:"coupling_min_cochanges"`
    CouplingMaxFiles     int  `yaml:"coupling_exclude_max_files"`
    FullReindex          bool `yaml:"-"` // CLI-only; not persisted
}
```

Replace the `window` setup block (around `indexer.go:29-36`):
```go
// Step 1: Parse git log (incremental unless --full)
window := fmt.Sprintf("%d months", cfg.WindowMonths)
if !cfg.FullReindex {
    last, ok, err := db.GetLatestFileHistoryTimestamp(ctx)
    if err != nil {
        return fmt.Errorf("history: latest timestamp: %w", err)
    }
    if ok {
        // Subtract a small safety overlap to catch commits with equal timestamps.
        since := last.Add(-1 * time.Minute).UTC().Format(time.RFC3339)
        window = since
        logger.Info("parsing git log (incremental)", "since", since)
    } else {
        logger.Info("parsing git log (no prior history, full window)", "window", window)
    }
} else {
    logger.Info("parsing git log (full reindex)", "window", window)
}
commits, err := ParseGitLog(repoPath, window)
if err != nil {
    return fmt.Errorf("history: parse git log: %w", err)
}
logger.Info("found commits", "count", len(commits))
```

**Note:** `ParseGitLog` currently passes its `window` param as `--since=<value>`. Git accepts both `"12 months"` and RFC3339 timestamps for `--since`, so no changes needed in `gitlog.go`.

Replace the coupling block (around `indexer.go:139-142`):
```go
// Step 6: Compute coupling over the full WindowMonths window from DB state.
logger.Info("computing co-change coupling from DB...")
windowCommits, err := db.ListCommitsInWindow(ctx, cfg.WindowMonths)
if err != nil {
    return fmt.Errorf("history: list commits in window: %w", err)
}
adapted := make([]Commit, len(windowCommits))
for i, w := range windowCommits {
    adapted[i] = Commit{Hash: w.Hash, Timestamp: w.Timestamp.Unix(), Files: w.Files}
}
pairs := ComputeCoupling(adapted, cfg.CouplingMinCoChanges, cfg.CouplingMaxFiles)
logger.Info("found coupling pairs", "count", len(pairs), "min_co_changes", cfg.CouplingMinCoChanges)
```

**Important:** Coupling edges currently accumulate — `InsertEdges` with existing pairs won't delete stale pairs. Decide whether to clear `co_changes` edges before re-inserting. Recommendation: clear them, since we just recomputed from the authoritative DB state.

Add before the `InsertEdges` call:
```go
if err := db.DeleteEdgesByType(ctx, "file", "file", "co_changes"); err != nil {
    return fmt.Errorf("history: clear coupling edges: %w", err)
}
```

Requires a new storage method (see task 6).

**Step 4: Run test to verify it passes**

Run: `go test ./internal/history/ -run TestIndexHistory_IncrementalSkipsOldCommits -v -tags=integration`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/history/indexer.go internal/history/indexer_test.go
git commit -m "feat(history): incremental git log parsing driven by file_history max ts"
```

---

### Task 6: Storage — `DeleteEdgesByType`

**Files:**
- Modify: `internal/storage/edges.go` (or wherever `InsertEdges` lives)
- Test: matching `_test.go` file.

**Step 1: Write the failing test**

```go
func TestDeleteEdgesByType(t *testing.T) {
    db := setupTestDB(t)
    defer db.Close()

    fA := seedFile(t, db, "a.go")
    fB := seedFile(t, db, "b.go")
    fC := seedFile(t, db, "c.go")

    mustInsertEdge(t, db, "file", fA, "file", fB, "co_changes")
    mustInsertEdge(t, db, "file", fA, "file", fC, "calls") // different edge_type, should survive

    if err := db.DeleteEdgesByType(ctx, "file", "file", "co_changes"); err != nil {
        t.Fatal(err)
    }

    if countEdges(t, db, "co_changes") != 0 {
        t.Error("expected co_changes edges to be gone")
    }
    if countEdges(t, db, "calls") != 1 {
        t.Error("expected unrelated edges to survive")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/ -run TestDeleteEdgesByType -v -tags=integration`
Expected: FAIL — undefined.

**Step 3: Implement**

```go
// DeleteEdgesByType removes all edges matching (source_type, target_type, edge_type).
func (db *DB) DeleteEdgesByType(ctx context.Context, sourceType, targetType, edgeType string) error {
    const query = `
        DELETE FROM edges
        WHERE source_type = $1 AND target_type = $2 AND edge_type = $3
    `
    _, err := db.Pool.Exec(ctx, query, sourceType, targetType, edgeType)
    if err != nil {
        return fmt.Errorf("storage: delete edges by type: %w", err)
    }
    return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/storage/ -run TestDeleteEdgesByType -v -tags=integration`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/storage/edges.go internal/storage/edges_test.go
git commit -m "feat(storage): DeleteEdgesByType for coupling recomputation"
```

---

### Task 7: CLI flag `--full` on `index-history`

**Files:**
- Modify: `cmd/projectlens/main.go:513-537` (`newIndexHistoryCmd`)
- Docs: `README.md` (usage table, around line 150)

**Step 1: Add flag + plumb to config**

Replace the current `newIndexHistoryCmd` body with:
```go
func newIndexHistoryCmd() *cobra.Command {
    var full bool
    cmd := &cobra.Command{
        Use:   "index-history",
        Short: "Index git change history and compute co-change coupling (incremental by default)",
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := context.Background()
            cfg, repoPath, err := loadCmdConfig(cmd)
            if err != nil { return err }
            db, err := storage.Connect(ctx, cfg.DatabaseURL)
            if err != nil { return fmt.Errorf("connecting to database: %w", err) }
            defer db.Close()

            return history.IndexHistory(ctx, db, repoPath, history.Config{
                WindowMonths:         cfg.History.WindowMonths,
                MinCommitsPerFile:    cfg.History.MinCommitsPerFile,
                CouplingMinCoChanges: cfg.History.CouplingMinCoChanges,
                CouplingMaxFiles:     cfg.History.CouplingMaxFiles,
                FullReindex:          full,
            })
        },
    }
    cmd.Flags().BoolVar(&full, "full", false, "Reparse the entire history window instead of incremental since last run")
    return cmd
}
```

**Step 2: Smoke-test manually**

Run:
```bash
go build ./cmd/projectlens/
./projectlens index-history --help
```
Expected: help text shows `--full` flag.

**Step 3: Update README**

In `README.md` around the CLI table:
```markdown
| `index-history` | Index git history incrementally (use `--full` to reparse entire window) |
```

And update any example invocations if present.

**Step 4: Commit**

```bash
git add cmd/projectlens/main.go README.md
git commit -m "feat(cli): --full flag on index-history, incremental by default"
```

---

### Task 8: End-to-end verification on the live DB

Not a code change — an operator checklist before declaring done. Do this against a copy of the DB or after backing it up.

**Step 1: Capture baseline**

```bash
psql "$DATABASE_URL" -c "SELECT COUNT(*) AS file_hist, MAX(committed_at) AS latest FROM file_history;"
psql "$DATABASE_URL" -c "SELECT COUNT(*) AS coupling FROM edges WHERE edge_type='co_changes';"
```
Note both numbers.

**Step 2: Run incremental**

```bash
time ./projectlens index-history --repo "$INGEST_REPO" --db "$DATABASE_URL"
```
Expected:
- Log line includes `"parsing git log (incremental) since=<timestamp>"`.
- Completes in **minutes, not ~34 minutes** for a 4-day delta.
- New `file_history` row count equals baseline + N (where N = new commits × indexed files touched).

**Step 3: Verify coupling consistency**

```bash
psql "$DATABASE_URL" -c "SELECT COUNT(*) FROM edges WHERE edge_type='co_changes';"
```
Coupling count should be comparable to baseline (small drift expected from new commits).

**Step 4: Verify MCP `get_change_history` for a symbol**

With MCP container restarted on the new image (Task 1):
- From Claude Code, invoke `get_change_history` with a symbol name you know changed recently.
- Expected: a list of commits with hashes, dates, authors, diff snippets — not an error.

**Step 5: Run full as a sanity check**

```bash
./projectlens index-history --full --repo "$INGEST_REPO" --db "$DATABASE_URL"
```
Expected: row counts stable (ON CONFLICT DO NOTHING keeps inserts idempotent); coupling regenerates deterministically.

---

## Rollout order

1. Task 1 + Task 2 (MCP fix) — independent, safe to ship immediately.
2. Tasks 3 → 4 → 6 → 5 → 7 — sequential (task 5 depends on 3, 4, 6).
3. Task 8 — final verification before relying on incremental for future reindexes.

## What's explicitly out of scope

- Populating `symbol_history` table. The git-based path (`GetSymbolEvolution`) is the primary, works well, and the DB fallback is only meaningful if/when we add a different data source. Not worth the churn now.
- Migrating per-file backfill (`ParseGitLogForFile`) to incremental — it's already bounded by `maxBackfillFiles=100` and runs rarely.
- Phase 4 (Confluence/Jira) — explicitly parked.
