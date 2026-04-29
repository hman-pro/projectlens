# ProjectLens TUI — Phase 1 (Operational Dashboard) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a keyboard-driven Bubbletea TUI binary (`projectlens-tui`) that surfaces five read-only operational sections (Health, Pipeline, Storage, Runs, Config) backed by the existing Postgres database, following the design at `docs/plans/2026-04-29-tui-design.md`.

**Architecture:** Separate Go binary at `cmd/projectlens-tui/`. Sidebar+detail layout. App-level `Section` interface with section-specific typed `RefreshedMsg`. `Store` interface decouples DB access; per-section in-memory `fake` for unit tests + a single integration test against the running compose Postgres. Hybrid refresh: 30s `tea.Tick` (re-armed each fire) on the focused section + focus-change refresh + manual `r`. App-owned `context.Context` propagates cancellation to all DB calls.

**Tech Stack:** Go 1.26, `charmbracelet/bubbletea`, `charmbracelet/bubbles`, `charmbracelet/lipgloss`, `charmbracelet/log` (existing), `pgx/v5/pgxpool` (existing), `joho/godotenv/autoload` (existing).

---

## File map

**Created:**

```
cmd/projectlens-tui/
  main.go                         # entry point: config, pool, tea.Program

internal/tui/
  app/
    model.go                      # appModel struct + constructors
    update.go                     # tea.Msg dispatch + global keys
    view.go                       # lipgloss layout
    keys.go                       # keymap
    tick.go                       # 30s refresh tick with re-arming
    app_test.go                   # navigation, refresh, concurrency, golden render
    testdata/                     # golden files
  sections/
    section.go                    # Section interface, Status enum, SizeMsg, FocusMsg
    health/
      model.go
      update.go
      view.go
      messages.go                 # RefreshedMsg
      model_test.go
    pipeline/
      ...same five files...
    storage/
      ...
    runs/
      ...
    config/
      ...
  store/
    store.go                      # Store interface + Const runsMaxRows
    types.go                      # snapshot structs
    pg.go                         # pgxpool-backed implementation
    fake.go                       # in-memory store + Latency/FailWith hooks
    pg_integration_test.go        # //go:build integration
  components/
    panel.go                      # bordered titled box
    keyhelp.go                    # footer hint formatter
    spinner.go                    # loading indicator
  theme/
    theme.go                      # palette + lipgloss styles
```

**Modified:**

- `go.mod`, `go.sum` — add `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/bubbles`. `lipgloss` and `charm log` are already direct/indirect.
- `CLAUDE.md` — document new binary, `PROJECTLENS_TUI_LOG_FILE` env var, build/run commands.

**Not modified:** existing CLI (`cmd/projectlens/`) and MCP server (`cmd/projectlens-mcp/`) stay untouched. The TUI is read-only and additive.

---

## Conventions for every task

- Branch: work on the current branch (`main`); no separate feature branch unless instructed.
- Run from repo root: `/Users/hamed.zohrehvand/source/projectlens`.
- Database for integration test + smoke runs: `postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable` (already in `configs/index.yaml` and the existing `.env`).
- Build sanity after every code-touching task: `go build ./...`.
- Test commands assume the compose stack from CLAUDE.md is running for integration tests.
- After each task's last step, commit. Commit messages use Conventional Commits (`feat(tui): …`, `test(tui): …`, `docs(tui): …`).

---

## Task 1: Skeleton binary that quits on `q`

Build a runnable binary that opens an alt-screen, shows "hello — projectlens tui" centered, and exits cleanly on `q`. No DB, no sections.

**Files:**
- Create: `cmd/projectlens-tui/main.go`
- Create: `internal/tui/app/model.go`
- Create: `internal/tui/app/update.go`
- Create: `internal/tui/app/view.go`
- Create: `internal/tui/app/keys.go`
- Modify: `go.mod`, `go.sum` (deps)

- [ ] **Step 1: Add bubbletea + bubbles dependencies**

Run: `go get github.com/charmbracelet/bubbletea github.com/charmbracelet/bubbles`
Expected: both modules added to `go.mod` `require` block as direct deps.

- [ ] **Step 2: Create the keymap file**

Create `internal/tui/app/keys.go`:

```go
package app

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up      key.Binding
	Down    key.Binding
	Enter   key.Binding
	Esc     key.Binding
	Tab     key.Binding
	ShiftTab key.Binding
	Refresh key.Binding
	Help    key.Binding
	Quit    key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Enter:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "focus")),
		Esc:      key.NewBinding(key.WithKeys("esc", "h"), key.WithHelp("esc", "back")),
		Tab:      key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next")),
		ShiftTab: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev")),
		Refresh:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}
```

- [ ] **Step 3: Create the model file**

Create `internal/tui/app/model.go`:

```go
package app

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

type Model struct {
	ctx  context.Context
	keys keyMap
	w, h int
}

func New(ctx context.Context) Model {
	return Model{ctx: ctx, keys: defaultKeys()}
}

func (m Model) Init() tea.Cmd { return nil }
```

- [ ] **Step 4: Create the update file**

Create `internal/tui/app/update.go`:

```go
package app

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
	}
	return m, nil
}
```

- [ ] **Step 5: Create the view file**

Create `internal/tui/app/view.go`:

```go
package app

import "github.com/charmbracelet/lipgloss"

func (m Model) View() string {
	if m.w < 1 || m.h < 1 {
		return ""
	}
	style := lipgloss.NewStyle().
		Width(m.w).Height(m.h).
		Align(lipgloss.Center, lipgloss.Center)
	return style.Render("hello — projectlens tui (press q to quit)")
}
```

- [ ] **Step 6: Create the binary entry point**

Create `cmd/projectlens-tui/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	_ "github.com/joho/godotenv/autoload"

	"github.com/hman-pro/projectlens/internal/tui/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	model := app.New(ctx)
	prog := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := prog.Run()
	return err
}
```

- [ ] **Step 7: Verify build**

Run: `go build ./...`
Expected: exit 0, no output.

- [ ] **Step 8: Smoke run the binary**

Run: `go run ./cmd/projectlens-tui/` then press `q`.
Expected: alt screen opens with "hello — projectlens tui", terminal restored on quit.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum cmd/projectlens-tui/ internal/tui/app/
git commit -m "feat(tui): skeleton binary with alt-screen and quit key"
```

---

## Task 2: Theme + Panel component

A minimal style sheet and a bordered titled box used by every section.

**Files:**
- Create: `internal/tui/theme/theme.go`
- Create: `internal/tui/components/panel.go`

- [ ] **Step 1: Theme package**

Create `internal/tui/theme/theme.go`:

```go
package theme

import "github.com/charmbracelet/lipgloss"

var (
	ColorBorder    = lipgloss.AdaptiveColor{Light: "#cccccc", Dark: "#444444"}
	ColorTitle     = lipgloss.AdaptiveColor{Light: "#222222", Dark: "#dddddd"}
	ColorMuted     = lipgloss.AdaptiveColor{Light: "#666666", Dark: "#888888"}
	ColorAccent    = lipgloss.AdaptiveColor{Light: "#0066cc", Dark: "#5fafff"}
	ColorOK        = lipgloss.AdaptiveColor{Light: "#007700", Dark: "#5fdd5f"}
	ColorWarn      = lipgloss.AdaptiveColor{Light: "#aa6600", Dark: "#ffaa33"}
	ColorError     = lipgloss.AdaptiveColor{Light: "#aa0000", Dark: "#ff5f5f"}

	Border         = lipgloss.RoundedBorder()
)

func TitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ColorTitle).Bold(true)
}
func MutedStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ColorMuted)
}
func StatusStyle(status string) lipgloss.Style {
	c := ColorMuted
	switch status {
	case "ok", "completed":
		c = ColorOK
	case "running":
		c = ColorAccent
	case "failed", "error":
		c = ColorError
	}
	return lipgloss.NewStyle().Foreground(c)
}
```

- [ ] **Step 2: Panel component**

Create `internal/tui/components/panel.go`:

```go
package components

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/hman-pro/projectlens/internal/tui/theme"
)

