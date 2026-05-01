# Review: TUI Phase 2 Implementation Plan

Reviewed against `docs/plans/2026-04-30-tui-phase2-design.md`, the Phase 1 TUI implementation, and current repo patterns.

## Findings

### High: app wiring snippets do not match the current model or Bubble Tea API

Task 17 uses `m.appCtx`, `m.store`, and `m.sections[id]` as if the app stores a map and a store field (`docs/plans/2026-04-30-tui-phase2-implementation.md:2179`, `:2250`). The current `internal/tui/app.Model` has `ctx` and `sections []sections.Section`, with no app-level store field. Task 19 also uses `tea.NewProgram(nil)`, `prog.SetModel`, `app.New(ctx, st, ...)`, and an undefined `st` (`docs/plans/2026-04-30-tui-phase2-implementation.md:2435`). Bubble Tea v1.3.10 does not provide `SetModel`.

Effect: the app integration task cannot be implemented as written, and refresh-on-success routing will not compile.

Recommended fix: update the plan around the current `app.New(ctx, []sections.Section)` shape, explicitly add any new fields required by the app, and refresh sections by iterating the slice and matching `sec.ID()`.

### High: cancellation will usually be reported as failure, not cancelled

Task 6 creates `context.WithCancel(context.Background())`, and completion classifies a run as `cancelled` only when `ctx.Err() != nil` (`docs/plans/2026-04-30-tui-phase2-implementation.md:832`, `:867`). Task 8 `Cancel()` only sends SIGTERM and later SIGKILL; it never calls `cancelFn` (`:1070`). It also does not use the app's `tea.WithContext` cancellation path.

Effect: `Cancel()` will generally produce a non-zero `cmd.Wait()` result and be classified as `failed`, so the planned cancel test and the design lifecycle disagree. TUI process shutdown does not reliably propagate to the subprocess policy.

Recommended fix: make `Cancel()` call `cancelFn` or explicitly set a `cancelRequested` state under the mutex and classify the resulting wait as `cancelled`. Tie runner context to the app context, while preserving the desired TERM-then-KILL behavior.

### High: preflight SQL does not match the current schema

The plan's Postgres preflight section references columns such as `files.package_path`, `summaries.target_type`, `summaries.target_id`, `file_history.author_date`, and `files.last_indexed_at` (`docs/plans/2026-04-30-tui-phase2-implementation.md:1317`, `:1321`, `:1340`, `:1364`). The current migrations use names like `files.package_name`, `files.indexed_at`, `summaries.package_name`, and `file_history.committed_at`.

Effect: the preflight methods will fail at runtime, blocking every action before confirmation.

Recommended fix: rewrite the preflight queries directly against the current migrations and add integration tests seeded with the exact tables/columns used.

### Medium: detach is promised but not actually planned

The design requires the second-`q` detach path to fsync the log and hand stdout/stderr off to the log file before quitting. Task 17 only says to wire the `__detach__` token to `tea.Quit` (`docs/plans/2026-04-30-tui-phase2-implementation.md:2276`, `:2291`).

Effect: the implementation can still leave the child attached to pipes that close when the TUI exits, contradicting the "detach" contract.

Recommended fix: either implement a real detach/handoff operation in the runner and test it, or drop the detach escape hatch from Phase 2 and keep `q` draining only.

### Medium: binary-missing behavior happens too late

The design says action keys should refuse immediately when the `projectlens` binary is missing. Task 17 starts preflight and opens confirmation without checking `binaryPath`; the failure only occurs later at `cmd.Start` (`docs/plans/2026-04-30-tui-phase2-implementation.md:2179`, `:838`, `:843`).

Effect: users can review and confirm an action that cannot be started.

Recommended fix: gate action-key handling on `target.BinaryPath != ""` before preflight, returning a visible message immediately.

### Medium: provider/cost driver text is hard-coded

The registry plan hard-codes `openai` for embed and `anthropic` for summarize (`docs/plans/2026-04-30-tui-phase2-implementation.md:1510`, `:1518`). Current config supports provider selection.

Effect: confirmation text can mislead users about billable work, which is part of the safety surface for these actions.

Recommended fix: derive the cost driver from loaded config, either by passing config into registry construction or by storing provider metadata in the store/preflight layer.

### Medium: app-level tests miss the core action flow

The design's app-level test text says `press R -> no modal -> RunSpecMsg`, while the same design requires preflight plus confirmation for every action (`docs/plans/2026-04-30-tui-phase2-design.md:35`, `:424`). The implementation plan's app test only covers quit drain (`docs/plans/2026-04-30-tui-phase2-implementation.md:2325`).

Effect: the most important flow, preflight -> confirm -> run -> refresh, can regress without focused tests.

Recommended fix: add app tests for action-key preflight dispatch, `PreflightDoneMsg` opening the right modal, yes/no cancel and confirm, typed confirm exact matching, binary-missing refusal, stale preflight response handling if multiple keys are pressed, and refresh-on-success by section ID.

### Medium: runner and scanner have data races around log writes

Task 6 starts two scanner goroutines that both call `fmt.Fprintf(logFile, ...)` concurrently (`docs/plans/2026-04-30-tui-phase2-implementation.md:857`, `:884`). Concurrent `os.File` writes are individually safe at the syscall level on Unix, but the plan does not ensure logical line ordering or protect against interleaving with sync/close on completion beyond `wg.Wait`.

Effect: the log may not preserve stdout/stderr order as intended by the drawer/log contract.

Recommended fix: send scanned lines into one writer goroutine that owns file writes and tail updates, or document that stdout/stderr interleaving is best-effort rather than ordered.

### Low: planned pointer receiver `Update` changes current app conventions

Current app `Update` is `func (m Model) Update(...)`, and Bubble Tea receives the value model from `tea.NewProgram(m, ...)`. Task 17 changes it to `func (m *Model) Update(...)` without showing the matching main/test construction changes (`docs/plans/2026-04-30-tui-phase2-implementation.md:2130`).

Effect: this is fixable, but the plan should be explicit so tests do not accidentally assert different model identities or type assertions.

Recommended fix: either keep value receiver style and return the updated value, or intentionally switch to pointer model everywhere and update `cmd/projectlens-tui/main.go` plus tests accordingly.

## Open Questions

- Should cancellation use customized `exec.Cmd.Cancel` / `WaitDelay`, or a manual process-group TERM/KILL path?
- Should `ActionableSection.Actions()` be derived from `jobs.DefaultRegistry()` so the Pipeline controls block cannot drift from actual hotkeys?
- Is a 200ms preflight budget realistic for the target repo and database, especially for changed-file and history counts?
