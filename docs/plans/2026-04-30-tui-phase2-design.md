# ProjectLens TUI — Phase 2 (Indexer Control Plane) Design

**Status:** Draft
**Date:** 2026-04-30
**Predecessors:** `2026-04-29-tui-design.md` (Foundation + Phase 1), `2026-04-29-tui-implementation.md`

## Scope

Add a write surface to the TUI so users can trigger the most common indexer
operations from inside the dashboard, watch progress live, and inspect logs
afterwards — without leaving the terminal session that already shows index
state.

### In scope

Five actions, surfaced as global hotkeys and rendered in the Pipeline section's
new "Controls" block:

| Key | Name              | Underlying CLI            | Confirmation        | Refresh on success      |
|-----|-------------------|---------------------------|---------------------|-------------------------|
| `R` | reindex           | `projectlens reindex`      | none (single key)   | pipeline, runs, storage |
| `F` | reindex --full    | `projectlens reindex --full` | typed (`reindex`) | pipeline, runs, storage |
| `E` | index-embed       | `projectlens index-embed`  | none                | pipeline, runs          |
| `S` | index-summarize   | `projectlens index-summarize` | none             | pipeline, runs          |
| `H` | index-history     | `projectlens index-history` | none               | pipeline, runs          |

`--db` and `--repo` flags injected by the runner from existing TUI config
(DATABASE_URL + REPO_PATH, already loaded by the Phase 1 store layer).

### Out of scope

- `bootstrap` from TUI (one-time op; CLI suffices).
- `index-knowledge` rebuild action (Phase 3 territory).
- Job queue / multi-slot concurrency.
- Per-stage progress percentages (subprocesses don't emit structured progress
  today; would require an indexer-side change).
- `index_runs.error` column (a separate migration; not blocking Phase 2).
- Run history scrollback beyond the current session (drawer keeps only the
  last completion).

## Foundation reuse

The Phase 1 design already anticipated this work:

> "Phase 2 will introduce a sibling interface (e.g. `ActionableSection` adding
> `Run(action Action) tea.Cmd` + progress messages) that Phase 1 sections
> continue to satisfy trivially. The store layer will gain a write surface at
> that point."

This spec realises that anticipation. Phase 1 sections (Health, Storage, Runs,
Config) continue to satisfy `Section` only and are not modified. The store
layer stays read-only — writes happen by shelling out to the existing CLI,
not via direct DB mutation.

## Architecture

### New package: `internal/tui/jobs/`

Three responsibilities:

1. **`Runner`** — single-slot subprocess executor.
   - Methods: `Start(spec) error`, `Cancel()`, `State() Snapshot`.
   - State: `*exec.Cmd`, stdout/stderr scanners, tail ring buffer (last 200
     lines), file logger handle, start time, status enum
     (`idle | running | cancelling | succeeded | failed | cancelled`).
   - Emits Bubbletea messages via `tea.Program.Send`:
     `JobStartedMsg`, `JobLineMsg{line, stream}`, `JobTickMsg`,
     `JobCompletedMsg{spec, exitCode, duration, logPath, tailSnapshot}`,
     `JobBusyMsg{wantedSpec, runningSpec}`.
   - Internal mutex guards state. The Bubbletea `Update` loop never blocks —
     long ops live in goroutines that `Send` back.

2. **`Spec`** — descriptor:
   ```go
   type Spec struct {
       Key        rune          // 'R', 'F', 'E', 'S', 'H'
       Name       string        // "reindex", "reindex --full", ...
       Args       []string      // ["reindex", "--full"]
       Confirm    ConfirmKind   // ConfirmNone | ConfirmTyped
       Phrase     string        // for ConfirmTyped, e.g. "reindex"
       RefreshOn  []string      // section IDs to refresh on success
   }
   ```

3. **`Registry`** — a `[]Spec` exported as a constant. Pipeline section reads
   it for the Controls block; app keymap reads it for routing.

### Binary resolution

At startup, `jobs.ResolveBinary()` runs the following checks in order:

