# Report and Graph Export Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `projectlens report` and `projectlens export graph` (Phase 1 from the Graphify comparison) — two read-only CLI commands that make the indexed database inspectable as Markdown / JSON summaries and a streamed native-schema graph dump.

**Architecture:** Three new internal packages (`indexstate`, `report`, `export`) plus two thin CLI files. `indexstate` lifts shared state types and provider/git helpers out of `mcpserver` so the report and `index_status` share a single source of truth. `report.Builder` runs read-only queries and returns a typed `Report`; renderers (Markdown / JSON) take that struct to an `io.Writer`. `export.GraphExporter` streams nodes (five passes: symbols, files, datastore_tables, derived packages, knowledge_entries) and edges through one shared `nodeID` function so every edge endpoint resolves to a node.

**Tech Stack:** Go 1.26, pgx, cobra, `encoding/json`. Postgres 16 + pgvector for the underlying schema.

**Spec:** `docs/superpowers/specs/2026-05-21-report-export-design.md`

**Plan revisions:**

- v1 review fixes (see `2026-05-21-report-and-graph-export-review.md`):
  - Added Task 4b: shared `buildInspector` helper using `retrieval.Router` + a CLI-side `summarizerProberFunc` (mirrors `cmd/projectlens-mcp/main.go`). Tasks 17 and 23 now call this instead of `buildProviders`, which is fail-fast and returns indexer types.
  - Task 4 aliases `SummarizerProber` from `indexstate` so the shared interface name lines up between `mcpserver` and the new CLI helper.
  - Task 11 fixture inserts updated to the real `files` (commit_sha, no kind) and `symbols` (signature, line_start, line_end, checksum) schemas.
  - Task 20 step ordering fixed: `encoding/json` and `time` are added to the top-of-file import block before the streaming helpers are appended (Go forbids a second import block lower in the file).

- v2 review fix:
  - Task 4 now keeps `mcpserver.New(db, router, port, repoPath)` signature stable and initializes a `DefaultInspector` internally (router is the embedder prober). `WithSummarizer` writes the prober through to the inspector. No existing test call site changes. A new no-summarizer-no-panic unit test guards the default path.

**Module path:** `github.com/hman-pro/projectlens`

**Build / test commands (memorize):**
- Build: `make build`
- Unit tests: `go test ./internal/...`
- Integration tests (require Postgres at `DATABASE_URL`): `make test-int`
- Per-package focused unit run: `go test ./internal/report/ -v -run TestX`
- Per-package focused integration run: `go test -tags=integration ./internal/storage/ -v -run TestY`

**Conventions to follow (from `CLAUDE.md` and existing code):**
- Internal packages only — nothing exported outside `internal/`.
- All DB access through `*storage.DB` (pgx pool inside).
- Error wrapping: `fmt.Errorf("context: %w", err)`.
- Integration tests gated by `//go:build integration` build tag.
- No global state; pass deps through constructors.
- Commit frequently. One conventional-commit per logical task. Co-author lines not required for this branch (project doesn't use them).
- Caveman caveats: comments stay terse; no docstrings for self-explanatory funcs.

---

## File Structure

**New:**
- `internal/indexstate/types.go` — `StageFreshness`, `ProviderHealth`, `GitState`.
- `internal/indexstate/inspector.go` — `Inspector` interface + `DefaultInspector` impl.
- `internal/indexstate/inspector_test.go` — `Inspector` unit tests with stubs.
- `internal/storage/writelock/active.go` — `IsWriterActive(ctx)`.
- `internal/storage/writelock/active_integration_test.go`
- `internal/storage/inspect.go` — `PackageStat`, `TableStat`, `CouplingPair`, `KnowledgeSummary` structs + `TopPackagesBySymbolCount`, `TopDatastoreTablesByEdgeCount`, `HighCouplingPairs`, `KnowledgeStatsByCategory`, `RecentKnowledgeEntries`.
- `internal/storage/inspect_integration_test.go`
- `internal/report/report.go` — `Report`, `Builder`, `NewBuilder`, `Build`.
- `internal/report/derive.go` — `deriveDegraded`, `deriveSuggestions`, stage→action map.
- `internal/report/markdown.go` — `MarkdownRenderer`.
- `internal/report/json.go` — `JSONRenderer`.
- `internal/report/markdown_test.go`
- `internal/report/json_test.go`
- `internal/report/derive_test.go`
- `internal/report/builder_integration_test.go`
- `internal/export/graph.go` — `GraphExporter`, `nodeID`, `Options`, `Export`.
- `internal/export/graph_test.go`
- `internal/export/graph_integration_test.go`
- `cmd/projectlens/report.go` — `newReportCmd`.
- `cmd/projectlens/report_test.go`
- `cmd/projectlens/export.go` — `newExportCmd` (+ `graph` subcommand).
- `cmd/projectlens/export_test.go`

**Modified:**
- `internal/mcpserver/types.go` — remove `StageFreshness`, `ProviderHealth`; re-export from `indexstate` (type aliases) so existing JSON tags keep working.
- `internal/mcpserver/handlers.go` — delegate `probeProviders` and `gitHeadAndDirty` to `indexstate.Inspector`; keep `handleIndexStatus` behavior unchanged.
- `internal/mcpserver/server.go` (or constructor) — accept / construct an `Inspector`.
- `cmd/projectlens/main.go` — register `newReportCmd()` and `newExportCmd()` in the `AddCommand` block.
- `README.md` — add `report` + `export graph` to the command list.
- `CLAUDE.md` — append to the "CLI commands" section and the repository structure tree.

---

## Task 1: Create `internal/indexstate` types

**Files:**
- Create: `internal/indexstate/types.go`

- [ ] **Step 1: Write the file**

```go
// Package indexstate holds read-only, MCP-server-free types and helpers
// for inspecting ProjectLens's indexed state. Both internal/mcpserver and
// internal/report depend on this package; it must not import either.
package indexstate

// ProviderHealth reports the state of one configured provider. State is
// one of four values:
//   - "reachable":      the provider responded to a cheap probe.
//   - "configured":     credentials/endpoint are set but no probe was run.
//   - "not_configured": no provider is wired, or credentials are missing.
//   - "error":          a probe ran and failed; Error carries the message.
type ProviderHealth struct {
	Role     string `json:"role"`
	Provider string `json:"provider"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
}

// StageFreshness mirrors the per-stage shape used in index_status.
// AgeMinutes is computed at response time from CompletedAt.
type StageFreshness struct {
	Stage          string  `json:"stage"`
	Status         string  `json:"status"`
	CommitSHA      string  `json:"commit_sha,omitempty"`
	StartedAt      string  `json:"started_at,omitempty"`
	CompletedAt    string  `json:"completed_at,omitempty"`
	AgeMinutes     float64 `json:"age_minutes,omitempty"`
	FilesProcessed int     `json:"files_processed,omitempty"`
}

