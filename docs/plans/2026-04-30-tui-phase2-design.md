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

| Key | Name              | Underlying CLI            | Confirmation               | Refresh on success      |
|-----|-------------------|---------------------------|----------------------------|-------------------------|
| `R` | reindex           | `projectlens reindex`      | y/N preflight              | pipeline, runs, storage |
| `F` | reindex --full    | `projectlens reindex --full` | typed (`reindex`)        | pipeline, runs, storage |
| `E` | index-embed       | `projectlens index-embed`  | y/N preflight              | pipeline, runs          |
| `S` | index-summarize   | `projectlens index-summarize` | y/N preflight           | pipeline, runs          |
| `H` | index-history     | `projectlens index-history` | y/N preflight             | pipeline, runs          |

**Explicit targeting (no env-only injection).** The runner constructs every
command line with explicit `--config <path> --db <url> --repo <path>` flags
resolved from the TUI's loaded `config.Config` + `DATABASE_URL` +
`REPO_PATH`. Subprocesses must never silently fall back to a different
config or DSN than what the TUI is rendering. A unit test
(`runner_command_test.go`) asserts each Spec's built `*exec.Cmd.Args`
contains those three flags with values matching the TUI's resolved config.

**Preflight before confirm.** Every action runs a fast read-only preflight
against the DB before opening its confirm modal:

| Action | Preflight query | Modal headline |
|--------|-----------------|----------------|
| reindex | files modified since last run | "reindex 124 changed files? [y/N]" |
| reindex --full | total files in scope | "RE-INDEX 2913 files (~8 min, rewrites embeddings) — type 'reindex' to confirm" |
| index-embed | chunks where `embedding IS NULL` | "embed 412 chunks via openai? [y/N]" |
| index-summarize | packages where `summary IS NULL` | "summarize 17 packages via anthropic? [y/N]" |
| index-history | commits since last `file_history.author_date` | "ingest 227 commits since 2026-04-29? [y/N]" |

Modal shows count + provider/cost driver before the user commits. y/N for
single-key confirms, typed phrase for `reindex --full`. Esc cancels at any
stage. Preflight queries use the existing `tui/store` read interface (each
gets a new method like `EmbedPending(ctx) (int, error)`).

### Out of scope

- `bootstrap` from TUI (one-time op; CLI suffices).
- `index-knowledge` rebuild action (Phase 3 territory).
- Job queue / multi-slot concurrency.
- Per-stage progress percentages (subprocesses don't emit structured progress
  today; would require an indexer-side change).
- `index_runs.error` column (a separate migration; not blocking Phase 2).
- Run history scrollback beyond the current session (drawer keeps only the
  last completion).
- **Cross-process writer lock** (see Prerequisites below). Phase 2 ships with
  a single-process slot only. A DB advisory lock that excludes concurrent
  CLI writers is a separate prerequisite design touching the indexer + CLI
  side, not just the TUI.

## Prerequisites (must land before Phase 2)

**Cross-process writer lock.** The indexer's mutating commands (`bootstrap`,
`reindex`, `index-embed`, `index-summarize`, `index-history`) currently
have no inter-process serialization. Two CLI invocations or a CLI + TUI
trigger overlapping today; the existing delete-then-insert flows for
symbols and `co_changes` edges can produce duplicates or stale rows under
concurrent writers. This is latent in the CLI but Phase 2 makes it easier
to hit.

The fix is a Postgres advisory lock acquired at the start of every
mutating command, released on completion. Failure-to-acquire surfaces as
a structured error the TUI maps to a "another writer holds the lock"
toast with the holder's PID/host/started-at (recorded in a small
`index_locks` row when the lock is taken).