1. `PROJECTLENS_BINARY` env var — if set and executable, use it.
2. `os.Executable()` → look for `projectlens` as sibling of the running
   `projectlens-tui` binary.
3. `exec.LookPath("projectlens")`.

If all three fail, the app records a warning. Action keys still render but
trigger a toast: `projectlens binary not found; set PROJECTLENS_BINARY`. Read-only
sections continue to work normally.

### New component: `internal/tui/components/jobdrawer/`

Bubbletea component rendering a fixed 8-row strip at the bottom of the
viewport. Visible when a job is in flight or its completion was less than
30 seconds ago. Layout:

```
┌─ reindex --full · 1m 24s · running ──────── c cancel · j hide ─┐
│ INFO indexing 2913 files                                       │
│ INFO chunked 23142 symbols                                     │
│ INFO embedding batch 4/9                                       │
│ ...                                                            │
└────────────────────────────────────────────────────────────────┘
```

Subscribes to `JobLineMsg`, `JobTickMsg`, `JobCompletedMsg`. Tail buffer is
the same ring the runner owns — drawer reads a snapshot per render.

Toggle: `j` hides/shows. When hidden, a single-line indicator
`[reindex 1m 24s]` appears in the existing footer/status bar so progress
isn't completely silent.

### New component: `internal/tui/components/confirmmodal/`

Centered overlay shown when a `Spec.Confirm == ConfirmTyped` action is
pressed:

```
┌─ Confirm reindex --full ───────────────────┐
│ Type 'reindex' and press enter to proceed. │
│ This rewrites embeddings & chunks (~8 min).│
│                                            │
│ > rein_                                    │
│                                            │
│ esc cancel                                 │
└────────────────────────────────────────────┘
```

Esc dismisses with no dispatch. Enter dispatches `RunSpecMsg{spec}` only if
the typed text exactly equals `Spec.Phrase`.

### Section interface evolution

`internal/tui/sections/section.go` gains a sibling interface:

```go
type ActionableSection interface {
    Section
    Actions() []jobs.Spec
}
```

Phase 1 sections continue satisfying `Section` only. Pipeline implements both.
The app's keymap routing iterates Actionable sections to discover hotkeys, but
since the Phase 2 keys are global and identical regardless of focus, in
practice the registry is the source of truth and `Actions()` mirrors it for
the Pipeline view.

### App model changes

`app.Model` gains:

- `runner *jobs.Runner`
- `drawer *jobdrawer.Model`
- `confirm *confirmmodal.Model` (nil when no confirm in flight)
- `binaryPath string` (resolved at `Init`; empty means missing)

Pipeline section's `View()` adds a Controls block listing the five hotkeys
with their action names. Other sections unchanged.

## Job lifecycle

### Trigger path

1. User presses an action key (e.g. `R`). App keymap matches against the
   registry.
2. If `Spec.Confirm == ConfirmTyped`, app pushes `confirmmodal` overlay.
   User types phrase + enter → emits `RunSpecMsg{spec}`. Esc cancels with
   no dispatch.
3. If `Spec.Confirm == ConfirmNone`, single keypress emits `RunSpecMsg{spec}`
   directly.
4. App handler calls `runner.Start(spec)`. If the runner is busy, it returns
   `ErrJobInFlight` and the app emits `JobBusyMsg` → toast
   `reindex already running, c to cancel`.

### Run path

1. `runner.Start` builds `exec.CommandContext(ctx, binaryPath, spec.Args...)`,
   sets `cmd.Env = os.Environ()` (TUI already has DATABASE_URL,
   ANTHROPIC_API_KEY, etc. loaded), opens log file
   `~/.projectlens/tui-runs/<RFC3339-ts>-<spec.Name>.log`, starts the process.
2. Two goroutines (`scanStream(stdout)`, `scanStream(stderr)`) read line by
   line. Each line: append to tail ring buffer (drop oldest at 200), write
   to file, send `JobLineMsg{line, stream}` to the program.
3. A tick goroutine sends `JobTickMsg` every 500ms so elapsed time animates
   even when the process is silent.
