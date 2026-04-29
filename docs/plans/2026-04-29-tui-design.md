# ProjectLens TUI — Foundation + Phase 1 (Operational Dashboard)

**Status:** Design
**Date:** 2026-04-29
**Author:** homan
**Scope:** Foundation TUI binary + Phase 1 dashboard (read-only operational view).
Phase 2 (indexer control plane) and Phase 3 (knowledge/code explorer) are out of scope for this spec but the foundation is shaped to absorb them.

## Goals

1. Give the operator a single keyboard-driven view into ProjectLens's current health: index freshness, pipeline state, storage stats, recent runs, and provider configuration.
2. Establish the foundation (binary, packages, store layer, section contract) that Phases 2 and 3 will extend without restructuring.
3. Read-only against the existing Postgres database. No writes from the TUI in Phase 1.

## Non-goals

- Triggering reindex or controlling the indexer (Phase 2).
- Browsing symbols / knowledge / running queries (Phase 3).
- Provider reachability checks (deferred until a `doctor` command exists; would block the UI loop).
- Persisting run errors (`index_runs` has no error column today; adding it is a separate change).
- Persisted user preferences (theme, layout ratios).
- Mouse interaction (Phase 1 is keyboard-only; mouse capture is intentionally not enabled).

## Tech choices

| Decision | Choice | Rationale |
|---|---|---|
| Framework | `charmbracelet/bubbletea` + `lipgloss` + `bubbles` | Idiomatic Go, charm stack already indirect dep, composable, testable, scales to Phases 2/3 (streaming via messages, list/table widgets). |
| Packaging | Separate binary `cmd/projectlens-tui/` | Clean dep boundary; mirrors existing `cmd/projectlens-mcp/`. CLI binary stays slim. |
| Layout | Sidebar (section list) + detail pane | Scales as sections grow rich; one section at a time gets full real estate. |
| Refresh | Hybrid — 30s tick on visible section + focus-change refresh + manual `r` | Cheap queries; staleness obvious during a reindex; no constant DB hammer. |
| DB access | `pgxpool` shared pool, 5s per-query timeout | Already in repo; safe across goroutines. |

## Architecture

```
cmd/projectlens-tui/
  main.go                  # config load, pool, tea.Program

internal/tui/
  app/
    model.go               # appModel: sections list, focused section, mode, status bar
    update.go
    view.go                # lipgloss layout: header | sidebar | detail | footer
    keys.go                # global keymap
    app_test.go            # navigation, focus, refresh wiring
  sections/
    section.go             # Section interface, RefreshedMsg, Status enum
    health/
    pipeline/
    storage/
    runs/
    config/
  store/
    store.go               # Store interface + snapshot types
    pg.go                  # pgxpool-backed implementation
    fake.go                # in-memory canned snapshots for tests
    pg_integration_test.go # //go:build integration
  components/
    panel.go               # bordered titled box
    keyhelp.go             # footer key hints
    spinner.go             # loading indicator
  theme/
    theme.go               # palette + lipgloss styles
```

**Boundaries:**

- `store` returns plain typed snapshots; no Bubbletea coupling.
- Each section package owns its own `tea.Model`, widgets, snapshot type, and tests. Self-contained.
- `app` owns sidebar, focus routing, refresh scheduling, status bar.
- Import direction: `app → sections → store`; `components`/`theme` are leaves.

## Components

### Section interface

```go
type Status int
const (
    StatusIdle Status = iota
    StatusLoading
    StatusOK
    StatusError
)

type Section interface {
    ID() string                              // stable routing key, e.g. "health"
    Title() string                           // sidebar label
    Init() tea.Cmd
    Update(msg tea.Msg) (Section, tea.Cmd)   // returns Section, NOT tea.Model — avoids casts
    View() string
    Refresh() tea.Cmd                        // yields a section-specific *RefreshedMsg
    Status() Status
    LastRefresh() time.Time
}
```

`Update` returns `Section` (not `tea.Model`) so the app router can store the
result back without runtime type assertions. All concrete section models are
pointer types (`*health.Model`, etc.) to make in-place state updates ergonomic.

