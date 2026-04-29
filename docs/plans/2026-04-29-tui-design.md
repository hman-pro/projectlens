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
- Provider reachability checks beyond config display (deferred until a `doctor` command exists).
- Persisted user preferences (theme, layout ratios).

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
    tea.Model
    ID() string                  // stable routing key, e.g. "health"
    Title() string               // sidebar label
    Refresh() tea.Cmd            // yields RefreshedMsg targeted at this section
    SetSize(w, h int)            // app forwards on WindowSizeMsg
    SetFocused(bool)             // summary vs detail rendering hint
    Status() Status
    LastRefresh() time.Time
}

type RefreshedMsg struct {
    SectionID string
    Snapshot  any
    Err       error
    Gen       uint64             // generation counter; section drops if stale
}
```

### Store interface

```go
type Store interface {
    Health(ctx context.Context) (HealthSnapshot, error)
    Pipeline(ctx context.Context) (PipelineSnapshot, error)
    Storage(ctx context.Context) (StorageSnapshot, error)
    Runs(ctx context.Context, limit int) (RunsSnapshot, error)
    Config(ctx context.Context) (ConfigSnapshot, error)
}

type HealthSnapshot struct {
    LastRun    time.Time
    Commit     string
    Stage      string
    Status     string
    Duration   time.Duration
    HeadCommit string
    Staleness  time.Duration
}

type StageStat struct {
    Name           string
    LastRunAt      time.Time
    Status         string
    ItemsProcessed int64
    Duration       time.Duration
}
type PipelineSnapshot struct{ Stages []StageStat }