4. When `cmd.Wait()` returns, send `JobCompletedMsg{spec, exitCode, duration,
   logPath, tailSnapshot}` and close the log file. Status transitions to
   `succeeded`, `failed`, or `cancelled`.

### Cancel path

1. `c` keypress while running → `runner.Cancel()` → status becomes
   `cancelling` → `cmd.Process.Signal(syscall.SIGTERM)`. Drawer renders
   `cancelling…`.
2. A watchdog goroutine waits 5 seconds. If `cmd.ProcessState` is still nil
   (process alive), call `cmd.Process.Kill()` (SIGKILL).
3. Either way, the normal `cmd.Wait()` path emits `JobCompletedMsg`. Synthetic
   exit code is whatever the OS returns (typically `-1` for signalled
   processes). Status enum disambiguates: cancelled vs failed.

### Post-completion

- Drawer header transitions to `ok in 2.1s · log: <path>` (success) or
  `FAILED exit 1 · log: <path>` (failure) or `cancelled · log: <path>`.
- Drawer auto-collapses 30 seconds after completion. User can re-open with
  `j` at any time before the next `Start` to inspect tail + log path.
- Tail buffer + log path are retained until the next `Start`.
- If `exitCode == 0`, the app dispatches `RefreshMsg` to each section ID in
  `spec.RefreshOn`. Existing Phase 1 refresh logic handles the actual reloads.

### Lifecycle on TUI quit

- If a job is running and the user presses `q`: prompt
  `job in flight, quit anyway? [y/N]` (reuses confirmmodal pattern with
  single-character phrase).
- `y` → `runner.Cancel()` then immediate `tea.Quit` without waiting for
  `cmd.Wait`. The subprocess becomes orphaned but its log file is already
  on disk, so no data is lost.
- `n` (or any other key) → cancel the quit, return to dashboard.

## Keymap precedence

`app.Update` evaluates key messages in this order:

1. If `confirm` modal open → consume keys for typed-confirm input.
2. Else if `drawer` is open and focused → drawer scroll keys (`↑/↓/PgUp/PgDn`).
3. Else global action keys (`R`, `F`, `E`, `S`, `H`, `c`, `j`).
4. Else section-local keys (Phase 1 nav, refresh, help).

Action keys are uppercase to avoid clashing with Phase 1 lowercase keys
(`r` already = refresh). `c` is only meaningful while a job runs; `j` is
only meaningful while a drawer slot is occupied.

## Logs

### Tail buffer

In-memory ring of 200 lines, owned by the runner. Drawer reads it per render.
Resets on each `Start`.

### File logger

Path: `~/.projectlens/tui-runs/<RFC3339-timestamp>-<spec.Name>.log`. Created
fresh per run. Captures full stdout + stderr interleaved (write-as-you-go,
no buffering games). On completion, drawer shows the path so the user can
`less` it from another terminal.

If `~/.projectlens/tui-runs/` is not writable, fall back to
`os.TempDir()/projectlens-tui-runs/` and log a one-time warning toast.

No retention/rotation in Phase 2 — out-of-scope; users can `rm -rf` the
directory if it grows. Document the path in the TUI README.

## Error handling + edge cases

| Case | Behaviour |
|------|-----------|
| Binary not found at startup | Warning toast at startup; action keys refuse with same message; read-only TUI continues to work. |
| Log dir not writable | Fall back to `os.TempDir()/projectlens-tui-runs/`; warn once. |
| Huge output (25k lines) | Tail ring drops oldest; file logger gets full output. No memory growth. |
| Subprocess hangs silent | Tick goroutine still drives elapsed redraw. User can cancel with `c`. |
| Subprocess killed externally (OOM, external `kill -9`) | `cmd.Wait` returns non-zero; normal `JobCompletedMsg{failed}` fires. |
| TUI crashes mid-run | Child inherits stdout/stderr pipes; pipes close on TUI death; child likely SIGPIPE on next write and exits. Not bulletproof. Documented in README. |
| Esc during typed-confirm | Modal closes, no dispatch, focus returns to prior view. |
| Multiple sections refresh post-success | Phase 1 already runs each section's refresh as an independent `tea.Cmd`; no new coupling needed. |