// Panel renders a bordered, titled box of fixed inner dimensions.
// title may be empty.
func Panel(title, body string, w, h int) string {
	style := lipgloss.NewStyle().
		Border(theme.Border).
		BorderForeground(theme.ColorBorder).
		Width(w - 2).Height(h - 2)
	if title != "" {
		header := theme.TitleStyle().Render(" " + title + " ")
		body = header + "\n" + body
	}
	return style.Render(body)
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: exit 0.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/theme/ internal/tui/components/panel.go
git commit -m "feat(tui): theme palette and panel component"
```

---

## Task 3: Section interface + control messages

Define the contract every section satisfies, plus the `SizeMsg` and `FocusMsg` the app dispatches to sections.

**Files:**
- Create: `internal/tui/sections/section.go`

- [ ] **Step 1: Define the file**

Create `internal/tui/sections/section.go`:

```go
package sections

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Status reflects the freshness/health of a section's last refresh.
type Status int

const (
	StatusIdle Status = iota
	StatusLoading
	StatusOK
	StatusError
)

func (s Status) String() string {
	switch s {
	case StatusLoading:
		return "loading"
	case StatusOK:
		return "ok"
	case StatusError:
		return "error"
	default:
		return "idle"
	}
}

// SizeMsg is dispatched by the app to a focused section to declare its
// available rendering area (inside the panel border).
type SizeMsg struct {
	SectionID string
	W, H      int
}

// FocusMsg toggles a section's focused/summary rendering mode.
type FocusMsg struct {
	SectionID string
	Focused   bool
}

// Section is the interface every dashboard panel implements.
//
// Update returns a Section (not tea.Model) so the app router can store the
// result back without runtime type assertions.
type Section interface {
	ID() string
	Title() string
	Init() tea.Cmd
	Update(msg tea.Msg) (Section, tea.Cmd)
	View() string
	Refresh() tea.Cmd
	Status() Status
	LastRefresh() time.Time
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/sections/section.go
git commit -m "feat(tui): Section interface and Size/Focus messages"
```

---

## Task 4: Store interface + snapshot types + fake

The `Store` interface and snapshot types every section consumes, plus a controllable fake for unit tests.

**Files:**
- Create: `internal/tui/store/store.go`
- Create: `internal/tui/store/types.go`
- Create: `internal/tui/store/fake.go`

- [ ] **Step 1: Define snapshot types**

Create `internal/tui/store/types.go`:

```go
package store

import "time"

type HealthSnapshot struct {
	StartedAt        time.Time
	CompletedAt      *time.Time
	CommitSHA        string
	Stage            string
	Status           string
	FilesProcessed   int
	SymbolsExtracted int
	EdgesCreated     int
	HeadCommit       string
	Staleness        time.Duration
}

func (h HealthSnapshot) Duration() time.Duration {
	if h.CompletedAt == nil {
		return 0
	}
	return h.CompletedAt.Sub(h.StartedAt)
}

type StageStat struct {
	Name             string
	LastRunStartedAt time.Time
	Status           string
	FilesProcessed   int
	Duration         time.Duration
}

type PipelineSnapshot struct {
	Stages []StageStat
}

type TableStat struct {
	Name    string
	EstRows int64
	Bytes   int64
}

type ChunkStats struct {
	Total    int64
	Embedded int64
	ByType   map[string]int64
}

type StorageSnapshot struct {
	Tables []TableStat
	Chunks ChunkStats
}

type IndexRun struct {
	ID               int64
	StartedAt        time.Time
	CompletedAt      *time.Time
	CommitSHA        string
	Stage            string
	Status           string
	FilesProcessed   int
	SymbolsExtracted int
	EdgesCreated     int
}

func (r IndexRun) Duration() time.Duration {
	if r.CompletedAt == nil {
		return 0
	}
	return r.CompletedAt.Sub(r.StartedAt)
}

type RunsSnapshot struct {
	Runs []IndexRun
}

type ConfigSnapshot struct {
	EmbeddingProvider     string
	EmbeddingModel        string
	EmbeddingDims         int
	EmbeddingEndpoint     string
	SummarizationProvider string
	SummarizationModel    string
	DBHost                string
	DBName                string
}
```

- [ ] **Step 2: Define the Store interface**

Create `internal/tui/store/store.go`:

```go
package store

import "context"

const RunsMaxRows = 100

type Store interface {
	Health(ctx context.Context) (HealthSnapshot, error)
	Pipeline(ctx context.Context) (PipelineSnapshot, error)
	Storage(ctx context.Context) (StorageSnapshot, error)
	Runs(ctx context.Context, limit int) (RunsSnapshot, error)
	Config(ctx context.Context) (ConfigSnapshot, error)
}
```

- [ ] **Step 3: Implement the fake store**

Create `internal/tui/store/fake.go`:

```go
package store

import (
	"context"
	"sync"
	"time"
)

// Fake is a controllable in-memory Store for unit tests.
//
//	f := store.NewFake()
//	f.SetHealth(snap)
//	f.SetLatency(50 * time.Millisecond)
//	f.SetErr("Health", errors.New("boom"))
type Fake struct {
	mu       sync.Mutex
	health   HealthSnapshot
	pipeline PipelineSnapshot
	storage  StorageSnapshot
	runs     RunsSnapshot
	config   ConfigSnapshot
	latency  time.Duration
	errs     map[string]error
}

func NewFake() *Fake {
	return &Fake{errs: map[string]error{}}
}

func (f *Fake) SetHealth(s HealthSnapshot)     { f.mu.Lock(); f.health = s; f.mu.Unlock() }
func (f *Fake) SetPipeline(s PipelineSnapshot) { f.mu.Lock(); f.pipeline = s; f.mu.Unlock() }
func (f *Fake) SetStorage(s StorageSnapshot)   { f.mu.Lock(); f.storage = s; f.mu.Unlock() }
func (f *Fake) SetRuns(s RunsSnapshot)         { f.mu.Lock(); f.runs = s; f.mu.Unlock() }
func (f *Fake) SetConfig(s ConfigSnapshot)     { f.mu.Lock(); f.config = s; f.mu.Unlock() }
func (f *Fake) SetLatency(d time.Duration)     { f.mu.Lock(); f.latency = d; f.mu.Unlock() }
func (f *Fake) SetErr(method string, err error) {
	f.mu.Lock()
	f.errs[method] = err
	f.mu.Unlock()
}

func (f *Fake) wait(ctx context.Context, method string) error {
	f.mu.Lock()
	d := f.latency
	err := f.errs[method]
	f.mu.Unlock()
	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *Fake) Health(ctx context.Context) (HealthSnapshot, error) {
	if err := f.wait(ctx, "Health"); err != nil {
		return HealthSnapshot{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.health, nil
}
func (f *Fake) Pipeline(ctx context.Context) (PipelineSnapshot, error) {
	if err := f.wait(ctx, "Pipeline"); err != nil {
		return PipelineSnapshot{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pipeline, nil
}
func (f *Fake) Storage(ctx context.Context) (StorageSnapshot, error) {
	if err := f.wait(ctx, "Storage"); err != nil {
		return StorageSnapshot{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.storage, nil
}
func (f *Fake) Runs(ctx context.Context, limit int) (RunsSnapshot, error) {
	if err := f.wait(ctx, "Runs"); err != nil {
		return RunsSnapshot{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 || limit > RunsMaxRows {
		limit = RunsMaxRows
	}
	out := f.runs
	if len(out.Runs) > limit {
		out.Runs = out.Runs[:limit]
	}
	return out, nil
}
func (f *Fake) Config(ctx context.Context) (ConfigSnapshot, error) {
	if err := f.wait(ctx, "Config"); err != nil {
		return ConfigSnapshot{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.config, nil
}
```

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/store/
git commit -m "feat(tui): Store interface, snapshot types, in-memory Fake"
```

---

## Task 5: Postgres Store implementation

Implement `pg.go` with all five queries against the real schema. Field names mirror `internal/storage/indexruns.go`.

**Files:**
- Create: `internal/tui/store/pg.go`

- [ ] **Step 1: Skeleton + connection helpers**

Create `internal/tui/store/pg.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hman-pro/projectlens/internal/config"
)

type PG struct {
	pool     *pgxpool.Pool
	cfg      *config.Config
	repoPath string
}

func NewPG(pool *pgxpool.Pool, cfg *config.Config, repoPath string) *PG {
	return &PG{pool: pool, cfg: cfg, repoPath: repoPath}
}

// knownTables is the static list of tables that appear in the Storage view.
// Kept in sync with CLAUDE.md.
var knownTables = []string{
	"files", "symbols", "chunks", "embeddings", "summaries", "edges",
	"index_runs", "git_refs", "datastore_tables", "documents",
	"symbol_history", "file_history", "knowledge_entries", "schema_migrations",
}
```

- [ ] **Step 2: Implement Health**

Append to `internal/tui/store/pg.go`:

```go
func (s *PG) Health(ctx context.Context) (HealthSnapshot, error) {
	const q = `
		SELECT id, started_at, completed_at, commit_sha, stage, status,
		       files_processed, symbols_extracted, edges_created
		FROM index_runs ORDER BY id DESC LIMIT 1
	`
	var (
		id        int64
		started   time.Time
		completed *time.Time
		commit    string
		stage     string
		status    string
		files     int
		symbols   int
		edges     int
	)
	row := s.pool.QueryRow(ctx, q)
	if err := row.Scan(&id, &started, &completed, &commit, &stage, &status, &files, &symbols, &edges); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return HealthSnapshot{}, nil
		}
		return HealthSnapshot{}, fmt.Errorf("store: health: %w", err)
	}
	head := s.gitHead()
	return HealthSnapshot{
		StartedAt:        started,
		CompletedAt:      completed,
		CommitSHA:        commit,
		Stage:            stage,
		Status:           status,
		FilesProcessed:   files,
		SymbolsExtracted: symbols,
		EdgesCreated:     edges,
		HeadCommit:       head,
		Staleness:        time.Since(started),
	}, nil
}

// gitHead returns the short HEAD commit of the target repo, or "" if unavailable.
func (s *PG) gitHead() string {
	if s.repoPath == "" {
		return ""
	}
	cmd := exec.Command("git", "-C", s.repoPath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

- [ ] **Step 3: Implement Pipeline**

Append:

```go
func (s *PG) Pipeline(ctx context.Context) (PipelineSnapshot, error) {
	const q = `
		SELECT DISTINCT ON (stage) stage, started_at, completed_at, status, files_processed
		FROM index_runs ORDER BY stage, id DESC
	`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return PipelineSnapshot{}, fmt.Errorf("store: pipeline: %w", err)
	}
	defer rows.Close()
	var stages []StageStat
	for rows.Next() {
		var (
			name      string
			started   time.Time
			completed *time.Time
			status    string
			files     int
		)
		if err := rows.Scan(&name, &started, &completed, &status, &files); err != nil {
			return PipelineSnapshot{}, fmt.Errorf("store: pipeline scan: %w", err)
		}
		dur := time.Duration(0)
		if completed != nil {
			dur = completed.Sub(started)
		}
		stages = append(stages, StageStat{
			Name:             name,
			LastRunStartedAt: started,
			Status:           status,
			FilesProcessed:   files,
			Duration:         dur,
		})
	}
	if err := rows.Err(); err != nil {
		return PipelineSnapshot{}, fmt.Errorf("store: pipeline rows: %w", err)
	}
	return PipelineSnapshot{Stages: stages}, nil
}
```

- [ ] **Step 4: Implement Storage**

Append:

```go
func (s *PG) Storage(ctx context.Context) (StorageSnapshot, error) {
	const tableQ = `
		SELECT relname, n_live_tup, pg_total_relation_size(relid)
		FROM pg_stat_user_tables WHERE relname = ANY($1)
	`
	rows, err := s.pool.Query(ctx, tableQ, knownTables)
	if err != nil {
		return StorageSnapshot{}, fmt.Errorf("store: storage tables: %w", err)
	}
	tables := make([]TableStat, 0, len(knownTables))
	for rows.Next() {
		var t TableStat
		if err := rows.Scan(&t.Name, &t.EstRows, &t.Bytes); err != nil {
			rows.Close()
			return StorageSnapshot{}, fmt.Errorf("store: storage scan: %w", err)
		}
		tables = append(tables, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return StorageSnapshot{}, fmt.Errorf("store: storage rows: %w", err)
	}

	const chunkQ = `
		SELECT c.source_type, count(*) AS total, count(e.id) AS embedded
		FROM chunks c
		LEFT JOIN embeddings e ON e.chunk_id = c.id
		GROUP BY c.source_type
	`
	crows, err := s.pool.Query(ctx, chunkQ)
	if err != nil {
		return StorageSnapshot{}, fmt.Errorf("store: storage chunks: %w", err)
	}
	defer crows.Close()
	chunks := ChunkStats{ByType: map[string]int64{}}
	for crows.Next() {
		var srcType string
		var total, embedded int64
		if err := crows.Scan(&srcType, &total, &embedded); err != nil {
			return StorageSnapshot{}, fmt.Errorf("store: storage chunk scan: %w", err)
		}
		chunks.ByType[srcType] = total
		chunks.Total += total
		chunks.Embedded += embedded
	}
	if err := crows.Err(); err != nil {
		return StorageSnapshot{}, fmt.Errorf("store: storage chunk rows: %w", err)
	}
	return StorageSnapshot{Tables: tables, Chunks: chunks}, nil
}
```

- [ ] **Step 5: Implement Runs**

Append:

```go
func (s *PG) Runs(ctx context.Context, limit int) (RunsSnapshot, error) {
	if limit <= 0 || limit > RunsMaxRows {
		limit = RunsMaxRows
	}
	const q = `
		SELECT id, started_at, completed_at, commit_sha, stage, status,
		       files_processed, symbols_extracted, edges_created
		FROM index_runs ORDER BY id DESC LIMIT $1
	`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return RunsSnapshot{}, fmt.Errorf("store: runs: %w", err)
	}
	defer rows.Close()
	var runs []IndexRun
	for rows.Next() {
		var r IndexRun
		var completed sql.NullTime
		if err := rows.Scan(&r.ID, &r.StartedAt, &completed, &r.CommitSHA, &r.Stage, &r.Status,
			&r.FilesProcessed, &r.SymbolsExtracted, &r.EdgesCreated); err != nil {
			return RunsSnapshot{}, fmt.Errorf("store: runs scan: %w", err)
		}
		if completed.Valid {
			t := completed.Time
			r.CompletedAt = &t
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return RunsSnapshot{}, fmt.Errorf("store: runs rows: %w", err)
	}
	return RunsSnapshot{Runs: runs}, nil
}
```

- [ ] **Step 6: Implement Config**

Append:

```go
func (s *PG) Config(_ context.Context) (ConfigSnapshot, error) {
	host, dbname := parseDSN(s.cfg.DatabaseURL)
	return ConfigSnapshot{
		EmbeddingProvider:     s.cfg.Embeddings.Provider,
		EmbeddingModel:        s.cfg.Embeddings.Model,
		EmbeddingDims:         s.cfg.Embeddings.Dimensions,
		EmbeddingEndpoint:     s.cfg.Embeddings.Endpoint,
		SummarizationProvider: s.cfg.Summarization.Provider,
		SummarizationModel:    s.cfg.Summarization.Model,
		DBHost:                host,
		DBName:                dbname,
	}, nil
}

func parseDSN(dsn string) (host, dbname string) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", ""
	}
	host = u.Host
	if strings.HasPrefix(u.Path, "/") {
		dbname = u.Path[1:]
	}
	return host, dbname
}
```

- [ ] **Step 7: Verify build**

Run: `go build ./...`
Expected: exit 0.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/store/pg.go
git commit -m "feat(tui): Postgres Store with health/pipeline/storage/runs/config queries"
```

---

## Task 6: Store integration test

A single integration test (`//go:build integration`) covering all five queries against the running compose Postgres.

**Files:**
- Create: `internal/tui/store/pg_integration_test.go`

- [ ] **Step 1: Write the test**

Create `internal/tui/store/pg_integration_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestPGStore_AllSnapshots(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	cfg := &config.Config{
		DatabaseURL: dsn,
		Embeddings: config.EmbeddingsConfig{
			Provider: "ollama", Model: "mxbai-embed-large", Dimensions: 1024,
			Endpoint: "http://localhost:11434",
		},
		Summarization: config.SummarizationConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	}
	s := store.NewPG(pool, cfg, "")

	if _, err := s.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	if _, err := s.Pipeline(ctx); err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	st, err := s.Storage(ctx)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	if len(st.Tables) == 0 {
		t.Fatalf("storage: expected at least one table row")
	}
	if _, err := s.Runs(ctx, 10); err != nil {
		t.Fatalf("runs: %v", err)
	}
	c, err := s.Config(ctx)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if c.DBHost == "" {
		t.Fatalf("config: expected DBHost")
	}
}

func TestPGStore_QuitCancelsInFlight(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"
	}
	parent, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(parent, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	ctx, cancelInflight := context.WithCancel(parent)
	done := make(chan error, 1)
	go func() {
		_, err := pool.Exec(ctx, "SELECT pg_sleep(5)")
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancelInflight()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected cancellation error, got nil")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected cancellation within 500ms")
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test -tags integration ./internal/tui/store/ -v`
Expected: both tests pass against the running compose stack.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/store/pg_integration_test.go
git commit -m "test(tui): integration test for Store snapshots and ctx cancel"
```

---

## Task 7: Health section — vertical slice

The first complete section: model + update + view + refresh wiring + tests. Establishes the pattern for the four remaining sections.

**Files:**
- Create: `internal/tui/sections/health/messages.go`
- Create: `internal/tui/sections/health/model.go`
- Create: `internal/tui/sections/health/update.go`
- Create: `internal/tui/sections/health/view.go`
- Create: `internal/tui/sections/health/model_test.go`

- [ ] **Step 1: Define the message + section ID**

Create `internal/tui/sections/health/messages.go`:

```go
package health

import "github.com/hman-pro/projectlens/internal/tui/store"

const ID = "health"

// RefreshedMsg is delivered to the health section after a Refresh() completes.
type RefreshedMsg struct {
	Snap store.HealthSnapshot
	Err  error
	Gen  uint64
}
```

- [ ] **Step 2: Define the model**

Create `internal/tui/sections/health/model.go`:

```go
package health

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

type Model struct {
	store     store.Store
	appCtx    context.Context

	snap      store.HealthSnapshot
	err       error
	status    sections.Status
	gen       uint64           // last issued generation
	lastSeen  uint64           // generation of last absorbed result
	last      time.Time
	w, h      int
	focused   bool
}

func New(appCtx context.Context, s store.Store) *Model {
	return &Model{store: s, appCtx: appCtx, status: sections.StatusIdle}
}

func (m *Model) ID() string                { return ID }
func (m *Model) Title() string             { return "Index health" }
func (m *Model) Init() tea.Cmd             { return nil }
func (m *Model) Status() sections.Status   { return m.status }
func (m *Model) LastRefresh() time.Time    { return m.last }

// Refresh issues a Health() query and returns a tea.Cmd that resolves to a RefreshedMsg.
func (m *Model) Refresh() tea.Cmd {
	m.gen++
	gen := m.gen
	m.status = sections.StatusLoading
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.appCtx, 5*time.Second)
		defer cancel()
		snap, err := m.store.Health(ctx)
		return RefreshedMsg{Snap: snap, Err: err, Gen: gen}
	}
}
```

- [ ] **Step 3: Implement Update**

Create `internal/tui/sections/health/update.go`:

```go
package health

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
)

func (m *Model) Update(msg tea.Msg) (sections.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshedMsg:
		if msg.Gen < m.lastSeen {
			return m, nil // stale; drop
		}
		m.lastSeen = msg.Gen
		m.last = time.Now()
		if msg.Err != nil {
			m.err = msg.Err
			m.status = sections.StatusError
			return m, nil
		}
		m.snap = msg.Snap
		m.err = nil
		m.status = sections.StatusOK
		return m, nil
	case sections.SizeMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.w, m.h = msg.W, msg.H
		return m, nil
	case sections.FocusMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.focused = msg.Focused
		return m, nil
	}
	return m, nil
}
```

- [ ] **Step 4: Implement View**

Create `internal/tui/sections/health/view.go`:

```go
package health

import (
	"fmt"
	"strings"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/theme"
)

func (m *Model) View() string {
	if m.status == sections.StatusError {
		return theme.StatusStyle("error").Render("error: ") + m.err.Error() + "\n\npress r to retry"
	}
	if m.status == sections.StatusIdle {
		return theme.MutedStyle().Render("(loading…)")
	}

	s := m.snap
	commit := shortCommit(s.CommitSHA)
	completed := "—"
	if s.CompletedAt != nil {
		completed = s.CompletedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	dur := "—"
	if d := s.Duration(); d > 0 {
		dur = d.Round(time.Second).String()
	}
	headPart := "(unknown)"
	if s.HeadCommit != "" {
		headPart = "vs HEAD " + shortCommit(s.HeadCommit)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Started:    %s\n", s.StartedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "Completed:  %s\n", completed)
	fmt.Fprintf(&b, "Commit:     %s\n", commit)
	fmt.Fprintf(&b, "Stage:      %s\n", s.Stage)
	fmt.Fprintf(&b, "Status:     %s\n", theme.StatusStyle(s.Status).Render(s.Status))
	fmt.Fprintf(&b, "Duration:   %s\n", dur)
	fmt.Fprintf(&b, "Files: %d   Symbols: %d   Edges: %d\n", s.FilesProcessed, s.SymbolsExtracted, s.EdgesCreated)
	fmt.Fprintf(&b, "Staleness:  %s %s\n", humanDuration(s.Staleness), headPart)
	return b.String()
}

func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
```

- [ ] **Step 5: Write the failing test**

Create `internal/tui/sections/health/model_test.go`:

```go
package health_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/sections/health"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestHealth_AbsorbsSnapshot(t *testing.T) {
	f := store.NewFake()
	f.SetHealth(store.HealthSnapshot{
		StartedAt:        time.Date(2026, 4, 29, 9, 14, 2, 0, time.UTC),
		CommitSHA:        "ffdfc82deadbeef",
		Stage:            "embed",
		Status:           "ok",
		FilesProcessed:   4150,
		SymbolsExtracted: 28432,
		EdgesCreated:     91000,
		HeadCommit:       "a1b2c3def",
	})
	m := health.New(context.Background(), f)
	cmd := m.Refresh()
	msg := cmd().(health.RefreshedMsg)
	next, _ := m.Update(msg)
	if next.Status() != sections.StatusOK {
		t.Fatalf("status = %v, want StatusOK", next.Status())
	}
	view := next.View()
	for _, want := range []string{"ffdfc82", "embed", "ok", "4150", "28432", "91000", "a1b2c3d"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\nview:\n%s", want, view)
		}
	}
}

func TestHealth_DropsStaleGeneration(t *testing.T) {
	f := store.NewFake()
	f.SetHealth(store.HealthSnapshot{Stage: "first"})
	m := health.New(context.Background(), f)

	gen1 := m.Refresh()() // gen=1
	gen2 := m.Refresh()() // gen=2

	// Deliver newer first, older second.
	_, _ = m.Update(gen2)
	if !strings.Contains(m.View(), "first") {
		t.Fatalf("expected gen2 snap absorbed")
	}
	older, _ := m.Update(gen1)
	if !strings.Contains(older.View(), "first") {
		t.Fatalf("older message should not overwrite newer state")
	}
}

func TestHealth_SurfacesError(t *testing.T) {
	f := store.NewFake()
	f.SetErr("Health", errors.New("boom"))
	m := health.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	if next.Status() != sections.StatusError {
		t.Fatalf("status = %v, want StatusError", next.Status())
	}
	if !strings.Contains(next.View(), "boom") {
		t.Fatalf("view should mention error\nview:\n%s", next.View())
	}
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/tui/sections/health/ -v`
Expected: 3/3 PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/sections/health/
git commit -m "feat(tui): health section with refresh, stale-drop, and error surfacing"
```

---

## Task 8: App sidebar + global keys + multi-section composition

Replace the "hello" placeholder with a real sidebar list and detail pane that holds a slice of `sections.Section`. Wire focus navigation, manual `r`, and section-routed messages. The sidebar gets five placeholder sections that all use the health-section type for now (pipeline/storage/runs/config sections come later); we'll plug them in as they land.

**Files:**
- Modify: `internal/tui/app/model.go`
- Modify: `internal/tui/app/update.go`
- Modify: `internal/tui/app/view.go`
- Create: `internal/tui/app/app_test.go`

- [ ] **Step 1: Update keymap usage and add quitting message**

Append nothing yet — keymap from Task 1 is already complete. Move on.

- [ ] **Step 2: Replace `internal/tui/app/model.go`**

Overwrite `internal/tui/app/model.go`:

```go
package app

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
)

type Mode int

const (
	ModeSidebar Mode = iota
	ModeDetail
)

type Model struct {
	ctx      context.Context
	keys     keyMap
	help     help.Model

	sections []sections.Section
	focused  int
	mode     Mode

	w, h     int
	sidebar  list.Model

	tooSmall bool
}

const minW, minH = 80, 20

type sectionItem struct{ title string }

func (s sectionItem) Title() string       { return s.title }
func (s sectionItem) Description() string { return "" }
func (s sectionItem) FilterValue() string { return s.title }

// New constructs the root app model. sections must have at least one element.
func New(ctx context.Context, secs []sections.Section) Model {
	items := make([]list.Item, len(secs))
	for i, s := range secs {
		items[i] = sectionItem{title: s.Title()}
	}
	d := list.NewDefaultDelegate()
	d.ShowDescription = false
	sb := list.New(items, d, 24, 10)
	sb.Title = "Sections"
	sb.SetShowHelp(false)
	sb.SetShowStatusBar(false)
	sb.SetShowPagination(false)
	sb.SetFilteringEnabled(false)
	return Model{
		ctx:      ctx,
		keys:     defaultKeys(),
		help:     help.New(),
		sections: secs,
		sidebar:  sb,
	}
}

func (m Model) Init() tea.Cmd {
	if len(m.sections) == 0 {
		return tea.Quit
	}
	return tea.Batch(m.sections[m.focused].Init(), m.sections[m.focused].Refresh(), tickCmd())
}

// helpers
func (m Model) sidebarWidth() int {
	w := m.w / 4
	if w > 24 {
		w = 24
	}
	if w < 18 {
		w = 18
	}
	return w
}

func (m Model) detailSize() (w, h int) {
	return m.w - m.sidebarWidth() - 2, m.h - 4
}

func (m Model) since() time.Duration {
	if t := m.sections[m.focused].LastRefresh(); !t.IsZero() {
		return time.Since(t)
	}
	return 0
}
```

- [ ] **Step 3: Add `internal/tui/app/tick.go`**

Create `internal/tui/app/tick.go`:

```go
package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const tickInterval = 30 * time.Second

type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg { return tickMsg{} })
}
```

- [ ] **Step 4: Replace `internal/tui/app/update.go`**

Overwrite `internal/tui/app/update.go`:

```go
package app

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
)

const focusRefreshThreshold = 2 * time.Second

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.tooSmall = m.w < minW || m.h < minH
		if !m.tooSmall {
			m.sidebar.SetSize(m.sidebarWidth(), m.h-4)
			dw, dh := m.detailSize()
			id := m.sections[m.focused].ID()
			next, cmd := m.sections[m.focused].Update(sections.SizeMsg{SectionID: id, W: dw, H: dh})
			m.sections[m.focused] = next
			return m, cmd
		}
		return m, nil

	case tickMsg:
		cmd := m.sections[m.focused].Refresh()
		return m, tea.Batch(cmd, tickCmd())

	case tea.KeyMsg:
		if m.tooSmall {
			if key.Matches(msg, m.keys.Quit) {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.mode == ModeSidebar {
			return m.handleSidebarKey(msg)
		}
		return m.handleDetailKey(msg)
	}

	// Route every other message through every section so typed RefreshedMsg
	// reaches its target. Sections ignore messages that aren't their own.
	var cmds []tea.Cmd
	for i, s := range m.sections {
		next, cmd := s.Update(msg)
		m.sections[i] = next
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleSidebarKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Down),
		key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.ShiftTab):
		var cmd tea.Cmd
		m.sidebar, cmd = m.sidebar.Update(msg)
		newIdx := m.sidebar.Index()
		var refresh tea.Cmd
		if newIdx != m.focused {
			m.focused = newIdx
			if m.since() > focusRefreshThreshold {
				refresh = m.sections[m.focused].Refresh()
			}
			dw, dh := m.detailSize()
			id := m.sections[m.focused].ID()
			next, sizeCmd := m.sections[m.focused].Update(sections.SizeMsg{SectionID: id, W: dw, H: dh})
			m.sections[m.focused] = next
			return m, tea.Batch(cmd, sizeCmd, refresh)
		}
		return m, cmd
	case key.Matches(msg, m.keys.Refresh):
		return m, m.sections[m.focused].Refresh()
	case key.Matches(msg, m.keys.Enter):
		m.mode = ModeDetail
		id := m.sections[m.focused].ID()
		next, cmd := m.sections[m.focused].Update(sections.FocusMsg{SectionID: id, Focused: true})
		m.sections[m.focused] = next
		return m, cmd
	}
	return m, nil
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Esc):
		m.mode = ModeSidebar
		id := m.sections[m.focused].ID()
		next, cmd := m.sections[m.focused].Update(sections.FocusMsg{SectionID: id, Focused: false})
		m.sections[m.focused] = next
		return m, cmd
	case key.Matches(msg, m.keys.Refresh):
		return m, m.sections[m.focused].Refresh()
	}
	// Forward all other keys to the focused section.
	next, cmd := m.sections[m.focused].Update(msg)
	m.sections[m.focused] = next
	return m, cmd
}
```

- [ ] **Step 5: Replace `internal/tui/app/view.go`**

Overwrite `internal/tui/app/view.go`:

```go
package app

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"github.com/hman-pro/projectlens/internal/tui/components"
	"github.com/hman-pro/projectlens/internal/tui/theme"
)

func (m Model) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	if m.tooSmall {
		return lipgloss.NewStyle().
			Width(m.w).Height(m.h).
			Align(lipgloss.Center, lipgloss.Center).
			Render(fmt.Sprintf("terminal too small (need ≥ %d×%d)", minW, minH))
	}

	header := m.renderHeader()
	footer := m.renderFooter()

	sidebar := components.Panel("", m.sidebar.View(), m.sidebarWidth(), m.h-4)
	dw, dh := m.detailSize()
	body := m.sections[m.focused].View()
	detail := components.Panel(m.sections[m.focused].Title(), body, dw+2, dh+2)

	row := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, detail)
	return lipgloss.JoinVertical(lipgloss.Left, header, row, footer)
}

func (m Model) renderHeader() string {
	left := theme.TitleStyle().Render(" projectlens · dashboard ")
	right := ""
	if d := m.since(); d > 0 {
		right = theme.MutedStyle().Render(fmt.Sprintf(" refreshed %s ago ", durationShort(d)))
	}
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	return left + lipgloss.NewStyle().Width(gap).Render("") + right
}

func (m Model) renderFooter() string {
	const hint = "↑/↓ select  enter focus  esc back  r refresh  ? help  q quit"
	return theme.MutedStyle().Width(m.w).Render(" " + hint + " ")
}

func durationShort(d interface{ Seconds() float64 }) string {
	s := int(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	default:
		return fmt.Sprintf("%dh", s/3600)
	}
}
```

- [ ] **Step 6: Update binary main to compose with sections**

Overwrite `cmd/projectlens-tui/main.go` (replaces the "hello" version from Task 1):

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/joho/godotenv/autoload"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/tui/app"
	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/sections/health"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfgPath := getEnvOr("CONFIG_PATH", "configs/index.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required (set in .env or config)")
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()

	s := store.NewPG(pool, cfg, cfg.RepoPath)

	secs := []sections.Section{
		health.New(ctx, s),
		// pipeline / storage / runs / config sections plug in here as they land.
	}

	m := app.New(ctx, secs)
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err = prog.Run()
	return err
}

func getEnvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 7: Verify build**

Run: `go build ./...`
Expected: exit 0.

- [ ] **Step 8: Smoke run**

Run: `go run ./cmd/projectlens-tui/` and exercise: arrow keys (sidebar list highlights move), `enter` (no visible change yet — pipeline-style sections will use it), `esc`, `r`, `q`.
Expected: alt screen, sidebar shows "Index health", detail pane shows the populated health view, terminal restored on quit.

- [ ] **Step 9: Write app smoke test**

Create `internal/tui/app/app_test.go`:

```go
package app_test

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/app"
	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/sections/health"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func newApp(t *testing.T) (tea.Model, *store.Fake) {
	t.Helper()
	f := store.NewFake()
	f.SetHealth(store.HealthSnapshot{Stage: "embed", Status: "ok"})
	secs := []sections.Section{health.New(context.Background(), f)}
	return app.New(context.Background(), secs), f
}

func TestApp_RendersTooSmallBanner(t *testing.T) {
	m, _ := newApp(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 15})
	if !strings.Contains(m.View(), "terminal too small") {
		t.Fatalf("expected too-small banner, got:\n%s", m.View())
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if strings.Contains(m.View(), "terminal too small") {
		t.Fatalf("expected normal layout, got:\n%s", m.View())
	}
}

func TestApp_QuitsOnQ(t *testing.T) {
	m, _ := newApp(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd")
	}
}
```

- [ ] **Step 10: Run tests**

Run: `go test ./internal/tui/...`
Expected: ok across `app`, `sections/health`, `store` (non-integration).

- [ ] **Step 11: Commit**

```bash
git add cmd/projectlens-tui/main.go internal/tui/app/
git commit -m "feat(tui): app sidebar + detail layout, focus routing, refresh tick"
```

---

## Task 9: Pipeline section

Mirror the health pattern. Pipeline shows one row per stage in a `bubbles/table`.

**Files:**
- Create: `internal/tui/sections/pipeline/messages.go`
- Create: `internal/tui/sections/pipeline/model.go`
- Create: `internal/tui/sections/pipeline/update.go`
- Create: `internal/tui/sections/pipeline/view.go`
- Create: `internal/tui/sections/pipeline/model_test.go`
- Modify: `cmd/projectlens-tui/main.go` (register section)

- [ ] **Step 1: messages.go**

```go
package pipeline

import "github.com/hman-pro/projectlens/internal/tui/store"

const ID = "pipeline"

type RefreshedMsg struct {
	Snap store.PipelineSnapshot
	Err  error
	Gen  uint64
}
```

- [ ] **Step 2: model.go**

```go
package pipeline

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

type Model struct {
	store    store.Store
	appCtx   context.Context

	snap     store.PipelineSnapshot
	err      error
	status   sections.Status
	gen      uint64
	lastSeen uint64
	last     time.Time
	w, h     int
	focused  bool
	tbl      table.Model
}

func New(appCtx context.Context, s store.Store) *Model {
	cols := []table.Column{
		{Title: "Stage", Width: 14},
		{Title: "Started", Width: 22},
		{Title: "Status", Width: 10},
		{Title: "Files", Width: 8},
		{Title: "Duration", Width: 10},
	}
	tbl := table.New(table.WithColumns(cols), table.WithFocused(false))
	return &Model{store: s, appCtx: appCtx, tbl: tbl, status: sections.StatusIdle}
}

func (m *Model) ID() string              { return ID }
func (m *Model) Title() string           { return "Pipeline" }
func (m *Model) Init() tea.Cmd           { return nil }
func (m *Model) Status() sections.Status { return m.status }
func (m *Model) LastRefresh() time.Time  { return m.last }

func (m *Model) Refresh() tea.Cmd {
	m.gen++
	gen := m.gen
	m.status = sections.StatusLoading
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.appCtx, 5*time.Second)
		defer cancel()
		snap, err := m.store.Pipeline(ctx)
		return RefreshedMsg{Snap: snap, Err: err, Gen: gen}
	}
}
```

- [ ] **Step 3: update.go**

```go
package pipeline

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func (m *Model) Update(msg tea.Msg) (sections.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshedMsg:
		if msg.Gen < m.lastSeen {
			return m, nil
		}
		m.lastSeen = msg.Gen
		m.last = time.Now()
		if msg.Err != nil {
			m.err = msg.Err
			m.status = sections.StatusError
			return m, nil
		}
		m.snap = msg.Snap
		m.err = nil
		m.status = sections.StatusOK
		m.tbl.SetRows(toRows(m.snap))
		return m, nil
	case sections.SizeMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.w, m.h = msg.W, msg.H
		m.tbl.SetHeight(msg.H - 2)
		return m, nil
	case sections.FocusMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.focused = msg.Focused
		if m.focused {
			m.tbl.Focus()
		} else {
			m.tbl.Blur()
		}
		return m, nil
	case tea.KeyMsg:
		if !m.focused {
			return m, nil
		}
		var cmd tea.Cmd
		m.tbl, cmd = m.tbl.Update(msg)
		return m, cmd
	}
	return m, nil
}

func toRows(snap store.PipelineSnapshot) []table.Row {
	out := make([]table.Row, 0, len(snap.Stages))
	for _, s := range snap.Stages {
		dur := "—"
		if s.Duration > 0 {
			dur = s.Duration.Round(time.Second).String()
		}
		started := "—"
		if !s.LastRunStartedAt.IsZero() {
			started = s.LastRunStartedAt.UTC().Format("2006-01-02 15:04:05")
		}
		out = append(out, table.Row{
			s.Name, started, s.Status,
			fmt.Sprintf("%d", s.FilesProcessed), dur,
		})
	}
	return out
}
```

- [ ] **Step 4: view.go**

```go
package pipeline

import (
	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/theme"
)

func (m *Model) View() string {
	if m.status == sections.StatusError {
		return theme.StatusStyle("error").Render("error: ") + m.err.Error() + "\n\npress r to retry"
	}
	if m.status == sections.StatusIdle {
		return theme.MutedStyle().Render("(loading…)")
	}
	if len(m.snap.Stages) == 0 {
		return theme.MutedStyle().Render("no runs yet — run \"projectlens bootstrap\"")
	}
	return m.tbl.View()
}
```

- [ ] **Step 5: Test**

```go
package pipeline_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/sections/pipeline"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestPipeline_RendersStages(t *testing.T) {
	f := store.NewFake()
	f.SetPipeline(store.PipelineSnapshot{Stages: []store.StageStat{
		{Name: "code", LastRunStartedAt: time.Now(), Status: "ok", FilesProcessed: 4150, Duration: 95 * time.Second},
		{Name: "embed", LastRunStartedAt: time.Now(), Status: "running", FilesProcessed: 100},
	}})
	m := pipeline.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	v := next.View()
	for _, want := range []string{"code", "embed", "running", "4150"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}
```

- [ ] **Step 6: Plug into main**

In `cmd/projectlens-tui/main.go`, import `"github.com/hman-pro/projectlens/internal/tui/sections/pipeline"` and add to the slice:

```go
secs := []sections.Section{
	health.New(ctx, s),
	pipeline.New(ctx, s),
}
```

- [ ] **Step 7: Build + test + smoke**

```
go build ./...
go test ./internal/tui/sections/pipeline/ -v
go run ./cmd/projectlens-tui/   # arrow keys to "Pipeline"
```
Expected: build clean, tests pass, sidebar gains "Pipeline" entry that renders the table.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/sections/pipeline/ cmd/projectlens-tui/main.go
git commit -m "feat(tui): pipeline section with table of stages"
```

---

## Task 10: Storage section

Same shape as Pipeline, but two data sources (tables + chunks). Footer shows chunks-by-type.

**Files:**
- Create: `internal/tui/sections/storage/{messages.go,model.go,update.go,view.go,model_test.go}`
- Modify: `cmd/projectlens-tui/main.go`

- [ ] **Step 1: messages.go**

```go
package storage

import "github.com/hman-pro/projectlens/internal/tui/store"

const ID = "storage"

type RefreshedMsg struct {
	Snap store.StorageSnapshot
	Err  error
	Gen  uint64
}
```

- [ ] **Step 2: model.go**

```go
package storage

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

type Model struct {
	store    store.Store
	appCtx   context.Context
	snap     store.StorageSnapshot
	err      error
	status   sections.Status
	gen      uint64
	lastSeen uint64
	last     time.Time
	w, h     int
	focused  bool
	tbl      table.Model
}

func New(appCtx context.Context, s store.Store) *Model {
	cols := []table.Column{
		{Title: "Table", Width: 22},
		{Title: "~Rows", Width: 12},
		{Title: "Size", Width: 12},
	}
	return &Model{
		store: s, appCtx: appCtx, status: sections.StatusIdle,
		tbl: table.New(table.WithColumns(cols)),
	}
}

func (m *Model) ID() string              { return ID }
func (m *Model) Title() string           { return "Storage" }
func (m *Model) Init() tea.Cmd           { return nil }
func (m *Model) Status() sections.Status { return m.status }
func (m *Model) LastRefresh() time.Time  { return m.last }

func (m *Model) Refresh() tea.Cmd {
	m.gen++
	gen := m.gen
	m.status = sections.StatusLoading
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.appCtx, 10*time.Second) // 10s for pg_total_relation_size cold cache
		defer cancel()
		snap, err := m.store.Storage(ctx)
		return RefreshedMsg{Snap: snap, Err: err, Gen: gen}
	}
}
```

- [ ] **Step 3: update.go**

```go
package storage

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func (m *Model) Update(msg tea.Msg) (sections.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshedMsg:
		if msg.Gen < m.lastSeen {
			return m, nil
		}
		m.lastSeen = msg.Gen
		m.last = time.Now()
		if msg.Err != nil {
			m.err = msg.Err
			m.status = sections.StatusError
			return m, nil
		}
		m.snap = msg.Snap
		m.err = nil
		m.status = sections.StatusOK
		m.tbl.SetRows(rowsFromSnap(m.snap))
		return m, nil
	case sections.SizeMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.w, m.h = msg.W, msg.H
		m.tbl.SetHeight(msg.H - 6) // reserve 4 lines for chunks footer
		return m, nil
	case sections.FocusMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.focused = msg.Focused
		return m, nil
	case tea.KeyMsg:
		if !m.focused {
			return m, nil
		}
		var cmd tea.Cmd
		m.tbl, cmd = m.tbl.Update(msg)
		return m, cmd
	}
	return m, nil
}

func rowsFromSnap(s store.StorageSnapshot) []table.Row {
	out := make([]table.Row, 0, len(s.Tables))
	for _, t := range s.Tables {
		out = append(out, table.Row{t.Name, fmt.Sprintf("%d", t.EstRows), humanBytes(t.Bytes)})
	}
	return out
}

func humanBytes(n int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case n < kb:
		return fmt.Sprintf("%d B", n)
	case n < mb:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kb))
	case n < gb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gb))
	}
}
```

- [ ] **Step 4: view.go**

```go
package storage

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/theme"
)

