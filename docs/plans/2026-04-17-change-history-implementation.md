# Change History (Phase 3) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Index git change history per file and detect co-change coupling, enabling "who changed what", "what changes together", and on-demand symbol evolution queries.

**Architecture:** One `git log` call parses all commits, filtered against indexed files. File-level history stored eagerly, coupling computed from co-occurrence in commits. Symbol-level diffs computed lazily at query time. Two new MCP tools expose the data.

**Tech Stack:** Go 1.26, `os/exec` for git commands, existing Postgres storage.

**Design doc:** `docs/plans/2026-04-17-change-history-design.md`

---

### Task 1: Implement git log parser

**Files:**
- Create: `internal/history/gitlog.go`
- Create: `internal/history/gitlog_test.go`

**What it does:** Run `git log` and parse the output into structured commit records with file lists.

**Types:**

```go
package history

// Commit represents a parsed git log entry.
type Commit struct {
    Hash      string
    Author    string
    Timestamp int64    // unix timestamp
    Message   string
    Files     []string // relative file paths changed
}

// ParseGitLog runs git log and returns parsed commits.
// window is the --since duration (e.g., "12 months").
// repoPath is the git repo directory.
func ParseGitLog(repoPath string, window string) ([]Commit, error)

// ParseGitLogForFile runs git log for a specific file with --follow.
// Returns up to maxCommits.
func ParseGitLogForFile(repoPath string, filePath string, maxCommits int) ([]Commit, error)
```

**Implementation:**
- `git log --name-only --no-merges --since="12 months" --format="COMMIT:%H|%an|%at|%s"` 
- Parse output: lines starting with `COMMIT:` are commit headers, non-empty lines after are file paths
- `ParseGitLogForFile`: `git log --follow -N --name-only --format="COMMIT:%H|%an|%at|%s" -- <file>`

**Tests (6+):**
- Parse well-formed git log output (mock via string, not actual git)
- Multiple commits with multiple files
- Commit with no files (merge-only — should be filtered)
- Single-file commit
- Empty output → empty slice
- Actually run against projectlens's own git repo (integration-style)

**Commit:** `"feat: implement git log parser for change history"`

---

### Task 2: Implement co-change coupling detector

**Files:**
- Create: `internal/history/coupling.go`
- Create: `internal/history/coupling_test.go`

**What it does:** Compute co-change coupling pairs from commit history.

**Types:**

```go
// CouplingPair represents two files that frequently change together.
type CouplingPair struct {
    FileA          string
    FileB          string
    CoChangeCount  int
    Strength       float64 // co_changes / max(changes_a, changes_b)
    LastCoChange   int64   // unix timestamp of most recent co-change
}

// ComputeCoupling analyzes commits and returns coupling pairs.
// Filters: min cochanges, max files per commit.
func ComputeCoupling(commits []Commit, minCoChanges int, maxFilesPerCommit int) []CouplingPair
```

**Implementation:**
- For each commit with <= maxFilesPerCommit files:
  - Generate all file pairs (A,B) where A < B (lexicographic, avoid duplicates)
  - Increment co-change count for each pair
- Track total changes per file
- Compute strength: `co_changes(A,B) / max(changes(A), changes(B))`
- Filter: only pairs with >= minCoChanges
- Sort by strength descending

**Tests (5+):**
- Two files appearing in 3 of 5 commits → strength 0.6
- File always changed alone → no coupling
- Commit with >20 files excluded
- Minimum co-change threshold filtering
- Empty commits → empty result

**Commit:** `"feat: implement co-change coupling detector"`

---

### Task 3: Implement history indexer orchestrator and CLI

**Files:**
- Create: `internal/history/indexer.go`
- Modify: `cmd/projectlens/main.go` — add `index-history` command
- Modify: `internal/config/config.go` — add history config section
- Modify: `configs/index.yaml` — add history defaults

**What it does:** Orchestrate: git log → filter against DB → store file_history → compute coupling → store edges.

**Orchestrator:**