This prerequisite is filed as `docs/plans/2026-04-30-indexer-writer-lock-design.md`
and must merge before the Phase 2 implementation begins.
Phase 2 itself depends on the lock semantics being in place; the TUI
displays the lock-busy state but does not invent it.

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
       Key       rune          // 'R', 'F', 'E', 'S', 'H'
       Name      string        // "reindex", "reindex --full", ...
       Args      []string      // ["reindex"] or ["reindex", "--full"] (no --db/--repo/--config; runner injects)
       Confirm   ConfirmKind   // ConfirmYesNo | ConfirmTyped
       Phrase    string        // for ConfirmTyped, e.g. "reindex"
       RefreshOn []string      // section IDs to refresh on success
       Preflight Preflight     // returns (count int, cost string, err error)
       Headline  HeadlineFn    // formats modal headline given preflight result
   }

   type Preflight func(ctx context.Context, store store.Store) (count int, cost string, err error)
   ```

   `Preflight` is a function the registry attaches per Spec. The Store
   interface gains read-only methods to back them: `EmbedPending`,
   `SummarizePending`, `HistoryNewCommits`, `ChangedFilesSinceLastRun`.

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
2. App dispatches `PreflightMsg{spec}` → spec's preflight method runs the
   read-only count query against the TUI's existing pgxpool (200ms budget;
   on error, surface "preflight failed: <err>" toast and abort).
3. App opens the appropriate confirm modal:
   - `Spec.Confirm == ConfirmYesNo` → `[y/N]` modal with the preflight
     headline (count + provider). `y` emits `RunSpecMsg{spec}`. Anything
     else cancels.
   - `Spec.Confirm == ConfirmTyped` → typed-phrase modal showing preflight
     count + cost driver. Enter dispatches only if typed text equals
     `Spec.Phrase`. Esc cancels.
4. App handler calls `runner.Start(spec)`. If the runner is busy, it returns
   `ErrJobInFlight` and the app emits `JobBusyMsg` → toast
   `reindex already running, c to cancel`. If the cross-process writer
   lock (see Prerequisites) is held by another process, the subprocess
   exits early with a known exit code and the drawer shows
   `another writer holds the lock: <holder>` from the captured stderr.

### Run path

1. `runner.Start(spec)` resolves the command line via `BuildArgs(spec, target)`
   where `target` is the `RunnerTarget` the runner was constructed with:

   ```go
   type RunnerTarget struct {
       BinaryPath string // resolved projectlens binary
       ConfigPath string // path to configs/index.yaml as loaded by the TUI
       DatabaseURL string // exact DSN the TUI's pgxpool is using
       RepoPath   string // exact REPO_PATH the TUI is rendering
   }

   func BuildArgs(spec Spec, t RunnerTarget) []string {
       out := append([]string{}, spec.Args...)
       out = append(out,
           "--config", t.ConfigPath,
           "--db", t.DatabaseURL,
           "--repo", t.RepoPath,
       )
       return out
   }
   ```

   The runner then calls `exec.CommandContext(ctx, t.BinaryPath, BuildArgs(spec, t)...)`
   and sets `cmd.Env = os.Environ()` for ancillary credentials
   (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OLLAMA_ENDPOINT`). The
   targeted DSN/repo/config are NOT relied on via env — they are
   command-line flags so a misaligned env can never silently retarget.
2. Opens log file `~/.projectlens/tui-runs/<RFC3339-ts>-<spec.Name>.log`,
   starts the process.
3. Two goroutines (`scanStream(stdout)`, `scanStream(stderr)`) read line by
   line. Each line: append to tail ring buffer (drop oldest at 200), write
   to file, send `JobLineMsg{line, stream}` to the program.
4. A tick goroutine sends `JobTickMsg` every 500ms so elapsed time animates
   even when the process is silent.
5. When `cmd.Wait()` returns, the runner closes the log file (flush +
   close in deferred order), then sends `JobCompletedMsg{spec, exitCode,
   duration, logPath, tailSnapshot}`. Status transitions to `succeeded`,
   `failed`, or `cancelled` — the message is sent exactly once.

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

A mutating subprocess must never be orphaned mid-write. The quit path is:

- If `runner.State()` is `idle | succeeded | failed | cancelled`: `q` quits
  immediately.
- If a job is running, `q` does NOT quit. Instead it triggers
  `runner.Cancel()` (SIGTERM, then SIGKILL after 5s as in the Cancel path)
  and shows a "draining: <spec.Name>… press q again to force-quit (will
  detach mutating subprocess)" banner in the drawer.
- The TUI stays alive in `cancelling` state. When `cmd.Wait()` returns and
  the log file is flushed + closed, the runner emits `JobCompletedMsg` and
  the app sees status `cancelled`. At that point a second `q` (or the
  initial `q`, if pressed after the drain finishes) calls `tea.Quit`
  cleanly.
- **Force-quit escape hatch.** A second `q` while still draining
  (i.e. before `Wait` returns) opens a typed-confirm modal:
  `Type 'detach' to quit before subprocess exits. The job will continue
  writing to the database without TUI supervision.` Only `detach`
  proceeds. This path explicitly acknowledges the danger; the log file
  is fsync'd before `tea.Quit` and the subprocess is left running with
  its stdout/stderr redirected to the log file (handed off via
  `os/exec.Cmd.ExtraFiles` or by re-`dup2`ing onto the open log fd
  before quit — implementation choice deferred, both achieve the goal).