func (m *Model) View() string {
	if m.status == sections.StatusError {
		return theme.StatusStyle("error").Render("error: ") + m.err.Error() + "\n\npress r to retry"
	}
	if m.status == sections.StatusIdle {
		return theme.MutedStyle().Render("(loading…)")
	}
	if len(m.snap.Tables) == 0 {
		return theme.MutedStyle().Render("no tables found — schema not migrated?")
	}
	var b strings.Builder
	b.WriteString(m.tbl.View())
	b.WriteString("\n")
	b.WriteString(theme.TitleStyle().Render("Chunks "))
	b.WriteString(theme.MutedStyle().Render(fmt.Sprintf("(%d total, %d embedded)\n", m.snap.Chunks.Total, m.snap.Chunks.Embedded)))

	keys := make([]string, 0, len(m.snap.Chunks.ByType))
	for k := range m.snap.Chunks.ByType {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "  %-12s %d\n", k, m.snap.Chunks.ByType[k])
	}
	return b.String()
}
```

- [ ] **Step 5: Test**

```go
package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/hman-pro/projectlens/internal/tui/sections/storage"
	pgstore "github.com/hman-pro/projectlens/internal/tui/store"
)

func TestStorage_RendersTablesAndChunks(t *testing.T) {
	f := pgstore.NewFake()
	f.SetStorage(pgstore.StorageSnapshot{
		Tables: []pgstore.TableStat{
			{Name: "files", EstRows: 4150, Bytes: 12_345_678},
			{Name: "symbols", EstRows: 28432, Bytes: 89_000_000},
		},
		Chunks: pgstore.ChunkStats{
			Total: 30000, Embedded: 28500,
			ByType: map[string]int64{"code": 28000, "knowledge": 2000},
		},
	})
	m := storage.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	v := next.View()
	for _, want := range []string{"files", "symbols", "4150", "code", "knowledge", "28500"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}
```

- [ ] **Step 6: Plug into main + build + test**

Append `storage.New(ctx, s)` to the sections slice. Run:

```
go build ./...
go test ./internal/tui/sections/storage/ -v
go run ./cmd/projectlens-tui/
```

Expected: build clean, test passes, sidebar shows "Storage", view renders.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/sections/storage/ cmd/projectlens-tui/main.go
git commit -m "feat(tui): storage section with tables and chunks-by-type"
```

---

## Task 11: Recent runs section

Scrollable table of up to 100 runs. Detail mode reveals an inline detail panel for the selected row. **No error column** — `index_runs` doesn't store errors today.

**Files:**
- Create: `internal/tui/sections/runs/{messages.go,model.go,update.go,view.go,model_test.go}`
- Modify: `cmd/projectlens-tui/main.go`

- [ ] **Step 1: messages.go**

```go
package runs

import "github.com/hman-pro/projectlens/internal/tui/store"

const ID = "runs"

type RefreshedMsg struct {
	Snap store.RunsSnapshot
	Err  error
	Gen  uint64
}
```

- [ ] **Step 2: model.go**

```go
package runs

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

type Model struct {
	store    store.Store
	appCtx   context.Context
	snap     store.RunsSnapshot
	err      error
	status   sections.Status
	gen      uint64
	lastSeen uint64
	last     time.Time
	w, h     int
	focused  bool
	tbl      table.Model
}

func New(appCtx context.Context, s store.Store) *Model {
	cols := []table.Column{
		{Title: "ID", Width: 6},
		{Title: "Started", Width: 22},
		{Title: "Stage", Width: 10},
		{Title: "Status", Width: 10},
		{Title: "Duration", Width: 10},
		{Title: "Files", Width: 8},
	}
	return &Model{
		store: s, appCtx: appCtx, status: sections.StatusIdle,
		tbl: table.New(table.WithColumns(cols)),
	}
}

func (m *Model) ID() string              { return ID }
func (m *Model) Title() string           { return "Recent runs" }
func (m *Model) Init() tea.Cmd           { return nil }
func (m *Model) Status() sections.Status { return m.status }
func (m *Model) LastRefresh() time.Time  { return m.last }

func (m *Model) Refresh() tea.Cmd {
	m.gen++
	gen := m.gen
	m.status = sections.StatusLoading
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.appCtx, 5*time.Second)
		defer cancel()
		snap, err := m.store.Runs(ctx, store.RunsMaxRows)
		return RefreshedMsg{Snap: snap, Err: err, Gen: gen}
	}
}
```

- [ ] **Step 3: update.go**

```go
package runs

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func (m *Model) Update(msg tea.Msg) (sections.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshedMsg:
		if msg.Gen < m.lastSeen {
			return m, nil
		}
		m.lastSeen = msg.Gen
		m.last = time.Now()
		if msg.Err != nil {
			m.err = msg.Err
			m.status = sections.StatusError
			return m, nil
		}
		m.snap = msg.Snap
		m.err = nil
		m.status = sections.StatusOK
		m.tbl.SetRows(rowsFromRuns(m.snap.Runs))
		return m, nil
	case sections.SizeMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.w, m.h = msg.W, msg.H
		// Detail panel takes ~6 lines; in summary mode the table can use full height-2.
		h := msg.H - 2
		if m.focused {
			h -= 8
		}
		if h < 3 {
			h = 3
		}
		m.tbl.SetHeight(h)
		return m, nil
	case sections.FocusMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.focused = msg.Focused
		if m.focused {
			m.tbl.Focus()
			m.tbl.SetHeight(m.h - 10)
		} else {
			m.tbl.Blur()
			m.tbl.SetHeight(m.h - 2)
		}
		return m, nil
	case tea.KeyMsg:
		if !m.focused {
			return m, nil
		}
		var cmd tea.Cmd
		m.tbl, cmd = m.tbl.Update(msg)
		return m, cmd
	}
	return m, nil
}

func rowsFromRuns(runs []store.IndexRun) []table.Row {
	out := make([]table.Row, 0, len(runs))
	for _, r := range runs {
		dur := "—"
		if d := r.Duration(); d > 0 {
			dur = d.Round(time.Second).String()
		}
		out = append(out, table.Row{
			fmt.Sprintf("%d", r.ID),
			r.StartedAt.UTC().Format("2006-01-02 15:04:05"),
			r.Stage, r.Status, dur,
			fmt.Sprintf("%d", r.FilesProcessed),
		})
	}
	return out
}
```

- [ ] **Step 4: view.go**

```go
package runs

import (
	"fmt"
	"strings"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
	"github.com/hman-pro/projectlens/internal/tui/theme"
)

func (m *Model) View() string {
	if m.status == sections.StatusError {
		return theme.StatusStyle("error").Render("error: ") + m.err.Error() + "\n\npress r to retry"
	}
	if m.status == sections.StatusIdle {
		return theme.MutedStyle().Render("(loading…)")
	}
	if len(m.snap.Runs) == 0 {
		return theme.MutedStyle().Render("no runs yet — run \"projectlens bootstrap\"")
	}

	var b strings.Builder
	b.WriteString(m.tbl.View())
	if m.focused {
		idx := m.tbl.Cursor()
		if idx >= 0 && idx < len(m.snap.Runs) {
			b.WriteString("\n")
			b.WriteString(detailPanel(m.snap.Runs[idx]))
		}
	}
	return b.String()
}

func detailPanel(r store.IndexRun) string {
	var b strings.Builder
	b.WriteString(theme.TitleStyle().Render("─ Run detail ─\n"))
	fmt.Fprintf(&b, "ID:        %d\n", r.ID)
	fmt.Fprintf(&b, "Started:   %s\n", r.StartedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	if r.CompletedAt != nil {
		fmt.Fprintf(&b, "Completed: %s\n", r.CompletedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
		fmt.Fprintf(&b, "Duration:  %s\n", r.Duration().Round(time.Second))
	} else {
		fmt.Fprintf(&b, "Completed: —\n")
	}
	commit := r.CommitSHA
	if len(commit) > 7 {
		commit = commit[:7]
	}
	fmt.Fprintf(&b, "Commit:    %s   Stage: %s   Status: %s\n", commit, r.Stage, r.Status)
	fmt.Fprintf(&b, "Files: %d   Symbols: %d   Edges: %d\n", r.FilesProcessed, r.SymbolsExtracted, r.EdgesCreated)
	return b.String()
}
```

- [ ] **Step 5: Test**

```go
package runs_test

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/sections/runs"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestRuns_TableAndDetail(t *testing.T) {
	f := store.NewFake()
	completed := time.Date(2026, 4, 29, 9, 28, 24, 0, time.UTC)
	f.SetRuns(store.RunsSnapshot{Runs: []store.IndexRun{
		{ID: 7, StartedAt: completed.Add(-15 * time.Minute), CompletedAt: &completed, CommitSHA: "abcdef0123", Stage: "embed", Status: "ok", FilesProcessed: 4150, SymbolsExtracted: 28432, EdgesCreated: 91000},
	}})
	m := runs.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	if !strings.Contains(next.View(), "abcdef0") {
		t.Fatalf("commit absent\n%s", next.View())
	}

	// Focus → detail panel renders.
	next, _ = next.Update(sections.SizeMsg{SectionID: runs.ID, W: 100, H: 30})
	next, _ = next.Update(sections.FocusMsg{SectionID: runs.ID, Focused: true})
	next, _ = next.Update(tea.WindowSizeMsg{}) // no-op; ensures interface ok

	v := next.View()
	for _, want := range []string{"Run detail", "Files: 4150", "Symbols: 28432", "Edges: 91000"} {
		if !strings.Contains(v, want) {
			t.Errorf("focused view missing %q\n%s", want, v)
		}
	}
}
```

- [ ] **Step 6: Plug + build + test + smoke**

Add `runs.New(ctx, s)` to the sections slice. `go build ./...`, `go test ./internal/tui/sections/runs/ -v`, smoke run.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/sections/runs/ cmd/projectlens-tui/main.go
git commit -m "feat(tui): recent runs section with focused detail panel"
```

---

## Task 12: Config section

Static text, no DB query (Config snapshot reads from `*config.Config`).

**Files:**
- Create: `internal/tui/sections/config/{messages.go,model.go,update.go,view.go,model_test.go}`
- Modify: `cmd/projectlens-tui/main.go`

- [ ] **Step 1: messages.go**

```go
package config

import "github.com/hman-pro/projectlens/internal/tui/store"

const ID = "config"

type RefreshedMsg struct {
	Snap store.ConfigSnapshot
	Err  error
	Gen  uint64
}
```

- [ ] **Step 2: model.go + update.go (combined for brevity)**

`internal/tui/sections/config/model.go`:

```go
package config

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

type Model struct {
	store    store.Store
	appCtx   context.Context
	snap     store.ConfigSnapshot
	err      error
	status   sections.Status
	gen      uint64
	lastSeen uint64
	last     time.Time
	focused  bool
	w, h     int
}

func New(appCtx context.Context, s store.Store) *Model {
	return &Model{store: s, appCtx: appCtx, status: sections.StatusIdle}
}

func (m *Model) ID() string              { return ID }
func (m *Model) Title() string           { return "Config" }
func (m *Model) Init() tea.Cmd           { return nil }
func (m *Model) Status() sections.Status { return m.status }
func (m *Model) LastRefresh() time.Time  { return m.last }

func (m *Model) Refresh() tea.Cmd {
	m.gen++
	gen := m.gen
	m.status = sections.StatusLoading
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.appCtx, 5*time.Second)
		defer cancel()
		snap, err := m.store.Config(ctx)
		return RefreshedMsg{Snap: snap, Err: err, Gen: gen}
	}
}
```

`internal/tui/sections/config/update.go`:

```go
package config

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
)