type TableStat struct {
    Name  string
    Rows  int64
    Bytes int64
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
    ID         int64
    StartedAt  time.Time
    Stage      string
    Status     string
    Duration   time.Duration
    Error      string
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

1. `main.go` loads config, opens `pgxpool`, constructs `store.NewPG(pool, cfg, repoPath)`.
2. `app.New(store, cfg)` builds 5 sections, sidebar list, footer help.
3. `tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()`.
4. `Init()` returns `tea.Batch(sections[0].Refresh(), tickCmd(30s))`.

**Steady state:**

- `tickMsg` every 30s → re-issue `Refresh()` on currently visible section only.
- Sidebar selection change → if `time.Since(focused.LastRefresh()) > 2s` issue `Refresh()`, else use cached snapshot.
- `keyMsg{r}` → unconditional `Refresh()` on focused section.
- `RefreshedMsg{SectionID,Snapshot,Err,Gen}` routed by ID; section absorbs into local state if `Gen` is current.
- `keyMsg{enter}` toggles focused section's `SetFocused(true)` — section may render expanded view.
- `keyMsg{esc/h}` toggles back to summary mode.

**Concurrency:**

- DB calls run inside `tea.Cmd` returned by `Refresh()`, wrapped with `context.WithTimeout(parent, 5s)`.
- Section state mutated only inside its own `Update`. No locks.
- `pgxpool` is thread-safe; `tea.Cmd` may run on any goroutine — message bus is the only crossing point back into the model.
- Per-section generation counter increments on each `Refresh()`; results with stale `Gen` are dropped.

## Per-section behavior

| Section | Summary view | Detail view (Enter) | Widget |
|---|---|---|---|
| Health | 6 key/value lines: last run, commit, stage, status, duration, staleness | same + 5 most recent runs as a mini-list, color-coded status | text + small list |
| Pipeline | table: stage / last-run / status / items / duration (one row per stage) | same table sortable by column (`s` cycles), `j/k` row nav | `bubbles/table` |
| Storage | table: table-name / rows / size + chunks-by-type footer | same + embedded vs unembedded bar per `source_type` | `bubbles/table` |
| Recent runs | last 10 runs as table | scrollable list of all runs, Enter on row → modal viewport with full error text | `bubbles/table` + `bubbles/viewport` |
| Config | static keys (provider/model/dim/host) + grey reachability dot ("unknown") | same + last successful provider call timestamp from `index_runs` metadata if present, else "unknown" | text |

**Reachability dots:** TUI does not ping providers (would block). Always renders as "unknown" in Phase 1; future `doctor` command will populate.

## Layout

```
┌─ projectlens · dashboard ──────────────────────────────── 11:42:07 ─┐
│┌─ Sections ──────┐┌─ Index health ───────────────────────────────┐│
││> Index health   ││ Last run:    2026-04-29 09:14:02 UTC         ││
││  Pipeline       ││ Commit:      ffdfc82                          ││
││  Storage        ││ Stage:       embed                            ││
││  Recent runs    ││ Status:      ok                               ││
││  Config         ││ Duration:    14m22s                           ││
│└─────────────────┘│ Staleness:   3h ago vs HEAD (a1b2c3d)          ││
│                   └────────────────────────────────────────────────┘│
│ ↑/↓ select  enter focus detail  r refresh  q quit                  │
└────────────────────────────────────────────────────────────────────┘
```

- Sidebar width: `min(24, w/4)`.
- Footer: 1 row of contextual key hints via `bubbles/help`.
- Header: 1 row, title left + clock right.
- `WindowSizeMsg` propagates to focused section via `SetSize(w-sidebarW-2, h-2)`.

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

| Snapshot | Query |
|---|---|
| Health | `SELECT started_at, finished_at, commit, stage, status FROM index_runs ORDER BY id DESC LIMIT 1` + `git -C <repoPath> rev-parse HEAD` via `os/exec` |
| Pipeline | `SELECT DISTINCT ON (stage) stage, finished_at, status, items_processed, finished_at - started_at FROM index_runs ORDER BY stage, id DESC` |
| Storage | `SELECT relname, n_live_tup, pg_total_relation_size(relid) FROM pg_stat_user_tables WHERE relname = ANY($1)` (param: 13 known tables) + `SELECT source_type, count(*), count(*) FILTER (WHERE id IN (SELECT chunk_id FROM embeddings)) FROM chunks GROUP BY source_type` |
| Runs | `SELECT id, started_at, stage, status, finished_at - started_at, COALESCE(error,'') FROM index_runs ORDER BY id DESC LIMIT $1` |
| Config | pure read of `*config.Config` + parse `DATABASE_URL` for host/dbname (no DB call) |

**Implementation note:** verify `index_runs` actual column names against the existing `projectlens status` command before finalizing queries. Current code is the source of truth — match it exactly.

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
| `store/pg_integration_test.go` (`//go:build integration`) | Each query returns expected snapshot shape against current schema | yes |
| `store/fake.go` | In-memory canned snapshots; consumed by all model tests | no |
| `sections/<name>/section_test.go` | Feed `RefreshedMsg{Snapshot}`; assert `View()` substrings; feed `tea.KeyMsg`; assert state | no |
| `app/app_test.go` | Boot with fake store; send key sequences; assert focus/mode transitions; golden-file render at 100×30 | no |
| `cmd/projectlens-tui/` | Smoke build only | no |

**Golden files:** `lipgloss.NewRenderer` with profile forced to `Ascii` in tests; goldens in `testdata/<case>.txt`; `-update` flag rewrites.

## Build sequence

Each step ends in green build + tests + a runnable binary.

1. **Skeleton binary** — `cmd/projectlens-tui/main.go` + minimal `app` package showing a "hello" view that quits on `q`.
2. **Store interface + fake** — define `Store`, snapshot types, in-memory fake. Compile-only.
3. **Store PG impl + integration test** — write all 5 queries; integration test against compose DB.
4. **Sidebar + theming + footer** — `app` renders fixed sidebar of 5 placeholder titles, footer keybindings, focus navigable.
5. **Health section (vertical slice)** — first end-to-end: section package, summary view, detail view, refresh wiring, unit tests. Validates the section contract.
6. **Pipeline / Storage / Runs / Config** — each cookie-cutter from health, parallelizable.
7. **Refresh tick + generation counter** — 30s tick, focus-change refresh, manual `r`, stale-result drop. App-level test.
8. **Resize / error / empty states** — window-size handling, error rendering, empty-DB rendering. Golden-file tests.
9. **Polish** — `?` help overlay, color theme, alt-screen, file logging.
10. **Docs** — `docs/plans/2026-04-29-tui-implementation.md`; CLAUDE.md updated with new binary, build instructions, and `PROJECTLENS_TUI_LOG_FILE` env var.

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| `index_runs` column names differ from assumption | Verify against existing `status` cmd source before query implementation. |
| `bubbles/table` styling on narrow terminals | Drop columns progressively: duration first, then status text → glyph. |
| 5s timeout too tight for cold-cache `pg_total_relation_size` | Bump to 10s only for storage if observed in practice. |
| Section package growth bloats `internal/tui/sections` | Each section already isolated to own subpackage; no shared mutable state. Future Phase 2/3 sections add as siblings. |
| Phase 2/3 want top-level tabs (Dashboard / Indexer / Explorer) | App's sidebar list of 5 becomes tab-of-sidebars; section interface unchanged. |