**Size and focus are messages, not setters.** The app emits `SizeMsg{W,H int}`
and `FocusMsg{Focused bool}` via `tea.Cmd` to the targeted section; sections
absorb them inside their own `Update`. No mutating methods on the interface.

**Refresh result types are section-specific** to avoid `any` payloads:

```go
// In package sections/health
type RefreshedMsg struct {
    Snap HealthSnapshot
    Err  error
    Gen  uint64
}

// Each section package defines its own RefreshedMsg with the typed snapshot.
// The app dispatches messages to all sections; each section ignores messages
// that aren't its own type.
```

The generation counter increments on each `Refresh()` call within the section;
incoming `RefreshedMsg` with stale `Gen` is dropped silently inside the
section's `Update`.

### Store interface

```go
type Store interface {
    Health(ctx context.Context) (HealthSnapshot, error)
    Pipeline(ctx context.Context) (PipelineSnapshot, error)
    Storage(ctx context.Context) (StorageSnapshot, error)
    Runs(ctx context.Context, limit int) (RunsSnapshot, error) // limit ≤ runsMaxRows (100)
    Config(ctx context.Context) (ConfigSnapshot, error)
}

const runsMaxRows = 100 // hard cap; Runs() returns at most this many rows

// Field names mirror the actual `index_runs` columns (started_at, completed_at,
// commit_sha, files_processed, symbols_extracted, edges_created, status, stage).
// No `error` column exists; runs detail does not surface error text.
type HealthSnapshot struct {
    StartedAt        time.Time
    CompletedAt      *time.Time     // nil while a run is in progress
    CommitSHA        string         // full SHA; UI may abbreviate to 7 chars
    Stage            string
    Status           string
    FilesProcessed   int
    SymbolsExtracted int
    EdgesCreated     int
    HeadCommit       string         // git HEAD of the target repo, may be empty
    Staleness        time.Duration  // time.Since(StartedAt)
}

func (h HealthSnapshot) Duration() time.Duration {
    if h.CompletedAt == nil { return 0 }
    return h.CompletedAt.Sub(h.StartedAt)
}

type StageStat struct {
    Name             string
    LastRunStartedAt time.Time
    Status           string
    FilesProcessed   int
    Duration         time.Duration  // 0 when not yet completed
}
type PipelineSnapshot struct{ Stages []StageStat }

type TableStat struct {
    Name        string
    EstRows     int64  // pg_stat_user_tables.n_live_tup — approximate
    Bytes       int64
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
type RunsSnapshot struct{ Runs []IndexRun }

type ConfigSnapshot struct {
    EmbeddingProvider     string
    EmbeddingModel        string
    EmbeddingDims         int
    SummarizationProvider string
    SummarizationModel    string
    DBHost                string
    DBName                string
}
```

## Data flow

**Boot:**

1. `main.go` calls `signal.NotifyContext(context.Background(), SIGINT, SIGTERM)` → `appCtx`. The cancel is `defer`-ed.
2. Load config, open `pgxpool` (using `appCtx`), construct `store.NewPG(pool, cfg, repoPath)`.
3. `app.New(appCtx, store, cfg)` builds the 5 sections and stores `appCtx` for use as the parent of all DB-call timeouts.
4. `tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(appCtx)).Run()`.
   - `WithMouseCellMotion()` is **not** enabled — Phase 1 is keyboard-only and mouse capture would alter terminal behavior for no benefit.
5. `Init()` returns `tea.Batch(sections[focused].Refresh(), tickCmd(30*time.Second))`.

**Tick re-arming.** `tea.Tick` fires once. The app's `tickMsg` handler must:
1. Issue `sections[focused].Refresh()` (only the visible section).
2. Return a fresh `tickCmd(30*time.Second)` so the next tick is scheduled.
There is no header clock; one tick stream is enough.

**Steady state:**