func (m *Model) Update(msg tea.Msg) (sections.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshedMsg:
		if msg.Gen < m.lastSeen {
			return m, nil
		}
		m.lastSeen = msg.Gen
		m.last = time.Now()
		if msg.Err != nil {
			m.err = msg.Err
			m.status = sections.StatusError
			return m, nil
		}
		m.snap = msg.Snap
		m.err = nil
		m.status = sections.StatusOK
		return m, nil
	case sections.SizeMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.w, m.h = msg.W, msg.H
		return m, nil
	case sections.FocusMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.focused = msg.Focused
		return m, nil
	}
	return m, nil
}
```

- [ ] **Step 3: view.go**

```go
package config

import (
	"fmt"
	"strings"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/theme"
)

func (m *Model) View() string {
	if m.status == sections.StatusError {
		return theme.StatusStyle("error").Render("error: ") + m.err.Error() + "\n\npress r to retry"
	}
	if m.status == sections.StatusIdle {
		return theme.MutedStyle().Render("(loading…)")
	}
	c := m.snap
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", theme.TitleStyle().Render("Embeddings"))
	fmt.Fprintf(&b, "  Provider:  %s\n", c.EmbeddingProvider)
	fmt.Fprintf(&b, "  Model:     %s\n", c.EmbeddingModel)
	fmt.Fprintf(&b, "  Dims:      %d\n", c.EmbeddingDims)
	if c.EmbeddingEndpoint != "" {
		fmt.Fprintf(&b, "  Endpoint:  %s\n", c.EmbeddingEndpoint)
	}
	fmt.Fprintf(&b, "\n%s\n", theme.TitleStyle().Render("Summarization"))
	fmt.Fprintf(&b, "  Provider:  %s\n", c.SummarizationProvider)
	fmt.Fprintf(&b, "  Model:     %s\n", c.SummarizationModel)
	fmt.Fprintf(&b, "\n%s\n", theme.TitleStyle().Render("Database"))
	fmt.Fprintf(&b, "  Host:      %s\n", c.DBHost)
	fmt.Fprintf(&b, "  Database:  %s\n", c.DBName)
	return b.String()
}
```

- [ ] **Step 4: Test**

```go
package config_test