```go
// IndexHistory runs the full history indexing pipeline.
func IndexHistory(ctx context.Context, db *storage.DB, repoPath string, cfg HistoryConfig) error {
    // 1. Parse git log (12 month window)
    // 2. Load indexed file paths from DB
    // 3. Filter commits to only indexed files
    // 4. For files with < minCommits, run targeted git log --follow
    // 5. Store file_history records (upsert, one per file+commit)
    // 6. Compute coupling pairs
    // 7. Store coupling as edges (edge_type='co_changes')
    // 8. Evict old file_history beyond window
}

type HistoryConfig struct {
    WindowMonths         int `yaml:"window_months"`          // default 12
    MinCommitsPerFile    int `yaml:"min_commits_per_file"`   // default 5
    CouplingMinCoChanges int `yaml:"coupling_min_cochanges"` // default 5
    CouplingMaxFiles     int `yaml:"coupling_exclude_max_files"` // default 20
}
```

**Config addition:**
```yaml
history:
  window_months: 12
  min_commits_per_file: 5
  coupling_min_cochanges: 5
  coupling_exclude_max_files: 20
```

**CLI:** `projectlens index-history --repo ... --db ...`

**Commit:** `"feat: implement index-history CLI command and orchestrator"`

---

### Task 4: Implement on-demand symbol evolution

**Files:**
- Create: `internal/history/symbol_evolution.go`
- Create: `internal/history/symbol_evolution_test.go`

**What it does:** Given a symbol name, find its file, run `git log -p`, and extract commits where the symbol's line range was modified.

```go
// SymbolChange represents one commit that modified a symbol.
type SymbolChange struct {
    Hash      string
    Author    string
    Timestamp int64
    Message   string
    DiffSnippet string // the relevant hunk
}

// GetSymbolEvolution finds recent commits that modified a specific symbol.
// It runs git log -p on the symbol's file and filters hunks by line range.
func GetSymbolEvolution(repoPath string, filePath string, symbolName string, lineStart, lineEnd int, maxCommits int) ([]SymbolChange, error)
```

**Implementation:**
- Run `git log -10 -p -- <filePath>`
- Parse diff output, find hunks (`@@ -start,len +start,len @@`)
- For each hunk, check if it overlaps the symbol's line range
- Also grep for the symbol name in the hunk content (catches moves)
- Return matching commits with their diff snippets

**Tests:**
- Parse a known diff format with hunks
- Hunk overlapping line range → included
- Hunk not overlapping → excluded
- Symbol name found in hunk content → included

**Commit:** `"feat: implement on-demand symbol evolution via git diff"`

---

### Task 5: Add get_change_history and get_coupling MCP tools

**Files:**
- Modify: `internal/mcpserver/handlers.go`
- Modify: `internal/mcpserver/tools.go`
- Modify: `internal/mcpserver/server.go`
- Modify: `internal/mcpserver/server_test.go`

**`get_change_history` tool:**
- Input: `name` (file path or symbol name), `limit` (default 10)
- If name matches a file path in DB → return file_history records
- If name matches a symbol → look up file, call GetSymbolEvolution
- Output: list of commits with hash, author, date, message

**`get_coupling` tool:**
- Input: `name` (file path or symbol name), `min_strength` (default 0.3)
- Look up file → query edges with edge_type='co_changes' where source or target is this file
- Output: co-change partners ranked by coupling strength, grouped by strong (>=0.5) and notable (>=0.3)

**Storage helper needed in edges.go:**
```go
func (db *DB) GetCouplingEdges(ctx context.Context, fileID int64, minStrength float32) ([]CouplingEdge, error)
```

**Commit:** `"feat: add get_change_history and get_coupling MCP tools"`

---

## Task Summary

| Task | What | Output |
|------|------|--------|
| 1 | Git log parser | Parse commits + file lists from git log |
| 2 | Coupling detector | Compute co-change pairs with strength |
| 3 | Orchestrator + CLI | `index-history` command, config, end-to-end |
| 4 | Symbol evolution | On-demand git diff → symbol line range mapping |
| 5 | MCP tools | `get_change_history` + `get_coupling` (8 tools total) |