- `tickMsg` (every 30s) → `Refresh()` focused section + reissue tick.
- Sidebar selection change → if `time.Since(focused.LastRefresh()) > 2s` issue `Refresh()`, else use cached snapshot.
- `keyMsg{r}` → unconditional `Refresh()` on focused section.
- Section-typed `RefreshedMsg` is delivered to all sections via `Update`; non-matching sections ignore it. The targeted section drops the message if its `Gen` is stale; otherwise it absorbs the snapshot.
- `keyMsg{enter}` → app emits `FocusMsg{Focused: true}` to the focused section, which transitions to its detail rendering.
- `keyMsg{esc/h}` → app emits `FocusMsg{Focused: false}`; section returns to summary rendering.
- `WindowSizeMsg` → app computes per-section width/height and emits `SizeMsg{W,H}` to that section.

**Concurrency:**

- All DB calls derive from `appCtx`: `ctx, cancel := context.WithTimeout(appCtx, 5*time.Second)` inside the `tea.Cmd`. On quit, `appCtx` is cancelled and in-flight queries return `context.Canceled`; `pgxpool.Close()` runs in `main` after `tea.Program.Run()` returns.
- Section state is only mutated inside that section's `Update`. No locks.
- `pgxpool` is goroutine-safe; `tea.Cmd` may run on any goroutine — the message bus is the only crossing back into the model.
- Generation counter increments on every `Refresh()` issued by a section; results with stale `Gen` are dropped (covers out-of-order responses where a fast retry races a slow first call).

## Per-section behavior

| Section | Summary view | Detail view (Enter) | Widget |
|---|---|---|---|
| Health | Key/value lines: started, completed, commit (7-char), stage, status, duration, files/symbols/edges counts, staleness | same + 5 most recent runs as a mini-list, color-coded status | text + small list |
| Pipeline | table: stage / last started / status / files / duration (one row per stage) | same table sortable by column (`s` cycles), `j/k` row nav | `bubbles/table` |
| Storage | table: table-name / rows (estimate) / size + chunks-by-type footer | same + embedded vs unembedded bar per `source_type` | `bubbles/table` |
| Recent runs | last 10 runs as table (id, started, stage, status, duration, files) | scrollable table of up to `runsMaxRows` (100) runs; Enter shows row detail panel inline (no modal): all stats + completed-at + commit. **No error text** — `index_runs` has no error column today. | `bubbles/table` |
| Config | static keys: embedding provider/model/dim/endpoint, summarization provider/model, DB host/dbname | same view; no provider reachability or "last successful call" — both require schema additions outside Phase 1 scope | text |

**Phase 1 read-only constraints surfaced in UI:**

- **Provider reachability** is not displayed. A future `projectlens doctor` command will own pings; until then the Config section omits status indicators rather than render misleading dots.
- **Run errors** are not displayed because `index_runs` does not currently store error text. Adding an `error TEXT` column is out of Phase 1 scope; once added, the runs detail can grow an inline error panel without contract changes.
- **Storage row counts** come from `pg_stat_user_tables.n_live_tup` and are labelled `~rows` (estimate). Exact counts would require `count(*)` per table on every refresh — too heavy for a 30s tick.

## Layout

```
┌─ projectlens · dashboard ─────────── refreshed 12s ago ─────────────┐
│┌─ Sections ──────┐┌─ Index health ───────────────────────────────┐│
││> Index health   ││ Started:     2026-04-29 09:14:02 UTC         ││
││  Pipeline       ││ Completed:   2026-04-29 09:28:24 UTC         ││
││  Storage        ││ Commit:      ffdfc82                          ││
││  Recent runs    ││ Stage:       embed                            ││
││  Config         ││ Status:      ok                               ││
│└─────────────────┘│ Duration:    14m22s                           ││
│                   │ Files:       4150  Symbols: 28432  Edges: 91k ││
│                   │ Staleness:   3h ago vs HEAD (a1b2c3d)          ││
│                   └────────────────────────────────────────────────┘│
│ ↑/↓ select  enter focus detail  r refresh  q quit                  │
└────────────────────────────────────────────────────────────────────┘
```

