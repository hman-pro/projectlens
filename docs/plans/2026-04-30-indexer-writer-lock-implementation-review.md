# Review: Indexer Writer Lock Implementation Plan

Reviewed against `docs/plans/2026-04-30-indexer-writer-lock-design.md` and the current repo shape.

## Findings

### Critical: `ForceUnlock` cannot release another session's advisory lock

The plan says `ForceUnlock` can use a fresh connection to call `pg_advisory_unlock_all()` and make a later `Acquire` succeed (`docs/plans/2026-04-30-indexer-writer-lock-implementation.md:763`, `:839`). Postgres session advisory locks can only be released by the session that holds them. Calling `pg_advisory_unlock_all()` on the new unlock connection only releases locks held by that new connection, not the original pinned holder connection.

Effect: `unlock --force` would delete `index_locks` identity while the original writer still holds the advisory lock. The planned Task 8 test should fail, and the system can lose holder visibility while still returning busy.

Recommended fix: store the holder backend PID from `pg_backend_pid()` and decide whether force unlock is allowed to terminate that backend with `pg_terminate_backend(backend_pid)`, or redefine force unlock as stale-row cleanup only when no advisory lock is held.

### Critical: stale-row cleanup compares client PID to Postgres backend PID

`Acquire` stores `os.Getpid()` in `index_locks.pid` (`docs/plans/2026-04-30-indexer-writer-lock-implementation.md:359`) but Task 5 reaps rows by comparing that value to `pg_stat_activity.pid` (`:524`). `pg_stat_activity.pid` is the server backend PID, not the client process PID.

Effect: a second writer can delete a live holder row before failing `pg_try_advisory_lock`, then return `ErrBusy` with empty holder identity. Crash recovery and the core visibility goal become unreliable.

Recommended fix: add separate columns such as `client_pid` and `backend_pid`; use `backend_pid = pg_backend_pid()` for liveness/reaping, and display `client_pid` for operator identity.

### High: wrapping `bootstrap` before migrations breaks fresh DB setup

Task 9 wraps `bootstrap` with `withWriteLock`, and the helper acquires the lock before running the command body (`docs/plans/2026-04-30-indexer-writer-lock-implementation.md:886`). Today `bootstrap` is the command that applies migrations before indexing. On a fresh database, `index_locks` does not exist until migration 005 is applied.

Effect: `projectlens bootstrap` cannot create the table needed by its own lock acquisition.

Recommended fix: either run migrations before `Acquire` for bootstrap, split bootstrap migration from mutating index work, or leave bootstrap unwrapped until after migration setup is complete.

### High: `withWriteLock` drops command context that existing bodies need

The helper signature passes only `(ctx, db)` to the wrapped body (`docs/plans/2026-04-30-indexer-writer-lock-implementation.md:873`, `:896`). Existing mutating commands need the loaded `cfg`, resolved `repoPath`, and command flags to construct providers and stage configs.

Effect: the refactor is not mechanical as written and risks either compile failures or duplicate config loading inside command bodies.

Recommended fix: pass a small struct containing `cfg`, `repoPath`, `db`, and `cmd`, or let the helper wrap a `func(*cobra.Command, []string, *storage.DB, *config.Config, string) error`.

### Medium: `knowledge delete` is classified as read-only

The plan leaves all `knowledge` subcommands unwrapped, including `knowledge delete` (`docs/plans/2026-04-30-indexer-writer-lock-implementation.md:945`). That command deletes knowledge rows and associated chunks/embeddings/edges.

Effect: if the lock is intended to cover all DB writers touching indexer-owned tables, this is a missed writer. If knowledge deletion is intentionally outside scope, the plan should explain why it is safe to race with `index-embed` and related scans.

Recommended fix: either wrap `knowledge delete`, or document and test the invariant that this write does not conflict with the serialized indexer commands.

### Medium: CLI contention test is nondeterministic

Task 11 is named `TestCLI_TwoReindexSerialize`, but it launches two short `index-embed` commands on what may be an empty queue (`docs/plans/2026-04-30-indexer-writer-lock-implementation.md:1075`, `:1090`). Both processes can finish sequentially with exit 0 without overlapping at the lock.

Effect: the test may flake or fail to prove the stated serialization property.

Recommended fix: use a deterministic test-only command path, a fixture that makes the winner hold the lock long enough, or an integration test that calls `Acquire` from one process and launches a CLI writer as the contender.

### Medium: integration cleanup can clobber a shared dev DB

The proposed `newTestDB` cleanup deletes all `index_locks` rows (`docs/plans/2026-04-30-indexer-writer-lock-implementation.md:282`). Existing integration tests in this repo tend to use marker cleanup rather than assuming the database is exclusively owned by one test.

Effect: a test run could disrupt a real in-flight writer or hide a pre-existing holder.

Recommended fix: use a dedicated test database/schema, or only clean rows created by the current test while avoiding global lock-row deletes unless the test owns the DB.

### Low: manual `pg_locks` query is wrong for the chosen bigint lock ID

The smoke query checks `objid = 9876543210`, but the one-argument bigint advisory lock is represented across `classid`/`objid` in `pg_locks` (`docs/plans/2026-04-30-indexer-writer-lock-implementation.md:1193`).

Effect: manual verification can report a false negative even when the lock is held.

Recommended fix: either query via `pg_locks` using the bigint split representation, or verify with a second connection calling `SELECT pg_try_advisory_lock($1)`.

## Open Questions

- Should `unlock --force` terminate the holder backend, or should it only remove stale bookkeeping after proving the advisory lock is already gone?
- Should the lock table record both client PID and Postgres backend PID?
- Is `knowledge delete` intentionally outside the writer-lock scope despite mutating shared rows?