- **No silent orphan.** The plain `q` path can never produce an orphan
  process; only the explicit typed-confirm `detach` path can, and the
  user has been told.

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
| TUI crashes mid-run | Child inherits stdout/stderr pipes; pipes close on TUI death; child likely SIGPIPE on next write and exits. Not bulletproof. Documented in README. The cross-process writer lock (Prerequisites) is the durable safety net here — even a hung orphan releases the lock when its connection drops. |
| Concurrent CLI run grabs the writer lock | Subprocess exits early with non-zero exit code and stderr `another writer holds the lock: <holder>`; drawer surfaces the message. TUI does not retry. |
| Preflight count is stale (rows change between count and run) | Acceptable; preflight is informational, not a contract. Worst case the user sees "embed 412 chunks" and the actual run does 415 because new chunks landed. Not a correctness issue. |
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
  - log file is flushed and closed *before* `JobCompletedMsg` is emitted
- `internal/tui/jobs/runner_command_test.go` — for every Spec in the
  registry, `BuildArgs(spec, target)` produces an argv that contains
  `--config <ConfigPath>`, `--db <DatabaseURL>`, `--repo <RepoPath>` with
  values matching the test's `RunnerTarget`. Failure mode: any Spec that
  forgets to thread the explicit flags is caught at test time.
- `internal/tui/jobs/registry_test.go` — assert no key collisions among Specs;
  every Spec has non-empty Name, Args, and Preflight; ConfirmTyped specs
  have non-empty Phrase.
- `internal/tui/components/confirmmodal/confirmmodal_test.go` — typed-confirm
  matches phrase exactly (case-sensitive); esc cancels; partial match no-op;
  y/N modal accepts only `y`/`Y` as confirm.
- `internal/tui/components/jobdrawer/jobdrawer_test.go` — `lipgloss`-rendered
  snapshots for states {idle, running, success, failed, cancelled, draining}.
- `internal/tui/app/quit_test.go` — quit path: pressing `q` with a running
  job triggers Cancel and stays alive; `JobCompletedMsg{cancelled}` then
  permits clean quit; second `q` during drain opens detach modal; only
  literal `detach` proceeds.

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
| Users accidentally trigger long ops | Every action runs a preflight count + `[y/N]` (or typed-`reindex` for `--full`) before kicking off. A stray uppercase keypress lands on a modal, never on a running subprocess. |
| Binary version skew (TUI ships with one `projectlens`, user has newer on PATH) | `PROJECTLENS_BINARY` env override gives an explicit pin; `os.Executable` sibling lookup makes co-located binaries Just Work. |
| Cancel races with normal completion | Status enum disambiguates; both paths funnel through the same `cmd.Wait` and `JobCompletedMsg` is sent exactly once. |
| Log file dir grows unbounded | Documented; manual cleanup by user. Add rotation later if it bites. |
| TUI launched against a different config/repo than the user expects | Explicit `--config --db --repo` flags on every command, asserted by `runner_command_test.go`. Drawer header shows the resolved DB host + repo path so the user can sanity-check before triggering. |
| Cross-process writers race on the same DB | Out-of-band: addressed by the Prerequisites writer-lock design, not by the runner. Phase 2 surfaces the failure mode but does not invent the lock. |

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

1. From a running TUI, pressing `R` runs a preflight ("reindex N changed
   files? [y/N]"), and on `y` triggers an incremental reindex with
   explicit `--config --db --repo` flags matching the TUI's loaded config.
   The bottom drawer streams log lines; Pipeline + Runs + Storage refresh
   on success.
2. Pressing `F` runs a preflight, then requires typing `reindex` to
   confirm, then runs `reindex --full`. Cancelling mid-run via `c` cleanly
   terminates the child and shows `cancelled` status.
3. Pressing `E`, `S`, or `H` runs a preflight that names the count and
   provider (`embed 412 chunks via openai? [y/N]`). A stray uppercase
   keypress cannot start an API-billable run without that confirmation.
4. Failed runs (e.g. embed stage hits a rate-limit) display
   `FAILED exit <n> · log: <path>` and the user can read the full log from
   another terminal.
5. If another writer (CLI from another shell) holds the writer lock, the
   subprocess exits early and the drawer shows the holder identity from
   stderr.
6. Phase 1 sections (Health, Storage, Runs, Config) continue to function
   unchanged. Their tests still pass with no modifications.
7. Pressing `q` mid-run does NOT silently quit. It triggers Cancel + drain,
   keeps the TUI alive until `cmd.Wait` returns, and only then exits. A
   second `q` during drain opens a typed `detach` confirm; only that path
   can produce an orphan, and the user has been told.
8. `runner_command_test.go` asserts every registered Spec produces an
   argv with `--config`, `--db`, and `--repo` flags equal to the test's
   `RunnerTarget`.