- Sidebar width: `min(24, w/4)`.
- Footer: 1 row of contextual key hints via `bubbles/help`.
- Header: 1 row, title left + "refreshed Ns ago" hint right (computed from focused section's `LastRefresh()` on each render — no separate clock tick).
- `WindowSizeMsg` is consumed by the app, which then dispatches `SizeMsg{W: w-sidebarW-2, H: h-2}` to the focused section.

## Keymap

```
↑ / k         move selection up
↓ / j         move selection down
enter         focus detail on selected section
esc / h       back to sidebar
tab           cycle to next section
shift+tab     cycle to previous section
r             refresh focused section
?             toggle full help overlay
q / ctrl+c    quit
```

Section-specific keys (e.g. column sort `s` in pipeline) take priority while focused.

## Query plan (`store/pg.go`)

Column names below mirror the actual `index_runs` schema (`migrations/001_initial_schema.up.sql` + the `stage` column added in `002`). The `projectlens status` command (`cmd/projectlens/main.go:188`) and `internal/storage/indexruns.go` are the source of truth.

| Snapshot | Query |
|---|---|
| Health | `SELECT id, started_at, completed_at, commit_sha, stage, status, files_processed, symbols_extracted, edges_created FROM index_runs ORDER BY id DESC LIMIT 1` + `git -C <repoPath> rev-parse HEAD` via `os/exec` (best-effort; absence is non-fatal) |
| Pipeline | `SELECT DISTINCT ON (stage) stage, started_at, completed_at, status, files_processed FROM index_runs ORDER BY stage, id DESC` (duration computed in Go from `completed_at - started_at`, zero when null) |
| Storage | `SELECT relname, n_live_tup, pg_total_relation_size(relid) FROM pg_stat_user_tables WHERE relname = ANY($1)` (param: known tables list) **plus** `SELECT c.source_type, count(*) AS total, count(e.id) AS embedded FROM chunks c LEFT JOIN embeddings e ON e.chunk_id = c.id GROUP BY c.source_type` |
| Runs | `SELECT id, started_at, completed_at, commit_sha, stage, status, files_processed, symbols_extracted, edges_created FROM index_runs ORDER BY id DESC LIMIT $1` with `$1 ≤ runsMaxRows (100)` |
| Config | pure read of `*config.Config` + parse `DATABASE_URL` for host/dbname (no DB call) |

The known-tables list for the Storage query is the set declared in CLAUDE.md (`files`, `symbols`, `chunks`, `embeddings`, `summaries`, `edges`, `index_runs`, `git_refs`, `datastore_tables`, `documents`, `symbol_history`, `file_history`, `knowledge_entries`, `schema_migrations`).

## Error handling

| Failure | Behavior |
|---|---|
| DB unreachable on boot | TUI launches; all sections render error state. Footer shows `r retry`. |
| Per-query timeout (>5s) | Section renders `error: query timeout (5s)`. Other sections unaffected. |
| Empty result (no `index_runs` rows) | Neutral message: `no runs yet — run "projectlens bootstrap"`. Not styled as error. |
| Stale `RefreshedMsg.Gen` | Dropped silently. |
| Git HEAD lookup fails | Health renders `staleness: unknown (git unavailable)`; rest of panel still renders. |
| Terminal smaller than 80×20 | Single message: `terminal too small (need ≥ 80×20)`. Resize larger to recover. |
| Panic inside section | Recover at app level; section transitions to `StatusError` with message; app keeps running. |

## Logging

- TUI must not write to stdout/stderr while running (would corrupt the alt screen).
- All logs routed through `charmbracelet/log` to file at `PROJECTLENS_TUI_LOG_FILE` (default `/tmp/projectlens-tui.log`). Empty path = silent.
- `store` errors are logged with full context, then surfaced to the section as a short message.

## Testing strategy

| Layer | Coverage | DB? |
|---|---|---|
| `store/pg_integration_test.go` (`//go:build integration`) | Each query returns expected snapshot shape against current schema; covers a populated index_runs and an empty one | yes |
| `store/fake.go` | In-memory canned snapshots; injectable failures and delays for model tests | no |
| `sections/<name>/section_test.go` | Feed typed `RefreshedMsg`; assert `View()` substrings; feed `tea.KeyMsg`; feed `SizeMsg`/`FocusMsg`; assert state transitions | no |
| `app/app_test.go` | Boot with fake store; send key sequences; assert focus/mode transitions; golden-file render at 100×30 | no |
| `cmd/projectlens-tui/` | Smoke build only | no |

**Concurrency / failure-edge tests required (in `app_test.go` unless noted):**

- **Stale-generation drop** — issue two refreshes in flight; deliver them out of order; assert only the newest snapshot ends up in section state.
- **Out-of-order refresh** — slow-fake returns the first snapshot after the second; same assertion.
- **Query timeout** — fake returns `context.DeadlineExceeded` after 5s simulated wait; section transitions to `StatusError` with `query timeout (5s)`.
- **DB unreachable on boot** — fake constructor returns `nil` store + boot error; TUI still launches; sections render error state with `r retry` hint; pressing `r` re-runs `Refresh()` and recovers when fake comes online.
- **Tiny terminal recovery** — feed `WindowSizeMsg{60,15}`, assert the small-terminal banner; feed `WindowSizeMsg{120,40}`, assert normal layout returns.
- **Tick re-arming** — drive 5 successive `tickMsg` deliveries and assert that each one yields both a refresh of the focused section and a fresh `tickCmd`.
- **Quit cancels in-flight queries** (in `store/pg_integration_test.go`) — start a slow query, cancel `appCtx`, assert the query returns `context.Canceled` within 50ms.

**Golden files:** `lipgloss.NewRenderer` with profile forced to `Ascii` in tests; goldens in `testdata/<case>.txt`; `-update` flag rewrites.

## Build sequence

Each step ends in green build + tests + a runnable binary.

1. **Skeleton binary** — `cmd/projectlens-tui/main.go` + minimal `app` package showing a "hello" view that quits on `q`.
2. **Store interface + fake** — define `Store`, snapshot types, in-memory fake. Compile-only.
3. **Store PG impl + integration test** — write all 5 queries; integration test against compose DB.
4. **Sidebar + theming + footer** — `app` renders fixed sidebar of 5 placeholder titles, footer keybindings, focus navigable.
5. **Health section (vertical slice)** — first end-to-end: section package, summary view, detail view, refresh wiring, unit tests. Validates the section contract.
6. **Pipeline / Storage / Runs / Config** — each cookie-cutter from health, parallelizable.
7. **Refresh tick + generation counter** — `tea.Tick(30s)` with explicit re-arming on each `tickMsg`; focus-change refresh (≥ 2s threshold); manual `r`; stale-`Gen` drop. Concurrency tests from the testing section (stale-drop, out-of-order, tick re-arm).
8. **Resize / error / empty states** — `WindowSizeMsg`/`SizeMsg` plumbing; small-terminal banner + recovery; per-section error rendering; DB-unreachable boot; empty-DB rendering. Golden-file tests.
9. **Polish** — `?` help overlay, color theme, alt-screen, file logging at `PROJECTLENS_TUI_LOG_FILE`.
10. **Docs** — `docs/plans/2026-04-29-tui-implementation.md`; CLAUDE.md updated with new binary, build instructions, and `PROJECTLENS_TUI_LOG_FILE` env var.

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| `bubbles/table` styling on narrow terminals | Drop columns progressively: duration first, then status text → glyph. |
| 5s timeout too tight for cold-cache `pg_total_relation_size` | Bump to 10s only for the Storage query if observed in practice. |
| Section package growth bloats `internal/tui/sections` | Each section already isolated to own subpackage; no shared mutable state. Future Phase 2/3 sections add as siblings. |
| Phase 2 (indexer control) needs writes/streams/confirms beyond `Refresh()` | The current `Section` interface is read-only by design. Phase 2 will introduce a sibling interface (e.g. `ActionableSection` adding `Run(action Action) tea.Cmd` + progress messages) that Phase 1 sections continue to satisfy trivially. The store layer will gain a write surface at that point. **The "no restructuring" claim only covers layout / sidebar / interface composition; the section contract will grow.** |
| `pg_stat_user_tables.n_live_tup` lags reality after large mutations | Labelled `~rows` in UI; if it becomes confusing, swap to `count(*)` per table behind a config toggle. |