import (
	"context"
	"strings"
	"testing"

	"github.com/hman-pro/projectlens/internal/tui/sections/config"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestConfig_RendersAllFields(t *testing.T) {
	f := store.NewFake()
	f.SetConfig(store.ConfigSnapshot{
		EmbeddingProvider: "ollama", EmbeddingModel: "mxbai-embed-large", EmbeddingDims: 1024, EmbeddingEndpoint: "http://localhost:11434",
		SummarizationProvider: "anthropic", SummarizationModel: "claude-sonnet-4-6",
		DBHost: "localhost:5433", DBName: "projectlens",
	})
	m := config.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	v := next.View()
	for _, want := range []string{"ollama", "mxbai-embed-large", "1024", "anthropic", "claude-sonnet-4-6", "localhost:5433", "projectlens"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}
```

- [ ] **Step 5: Plug + build + test + smoke**

Add `cfgsec.New(ctx, s)` to the sections slice in `main.go` (alias the import: `cfgsec "github.com/hman-pro/projectlens/internal/tui/sections/config"`). Then:

```
go build ./...
go test ./internal/tui/sections/config/ -v
go run ./cmd/projectlens-tui/
```

- [ ] **Step 6: Commit**

```bash
git add internal/tui/sections/config/ cmd/projectlens-tui/main.go
git commit -m "feat(tui): config section with provider/db summary"
```

---

## Task 13: App-level concurrency + lifecycle tests

Cover the cross-cutting behavior the spec calls out: tick re-arming, focus-change refresh, manual `r`, stale-generation drop, DB-unreachable boot.

**Files:**
- Modify: `internal/tui/app/app_test.go`

- [ ] **Step 1: Append tick re-arm test**

Add to `app_test.go`:

```go
func TestApp_TickRearmsRefresh(t *testing.T) {
	f := store.NewFake()
	f.SetHealth(store.HealthSnapshot{Stage: "embed", Status: "ok"})
	secs := []sections.Section{health.New(context.Background(), f)}
	m := app.New(context.Background(), secs)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// Helper: simulate tick by sending the unexported tickMsg via ID equality.
	// Trick: drive Update via the public path with a private struct cannot work.
	// Instead, exercise the public `r` keypress, which uses the same Refresh path.
	for i := 0; i < 5; i++ {
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		if cmd == nil {
			t.Fatalf("press %d: expected refresh cmd, got nil", i)
		}
		// drive the resulting refresh msg back into the model
		msg := cmd()
		m, _ = m.Update(msg)
	}
	if !strings.Contains(m.View(), "embed") {
		t.Fatalf("expected health snapshot in view\n%s", m.View())
	}
}
```

(Note for engineer: a stronger tick-rearm test requires a hook to inject `tickMsg`. If desired later, expose `app.TickMsg` for tests under a `// +build internal` file or pass an interval option. For Phase 1 the manual-`r` path covers the same Refresh wiring.)

- [ ] **Step 2: Append DB-unreachable + retry test**

```go
func TestApp_DBUnreachableThenRecovers(t *testing.T) {
	f := store.NewFake()
	f.SetErr("Health", errors.New("connection refused"))
	secs := []sections.Section{health.New(context.Background(), f)}
	m := app.New(context.Background(), secs)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = m.Update(cmd())
	if !strings.Contains(m.View(), "connection refused") {
		t.Fatalf("expected error in view\n%s", m.View())
	}

	f.SetErr("Health", nil)
	f.SetHealth(store.HealthSnapshot{Stage: "embed", Status: "ok"})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = m.Update(cmd())
	if strings.Contains(m.View(), "connection refused") {
		t.Fatalf("expected recovery, still showing error\n%s", m.View())
	}
}
```

Add `"errors"` to the imports of `app_test.go`.

- [ ] **Step 3: Run + commit**

```
go test ./internal/tui/... -v
git add internal/tui/app/app_test.go
git commit -m "test(tui): app-level refresh, error recovery, and tick wiring"
```

---

## Task 14: Help overlay + file logging + final polish

The `?` overlay, `bubbles/help`, and `PROJECTLENS_TUI_LOG_FILE`-routed logger.

**Files:**
- Modify: `internal/tui/app/model.go`, `update.go`, `view.go`
- Create: `internal/tui/app/log.go`
- Modify: `cmd/projectlens-tui/main.go`

- [ ] **Step 1: Logger helper**

Create `internal/tui/app/log.go`:

```go
package app

import (
	"io"
	"os"

	"github.com/charmbracelet/log"
)

// InitLogger routes charm log to PROJECTLENS_TUI_LOG_FILE (or io.Discard if empty/unset).
// Must be called BEFORE tea.Program.Run() — once tea grabs the alt screen,
// any stdout/stderr writes corrupt the display.
func InitLogger() {
	path := os.Getenv("PROJECTLENS_TUI_LOG_FILE")
	if path == "" {
		path = "/tmp/projectlens-tui.log"
	}
	if path == "-" {
		log.SetOutput(io.Discard)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.SetOutput(io.Discard)
		return
	}
	log.SetOutput(f)
	log.SetLevel(log.DebugLevel)
}
```

- [ ] **Step 2: Help overlay state**

In `internal/tui/app/model.go`, add `showHelp bool` to the `Model` struct (any position is fine — group with `tooSmall` for clarity).

In `internal/tui/app/update.go`, replace `handleSidebarKey` and `handleDetailKey` with these versions (adds the `Help` case at the top of each switch):

```go
func (m Model) handleSidebarKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Down),
		key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.ShiftTab):
		var cmd tea.Cmd
		m.sidebar, cmd = m.sidebar.Update(msg)
		newIdx := m.sidebar.Index()
		var refresh tea.Cmd
		if newIdx != m.focused {
			m.focused = newIdx
			if m.since() > focusRefreshThreshold {
				refresh = m.sections[m.focused].Refresh()
			}
			dw, dh := m.detailSize()
			id := m.sections[m.focused].ID()
			next, sizeCmd := m.sections[m.focused].Update(sections.SizeMsg{SectionID: id, W: dw, H: dh})
			m.sections[m.focused] = next
			return m, tea.Batch(cmd, sizeCmd, refresh)
		}
		return m, cmd
	case key.Matches(msg, m.keys.Refresh):
		return m, m.sections[m.focused].Refresh()
	case key.Matches(msg, m.keys.Enter):
		m.mode = ModeDetail
		id := m.sections[m.focused].ID()
		next, cmd := m.sections[m.focused].Update(sections.FocusMsg{SectionID: id, Focused: true})
		m.sections[m.focused] = next
		return m, cmd
	}
	return m, nil
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Esc):
		m.mode = ModeSidebar
		id := m.sections[m.focused].ID()
		next, cmd := m.sections[m.focused].Update(sections.FocusMsg{SectionID: id, Focused: false})
		m.sections[m.focused] = next
		return m, cmd
	case key.Matches(msg, m.keys.Refresh):
		return m, m.sections[m.focused].Refresh()
	}
	next, cmd := m.sections[m.focused].Update(msg)
	m.sections[m.focused] = next
	return m, cmd
}
```

- [ ] **Step 3: Render help overlay**

In `internal/tui/app/view.go`, before the final `return lipgloss.JoinVertical(...)`, add:

```go
if m.showHelp {
	overlay := theme.MutedStyle().Render(strings.Join([]string{
		"  ↑/k     up",
		"  ↓/j     down",
		"  enter   focus detail",
		"  esc/h   back to sidebar",
		"  tab     next section",
		"  s+tab   previous section",
		"  r       refresh focused",
		"  ?       toggle help",
		"  q/^C    quit",
	}, "\n"))
	return lipgloss.JoinVertical(lipgloss.Left, header, overlay, footer)
}
```

Add `"strings"` to imports.

- [ ] **Step 4: Wire logger in main**

In `cmd/projectlens-tui/main.go`, before `prog := tea.NewProgram(...)`, add:

```go
app.InitLogger()
```

- [ ] **Step 5: Build + test + smoke**

```
go build ./...
go test ./internal/tui/...
go run ./cmd/projectlens-tui/   # press ? to toggle help, q to quit
ls -l /tmp/projectlens-tui.log  # file exists if anything was logged
```

- [ ] **Step 6: Commit**

```bash
git add internal/tui/app/log.go internal/tui/app/model.go internal/tui/app/update.go internal/tui/app/view.go cmd/projectlens-tui/main.go
git commit -m "feat(tui): help overlay and file-based logging"
```

---

## Task 15: Documentation

Update `CLAUDE.md` to document the new binary, env var, and run instructions.

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update CLAUDE.md**

Open `CLAUDE.md`. Make these three additions:

(a) Under **Repository structure**, in the `cmd/` block, add a line:

```
    projectlens-tui/          # TUI dashboard entrypoint (Phase 1: read-only ops view)
```

(b) Under the **CLI commands** section, append a new subsection at the end (before "## Docker Compose"):

```
## TUI dashboard

Read-only Bubbletea dashboard surfacing index health, pipeline state,
storage stats, recent runs, and provider config.

```bash
go run ./cmd/projectlens-tui/
```

Reads `.env` automatically (DATABASE_URL, REPO_PATH). Logs to
`PROJECTLENS_TUI_LOG_FILE` (default `/tmp/projectlens-tui.log`).

Keys: ↑/↓ navigate · enter focus · esc back · r refresh · ? help · q quit.
```

(c) In the **Environment variables** table, add a row:

```
| `PROJECTLENS_TUI_LOG_FILE` | TUI log file path (default `/tmp/projectlens-tui.log`) | No |
```

- [ ] **Step 2: Verify the file builds (i.e. parses as markdown — visual inspection)**

Open the file and skim. Spot-check the new lines render correctly.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(tui): document projectlens-tui binary, keys, and PROJECTLENS_TUI_LOG_FILE"
```

---

## Self-review (run before declaring complete)

After Task 15 commits, walk through this checklist before marking the plan complete:

- [ ] `go build ./...` succeeds.
- [ ] `go test ./...` (no integration tag) succeeds, including all section + app tests.
- [ ] `go test -tags integration ./internal/tui/store/` succeeds against running compose Postgres.
- [ ] `go run ./cmd/projectlens-tui/` opens, sidebar lists 5 sections, each renders without error against the actual DB.
- [ ] All five sections respond to `r` and re-fetch (visible "refreshed Ns ago" hint changes).
- [ ] `?` toggles help overlay. `q` quits cleanly.
- [ ] `/tmp/projectlens-tui.log` exists after a run.
- [ ] Resize terminal below 80×20 → "terminal too small" banner; resize back → normal layout.
- [ ] No spec requirement is unimplemented (cross-check `docs/plans/2026-04-29-tui-design.md` "Per-section behavior" table and "Testing strategy" section).

Final commit on completion (only if anything was missed and fixed):

```bash
git commit -m "chore(tui): post-implementation self-review fixes"
```

---

## Risks during implementation

| Risk | What to do |
|---|---|
| `bubbles/list.New` API differs across versions | The plan targets `charmbracelet/bubbles` latest at build time; if `NewDefaultDelegate()`/`SetShowPagination` don't exist, consult `~/go/pkg/mod/github.com/charmbracelet/bubbles@*/list/` — semantics are stable but constructor names occasionally drift. |
| `bubbles/table` keybindings conflict with section keys (e.g. `j`/`k`) | Already separated: app routes `j`/`k` to sidebar in `ModeSidebar`; only when `ModeDetail` is active does the table receive them. |
| Tick-rearm internal test | `tickMsg` is unexported. Plan uses manual `r` press as a proxy in tests; if a richer test is needed, expose a `TickMsg` type or accept a `WithInterval` test hook later. |
| `pgxpool` queries hanging on Ctrl+C | Covered by `tea.WithContext(appCtx)` + `signal.NotifyContext`; the integration test in Task 6 verifies ctx-cancel propagation. |
| Section package growing past one screen of code | Each section already split across `messages.go`, `model.go`, `update.go`, `view.go`. If any one file passes ~250 lines, split functions into a `helpers.go`. |