## Testing strategy

### Unit

- `internal/tui/jobs/runner_test.go` — fake `Cmd` via an injected factory
  (`type cmdFactory func(name string, args []string) *exec.Cmd`); verify:
  - state transitions (idle → running → succeeded/failed/cancelled)
  - tail buffer ring eviction at capacity
  - file write happens line by line
  - cancel emits SIGTERM then SIGKILL after 5s
- `internal/tui/jobs/registry_test.go` — assert no key collisions among Specs;
  every Spec has non-empty Name and Args; ConfirmTyped specs have non-empty
  Phrase.
- `internal/tui/components/confirmmodal/confirmmodal_test.go` — typed-confirm
  matches phrase exactly (case-sensitive); esc cancels; partial match no-op.
- `internal/tui/components/jobdrawer/jobdrawer_test.go` — `lipgloss`-rendered
  snapshots for states {idle, running, success, failed, cancelled}.

### Integration (`//go:build integration`)

- `internal/tui/jobs/runner_integration_test.go` — spawn real
  `projectlens status` (cheapest non-mutating subcommand) end-to-end, assert
  `JobCompletedMsg` fires with exit 0 and ≥1 line in tail. Requires DB
  available (same gate as existing Phase 1 integration tests).

### App-level

- Extend existing `internal/tui/app/app_test.go` with action-key flow:
  press `R` → no modal → `RunSpecMsg` dispatched; press `F` → modal opens →
  type `reindex` + enter → `RunSpecMsg` dispatched; press `F` → modal opens
  → esc → no dispatch. Uses a stub runner that captures dispatches without
  actually running anything.

### Not tested

- Real `reindex --full` in CI (slow, mutates DB).
- Watchdog SIGKILL timing (OS-dependent; verified manually).

## Risks

| Risk | Mitigation |
|------|------------|
| Subprocess output volume DoSes the Bubbletea message channel | `JobLineMsg` send uses non-blocking `Program.Send` and Bubbletea coalesces; tail buffer is fixed-size; file write is the durable record. If observed in practice, batch lines (e.g. emit every 50 lines or 100ms) — defer until measured. |
| Users accidentally trigger long ops | Tiered confirm (typed for `--full`); single-key for cheap ops mirrors the cost of those ops (incremental reindex on Ingest = ~2s). |
| Binary version skew (TUI ships with one `projectlens`, user has newer on PATH) | `PROJECTLENS_BINARY` env override gives an explicit pin; `os.Executable` sibling lookup makes co-located binaries Just Work. |
| Cancel races with normal completion | Status enum disambiguates; both paths funnel through the same `cmd.Wait` and `JobCompletedMsg` is sent exactly once. |
| Log file dir grows unbounded | Documented; manual cleanup by user. Add rotation later if it bites. |

## Open questions deferred to implementation

- Exact line-batching threshold for `JobLineMsg` if profiling shows lag.
  Default: send every line.
- Whether the drawer should expose a "open log file" hotkey that shells out
  to `$EDITOR` or `less`. Probably yes, but defer to a follow-up commit if
  scope balloons.
- Whether `index-history` should default to `--since` from the last run
  timestamp (already the CLI default since 2026-04-22) — yes, no special
  TUI handling needed.

## Success criteria

1. From a running TUI, pressing `R` triggers an incremental reindex,
   shows the bottom drawer with live log lines, and the Pipeline + Runs +
   Storage sections refresh automatically on success.
2. Pressing `F` requires typing `reindex` to confirm, then runs
   `reindex --full`. Cancelling mid-run via `c` cleanly terminates the child
   and shows `cancelled` status.
3. Failed runs (e.g. embed stage hits a rate-limit) display
   `FAILED exit <n> · log: <path>` and the user can read the full log from
   another terminal.
4. Phase 1 sections (Health, Storage, Runs, Config) continue to function
   unchanged. Their tests still pass with no modifications.
5. Quitting the TUI mid-run prompts for confirmation; on confirm, the TUI
   exits cleanly without orphaning a zombie.