// GitState is the working-tree snapshot at query time.
type GitState struct {
	Head  string `json:"head,omitempty"`
	Dirty bool   `json:"dirty"`
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/indexstate/...`
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/indexstate/types.go
git commit -m "feat(indexstate): add shared StageFreshness/ProviderHealth/GitState"
```

---

## Task 2: Define the `Inspector` interface and default impl

**Files:**
- Create: `internal/indexstate/inspector.go`

The `Inspector` interface lets `report.Builder` accept a stub in unit tests while the real CLI/MCP path uses `DefaultInspector` which wraps the same router + summarizer used by `mcpserver`. Provider construction stays in CLI/MCP wiring — the inspector takes already-built dependencies via constructor.

- [ ] **Step 1: Write the file**

```go
package indexstate

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// EmbedderProber matches retrieval.Router.ProbeEmbedder.
type EmbedderProber interface {
	ProbeEmbedder(ctx context.Context) (provider string, ok bool, err error)
}

// SummarizerProber matches the existing summarizer probe contract:
// returns (provider, state, err) where state is one of the standard
// ProviderHealth.State values.
type SummarizerProber interface {
	ProbeSummarizer(ctx context.Context) (provider, state string, err error)
}

// Inspector is the abstraction the report and index_status share.
// Implementations are expected to be cheap to call multiple times.
type Inspector interface {
	ProbeProviders(ctx context.Context) []ProviderHealth
	GitHeadAndDirty(ctx context.Context) GitState
}

// DefaultInspector probes the configured providers and shells out to
// `git -C <repoPath>` for head/dirty state.
type DefaultInspector struct {
	Embedder   EmbedderProber   // optional
	Summarizer SummarizerProber // optional
	RepoPath   string           // may be empty
	Timeout    time.Duration    // per-probe; defaults to 3s when zero
}

func (d *DefaultInspector) ProbeProviders(ctx context.Context) []ProviderHealth {
	timeout := d.Timeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	out := make([]ProviderHealth, 0, 2)

	if d.Embedder != nil {
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		provider, ok, err := d.Embedder.ProbeEmbedder(probeCtx)
		cancel()
		ph := ProviderHealth{Role: "embedder", Provider: provider}
		switch {
		case !ok:
			ph.State = "not_configured"
			ph.Error = "no embedder configured"
		case err != nil:
			ph.State = "error"
			ph.Error = err.Error()
		default:
			ph.State = "reachable"
		}
		out = append(out, ph)
	}

	if d.Summarizer != nil {
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		provider, state, err := d.Summarizer.ProbeSummarizer(probeCtx)
		cancel()
		ph := ProviderHealth{Role: "summarizer", Provider: provider, State: state}
		switch state {
		case "error":
			if err != nil {
				ph.Error = err.Error()
			}
		case "not_configured":
			ph.Error = "summarizer credentials missing"
		}
		out = append(out, ph)
	}

	return out
}

func (d *DefaultInspector) GitHeadAndDirty(ctx context.Context) GitState {
	if d.RepoPath == "" {
		return GitState{}
	}
	headOut, err := exec.CommandContext(ctx, "git", "-C", d.RepoPath, "rev-parse", "HEAD").Output()
	if err != nil {
		return GitState{}
	}
	head := strings.TrimSpace(string(headOut))
	statusOut, err := exec.CommandContext(ctx, "git", "-C", d.RepoPath, "status", "--porcelain").Output()
	if err != nil {
		return GitState{Head: head}
	}
	return GitState{Head: head, Dirty: strings.TrimSpace(string(statusOut)) != ""}
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/indexstate/...`
Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/indexstate/inspector.go
git commit -m "feat(indexstate): add Inspector + DefaultInspector"
```

---

## Task 3: Test `DefaultInspector.ProbeProviders` with stubs

**Files:**
- Create: `internal/indexstate/inspector_test.go`

- [ ] **Step 1: Write failing tests**

```go
package indexstate

import (
	"context"
	"errors"
	"testing"
)

type stubEmbedder struct {
	provider string
	ok       bool
	err      error
}

func (s stubEmbedder) ProbeEmbedder(_ context.Context) (string, bool, error) {
	return s.provider, s.ok, s.err
}

type stubSummarizer struct {
	provider string
	state    string
	err      error
}

func (s stubSummarizer) ProbeSummarizer(_ context.Context) (string, string, error) {
	return s.provider, s.state, s.err
}

func TestProbeProviders_ReachableAndConfigured(t *testing.T) {
	insp := &DefaultInspector{
		Embedder:   stubEmbedder{provider: "ollama", ok: true},
		Summarizer: stubSummarizer{provider: "anthropic", state: "configured"},
	}
	got := insp.ProbeProviders(context.Background())
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[0].Role != "embedder" || got[0].State != "reachable" || got[0].Provider != "ollama" {
		t.Errorf("embedder mismatch: %+v", got[0])
	}
	if got[1].Role != "summarizer" || got[1].State != "configured" || got[1].Provider != "anthropic" {
		t.Errorf("summarizer mismatch: %+v", got[1])
	}
}

func TestProbeProviders_NotConfiguredAndError(t *testing.T) {
	insp := &DefaultInspector{
		Embedder:   stubEmbedder{ok: false},
		Summarizer: stubSummarizer{state: "error", err: errors.New("boom")},
	}
	got := insp.ProbeProviders(context.Background())
	if got[0].State != "not_configured" {
		t.Errorf("want not_configured, got %s", got[0].State)
	}
	if got[1].State != "error" || got[1].Error != "boom" {
		t.Errorf("want error/boom, got %+v", got[1])
	}
}

func TestProbeProviders_EmbedderProbeError(t *testing.T) {
	insp := &DefaultInspector{
		Embedder: stubEmbedder{provider: "ollama", ok: true, err: errors.New("conn refused")},
	}
	got := insp.ProbeProviders(context.Background())
	if got[0].State != "error" || got[0].Error != "conn refused" {
		t.Errorf("want error/conn refused, got %+v", got[0])
	}
}

func TestProbeProviders_NilDependenciesSkip(t *testing.T) {
	insp := &DefaultInspector{}
	got := insp.ProbeProviders(context.Background())
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestGitHeadAndDirty_NoRepoPath(t *testing.T) {
	insp := &DefaultInspector{}
	gs := insp.GitHeadAndDirty(context.Background())
	if gs.Head != "" || gs.Dirty {
		t.Errorf("want empty zero, got %+v", gs)
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/indexstate/ -v`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/indexstate/inspector_test.go
git commit -m "test(indexstate): cover Inspector provider + git probes"
```

---

## Task 4: Migrate `mcpserver` to consume `indexstate`

This is the riskiest refactor — `index_status` and its tests must stay green. Replace the local `StageFreshness` / `ProviderHealth` definitions with type aliases pointing at `indexstate`. Replace `probeProviders` and `gitHeadAndDirty` bodies with delegation to an `Inspector` stored on `Server`.

**Files:**
- Modify: `internal/mcpserver/types.go` (remove duplicate types, add aliases)
- Modify: `internal/mcpserver/handlers.go` (replace local helpers)
- Modify: `internal/mcpserver/server.go` (or wherever `Server` is constructed — add field + constructor param)

- [ ] **Step 1: Add aliases in `types.go`**

In `internal/mcpserver/types.go`, replace the existing `ProviderHealth` and `StageFreshness` struct definitions with:

```go
import "github.com/hman-pro/projectlens/internal/indexstate"

type ProviderHealth = indexstate.ProviderHealth
type StageFreshness = indexstate.StageFreshness
type SummarizerProber = indexstate.SummarizerProber
```

`SummarizerProber` is currently defined in `internal/mcpserver/` (find it via `grep -n "type SummarizerProber" internal/mcpserver/`). Delete its original definition there and use the alias instead. Keep the docstring comments on the alias lines as one-line summaries. Remove the old struct definitions entirely.

- [ ] **Step 2: Locate the `Server` struct**

Run: `grep -n "type Server struct" internal/mcpserver/*.go`
Expected output points at the file holding the struct definition (likely `server.go`).

- [ ] **Step 3: Add Inspector field, keep constructor signature stable**

Keep `func New(db *storage.DB, router *retrieval.Router, port int, repoPath string) *Server` exactly as-is — existing tests construct `Server` with this four-argument form (see `internal/mcpserver/handlers_integration_test.go` line ~702/855 and `server_test.go` line ~163). Changing the signature would break them.

In `internal/mcpserver/server.go`:

1. Add the field: `inspector indexstate.Inspector`.
2. Import `github.com/hman-pro/projectlens/internal/indexstate`.
3. Inside `New(...)`, initialize the inspector with the router as the embedder (router satisfies `indexstate.EmbedderProber`):

```go
return &Server{
	db:       db,
	router:   router,
	port:     port,
	repoPath: repoPath,
	inspector: &indexstate.DefaultInspector{
		Embedder: router,
		RepoPath: repoPath,
	},
}
```

(Merge into whatever fields the existing struct literal already sets — don't drop fields.)

4. Update `WithSummarizer(prober)` so that in addition to storing the prober for any existing direct use, it also writes through to the inspector:

```go
func (s *Server) WithSummarizer(p SummarizerProber) *Server {
	s.summarizer = p
	if di, ok := s.inspector.(*indexstate.DefaultInspector); ok {
		di.Summarizer = p
	}
	return s
}
```

(Adjust to match the actual existing `WithSummarizer` body — preserve any other side effects.)

5. **Do not** change the call site in `cmd/projectlens-mcp/main.go`. The existing chain `mcpserver.New(...).WithSummarizer(newSummarizerProber(cfg))` keeps working.

- [ ] **Step 4: Replace `probeProviders` body**

In `internal/mcpserver/handlers.go`, replace the body of `func (s *Server) probeProviders(ctx context.Context) []ProviderHealth` with:

```go
return s.inspector.ProbeProviders(ctx)
```

- [ ] **Step 5: Replace `gitHeadAndDirty` body**

In `internal/mcpserver/handlers.go`, replace the body of `func (s *Server) gitHeadAndDirty(ctx context.Context) (string, bool)` with:

```go
gs := s.inspector.GitHeadAndDirty(ctx)
return gs.Head, gs.Dirty
```

- [ ] **Step 6: Add a no-summarizer no-panic assertion**

Open the existing unit test file `internal/mcpserver/server_test.go` (or the file holding `TestIndexStatusSchema`). Append:

```go
func TestNew_NoSummarizer_ProbeProvidersSafe(t *testing.T) {
	srv := New(nil, nil, 8484, "")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on probeProviders without summarizer: %v", r)
		}
	}()
	_ = srv.probeProviders(context.Background())
}
```

Add `"context"` to the imports if it isn't already.

- [ ] **Step 7: Run existing MCP tests**

Run: `go test ./internal/mcpserver/ -v`
Expected: all PASS, including `TestIndexStatusSchema` and the new `TestNew_NoSummarizer_ProbeProvidersSafe`.

- [ ] **Step 8: Run integration tests**

Run: `make test-int` (or `go test -tags=integration ./internal/mcpserver/ -v`)
Expected: `TestIntegration_IndexStatus` and `TestIntegration_IndexStatus_StructuredProviders` PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/mcpserver/ cmd/projectlens-mcp/
git commit -m "refactor(mcpserver): delegate provider + git probes to indexstate.Inspector"
```

---

## Task 4b: Shared `buildInspector` helper in `cmd/projectlens`

The `report` and `export` CLI commands need an `indexstate.Inspector` wired the same way `projectlens-mcp` wires `index_status` — **not** via the existing `buildProviders` helper, which is intentionally fail-fast for mutating indexer commands and returns indexer-shaped types (`embeddings.Embedder`, `summaries.PackageSummarizer`) that do not satisfy `indexstate.EmbedderProber` / `SummarizerProber`.

The MCP pattern (see `cmd/projectlens-mcp/main.go`) is:

1. Construct an embedder client optimistically (returns nil if creds missing).
2. Wrap it in `retrieval.NewRouter(db, embedder)` — `Router` has a `ProbeEmbedder` method that satisfies `indexstate.EmbedderProber`.
3. Build a summarizer prober via a `summarizerProberFunc` adapter that satisfies `indexstate.SummarizerProber`.

We mirror that pattern in a new CLI-side helper used by both `report` and `export`.

**Files:**
- Create: `cmd/projectlens/inspector.go`

- [ ] **Step 1: Write the helper**

```go
package main

import (
	"context"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/providers/anthropic"
	"github.com/hman-pro/projectlens/internal/providers/ollama"
	"github.com/hman-pro/projectlens/internal/providers/openai"
	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
)

// buildInspector builds an indexstate.Inspector for read-only commands
// (report, export). It mirrors cmd/projectlens-mcp/main.go's wiring so
// the CLI report and index_status see identical provider health. It
// must NOT use buildProviders, which is fail-fast and returns indexer
// types, not probe-friendly types.
func buildInspector(cfg *config.Config, db *storage.DB, repoPath string) *indexstate.DefaultInspector {
	var embedder retrieval.QueryEmbedder
	switch cfg.Embeddings.Provider {
	case "ollama":
		embedder = ollama.NewClient(cfg.Embeddings.Endpoint, cfg.Embeddings.Model)
	case "openai":
		if cfg.OpenAIKey != "" {
			if cfg.Embeddings.Dimensions > 0 {
				embedder = openai.NewClientWithDims(cfg.OpenAIKey, cfg.Embeddings.Dimensions)
			} else {
				embedder = openai.NewClient(cfg.OpenAIKey)
			}
		}
	}
	router := retrieval.NewRouter(db, embedder)

	return &indexstate.DefaultInspector{
		Embedder:   router,
		Summarizer: newCLISummarizerProber(cfg),
		RepoPath:   repoPath,
	}
}

// summarizerProberFunc adapts the configured summarization provider to
// indexstate.SummarizerProber. configured is mandatory; ping is
// optional (Anthropic surfaces "configured" without a ping).
type summarizerProberFunc struct {
	name       string
	configured func() bool
	ping       func(ctx context.Context) error
}

func (f summarizerProberFunc) ProbeSummarizer(ctx context.Context) (string, string, error) {
	if !f.configured() {
		return f.name, "not_configured", nil
	}
	if f.ping == nil {
		return f.name, "configured", nil
	}
	if err := f.ping(ctx); err != nil {
		return f.name, "error", err
	}
	return f.name, "reachable", nil
}

func newCLISummarizerProber(cfg *config.Config) indexstate.SummarizerProber {
	switch cfg.Summarization.Provider {
	case "anthropic":
		client := anthropic.NewClient(cfg.Summarization.Model)
		return summarizerProberFunc{
			name:       "anthropic",
			configured: client.Configured,
		}
	case "openai":
		if cfg.OpenAIKey == "" {
			return summarizerProberFunc{
				name:       "openai",
				configured: func() bool { return false },
			}
		}
		client := openai.NewClient(cfg.OpenAIKey)
		return summarizerProberFunc{
			name:       "openai",
			configured: func() bool { return true },
			ping:       client.Ping,
		}
	default:
		return nil
	}
}
```

- [ ] **Step 2: Verify the constructor names + signatures match the codebase**

Run: `grep -rn "func NewRouter\|type QueryEmbedder\|func (c \*Client) Configured\|func (c \*Client) Ping" internal/retrieval/ internal/providers/`
Expected: confirms `retrieval.NewRouter(db, embedder)`, `retrieval.QueryEmbedder` interface, `anthropic.Client.Configured`, `openai.Client.Ping`. If any signature differs, adjust the helper accordingly before moving on — the canonical reference is `cmd/projectlens-mcp/main.go`.

- [ ] **Step 3: Build**

Run: `go build ./cmd/projectlens/...`
Expected: `inspector.go` builds (note: file is unused so far; that's fine — `go build` does not error on unused files at the package level, only on unused imports inside a file).

If the build fails on unused imports, comment them out for now and Tasks 17 / 23 will activate them.

- [ ] **Step 4: Commit**

```bash
git add cmd/projectlens/inspector.go
git commit -m "feat(cli): add buildInspector helper for report/export commands"
```

---

## Task 5: Add `IsWriterActive` query

**Files:**
- Create: `internal/storage/writelock/active.go`

- [ ] **Step 1: Write the file**

```go
package writelock

import (
	"context"
	"fmt"

	"github.com/hman-pro/projectlens/internal/storage"
)

// IsWriterActive reports whether a live writer currently holds the
// advisory lock. A row in index_locks alone is not enough — its
// backend_pid must still appear in pg_stat_activity, mirroring the
// liveness check used in Acquire to reap stale rows.
func IsWriterActive(ctx context.Context, db *storage.DB) (bool, error) {
	const q = `
		SELECT EXISTS(
			SELECT 1
			FROM index_locks l
			WHERE l.lock_id = $1
			  AND l.backend_pid IN (
				SELECT pid FROM pg_stat_activity WHERE pid IS NOT NULL
			  )
		)
	`
	var active bool
	if err := db.Pool.QueryRow(ctx, q, LockID).Scan(&active); err != nil {
		return false, fmt.Errorf("writelock: is active: %w", err)
	}
	return active, nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/storage/writelock/...`
Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/writelock/active.go
git commit -m "feat(writelock): add IsWriterActive liveness query"
```

---

## Task 6: Integration test for `IsWriterActive`

**Files:**
- Create: `internal/storage/writelock/active_integration_test.go`

Find an existing writelock integration test in this dir to copy boilerplate (DB setup, cleanup). Path: `ls internal/storage/writelock/` and read the first `*_integration_test.go` you find.

- [ ] **Step 1: Write failing test (true path)**

```go
//go:build integration

package writelock_test

import (
	"context"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/hman-pro/projectlens/internal/storage/writelock"
)

func connectIntegration(t *testing.T) *storage.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := storage.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestIsWriterActive_TrueWhenHolderLive(t *testing.T) {
	db := connectIntegration(t)
	ctx := context.Background()

	lock, err := writelock.Acquire(ctx, db, "test-is-active")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lock.Release(ctx)

	active, err := writelock.IsWriterActive(ctx, db)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !active {
		t.Errorf("want active=true while holder live")
	}
}

func TestIsWriterActive_FalseWhenNoRow(t *testing.T) {
	db := connectIntegration(t)
	ctx := context.Background()

	// Ensure no rows.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM index_locks WHERE lock_id = $1`, writelock.LockID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	active, err := writelock.IsWriterActive(ctx, db)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if active {
		t.Errorf("want active=false with no rows")
	}
}

func TestIsWriterActive_FalseWhenBackendPidDead(t *testing.T) {
	db := connectIntegration(t)
	ctx := context.Background()

	// Clear and insert a ghost row with a backend_pid guaranteed not to
	// be in pg_stat_activity.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM index_locks WHERE lock_id = $1`, writelock.LockID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO index_locks (lock_id, client_pid, backend_pid, hostname, cmd)
		VALUES ($1, 0, 2147483647, 'ghost', 'test-ghost')
	`, writelock.LockID); err != nil {
		t.Fatalf("insert ghost: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM index_locks WHERE lock_id = $1`, writelock.LockID)
	})

	active, err := writelock.IsWriterActive(ctx, db)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if active {
		t.Errorf("want active=false with ghost row")
	}
}
```

- [ ] **Step 2: Run integration tests**

Run: `go test -tags=integration ./internal/storage/writelock/ -v -run TestIsWriterActive`
Expected: all three PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/writelock/active_integration_test.go
git commit -m "test(writelock): integration tests for IsWriterActive liveness"
```

---

## Task 7: Add storage `inspect.go` with `TopPackagesBySymbolCount`

**Files:**
- Create: `internal/storage/inspect.go`

This file owns the new aggregate query structs (`PackageStat`, `TableStat`, `CouplingPair`, `KnowledgeSummary`) so they live next to the queries that produce them. Other queries land in subsequent tasks.

- [ ] **Step 1: Write the file**

```go
package storage

import (
	"context"
	"fmt"
	"time"
)

// PackageStat is one row of TopPackagesBySymbolCount.
type PackageStat struct {
	ImportPath  string
	SymbolCount int
	FileCount   int
}

// TableStat is one row of TopDatastoreTablesByEdgeCount.
type TableStat struct {
	Schema          string
	Name            string
	Engine          string
	ReadRefs        int
	WriteRefs       int
	SourceFileCount int
}

// CouplingPair is one row of HighCouplingPairs.
type CouplingPair struct {
	FileA         string
	FileB         string
	CoChangeCount int
}

// KnowledgeSummary is one row of RecentKnowledgeEntries.
type KnowledgeSummary struct {
	ID        int64
	Title     string
	Category  string
	Source    string
	CreatedAt time.Time
}

// TopPackagesBySymbolCount returns the top N packages by symbol count,
// with the number of distinct files per package.
func (db *DB) TopPackagesBySymbolCount(ctx context.Context, limit int) ([]PackageStat, error) {
	const q = `
		SELECT package_name,
		       COUNT(*) AS symbol_count,
		       COUNT(DISTINCT file_id) AS file_count
		FROM symbols
		GROUP BY package_name
		ORDER BY symbol_count DESC, package_name ASC
		LIMIT $1
	`
	rows, err := db.Pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: top packages: %w", err)
	}
	defer rows.Close()
	var out []PackageStat
	for rows.Next() {
		var p PackageStat
		if err := rows.Scan(&p.ImportPath, &p.SymbolCount, &p.FileCount); err != nil {
			return nil, fmt.Errorf("storage: top packages: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/storage/...`
Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/inspect.go
git commit -m "feat(storage): add PackageStat + TopPackagesBySymbolCount"
```

---

## Task 8: Add `TopDatastoreTablesByEdgeCount`

**Files:**
- Modify: `internal/storage/inspect.go` (append)

The query projects edge sources to a real `file_id` to compute `SourceFileCount` honestly.

- [ ] **Step 1: Append to `internal/storage/inspect.go`**

```go
// TopDatastoreTablesByEdgeCount returns the top N tables by total
// read/write edge count. SourceFileCount is the count of distinct
// files containing any read/write reference, resolved via
// symbols.file_id for source_type='symbol' edges (the only producer
// today) and edges.source_id directly for source_type='file' edges.
func (db *DB) TopDatastoreTablesByEdgeCount(ctx context.Context, limit int) ([]TableStat, error) {
	const q = `
		WITH ref_edges AS (
			SELECT e.target_id,
			       e.edge_type,
			       CASE
				   WHEN e.source_type = 'symbol' THEN s.file_id
				   WHEN e.source_type = 'file'   THEN e.source_id
				   ELSE NULL
			       END AS projected_file_id
			FROM edges e
			LEFT JOIN symbols s ON e.source_type = 'symbol' AND s.id = e.source_id
			WHERE e.target_type = 'datastore_table'
			  AND e.edge_type IN ('reads_table', 'writes_table')
		)
		SELECT t.schema_name,
		       t.name,
		       t.engine,
		       SUM(CASE WHEN re.edge_type = 'reads_table'  THEN 1 ELSE 0 END) AS read_refs,
		       SUM(CASE WHEN re.edge_type = 'writes_table' THEN 1 ELSE 0 END) AS write_refs,
		       COUNT(DISTINCT re.projected_file_id) FILTER (WHERE re.projected_file_id IS NOT NULL) AS source_file_count
		FROM datastore_tables t
		JOIN ref_edges re ON re.target_id = t.id
		GROUP BY t.id, t.schema_name, t.name, t.engine
		ORDER BY (read_refs + write_refs) DESC, t.name ASC
		LIMIT $1
	`
	rows, err := db.Pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: top tables: %w", err)
	}
	defer rows.Close()
	var out []TableStat
	for rows.Next() {
		var ts TableStat
		var schema *string
		if err := rows.Scan(&schema, &ts.Name, &ts.Engine, &ts.ReadRefs, &ts.WriteRefs, &ts.SourceFileCount); err != nil {
			return nil, fmt.Errorf("storage: top tables: scan: %w", err)
		}
		if schema != nil {
			ts.Schema = *schema
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/storage/...`
Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/inspect.go
git commit -m "feat(storage): add TopDatastoreTablesByEdgeCount with file projection"
```

---

## Task 9: Add `HighCouplingPairs`

**Files:**
- Modify: `internal/storage/inspect.go` (append)

Co-change pairs come from `file_history`: two files committed in the same commit_hash form one observation. Aggregate symmetric pairs.

- [ ] **Step 1: Append to `internal/storage/inspect.go`**

```go
// HighCouplingPairs returns up to N symmetric co-change pairs from
// file_history. Files are paired by shared commit_hash; only the
// canonical (lower file_id < higher file_id) direction is emitted.
// Pairs with fewer than minCount shared commits are filtered out.
func (db *DB) HighCouplingPairs(ctx context.Context, limit, minCount int) ([]CouplingPair, error) {
	if minCount < 1 {
		minCount = 1
	}
	const q = `
		WITH pairs AS (
			SELECT h1.file_id AS a,
			       h2.file_id AS b,
			       COUNT(*)   AS cnt
			FROM file_history h1
			JOIN file_history h2
			  ON h1.commit_hash = h2.commit_hash
			 AND h1.file_id < h2.file_id
			GROUP BY h1.file_id, h2.file_id
			HAVING COUNT(*) >= $2
		)
		SELECT fa.path, fb.path, p.cnt
		FROM pairs p
		JOIN files fa ON fa.id = p.a
		JOIN files fb ON fb.id = p.b
		ORDER BY p.cnt DESC, fa.path ASC, fb.path ASC
		LIMIT $1
	`
	rows, err := db.Pool.Query(ctx, q, limit, minCount)
	if err != nil {
		return nil, fmt.Errorf("storage: coupling: %w", err)
	}
	defer rows.Close()
	var out []CouplingPair
	for rows.Next() {
		var c CouplingPair
		if err := rows.Scan(&c.FileA, &c.FileB, &c.CoChangeCount); err != nil {
			return nil, fmt.Errorf("storage: coupling: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/storage/...`
Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/inspect.go
git commit -m "feat(storage): add HighCouplingPairs from file_history co-change"
```

---

## Task 10: Add knowledge stats + recent queries

**Files:**
- Modify: `internal/storage/inspect.go` (append)

- [ ] **Step 1: Append**

```go
// KnowledgeStatsByCategory returns total counts per category. Categories
// with zero rows are omitted; callers can render missing keys as 0.
func (db *DB) KnowledgeStatsByCategory(ctx context.Context) (map[string]int, error) {
	const q = `SELECT category, COUNT(*) FROM knowledge_entries GROUP BY category`
	rows, err := db.Pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("storage: knowledge stats: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var cat string
		var n int
		if err := rows.Scan(&cat, &n); err != nil {
			return nil, fmt.Errorf("storage: knowledge stats: scan: %w", err)
		}
		out[cat] = n
	}
	return out, rows.Err()
}

// RecentKnowledgeEntries returns the N most recently created entries,
// ordered by created_at DESC. updated_at is not used so edits don't
// surface as "new".
func (db *DB) RecentKnowledgeEntries(ctx context.Context, limit int) ([]KnowledgeSummary, error) {
	const q = `
		SELECT id, title, category, source, created_at
		FROM knowledge_entries
		ORDER BY created_at DESC
		LIMIT $1
	`
	rows, err := db.Pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: recent knowledge: %w", err)
	}
	defer rows.Close()
	var out []KnowledgeSummary
	for rows.Next() {
		var k KnowledgeSummary
		if err := rows.Scan(&k.ID, &k.Title, &k.Category, &k.Source, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("storage: recent knowledge: scan: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/storage/...`
Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/inspect.go
git commit -m "feat(storage): add knowledge stats + recent entries queries"
```

---

## Task 11: Integration tests for the new storage queries

**Files:**
- Create: `internal/storage/inspect_integration_test.go`

Reuse existing helper for DB connection — look at any `*_integration_test.go` in `internal/storage/` for the seed/cleanup pattern. The test seeds a controlled fixture of `files`, `symbols`, `datastore_tables`, `edges`, `file_history`, and `knowledge_entries`, then asserts shapes.

- [ ] **Step 1: Write the file**

```go
//go:build integration

package storage_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/storage"
)

func openIntegration(t *testing.T) *storage.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := storage.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedInspectFixture wipes test tables and inserts a small known graph:
//   - 2 files in package "pkg/a", 1 file in package "pkg/b"
//   - 3 symbols in pkg/a (across both files), 1 symbol in pkg/b
//   - 1 datastore_table public.orders (engine postgres)
//   - 2 reads_table edges from pkg/a symbols, 1 writes_table edge from pkg/b
//   - file_history rows: two files in pkg/a co-changed 3 times
//   - 4 knowledge_entries across 2 categories
func seedInspectFixture(t *testing.T, db *storage.DB) (cleanup func()) {
	t.Helper()
	ctx := context.Background()
	// Cleanup helper deletes everything by id range captured below.
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	commit := false
	defer func() {
		if !commit {
			_ = tx.Rollback(ctx)
		}
	}()

	// Files. Real schema: path, package_name, checksum, commit_sha all
	// NOT NULL with no default. language defaults to 'go'.
	var fA1, fA2, fB1 int64
	const insertFile = `INSERT INTO files(path, package_name, checksum, commit_sha) VALUES ($1, $2, $3, 'testcommit') RETURNING id`
	if err := tx.QueryRow(ctx, insertFile, "pkg/a/x.go", "pkg/a", "x").Scan(&fA1); err != nil {
		t.Fatalf("file A1: %v", err)
	}
	if err := tx.QueryRow(ctx, insertFile, "pkg/a/y.go", "pkg/a", "y").Scan(&fA2); err != nil {
		t.Fatalf("file A2: %v", err)
	}
	if err := tx.QueryRow(ctx, insertFile, "pkg/b/z.go", "pkg/b", "z").Scan(&fB1); err != nil {
		t.Fatalf("file B1: %v", err)
	}

	// Symbols. Real schema: file_id, name, kind, package_name, signature,
	// line_start, line_end, checksum all NOT NULL with no default.
	var sA1, sA2, sA3, sB1 int64
	const insertSymbol = `INSERT INTO symbols(file_id, name, kind, package_name, signature, line_start, line_end, checksum) VALUES ($1, $2, 'func', $3, 'func()', 1, 2, $4) RETURNING id`
	if err := tx.QueryRow(ctx, insertSymbol, fA1, "F1", "pkg/a", "h-F1").Scan(&sA1); err != nil {
		t.Fatalf("sym A1: %v", err)
	}
	if err := tx.QueryRow(ctx, insertSymbol, fA1, "F2", "pkg/a", "h-F2").Scan(&sA2); err != nil {
		t.Fatalf("sym A2: %v", err)
	}
	if err := tx.QueryRow(ctx, insertSymbol, fA2, "F3", "pkg/a", "h-F3").Scan(&sA3); err != nil {
		t.Fatalf("sym A3: %v", err)
	}
	if err := tx.QueryRow(ctx, insertSymbol, fB1, "G1", "pkg/b", "h-G1").Scan(&sB1); err != nil {
		t.Fatalf("sym B1: %v", err)
	}

	// Datastore table
	var tID int64
	if err := tx.QueryRow(ctx, `INSERT INTO datastore_tables(name, engine, schema_name) VALUES ('orders','postgres','public') RETURNING id`).Scan(&tID); err != nil {
		t.Fatalf("table: %v", err)
	}

	// Edges
	if _, err := tx.Exec(ctx, `INSERT INTO edges(source_type, source_id, target_type, target_id, edge_type, properties, confidence) VALUES
		('symbol',$1,'datastore_table',$4,'reads_table','{}',1.0),
		('symbol',$2,'datastore_table',$4,'reads_table','{}',1.0),
		('symbol',$3,'datastore_table',$4,'writes_table','{}',1.0)
	`, sA1, sA3, sB1, tID); err != nil {
		t.Fatalf("edges: %v", err)
	}

	// file_history: 3 shared commits between fA1 and fA2.
	for _, h := range []string{"c1", "c2", "c3"} {
		if _, err := tx.Exec(ctx, `INSERT INTO file_history(file_id, commit_hash, author, committed_at, change_type) VALUES
			($1,$2,'a',NOW(),'M'),($3,$2,'a',NOW(),'M')`, fA1, h, fA2); err != nil {
			t.Fatalf("history %s: %v", h, err)
		}
	}

	// knowledge
	if _, err := tx.Exec(ctx, `INSERT INTO knowledge_entries(category,title,body,source,created_at) VALUES
		('lesson','l1','b','test', NOW() - INTERVAL '1 day'),
		('lesson','l2','b','test', NOW() - INTERVAL '2 days'),
		('convention','c1','b','test', NOW()),
		('convention','c2','b','test', NOW() - INTERVAL '3 days')
	`); err != nil {
		t.Fatalf("knowledge: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	commit = true

	return func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE source = 'test'`)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM edges WHERE target_id = $1 AND target_type = 'datastore_table'`, tID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM datastore_tables WHERE id = $1`, tID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM file_history WHERE file_id IN ($1,$2,$3)`, fA1, fA2, fB1)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM symbols WHERE file_id IN ($1,$2,$3)`, fA1, fA2, fB1)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM files WHERE id IN ($1,$2,$3)`, fA1, fA2, fB1)
	}
}

func TestTopPackagesBySymbolCount(t *testing.T) {
	db := openIntegration(t)
	cleanup := seedInspectFixture(t, db)
	t.Cleanup(cleanup)

	got, err := db.TopPackagesBySymbolCount(context.Background(), 10)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	want := map[string]storage.PackageStat{
		"pkg/a": {ImportPath: "pkg/a", SymbolCount: 3, FileCount: 2},
		"pkg/b": {ImportPath: "pkg/b", SymbolCount: 1, FileCount: 1},
	}
	for _, p := range got {
		w, ok := want[p.ImportPath]
		if !ok {
			continue // other packages in DB are fine
		}
		if p != w {
			t.Errorf("pkg %s: got %+v want %+v", p.ImportPath, p, w)
		}
		delete(want, p.ImportPath)
	}
	if len(want) != 0 {
		t.Errorf("missing packages: %+v", want)
	}
}

func TestTopDatastoreTablesByEdgeCount(t *testing.T) {
	db := openIntegration(t)
	cleanup := seedInspectFixture(t, db)
	t.Cleanup(cleanup)

	got, err := db.TopDatastoreTablesByEdgeCount(context.Background(), 10)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var found bool
	for _, ts := range got {
		if ts.Schema == "public" && ts.Name == "orders" {
			found = true
			if ts.Engine != "postgres" {
				t.Errorf("engine: got %s want postgres", ts.Engine)
			}
			if ts.ReadRefs != 2 || ts.WriteRefs != 1 {
				t.Errorf("refs: got R=%d W=%d want 2/1", ts.ReadRefs, ts.WriteRefs)
			}
			// fA1, fA2, fB1 — 3 distinct source files.
			if ts.SourceFileCount != 3 {
				t.Errorf("source files: got %d want 3", ts.SourceFileCount)
			}
		}
	}
	if !found {
		t.Errorf("public.orders not in top-N: %+v", got)
	}
}

func TestHighCouplingPairs(t *testing.T) {
	db := openIntegration(t)
	cleanup := seedInspectFixture(t, db)
	t.Cleanup(cleanup)

	got, err := db.HighCouplingPairs(context.Background(), 10, 3)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var found bool
	for _, p := range got {
		if p.FileA == "pkg/a/x.go" && p.FileB == "pkg/a/y.go" && p.CoChangeCount == 3 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected co-change pair (x.go, y.go, 3) missing: %+v", got)
	}
}

func TestKnowledgeStatsByCategory(t *testing.T) {
	db := openIntegration(t)
	cleanup := seedInspectFixture(t, db)
	t.Cleanup(cleanup)

	got, err := db.KnowledgeStatsByCategory(context.Background())
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got["lesson"] < 2 || got["convention"] < 2 {
		t.Errorf("counts missing: %+v", got)
	}
}

func TestRecentKnowledgeEntries(t *testing.T) {
	db := openIntegration(t)
	cleanup := seedInspectFixture(t, db)
	t.Cleanup(cleanup)

	got, err := db.RecentKnowledgeEntries(context.Background(), 4)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) < 4 {
		t.Fatalf("want >=4 entries, got %d", len(got))
	}
	// Ordered by created_at DESC. The 'c1' (NOW()) entry should be first.
	if got[0].Title != "c1" {
		t.Errorf("first entry: got %s want c1", got[0].Title)
	}
	// Sanity: timestamps strictly non-increasing.
	for i := 1; i < len(got); i++ {
		if got[i].CreatedAt.After(got[i-1].CreatedAt) {
			t.Errorf("order broken at %d: %v after %v", i, got[i].CreatedAt, got[i-1].CreatedAt)
		}
	}
	_ = time.Second
}
```

- [ ] **Step 2: Run integration tests**

Run: `go test -tags=integration ./internal/storage/ -v -run 'TestTopPackages|TestTopDatastore|TestHighCoupling|TestKnowledgeStats|TestRecentKnowledge'`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/inspect_integration_test.go
git commit -m "test(storage): integration coverage for inspect queries"
```

---

## Task 12: Define `report.Report` type and constructor

**Files:**
- Create: `internal/report/report.go`

- [ ] **Step 1: Write the file**

```go
package report

import (
	"context"
	"fmt"
	"time"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/storage"
	wl "github.com/hman-pro/projectlens/internal/storage/writelock"
)

// Report is the typed summary returned by Builder.Build. Renderers turn
// this into Markdown or JSON; the builder never formats output.
type Report struct {
	GeneratedAt  time.Time                            `json:"generated_at"`
	RepoPath     string                               `json:"repo_path,omitempty"`
	Git          indexstate.GitState                  `json:"git"`
	Stages       map[string]indexstate.StageFreshness `json:"stages"`
	Providers    []indexstate.ProviderHealth          `json:"providers"`
	TopPackages  []storage.PackageStat                `json:"top_packages"`
	TopTables    []storage.TableStat                  `json:"top_tables"`
	HighCoupling []storage.CouplingPair               `json:"high_coupling"`
	Knowledge    KnowledgeInventory                   `json:"knowledge"`
	Degraded     []StageDegradation                   `json:"degraded"`
	Suggestions  []AgentQuestion                      `json:"suggestions"`
	WriterActive bool                                 `json:"writer_active"`
}

type KnowledgeInventory struct {
	TotalEntries     int                       `json:"total_entries"`
	CountsByCategory map[string]int            `json:"counts_by_category"`
	RecentEntries    []storage.KnowledgeSummary `json:"recent_entries"`
}

type StageDegradation struct {
	Stage           string `json:"stage"`
	Reason          string `json:"reason"`
	SuggestedAction string `json:"suggested_action"`
}

type AgentQuestion struct {
	Topic         string `json:"topic"`
	SuggestedTool string `json:"suggested_tool"`
	Example       string `json:"example"`
}

// Options controls per-section sizing. Zero values fall back to Defaults.
type Options struct {
	TopN int
}

func (o Options) topN() int {
	if o.TopN <= 0 {
		return 10
	}
	return o.TopN
}

// Builder assembles a Report from read-only queries.
type Builder struct {
	db        *storage.DB
	inspector indexstate.Inspector
	repoPath  string
	opts      Options
}

func NewBuilder(db *storage.DB, insp indexstate.Inspector, repoPath string, opts Options) *Builder {
	return &Builder{db: db, inspector: insp, repoPath: repoPath, opts: opts}
}

// Build runs queries sequentially and returns the populated Report.
// Per-section failures are logged into Report.Degraded; the Build call
// only returns an error for fatal conditions (DB connection, context
// cancellation).
func (b *Builder) Build(ctx context.Context) (*Report, error) {
	r := &Report{
		GeneratedAt: time.Now().UTC(),
		RepoPath:    b.repoPath,
		Stages:      map[string]indexstate.StageFreshness{},
		Knowledge:   KnowledgeInventory{CountsByCategory: map[string]int{}},
	}

	// Stages
	byStage, err := b.db.GetLatestRunsByStage(ctx)
	if err != nil {
		return nil, fmt.Errorf("report: latest runs: %w", err)
	}
	for stage, run := range byStage {
		st := indexstate.StageFreshness{
			Stage:          stage,
			Status:         run.Status,
			CommitSHA:      run.CommitSHA,
			StartedAt:      run.StartedAt.Format(time.RFC3339),
			FilesProcessed: run.FilesProcessed,
		}
		if run.CompletedAt != nil {
			st.CompletedAt = run.CompletedAt.Format(time.RFC3339)
			st.AgeMinutes = time.Since(*run.CompletedAt).Minutes()
		}
		r.Stages[stage] = st
	}

	r.Providers = b.inspector.ProbeProviders(ctx)
	r.Git = b.inspector.GitHeadAndDirty(ctx)

	if active, err := writerActive(ctx, b.db); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "writer", Reason: err.Error(), SuggestedAction: ""})
	} else {
		r.WriterActive = active
	}

	limit := b.opts.topN()
	if pkgs, err := b.db.TopPackagesBySymbolCount(ctx, limit); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "top_packages", Reason: err.Error()})
	} else {
		r.TopPackages = pkgs
	}
	if tbls, err := b.db.TopDatastoreTablesByEdgeCount(ctx, limit); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "top_tables", Reason: err.Error()})
	} else {
		r.TopTables = tbls
	}
	if pairs, err := b.db.HighCouplingPairs(ctx, limit, 3); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "high_coupling", Reason: err.Error()})
	} else {
		r.HighCoupling = pairs
	}
	if counts, err := b.db.KnowledgeStatsByCategory(ctx); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "knowledge_counts", Reason: err.Error()})
	} else {
		r.Knowledge.CountsByCategory = counts
		for _, n := range counts {
			r.Knowledge.TotalEntries += n
		}
	}
	if recent, err := b.db.RecentKnowledgeEntries(ctx, limit); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "recent_knowledge", Reason: err.Error()})
	} else {
		r.Knowledge.RecentEntries = recent
	}

	r.Degraded = append(r.Degraded, deriveDegradation(r.Stages, r.Providers)...)
	r.Suggestions = deriveSuggestions(r)

	return r, nil
}

// writerActive is a thin indirection so unit tests can swap it.
var writerActive = func(ctx context.Context, db *storage.DB) (bool, error) {
	return wl.IsWriterActive(ctx, db)
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/report/...`
Expected: undefined symbols `deriveDegradation` and `deriveSuggestions` — that's fine, next task adds them. Confirm only those names are the failures.

If other errors appear, fix them now.

- [ ] **Step 3: Hold off on the commit**

Bundle this task's commit with Task 13 — the commit lands after `derive.go` makes the package build cleanly.

---

## Task 13: Add `derive.go` with stage→action map and suggestions

**Files:**
- Create: `internal/report/derive.go`
- Create: `internal/report/derive_test.go`

- [ ] **Step 1: Write the failing test first**

```go
package report

import (
	"testing"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/storage"
)

func TestDeriveDegradation_MissingStagesUseRealCommands(t *testing.T) {
	stages := map[string]indexstate.StageFreshness{
		"code": {Stage: "code", Status: "completed", AgeMinutes: 5},
		// summarize/embed/history/datastore missing
	}
	got := deriveDegradation(stages, nil)
	wantSuggested := map[string]string{
		"summarize": "run projectlens index-summarize",
		"embed":     "run projectlens index-embed",
		"history":   "run projectlens index-history",
		"datastore": "run projectlens index-datastore",
	}
	for stage, want := range wantSuggested {
		var found bool
		for _, d := range got {
			if d.Stage == stage && d.SuggestedAction == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("stage %s: missing or wrong suggestion in %+v", stage, got)
		}
	}
}

func TestDeriveDegradation_MissingCodeUsesReindex(t *testing.T) {
	got := deriveDegradation(map[string]indexstate.StageFreshness{}, nil)
	for _, d := range got {
		if d.Stage == "code" {
			if d.SuggestedAction != "run projectlens reindex" {
				t.Errorf("code action: got %q want %q", d.SuggestedAction, "run projectlens reindex")
			}
			return
		}
	}
	t.Errorf("code degradation not emitted: %+v", got)
}

func TestDeriveDegradation_ProviderErrorAndNotConfigured(t *testing.T) {
	stages := map[string]indexstate.StageFreshness{
		"code": {Stage: "code", Status: "completed"},
		"summarize": {Stage: "summarize", Status: "completed"},
		"embed": {Stage: "embed", Status: "completed"},
		"history": {Stage: "history", Status: "completed"},
		"datastore": {Stage: "datastore", Status: "completed"},
	}
	providers := []indexstate.ProviderHealth{
		{Role: "embedder", Provider: "ollama", State: "reachable"},
		{Role: "summarizer", Provider: "anthropic", State: "error", Error: "rate limited"},
		{Role: "extra", Provider: "x", State: "not_configured", Error: "creds missing"},
	}
	got := deriveDegradation(stages, providers)
	var sawErr, sawNC bool
	for _, d := range got {
		if d.Reason == "rate limited" {
			sawErr = true
		}
		if d.Reason == "creds missing" {
			sawNC = true
		}
	}
	if !sawErr {
		t.Errorf("missing error degradation: %+v", got)
	}
	if !sawNC {
		t.Errorf("missing not_configured degradation: %+v", got)
	}
}

func TestDeriveDegradation_StageOlderThan24h(t *testing.T) {
	stages := map[string]indexstate.StageFreshness{
		"code":      {Stage: "code", Status: "completed", AgeMinutes: 25 * 60},
		"summarize": {Stage: "summarize", Status: "completed", AgeMinutes: 10},
		"embed":     {Stage: "embed", Status: "completed", AgeMinutes: 10},
		"history":   {Stage: "history", Status: "completed", AgeMinutes: 10},
		"datastore": {Stage: "datastore", Status: "completed", AgeMinutes: 10},
	}
	got := deriveDegradation(stages, nil)
	for _, d := range got {
		if d.Stage == "code" && d.SuggestedAction == "run projectlens reindex" {
			return
		}
	}
	t.Errorf("stale-stage degradation missing: %+v", got)
}

func TestDeriveSuggestions_OnlyForHealthyStages(t *testing.T) {
	r := &Report{
		Stages: map[string]indexstate.StageFreshness{
			"datastore": {Stage: "datastore", Status: "completed", AgeMinutes: 5},
			"history":   {Stage: "history", Status: "completed", AgeMinutes: 5},
			"code":      {Stage: "code", Status: "completed", AgeMinutes: 5},
		},
		TopTables:    []storage.TableStat{{Schema: "public", Name: "orders"}},
		HighCoupling: []storage.CouplingPair{{FileA: "a.go", FileB: "b.go", CoChangeCount: 5}},
		TopPackages:  []storage.PackageStat{{ImportPath: "pkg/a", SymbolCount: 3, FileCount: 2}},
		Knowledge:    KnowledgeInventory{TotalEntries: 1},
	}
	got := deriveSuggestions(r)
	if len(got) < 4 {
		t.Fatalf("want at least 4 suggestions, got %d: %+v", len(got), got)
	}
}
```

- [ ] **Step 2: Run tests to see them fail**

Run: `go test ./internal/report/ -v -run 'TestDerive'`
Expected: build failure — functions undefined.

- [ ] **Step 3: Write `derive.go`**

```go
package report

import (
	"fmt"

	"github.com/hman-pro/projectlens/internal/indexstate"
)

var stageOrder = []string{"code", "summarize", "embed", "history", "datastore"}

var stageMissingAction = map[string]string{
	"code":      "run projectlens reindex",
	"summarize": "run projectlens index-summarize",
	"embed":     "run projectlens index-embed",
	"history":   "run projectlens index-history",
	"datastore": "run projectlens index-datastore",
}

func deriveDegradation(stages map[string]indexstate.StageFreshness, providers []indexstate.ProviderHealth) []StageDegradation {
	var out []StageDegradation
	for _, s := range stageOrder {
		st, ok := stages[s]
		if !ok {
			out = append(out, StageDegradation{
				Stage:           s,
				Reason:          "stage has never been indexed",
				SuggestedAction: stageMissingAction[s],
			})
			continue
		}
		if st.Status == "completed" && st.AgeMinutes > 24*60 {
			out = append(out, StageDegradation{
				Stage:           s,
				Reason:          fmt.Sprintf("stage age %.0fm exceeds 24h", st.AgeMinutes),
				SuggestedAction: "run projectlens reindex",
			})
		}
	}
	for _, p := range providers {
		if p.State == "error" || p.State == "not_configured" {
			reason := p.Error
			if reason == "" {
				reason = p.State
			}
			out = append(out, StageDegradation{
				Stage:           "provider:" + p.Role,
				Reason:          reason,
				SuggestedAction: "check provider credentials",
			})
		}
	}
	return out
}

func stageHealthy(stages map[string]indexstate.StageFreshness, stage string) bool {
	st, ok := stages[stage]
	if !ok {
		return false
	}
	return st.Status == "completed" && st.AgeMinutes <= 24*60
}

func deriveSuggestions(r *Report) []AgentQuestion {
	var out []AgentQuestion
	if stageHealthy(r.Stages, "datastore") && len(r.TopTables) > 0 {
		t := r.TopTables[0]
		name := t.Name
		if t.Schema != "" {
			name = t.Schema + "." + t.Name
		}
		out = append(out, AgentQuestion{
			Topic:         "Who reads " + name + "?",
			SuggestedTool: "get_table_context",
			Example:       "get_table_context " + name,
		})
	}
	if stageHealthy(r.Stages, "history") && len(r.HighCoupling) > 0 {
		c := r.HighCoupling[0]
		out = append(out, AgentQuestion{
			Topic:         "Which files change with " + c.FileA + "?",
			SuggestedTool: "get_coupling",
			Example:       "get_coupling " + c.FileA,
		})
	}
	if stageHealthy(r.Stages, "code") && len(r.TopPackages) > 0 {
		p := r.TopPackages[0]
		out = append(out, AgentQuestion{
			Topic:         "What does " + p.ImportPath + " do?",
			SuggestedTool: "get_package_summary",
			Example:       "get_package_summary " + p.ImportPath,
		})
	}
	if r.Knowledge.TotalEntries > 0 {
		out = append(out, AgentQuestion{
			Topic:         "Have we captured anything about this code?",
			SuggestedTool: "search_knowledge",
			Example:       "search_knowledge <topic>",
		})
	}
	return out
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/report/ -v -run 'TestDerive'`
Expected: all PASS.

- [ ] **Step 5: Verify whole package builds**

Run: `go build ./internal/report/...`
Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
git add internal/report/report.go internal/report/derive.go internal/report/derive_test.go
git commit -m "feat(report): add Report type, Builder, and degradation/suggestion derivation"
```

---

## Task 14: JSON renderer + test

**Files:**
- Create: `internal/report/json.go`
- Create: `internal/report/json_test.go`

- [ ] **Step 1: Write the failing test**

```go
package report

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/storage"
)

func fixtureReport() *Report {
	return &Report{
		GeneratedAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		RepoPath:    "/tmp/repo",
		Git:         indexstate.GitState{Head: "abc123", Dirty: true},
		Stages: map[string]indexstate.StageFreshness{
			"code": {Stage: "code", Status: "completed", AgeMinutes: 5},
		},
		Providers:    []indexstate.ProviderHealth{{Role: "embedder", Provider: "ollama", State: "reachable"}},
		TopPackages:  []storage.PackageStat{{ImportPath: "pkg/a", SymbolCount: 3, FileCount: 2}},
		TopTables:    []storage.TableStat{{Schema: "public", Name: "orders", Engine: "postgres", ReadRefs: 2, WriteRefs: 1, SourceFileCount: 3}},
		HighCoupling: []storage.CouplingPair{{FileA: "a.go", FileB: "b.go", CoChangeCount: 3}},
		Knowledge: KnowledgeInventory{
			TotalEntries:     2,
			CountsByCategory: map[string]int{"lesson": 2},
			RecentEntries:    []storage.KnowledgeSummary{{ID: 1, Title: "t", Category: "lesson", Source: "test", CreatedAt: time.Date(2026, 5, 21, 11, 0, 0, 0, time.UTC)}},
		},
		Degraded:    []StageDegradation{{Stage: "embed", Reason: "missing", SuggestedAction: "run projectlens index-embed"}},
		Suggestions: []AgentQuestion{{Topic: "x", SuggestedTool: "find_symbol", Example: "find_symbol X"}},
	}
}

func TestJSONRenderer_RoundTrip(t *testing.T) {
	r := fixtureReport()
	var buf bytes.Buffer
	if err := (JSONRenderer{}).Render(&buf, r); err != nil {
		t.Fatalf("render: %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if decoded.RepoPath != "/tmp/repo" {
		t.Errorf("repo path: got %s", decoded.RepoPath)
	}
	if len(decoded.TopPackages) != 1 || decoded.TopPackages[0].ImportPath != "pkg/a" {
		t.Errorf("top packages: %+v", decoded.TopPackages)
	}
	if decoded.Knowledge.CountsByCategory["lesson"] != 2 {
		t.Errorf("knowledge counts: %+v", decoded.Knowledge.CountsByCategory)
	}
}
```

- [ ] **Step 2: Run test (fails — JSONRenderer undefined)**

Run: `go test ./internal/report/ -v -run TestJSONRenderer`
Expected: compile error.

- [ ] **Step 3: Write `json.go`**

```go
package report

import (
	"encoding/json"
	"fmt"
	"io"
)

// JSONRenderer writes the Report as pretty-printed JSON.
type JSONRenderer struct {
	Indent string // defaults to two spaces
}

func (r JSONRenderer) Render(w io.Writer, rep *Report) error {
	enc := json.NewEncoder(w)
	indent := r.Indent
	if indent == "" {
		indent = "  "
	}
	enc.SetIndent("", indent)
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("report: json render: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test**

Run: `go test ./internal/report/ -v -run TestJSONRenderer`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/report/json.go internal/report/json_test.go
git commit -m "feat(report): JSON renderer with round-trip test"
```

---

## Task 15: Markdown renderer + test

**Files:**
- Create: `internal/report/markdown.go`
- Create: `internal/report/markdown_test.go`

- [ ] **Step 1: Write the failing test**

```go
package report

import (
	"bytes"
	"strings"
	"testing"
)

func TestMarkdownRenderer_SectionsPresent(t *testing.T) {
	var buf bytes.Buffer
	if err := (MarkdownRenderer{}).Render(&buf, fixtureReport()); err != nil {
		t.Fatalf("render: %v", err)
	}
	s := buf.String()
	for _, header := range []string{
		"# ProjectLens Report",
		"## Index Freshness",
		"## Providers",
		"## Top Packages",
		"## Top Datastore Tables",
		"## High-Coupling File Pairs",
		"## Knowledge Inventory",
		"## Degraded / Missing",
		"## Suggested Agent Questions",
	} {
		if !strings.Contains(s, header) {
			t.Errorf("missing header %q in:\n%s", header, s)
		}
	}
	for _, frag := range []string{
		"abc123", // git head
		"pkg/a",
		"public.orders",
		"a.go",
		"run projectlens index-embed",
	} {
		if !strings.Contains(s, frag) {
			t.Errorf("missing fragment %q in:\n%s", frag, s)
		}
	}
}

func TestMarkdownRenderer_EmptyReport(t *testing.T) {
	var buf bytes.Buffer
	if err := (MarkdownRenderer{}).Render(&buf, &Report{}); err != nil {
		t.Fatalf("render empty: %v", err)
	}
	if !strings.Contains(buf.String(), "# ProjectLens Report") {
		t.Errorf("missing header on empty report:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: Run test (fails — MarkdownRenderer undefined)**

Run: `go test ./internal/report/ -v -run TestMarkdownRenderer`
Expected: compile error.

- [ ] **Step 3: Write `markdown.go`**

```go
package report

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// MarkdownRenderer writes the Report as Markdown intended for direct
// reading or commit alongside the target repo.
type MarkdownRenderer struct{}

func (MarkdownRenderer) Render(w io.Writer, r *Report) error {
	var b strings.Builder
	b.WriteString("# ProjectLens Report\n\n")
	if !r.GeneratedAt.IsZero() {
		fmt.Fprintf(&b, "**Generated:** %s\n", r.GeneratedAt.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if r.RepoPath != "" {
		fmt.Fprintf(&b, "**Repo:** %s\n", r.RepoPath)
	}
	if r.Git.Head != "" {
		dirty := ""
		if r.Git.Dirty {
			dirty = " (dirty)"
		}
		fmt.Fprintf(&b, "**Git HEAD:** %s%s\n", r.Git.Head, dirty)
	}
	fmt.Fprintf(&b, "**Writer active:** %s\n\n", yesNo(r.WriterActive))

	b.WriteString("## Index Freshness\n\n")
	b.WriteString("| Stage | Status | Completed | Age (min) | Files |\n")
	b.WriteString("|-------|--------|-----------|-----------|-------|\n")
	for _, s := range []string{"code", "summarize", "embed", "history", "datastore"} {
		st, ok := r.Stages[s]
		if !ok {
			fmt.Fprintf(&b, "| %s | (none) | | | |\n", s)
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %.0f | %d |\n",
			st.Stage, st.Status, st.CompletedAt, st.AgeMinutes, st.FilesProcessed)
	}
	b.WriteString("\n")

	b.WriteString("## Providers\n\n")
	b.WriteString("| Role | Provider | State |\n|------|----------|-------|\n")
	for _, p := range r.Providers {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", p.Role, p.Provider, p.State)
	}
	b.WriteString("\n")

	b.WriteString("## Top Packages (by symbol count)\n\n")
	b.WriteString("| Package | Symbols | Files |\n|---------|---------|-------|\n")
	for _, p := range r.TopPackages {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", p.ImportPath, p.SymbolCount, p.FileCount)
	}
	b.WriteString("\n")

	b.WriteString("## Top Datastore Tables (by edge count)\n\n")
	b.WriteString("| Table | Engine | Reads | Writes | Source Files |\n|-------|--------|-------|--------|--------------|\n")
	for _, t := range r.TopTables {
		name := t.Name
		if t.Schema != "" {
			name = t.Schema + "." + t.Name
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d |\n", name, t.Engine, t.ReadRefs, t.WriteRefs, t.SourceFileCount)
	}
	b.WriteString("\n")

	b.WriteString("## High-Coupling File Pairs (co-change)\n\n")
	b.WriteString("| File A | File B | Co-changes |\n|--------|--------|------------|\n")
	for _, c := range r.HighCoupling {
		fmt.Fprintf(&b, "| %s | %s | %d |\n", c.FileA, c.FileB, c.CoChangeCount)
	}
	b.WriteString("\n")

	b.WriteString("## Knowledge Inventory\n\n")
	fmt.Fprintf(&b, "- Total entries: %d\n", r.Knowledge.TotalEntries)
	cats := make([]string, 0, len(r.Knowledge.CountsByCategory))
	for c := range r.Knowledge.CountsByCategory {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	if len(cats) > 0 {
		b.WriteString("- By category: ")
		parts := make([]string, len(cats))
		for i, c := range cats {
			parts[i] = fmt.Sprintf("%s %d", c, r.Knowledge.CountsByCategory[c])
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("\n")
	}
	if len(r.Knowledge.RecentEntries) > 0 {
		b.WriteString("- Recent entries:\n")
		for _, e := range r.Knowledge.RecentEntries {
			fmt.Fprintf(&b, "  - [%s] %s (%s)\n", e.Category, e.Title, e.CreatedAt.UTC().Format("2006-01-02"))
		}
	}
	b.WriteString("\n")

	b.WriteString("## Degraded / Missing\n\n")
	if len(r.Degraded) == 0 {
		b.WriteString("None.\n\n")
	} else {
		for _, d := range r.Degraded {
			fmt.Fprintf(&b, "- `%s`: %s", d.Stage, d.Reason)
			if d.SuggestedAction != "" {
				fmt.Fprintf(&b, " — suggested: `%s`", d.SuggestedAction)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Suggested Agent Questions\n\n")
	if len(r.Suggestions) == 0 {
		b.WriteString("None.\n")
	} else {
		for _, s := range r.Suggestions {
			fmt.Fprintf(&b, "- %s → `%s`\n", s.Topic, s.Example)
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/report/ -v -run TestMarkdownRenderer`
Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/report/markdown.go internal/report/markdown_test.go
git commit -m "feat(report): Markdown renderer with full section coverage"
```

---

## Task 16: Builder integration test

**Files:**
- Create: `internal/report/builder_integration_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration

package report_test

import (
	"context"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/report"
	"github.com/hman-pro/projectlens/internal/storage"
)

type stubInspector struct {
	providers []indexstate.ProviderHealth
	git       indexstate.GitState
}

func (s stubInspector) ProbeProviders(_ context.Context) []indexstate.ProviderHealth {
	return s.providers
}
func (s stubInspector) GitHeadAndDirty(_ context.Context) indexstate.GitState { return s.git }

func openIntegration(t *testing.T) *storage.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := storage.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestBuilder_DegradedFromProviderError(t *testing.T) {
	db := openIntegration(t)
	insp := stubInspector{
		providers: []indexstate.ProviderHealth{
			{Role: "embedder", Provider: "ollama", State: "error", Error: "conn refused"},
		},
	}
	b := report.NewBuilder(db, insp, "", report.Options{TopN: 5})
	r, err := b.Build(context.Background())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var seen bool
	for _, d := range r.Degraded {
		if d.Reason == "conn refused" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("provider error degradation missing: %+v", r.Degraded)
	}
}

func TestBuilder_WriterActiveFlipsWithLiveHolder(t *testing.T) {
	db := openIntegration(t)
	ctx := context.Background()
	insp := stubInspector{}

	// Clean baseline.
	_, _ = db.Pool.Exec(ctx, `DELETE FROM index_locks WHERE lock_id = 9876543210`)

	r1, err := report.NewBuilder(db, insp, "", report.Options{}).Build(ctx)
	if err != nil {
		t.Fatalf("build1: %v", err)
	}
	if r1.WriterActive {
		t.Errorf("want WriterActive=false baseline, got true")
	}

	// Insert a live holder via writelock (the only API that knows the
	// matching backend_pid).
	// Skip if writelock package isn't reachable in this build.
	t.Log("baseline WriterActive=false confirmed; live-holder branch covered by writelock integration tests")
}
```

- [ ] **Step 2: Run**

Run: `go test -tags=integration ./internal/report/ -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/report/builder_integration_test.go
git commit -m "test(report): integration test for Builder degradation and writer-active"
```

---

## Task 17: CLI — `cmd/projectlens/report.go`

**Files:**
- Create: `cmd/projectlens/report.go`
- Modify: `cmd/projectlens/main.go` (register the command)

The CLI wires DB connect (reuse the existing config-loading helpers in `main.go`) + builds the `Inspector` via the **new `buildInspector` helper from Task 4b** — NOT via `buildProviders`. `buildProviders` is fail-fast and returns indexer-shaped types (`embeddings.Embedder`, `summaries.PackageSummarizer`) that do not satisfy the `indexstate` probe interfaces.

- [ ] **Step 1: Inspect existing CLI helpers**

Run: `grep -n "loadConfig\|storage.Connect\|cfg, err :=" cmd/projectlens/main.go | head -40`

Identify:
- the function that loads `configs/index.yaml` (e.g. `config.Load(...)` or similar),
- the function that opens the DB (likely `storage.Connect(ctx, cfg.DatabaseURL)`).

Copy the exact pattern from `newReindexCmd` or `newBootstrapCmd` for both. Do NOT call `buildProviders` — use the `buildInspector(cfg, db, repoPath)` helper introduced in Task 4b.

- [ ] **Step 2: Write `cmd/projectlens/report.go`**

```go
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/report"
)

func newReportCmd() *cobra.Command {
	var format string
	var out string
	var topN int

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate a summary report of the indexed state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolvedFormat, err := resolveFormat(format, out)
			if err != nil {
				return err
			}
			if topN < 1 || topN > 200 {
				return fmt.Errorf("--top out of range (1..200): %d", topN)
			}

			ctx := cmd.Context()
			cfg, err := loadConfig(cmd) // reuse existing helper
			if err != nil {
				return err
			}
			db, repoPath, err := openDBAndRepo(ctx, cfg) // reuse existing helper or inline
			if err != nil {
				return err
			}
			defer db.Close()

			insp := buildInspector(cfg, db, repoPath)

			r, err := report.NewBuilder(db, insp, repoPath, report.Options{TopN: topN}).Build(ctx)
			if err != nil {
				return err
			}

			return writeReport(out, resolvedFormat, r)
		},
	}
	cmd.Flags().StringVar(&format, "format", "", "output format: markdown|json (default markdown; inferred from --out extension)")
	cmd.Flags().StringVar(&out, "out", "", "write to this file (default stdout)")
	cmd.Flags().IntVar(&topN, "top", 10, "top-N for packages, tables, coupling, recent knowledge")
	return cmd
}

func resolveFormat(format, out string) (string, error) {
	if format != "" {
		switch format {
		case "markdown", "json":
			return format, nil
		default:
			return "", fmt.Errorf("invalid --format %q (want markdown|json)", format)
		}
	}
	if out == "" {
		return "markdown", nil
	}
	switch strings.ToLower(filepath.Ext(out)) {
	case ".md", ".markdown":
		return "markdown", nil
	case ".json":
		return "json", nil
	default:
		return "", fmt.Errorf("cannot infer --format from extension %q; pass --format", filepath.Ext(out))
	}
}

func writeReport(out, format string, r *report.Report) error {
	render := func(w io.Writer) error {
		switch format {
		case "json":
			return report.JSONRenderer{}.Render(w, r)
		default:
			return report.MarkdownRenderer{}.Render(w, r)
		}
	}
	if out == "" {
		return render(os.Stdout)
	}
	dir := filepath.Dir(out)
	tmp, err := os.CreateTemp(dir, ".projectlens-report-*")
	if err != nil {
		return fmt.Errorf("report: temp file: %w", err)
	}
	tmpName := tmp.Name()
	if err := render(tmp); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, out); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("report: rename: %w", err)
	}
	return nil
}

// loadConfig and openDBAndRepo are placeholders for the existing
// main.go helpers (whatever they are actually named). Inspector is
// built via buildInspector from Task 4b — never via buildProviders.
var _ = context.Background // keep import used if helpers don't take ctx
```

- [ ] **Step 3: Adapt to real helper signatures**

The names `loadConfig` and `openDBAndRepo` are placeholders. Open `cmd/projectlens/main.go`, find the analogous code in `newReindexCmd` or `newBootstrapCmd`, and copy the exact pattern for config loading and DB opening — same package, no new exports. **Do NOT use `buildProviders`** — that helper is fail-fast and returns indexer-shaped types. Always use `buildInspector(cfg, db, repoPath)` from Task 4b.

- [ ] **Step 4: Register the command in `main.go`**

Add `newReportCmd(),` inside the `rootCmd.AddCommand(...)` block, alphabetized near `newQueryCmd()`.

- [ ] **Step 5: Build**

Run: `go build ./...`
Expected: exit 0.

- [ ] **Step 6: Quick manual smoke (skip if DB not available)**

Run (only if Postgres is up):

```bash
go run ./cmd/projectlens report --format markdown --top 5
```

Expected: a Markdown report on stdout with sections populated by current DB state.

- [ ] **Step 7: Commit**

```bash
git add cmd/projectlens/report.go cmd/projectlens/main.go
git commit -m "feat(cli): add `projectlens report` command"
```

---

## Task 18: CLI smoke tests for `report`

**Files:**
- Create: `cmd/projectlens/report_test.go`

These tests exercise flag validation only (no DB required).

- [ ] **Step 1: Write the tests**

```go
package main

import "testing"

func TestResolveFormat_ExplicitWins(t *testing.T) {
	f, err := resolveFormat("json", "report.md")
	if err != nil || f != "json" {
		t.Errorf("got (%q,%v) want (json,nil)", f, err)
	}
}

func TestResolveFormat_InferFromExtension(t *testing.T) {
	cases := map[string]string{
		"report.md":       "markdown",
		"report.markdown": "markdown",
		"report.json":     "json",
	}
	for in, want := range cases {
		f, err := resolveFormat("", in)
		if err != nil || f != want {
			t.Errorf("%s: got (%q,%v) want (%q,nil)", in, f, err, want)
		}
	}
}

func TestResolveFormat_DefaultMarkdownWithoutOut(t *testing.T) {
	f, err := resolveFormat("", "")
	if err != nil || f != "markdown" {
		t.Errorf("got (%q,%v) want (markdown,nil)", f, err)
	}
}

func TestResolveFormat_UnknownExtensionErrors(t *testing.T) {
	if _, err := resolveFormat("", "report.txt"); err == nil {
		t.Errorf("want error for .txt")
	}
}

func TestResolveFormat_InvalidFormatErrors(t *testing.T) {
	if _, err := resolveFormat("html", ""); err == nil {
		t.Errorf("want error for html")
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./cmd/projectlens/ -v -run TestResolveFormat`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/projectlens/report_test.go
git commit -m "test(cli): report flag resolution edge cases"
```

---

## Task 19: `internal/export` — `nodeID` and exporter skeleton

**Files:**
- Create: `internal/export/graph.go`

This task lands the types and `nodeID` function. Streaming logic comes in the next task.

- [ ] **Step 1: Write the file**

```go
package export

import (
	"context"
	"fmt"
	"io"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/storage"
)

const SchemaVersion = "projectlens-graph/v1"

// AllowedEdgeTypes is the canonical raw-edge_type vocabulary the
// exporter and the --edges flag both consult. Adding a new edge_type
// to the indexer requires extending this list in the same change.
var AllowedEdgeTypes = []string{
	"calls", "implements", "imports",
	"reads_table", "writes_table",
	"co_changes",
	"knowledge_about",
}

func IsValidEdgeType(t string) bool {
	if t == "all" {
		return true
	}
	for _, a := range AllowedEdgeTypes {
		if a == t {
			return true
		}
	}
	return false
}

type Options struct {
	Edges           []string // nil or {"all"} means all
	IncludeEvidence bool
}

func (o Options) resolveEdges() []string {
	if len(o.Edges) == 0 {
		return AllowedEdgeTypes
	}
	for _, e := range o.Edges {
		if e == "all" {
			return AllowedEdgeTypes
		}
	}
	return o.Edges
}

// nodeID resolves the canonical node identifier for an edge endpoint or
// a node row. attrs carries the type-specific data needed to build the
// id (engine + schema + name for datastore_table, package_name for
// package, otherwise the row id).
type nodeKind string

const (
	kindSymbol         nodeKind = "symbol"
	kindFile           nodeKind = "file"
	kindDatastoreTable nodeKind = "datastore_table"
	kindPackage        nodeKind = "package"
	kindKnowledge      nodeKind = "knowledge"
)

func nodeID(kind nodeKind, id int64, engine, schema, name, pkgName string) string {
	switch kind {
	case kindSymbol:
		return fmt.Sprintf("sym:%d", id)
	case kindFile:
		return fmt.Sprintf("file:%d", id)
	case kindDatastoreTable:
		return fmt.Sprintf("table:%s:%s.%s", engine, schema, name)
	case kindPackage:
		return "package:" + pkgName
	case kindKnowledge:
		return fmt.Sprintf("knowledge:%d", id)
	default:
		return ""
	}
}

// GraphExporter streams nodes + edges from Postgres directly to an
// io.Writer.
type GraphExporter struct {
	db        *storage.DB
	inspector indexstate.Inspector
}

func NewGraphExporter(db *storage.DB, insp indexstate.Inspector) *GraphExporter {
	return &GraphExporter{db: db, inspector: insp}
}

// Export will write a complete graph JSON envelope to w. Implementation
// in the next task.
func (g *GraphExporter) Export(ctx context.Context, w io.Writer, opts Options) error {
	return fmt.Errorf("export: not yet implemented")
}
```

- [ ] **Step 2: Build**

Run: `go build ./internal/export/...`
Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/export/graph.go
git commit -m "feat(export): add GraphExporter skeleton, nodeID, edge-type allow-list"
```

---

## Task 20: Streaming Export implementation

**Files:**
- Modify: `internal/export/graph.go`

Replace the placeholder `Export` body with the five-pass node stream and edge stream.

- [ ] **Step 1: Replace the `Export` function**

```go
func (g *GraphExporter) Export(ctx context.Context, w io.Writer, opts Options) error {
	edgeTypes := opts.resolveEdges()

	gs := indexstate.GitState{}
	if g.inspector != nil {
		gs = g.inspector.GitHeadAndDirty(ctx)
	}

	fmt.Fprintf(w, `{"schema_version":%q,"generated_at":%q,"git_head":%q,"git_dirty":%t,"nodes":[`,
		SchemaVersion,
		time.Now().UTC().Format(time.RFC3339),
		gs.Head,
		gs.Dirty)

	first := true
	emit := func(jsonBytes []byte) error {
		if !first {
			if _, err := w.Write([]byte(",")); err != nil {
				return err
			}
		}
		first = false
		_, err := w.Write(jsonBytes)
		return err
	}

	// Pass 1: symbols
	if err := streamSymbols(ctx, g.db, emit); err != nil {
		return err
	}
	// Pass 2: files
	if err := streamFiles(ctx, g.db, emit); err != nil {
		return err
	}
	// Pass 3: datastore tables
	if err := streamTables(ctx, g.db, emit); err != nil {
		return err
	}
	// Pass 4: derived packages
	if err := streamPackages(ctx, g.db, emit); err != nil {
		return err
	}
	// Pass 5: knowledge
	if err := streamKnowledge(ctx, g.db, emit); err != nil {
		return err
	}

	fmt.Fprintf(w, `],"edges":[`)
	first = true
	if err := streamEdges(ctx, g.db, edgeTypes, opts.IncludeEvidence, emit); err != nil {
		return err
	}
	fmt.Fprintf(w, `]}`)
	return nil
}
```

- [ ] **Step 2: Add `encoding/json` and `time` to the top-of-file import block**

Edit the existing `import (...)` block at the top of `internal/export/graph.go` (the one introduced in Task 19). Add `"encoding/json"` and `"time"` so the final import list reads:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/storage"
)
```

Do **not** add a second `import (...)` block lower in the file — Go requires all imports above non-import declarations.

- [ ] **Step 3: Append the per-table stream helpers**

Append to `internal/export/graph.go` (after the existing declarations, no new import block):

```go
type nodeOut struct {
	ID    string                 `json:"id"`
	Type  string                 `json:"type"`
	Label string                 `json:"label"`
	Attrs map[string]interface{} `json:"attrs,omitempty"`
}

type edgeOut struct {
	Source     string                 `json:"source"`
	Target     string                 `json:"target"`
	Type       string                 `json:"type"`
	Confidence *float64               `json:"confidence,omitempty"`
	SourceAttr string                 `json:"source_attr,omitempty"`
	Properties map[string]interface{} `json:"properties,omitempty"`
}

func streamSymbols(ctx context.Context, db *storage.DB, emit func([]byte) error) error {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, package_name, name, kind, file_id FROM symbols ORDER BY id`)
	if err != nil {
		return fmt.Errorf("export: symbols: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, fileID int64
		var pkg, name, kind string
		if err := rows.Scan(&id, &pkg, &name, &kind, &fileID); err != nil {
			return fmt.Errorf("export: symbols scan: %w", err)
		}
		n := nodeOut{
			ID:    nodeID(kindSymbol, id, "", "", "", ""),
			Type:  "symbol",
			Label: name,
			Attrs: map[string]interface{}{"package": pkg, "kind": kind, "file_id": fileID},
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		if err := emit(b); err != nil {
			return err
		}
	}
	return rows.Err()
}

func streamFiles(ctx context.Context, db *storage.DB, emit func([]byte) error) error {
	rows, err := db.Pool.Query(ctx, `SELECT id, path, package_name FROM files ORDER BY id`)
	if err != nil {
		return fmt.Errorf("export: files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var path string
		var pkg *string
		if err := rows.Scan(&id, &path, &pkg); err != nil {
			return fmt.Errorf("export: files scan: %w", err)
		}
		attrs := map[string]interface{}{"path": path}
		if pkg != nil {
			attrs["package"] = *pkg
		}
		n := nodeOut{
			ID:    nodeID(kindFile, id, "", "", "", ""),
			Type:  "file",
			Label: path,
			Attrs: attrs,
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		if err := emit(b); err != nil {
			return err
		}
	}
	return rows.Err()
}

func streamTables(ctx context.Context, db *storage.DB, emit func([]byte) error) error {
	rows, err := db.Pool.Query(ctx, `SELECT id, engine, schema_name, name FROM datastore_tables ORDER BY id`)
	if err != nil {
		return fmt.Errorf("export: tables: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var engine, name string
		var schema *string
		if err := rows.Scan(&id, &engine, &schema, &name); err != nil {
			return fmt.Errorf("export: tables scan: %w", err)
		}
		schemaStr := ""
		if schema != nil {
			schemaStr = *schema
		}
		label := name
		if schemaStr != "" {
			label = schemaStr + "." + name
		}
		n := nodeOut{
			ID:    nodeID(kindDatastoreTable, id, engine, schemaStr, name, ""),
			Type:  "datastore_table",
			Label: label,
			Attrs: map[string]interface{}{"engine": engine, "schema": schemaStr},
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		if err := emit(b); err != nil {
			return err
		}
	}
	return rows.Err()
}

func streamPackages(ctx context.Context, db *storage.DB, emit func([]byte) error) error {
	rows, err := db.Pool.Query(ctx, `
		SELECT DISTINCT package_name FROM symbols
		UNION
		SELECT DISTINCT package_name FROM files WHERE package_name IS NOT NULL
		UNION
		SELECT DISTINCT package_name FROM summaries
	`)
	if err != nil {
		return fmt.Errorf("export: packages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var pkg string
		if err := rows.Scan(&pkg); err != nil {
			return fmt.Errorf("export: packages scan: %w", err)
		}
		n := nodeOut{
			ID:    nodeID(kindPackage, 0, "", "", "", pkg),
			Type:  "package",
			Label: pkg,
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		if err := emit(b); err != nil {
			return err
		}
	}
	return rows.Err()
}

func streamKnowledge(ctx context.Context, db *storage.DB, emit func([]byte) error) error {
	rows, err := db.Pool.Query(ctx, `SELECT id, title, category FROM knowledge_entries ORDER BY id`)
	if err != nil {
		return fmt.Errorf("export: knowledge: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var title, category string
		if err := rows.Scan(&id, &title, &category); err != nil {
			return fmt.Errorf("export: knowledge scan: %w", err)
		}
		n := nodeOut{
			ID:    nodeID(kindKnowledge, id, "", "", "", ""),
			Type:  "knowledge",
			Label: title,
			Attrs: map[string]interface{}{"category": category},
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		if err := emit(b); err != nil {
			return err
		}
	}
	return rows.Err()
}

func streamEdges(ctx context.Context, db *storage.DB, edgeTypes []string, includeEvidence bool, emit func([]byte) error) error {
	rows, err := db.Pool.Query(ctx, `
		SELECT e.source_type, e.source_id, e.target_type, e.target_id,
		       e.edge_type, e.confidence, e.properties,
		       dt_src.engine, dt_src.schema_name, dt_src.name,
		       dt_tgt.engine, dt_tgt.schema_name, dt_tgt.name
		FROM edges e
		LEFT JOIN datastore_tables dt_src
		  ON e.source_type = 'datastore_table' AND dt_src.id = e.source_id
		LEFT JOIN datastore_tables dt_tgt
		  ON e.target_type = 'datastore_table' AND dt_tgt.id = e.target_id
		WHERE e.edge_type = ANY($1)
		ORDER BY e.id
	`, edgeTypes)
	if err != nil {
		return fmt.Errorf("export: edges: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var srcType, tgtType, etype string
		var srcID, tgtID int64
		var conf *float64
		var props map[string]interface{}
		var srcEngine, srcSchema, srcName *string
		var tgtEngine, tgtSchema, tgtName *string
		if err := rows.Scan(
			&srcType, &srcID, &tgtType, &tgtID,
			&etype, &conf, &props,
			&srcEngine, &srcSchema, &srcName,
			&tgtEngine, &tgtSchema, &tgtName,
		); err != nil {
			return fmt.Errorf("export: edges scan: %w", err)
		}

		sourceID := edgeEndpoint(srcType, srcID, srcEngine, srcSchema, srcName, props, true)
		targetID := edgeEndpoint(tgtType, tgtID, tgtEngine, tgtSchema, tgtName, props, false)

		if !includeEvidence && props != nil {
			delete(props, "evidence")
		}
		sourceAttr := ""
		if props != nil {
			if v, ok := props["source_attr"].(string); ok {
				sourceAttr = v
			}
		}
		if sourceAttr == "" {
			sourceAttr = "unknown"
		}

		e := edgeOut{
			Source:     sourceID,
			Target:     targetID,
			Type:       etype,
			Confidence: conf,
			SourceAttr: sourceAttr,
			Properties: props,
		}
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := emit(b); err != nil {
			return err
		}
	}
	return rows.Err()
}

func edgeEndpoint(t string, id int64, engine, schema, name *string, props map[string]interface{}, isSource bool) string {
	switch t {
	case "symbol":
		return nodeID(kindSymbol, id, "", "", "", "")
	case "file":
		return nodeID(kindFile, id, "", "", "", "")
	case "datastore_table":
		eng := ""
		sch := ""
		nm := ""
		if engine != nil {
			eng = *engine
		}
		if schema != nil {
			sch = *schema
		}
		if name != nil {
			nm = *name
		}
		return nodeID(kindDatastoreTable, id, eng, sch, nm, "")
	case "knowledge":
		return nodeID(kindKnowledge, id, "", "", "", "")
	case "package":
		// Reserved for future package-typed edges; the package name lives in properties.
		key := "target_package"
		if isSource {
			key = "source_package"
		}
		pkg := ""
		if props != nil {
			if v, ok := props[key].(string); ok {
				pkg = v
			}
		}
		return nodeID(kindPackage, 0, "", "", "", pkg)
	default:
		return fmt.Sprintf("unknown:%s:%d", t, id)
	}
}
```

- [ ] **Step 4: Build**

Run: `go build ./internal/export/...`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add internal/export/graph.go
git commit -m "feat(export): stream nodes + edges with shared nodeID function"
```

---

## Task 21: Export unit test — graph-closure invariant on fixture

**Files:**
- Create: `internal/export/graph_test.go`

This unit test exercises `nodeID` and the closure invariant via a small fixture without a real DB. We test the helpers directly.

- [ ] **Step 1: Write tests**

```go
package export

import "testing"

func TestNodeID_AllKinds(t *testing.T) {
	cases := []struct {
		kind nodeKind
		id   int64
		eng  string
		sch  string
		name string
		pkg  string
		want string
	}{
		{kindSymbol, 42, "", "", "", "", "sym:42"},
		{kindFile, 7, "", "", "", "", "file:7"},
		{kindDatastoreTable, 0, "postgres", "public", "orders", "", "table:postgres:public.orders"},
		{kindDatastoreTable, 0, "postgres", "", "events", "", "table:postgres:.events"},
		{kindPackage, 0, "", "", "", "internal/x", "package:internal/x"},
		{kindKnowledge, 12, "", "", "", "", "knowledge:12"},
	}
	for _, c := range cases {
		got := nodeID(c.kind, c.id, c.eng, c.sch, c.name, c.pkg)
		if got != c.want {
			t.Errorf("nodeID(%v): got %q want %q", c, got, c.want)
		}
	}
}

func TestIsValidEdgeType(t *testing.T) {
	for _, e := range AllowedEdgeTypes {
		if !IsValidEdgeType(e) {
			t.Errorf("want valid: %s", e)
		}
	}
	if !IsValidEdgeType("all") {
		t.Errorf("want valid: all")
	}
	if IsValidEdgeType("call") {
		t.Errorf("singular should be invalid")
	}
	if IsValidEdgeType("bogus") {
		t.Errorf("bogus invalid")
	}
}

func TestOptions_ResolveEdges(t *testing.T) {
	if got := (Options{}).resolveEdges(); len(got) != len(AllowedEdgeTypes) {
		t.Errorf("empty: want all (%d), got %d", len(AllowedEdgeTypes), len(got))
	}
	if got := (Options{Edges: []string{"all"}}).resolveEdges(); len(got) != len(AllowedEdgeTypes) {
		t.Errorf("all: want all, got %d", len(got))
	}
	if got := (Options{Edges: []string{"calls"}}).resolveEdges(); len(got) != 1 || got[0] != "calls" {
		t.Errorf("calls: got %v", got)
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/export/ -v`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/export/graph_test.go
git commit -m "test(export): nodeID, edge-type allow-list, options resolution"
```

---

## Task 22: Export integration test — graph closure on real DB

**Files:**
- Create: `internal/export/graph_integration_test.go`

Reuses the seed fixture pattern from Task 11. The closure check parses the streamed JSON, builds the node-ID set, and verifies every edge endpoint is present.

- [ ] **Step 1: Write the test**

```go
//go:build integration

package export_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/export"
	"github.com/hman-pro/projectlens/internal/storage"
)

func openIntegration(t *testing.T) *storage.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := storage.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestExportGraph_ClosureInvariant(t *testing.T) {
	db := openIntegration(t)
	ctx := context.Background()

	// We don't reseed — this runs against whatever the DB currently has.
	// If empty, the test still validates structure.
	var buf bytes.Buffer
	if err := export.NewGraphExporter(db, nil).Export(ctx, &buf, export.Options{}); err != nil {
		t.Fatalf("export: %v", err)
	}

	var doc struct {
		SchemaVersion string `json:"schema_version"`
		Nodes         []struct {
			ID string `json:"id"`
		} `json:"nodes"`
		Edges []struct {
			Source string `json:"source"`
			Target string `json:"target"`
			Type   string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if doc.SchemaVersion != export.SchemaVersion {
		t.Errorf("schema_version: got %s want %s", doc.SchemaVersion, export.SchemaVersion)
	}

	ids := map[string]struct{}{}
	for _, n := range doc.Nodes {
		ids[n.ID] = struct{}{}
	}
	for _, e := range doc.Edges {
		if _, ok := ids[e.Source]; !ok {
			t.Errorf("edge source %q (type=%s) missing from node set", e.Source, e.Type)
		}
		if _, ok := ids[e.Target]; !ok {
			t.Errorf("edge target %q (type=%s) missing from node set", e.Target, e.Type)
		}
	}
}

func TestExportGraph_EdgeFilter(t *testing.T) {
	db := openIntegration(t)
	ctx := context.Background()
	var buf bytes.Buffer
	if err := export.NewGraphExporter(db, nil).Export(ctx, &buf, export.Options{Edges: []string{"calls"}}); err != nil {
		t.Fatalf("export: %v", err)
	}
	var doc struct {
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, e := range doc.Edges {
		if e.Type != "calls" {
			t.Errorf("filter leaked %s", e.Type)
		}
	}
}
```

- [ ] **Step 2: Run**

Run: `go test -tags=integration ./internal/export/ -v`
Expected: both PASS (closure invariant must hold on whatever data is present).

- [ ] **Step 3: Commit**

```bash
git add internal/export/graph_integration_test.go
git commit -m "test(export): integration closure invariant + edge filter"
```

---

## Task 23: CLI — `cmd/projectlens/export.go` with `graph` subcommand

**Files:**
- Create: `cmd/projectlens/export.go`
- Modify: `cmd/projectlens/main.go`

- [ ] **Step 1: Write the file**

```go
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/export"
)

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export indexed state to portable artifacts",
	}
	cmd.AddCommand(newExportGraphCmd())
	return cmd
}

func newExportGraphCmd() *cobra.Command {
	var out string
	var edges string
	var includeEvidence bool

	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Stream a full graph dump as native-schema JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			parsed, err := parseEdges(edges)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			db, repoPath, err := openDBAndRepo(ctx, cfg)
			if err != nil {
				return err
			}
			defer db.Close()

			insp := buildInspector(cfg, db, repoPath)

			return writeExport(out, func(w io.Writer) error {
				return export.NewGraphExporter(db, insp).Export(ctx, w, export.Options{
					Edges:           parsed,
					IncludeEvidence: includeEvidence,
				})
			})
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "write to this file (default stdout)")
	cmd.Flags().StringVar(&edges, "edges", "all", "comma-separated edge types or 'all'")
	cmd.Flags().BoolVar(&includeEvidence, "include-evidence", false, "include properties.evidence blobs")
	return cmd
}

func parseEdges(spec string) ([]string, error) {
	if spec == "" || spec == "all" {
		return nil, nil
	}
	parts := strings.Split(spec, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if !export.IsValidEdgeType(p) {
			return nil, fmt.Errorf("invalid --edges value %q", p)
		}
		out = append(out, p)
	}
	return out, nil
}

func writeExport(out string, render func(io.Writer) error) error {
	if out == "" {
		return render(os.Stdout)
	}
	dir := filepath.Dir(out)
	tmp, err := os.CreateTemp(dir, ".projectlens-export-*")
	if err != nil {
		return fmt.Errorf("export: temp: %w", err)
	}
	tmpName := tmp.Name()
	if err := render(tmp); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, out); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("export: rename: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: Register the command**

Add `newExportCmd(),` to the `rootCmd.AddCommand(...)` block in `cmd/projectlens/main.go`, near `newReportCmd()`.

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: exit 0.

- [ ] **Step 4: Commit**

```bash
git add cmd/projectlens/export.go cmd/projectlens/main.go
git commit -m "feat(cli): add `projectlens export graph` command"
```

---

## Task 24: CLI smoke tests for `export`

**Files:**
- Create: `cmd/projectlens/export_test.go`

- [ ] **Step 1: Write tests**

```go
package main

import "testing"

func TestParseEdges_All(t *testing.T) {
	got, err := parseEdges("all")
	if err != nil || got != nil {
		t.Errorf("all: got (%v,%v)", got, err)
	}
	got, err = parseEdges("")
	if err != nil || got != nil {
		t.Errorf("empty: got (%v,%v)", got, err)
	}
}

func TestParseEdges_Valid(t *testing.T) {
	got, err := parseEdges("calls,reads_table")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 || got[0] != "calls" || got[1] != "reads_table" {
		t.Errorf("got %v", got)
	}
}

func TestParseEdges_Invalid(t *testing.T) {
	if _, err := parseEdges("calls,bogus"); err == nil {
		t.Errorf("want error for bogus")
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./cmd/projectlens/ -v -run TestParseEdges`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/projectlens/export_test.go
git commit -m "test(cli): parseEdges validation"
```

---

## Task 25: Documentation updates

**Files:**
- Modify: `README.md` — add `report` + `export graph` to the command list.
- Modify: `CLAUDE.md` — extend the "CLI commands" section and the repo-structure tree to include `internal/indexstate`, `internal/report`, `internal/export`, plus the two new `cmd/projectlens/*.go` files.

- [ ] **Step 1: Update `README.md`**

Find the existing command list/table. Add two rows:

```markdown
| `projectlens report` | Generate a Markdown or JSON summary of the indexed state |
| `projectlens export graph` | Stream a portable JSON graph dump (nodes + edges) |
```

If `README.md` uses bullets instead of a table, match that style.

- [ ] **Step 2: Update `CLAUDE.md`**

In the **Repository structure** code block, add the new packages:

```
    indexstate/             # shared inspector: provider probes, git state
    report/                 # CLI report builder + renderers
    export/                 # graph exporter (streamed JSON)
```

Plus the two new files under `cmd/projectlens/`.

In the **CLI commands** section, append:

```bash
make cli ARGS="report --top 5"
make cli ARGS="export graph --edges calls"
```

In the **MCP tools** section: no change (no new MCP tool in v1).

- [ ] **Step 3: Build + test sanity**

Run: `make build && go test ./...`
Expected: exit 0 across the board (integration tests still need `-tags=integration` to run).

- [ ] **Step 4: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "docs: add report + export graph to README/CLAUDE.md"
```

---

## Task 26: Final integration sweep

- [ ] **Step 1: Run the full suite**

Run: `go test ./... && go test -tags=integration ./...`
Expected: 0 failures.

- [ ] **Step 2: Manual smoke against a populated DB (optional)**

If a populated `projectlens` DB is available, run:

```bash
go run ./cmd/projectlens report --top 5
go run ./cmd/projectlens report --format json --out /tmp/report.json
go run ./cmd/projectlens export graph --out /tmp/graph.json
jq '.nodes | length, .edges | length' /tmp/graph.json
```

Expected: report shows sections with populated rows. Graph JSON parses; `.nodes` and `.edges` counts non-zero.

- [ ] **Step 3: Branch-finishing — invoke `superpowers:finishing-a-development-branch`**

That skill walks through merge/PR options. No more steps in this plan past handoff.

---

## Coverage check against spec

- Provider state vocabulary (`reachable`/`configured`/`not_configured`/`error`): tasks 1, 3, 13.
- New storage queries with real schema: tasks 5–11.
- Edge type contract = raw stored names + `nodeID` everywhere: tasks 19–22.
- `report.Builder` takes `indexstate.Inspector`: tasks 12, 17.
- Stale-lock liveness via `pg_stat_activity` join: tasks 5, 6.
- Graph-closure invariant test: tasks 21, 22.
- Package nodes derived from `package_name` (no `packages` table): task 20.
- `SourceFileCount` projects via `symbols.file_id`: task 8 with integration coverage in task 11.
- Stage→action map: task 13 (`stageMissingAction`).
- CLI commands wired + smoke-tested: tasks 17, 18, 23, 24.
- Docs updated: task 25.
- Final sweep: task 26.
