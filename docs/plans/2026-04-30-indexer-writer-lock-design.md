# Indexer Cross-Process Writer Lock Design

**Status:** Draft (rev 2 — review feedback applied 2026-05-01)
**Date:** 2026-04-30
**Drives:** TUI Phase 2 (`docs/plans/2026-04-30-tui-phase2-design.md`) — Phase 2 implementation cannot begin until this lock lands.

## Revision 2 — review responses

The 2026-04-30 implementation review surfaced findings that change the
shape of the lock table and `ForceUnlock`. Summary of resolutions
applied below; deltas are inlined in their respective sections.

| Review finding | Resolution |
|----------------|------------|
| `ForceUnlock` cannot release another session's advisory lock | Recorded the holder's Postgres backend PID alongside the client PID; `ForceUnlock` calls `pg_terminate_backend(backend_pid)` to drop the holder's session, which releases the advisory lock; falls back to row deletion only when no live backend is found. |
| Stale-row cleanup compared client PID to backend PID | `index_locks` now has both `client_pid` (cosmetic, from `os.Getpid()`) and `backend_pid` (from `pg_backend_pid()`); reap joins on `backend_pid`, identity messages display `client_pid`. |
| Wrapping `bootstrap` blocked fresh-DB setup | `bootstrap` runs migrations BEFORE `Acquire`. Implementation handles this by wrapping bootstrap with a "migrate-first" variant that calls `db.Migrate` before `withWriteLock`. |
| `withWriteLock` dropped command context | Helper now wraps `func(*cobra.Command, []string, *storage.DB, *config.Config, string) error`, passing `cmd`, `cfg`, and resolved `repoPath`. |
| `knowledge delete` left unwrapped | Documented invariant: deletion mutates `knowledge_entries` rows + paired chunks/embeddings only; does NOT race the indexer because `index-embed` reads `chunks WHERE embedding IS NULL` and a deleted chunk simply disappears from the candidate set. Test added. |
| CLI contention test nondeterministic | Test introduces a `--test-hold-lock` hidden flag that holds the lock for a fixed duration; both racers attach to it. |
| `pg_locks` smoke query wrong for bigint advisory lock | Query rewritten using the bigint-split (`classid`, `objid`) representation. |

## Problem

ProjectLens's mutating commands today have no inter-process serialization.
Two CLI invocations in parallel — or, soon, a CLI run plus a TUI-triggered
run — race on:

- `internal/storage/symbols.go` delete-then-insert under
  `IndexCode` / reindex.
- `internal/history/indexer.go` clear-then-reinsert of `co_changes` edges
  during the coupling recompute.
- `internal/indexer/indexer.go` writes to `index_runs`, `chunks`,
  `embeddings`, `summaries` interleaved across stages.

The result of an overlap is duplicated symbols, stale edges, missing
embeddings, or partially rebuilt derived data. None of these surface as
an error today; they corrupt silently.

The TUI Phase 2 spec (Codex review finding [high]) identified this as
the blocking risk for adding a write surface to the dashboard.

## Goals

1. Exactly one mutating indexer process at a time, system-wide.
2. Read paths (`status`, `query`, `inspect-*`, MCP read tools) untouched.
3. Visible holder identity so a busy second writer knows whose run is in
   flight.
4. Crash recovery with no manual intervention in the common case.
5. An explicit `projectlens unlock --force` escape hatch when auto-recovery
   fails (e.g. recycled PID).

## Non-goals

- Per-stage parallelism (embed alongside history alongside summarize). The
  current pipelines mutate overlapping tables and call external APIs that
  share rate limits; serializing all writers is the simplest correct
  posture.
- Cluster / multi-region distributed leases.
- Lock metrics in the TUI Storage section.
- Locking `save_knowledge` MCP writes (single-row insert with no
  contention against the indexer pipeline).
- Locking new MCP write tools that do not exist today.

## Approach

A single Postgres advisory lock at a fixed `LockID` plus a small
`index_locks` bookkeeping table for holder identity. Acquire is
**fail-fast**: if the lock is held, the new process exits non-zero
immediately with the holder's PID, host, command name, and start time on
stderr.

Auto-recovery uses Postgres' own connection-drop semantics:
`pg_try_advisory_lock` is session-scoped, so any abrupt termination of
the holder process releases the lock at the database level. The
`index_locks` row left behind is reaped by the next `Acquire` call via a
join against `pg_stat_activity`.

The escape hatch is `projectlens unlock --force`, which drops the row and
calls `pg_advisory_unlock_all` on a fresh connection.

## Architecture

### New package: `internal/storage/writelock/`

```
internal/storage/writelock/
  lock.go                  // LockID constant, Lock struct, ErrBusy
  acquire.go               // Acquire(ctx, db, cmdName) (*Lock, error)
  release.go               // (*Lock).Release(ctx) error
  force.go                 // ForceUnlock(ctx, db) error
  lock_test.go             // unit tests
  integration_test.go      // //go:build integration
```

Public surface:

```go
// LockID is the fixed advisory-lock id used by every mutating indexer
// command. Picked arbitrarily; documented to be stable forever.
const LockID int64 = 9_876_543_210

// Lock is the handle returned by a successful Acquire. Holding it pins
// the same pgx connection that took the advisory lock; Release returns
// the connection to the pool.
type Lock struct {
    conn     *pgxpool.Conn
    rowID    int64
    released bool
}

// ErrBusy is returned by Acquire when another writer holds the lock.
// Its fields describe that holder so the caller can surface a useful
// message.
type ErrBusy struct {
    HolderPID       int
    HolderHost      string
    HolderCmd       string
    HolderStartedAt time.Time
}

func (e ErrBusy) Error() string {
    return fmt.Sprintf(
        "another writer holds the lock: pid=%d host=%s cmd=%q started=%s",
        e.HolderPID, e.HolderHost, e.HolderCmd,
        e.HolderStartedAt.Format(time.RFC3339))
}

// Acquire takes the writer lock on a dedicated connection pulled from
// the pool. Returns ErrBusy if another session holds it.
func Acquire(ctx context.Context, db *storage.DB, cmdName string) (*Lock, error)

// Release drops the bookkeeping row, calls pg_advisory_unlock, and
// releases the pinned connection back to the pool. Idempotent.
func (l *Lock) Release(ctx context.Context) error

// ForceUnlock is the escape hatch behind `projectlens unlock --force`.
// Drops the index_locks row, calls pg_advisory_unlock_all on a fresh
// connection, logs the previous holder identity. Used only when
// auto-recovery has failed.
func ForceUnlock(ctx context.Context, db *storage.DB) error
```

### Connection lifetime

`pg_try_advisory_lock` is **session-scoped** (`pg_advisory_xact_lock`
would require running every mutating command inside one open
transaction, which the multi-step indexer pipeline cannot do). The
session in pgx is the connection.

`Acquire` therefore pins a single `*pgxpool.Conn` from `db.Pool.Acquire`,
runs every lock-related query on that connection, and stores it on the
returned `*Lock`. `Release` runs the unlock on the same connection then
calls `conn.Release()` to return it to the pool. The pool size is
effectively reduced by one for the duration of the run; given the
indexer is the dominant workload during a write, this is acceptable.

If `Acquire` fails after pinning the connection, the conn is released
back to the pool inside the function before returning.

### Acquire flow

```go
func Acquire(ctx context.Context, db *storage.DB, cmdName string) (*Lock, error) {
    conn, err := db.Pool.Acquire(ctx)
    if err != nil {
        return nil, err
    }
    success := false
    defer func() {
        if !success {
            conn.Release()
        }
    }()

    // 1. Reap stale rows.
    //
    // Reap by Postgres backend PID, not client PID. pg_stat_activity.pid is
    // the backend's PID and is comparable only to pg_backend_pid()
    // (which we stored as backend_pid at INSERT time).
    if _, err := conn.Exec(ctx, `
        DELETE FROM index_locks
        WHERE backend_pid NOT IN (SELECT pid FROM pg_stat_activity WHERE pid IS NOT NULL)
    `); err != nil {
        return nil, fmt.Errorf("writelock: reap stale: %w", err)
    }

    // 2. Try advisory lock.
    var got bool
    if err := conn.QueryRow(ctx,
        `SELECT pg_try_advisory_lock($1)`, LockID).Scan(&got); err != nil {
        return nil, fmt.Errorf("writelock: try lock: %w", err)
    }
    if !got {
        var b ErrBusy
        // Best-effort identity read; even if it fails, return ErrBusy
        // with zero fields so the caller still sees the right error
        // type.
        _ = conn.QueryRow(ctx, `
            SELECT client_pid, hostname, cmd, started_at FROM index_locks
            WHERE lock_id = $1`, LockID).
            Scan(&b.HolderPID, &b.HolderHost, &b.HolderCmd, &b.HolderStartedAt)
        return nil, b
    }

    // 3. Insert holder row.
    //
    // We record TWO pids:
    //   client_pid  = os.Getpid()  (cosmetic; what the operator sees in `ps`)
    //   backend_pid = pg_backend_pid() (used for liveness reaping +
    //                                   force-unlock; comparable to
    //                                   pg_stat_activity.pid)
    clientPID := os.Getpid()
    host, _ := os.Hostname()
    var rowID int64
    if err := conn.QueryRow(ctx, `
        INSERT INTO index_locks (lock_id, client_pid, backend_pid, hostname, cmd)
        VALUES ($1, $2, pg_backend_pid(), $3, $4)
        RETURNING id
    `, LockID, clientPID, host, cmdName).Scan(&rowID); err != nil {
        // Race lost between try_advisory_lock and INSERT — release the
        // advisory lock so we don't strand the row.
        _, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, LockID)
        return nil, fmt.Errorf("writelock: insert holder: %w", err)
    }

    success = true
    return &Lock{conn: conn, rowID: rowID}, nil
}
```

### Release flow

```go
func (l *Lock) Release(ctx context.Context) error {
    if l.released {
        return nil
    }
    l.released = true
    defer l.conn.Release()

    if _, err := l.conn.Exec(ctx,
        `DELETE FROM index_locks WHERE id = $1`, l.rowID); err != nil {
        return fmt.Errorf("writelock: delete row: %w", err)
    }
    var ok bool
    if err := l.conn.QueryRow(ctx,
        `SELECT pg_advisory_unlock($1)`, LockID).Scan(&ok); err != nil {
        return fmt.Errorf("writelock: advisory unlock: %w", err)
    }
    return nil
}
```

`released` is a plain bool; concurrent calls to `Release` on the same
`*Lock` are not supported. Callers always invoke it from one defer.

### ForceUnlock

> **Critical correction (rev 2):** Postgres session advisory locks can
> only be released by the session that holds them.
> `pg_advisory_unlock_all()` on a *different* connection releases only
> that other connection's locks — it cannot evict the original holder.
> The escape hatch therefore terminates the holder's backend session,
> which Postgres treats as a connection drop and auto-releases the
> advisory lock for us.

```go
func ForceUnlock(ctx context.Context, db *storage.DB) error {
    conn, err := db.Pool.Acquire(ctx)
    if err != nil {
        return err
    }
    defer conn.Release()

    // Read holder identity (client + backend pids) for the audit log
    // and the termination call.
    var clientPID, backendPID int
    var host, cmd string
    var started time.Time
    err = conn.QueryRow(ctx, `
        SELECT client_pid, backend_pid, hostname, cmd, started_at
        FROM index_locks
        WHERE lock_id = $1`, LockID).
        Scan(&clientPID, &backendPID, &host, &cmd, &started)
    switch {
    case err == nil:
        log.Printf("writelock: force-unlocking holder client_pid=%d backend_pid=%d host=%s cmd=%q started=%s",
            clientPID, backendPID, host, cmd, started.Format(time.RFC3339))
        // Terminate the holder's backend session. Postgres releases the
        // advisory lock automatically on session drop. We do NOT call
        // pg_advisory_unlock_all on this connection — that would only
        // release locks held by THIS session, not the holder's.
        if backendPID > 0 {
            var killed bool
            if err := conn.QueryRow(ctx,
                `SELECT pg_terminate_backend($1)`, backendPID).Scan(&killed); err != nil {
                return fmt.Errorf("writelock: terminate backend %d: %w", backendPID, err)
            }
            if !killed {
                log.Printf("writelock: pg_terminate_backend(%d) returned false (backend already gone); proceeding with row cleanup", backendPID)
            }
        }
    case errors.Is(err, pgx.ErrNoRows):
        log.Printf("writelock: no active holder; running unlock anyway")
    default:
        return err
    }

    // Delete the bookkeeping row last. If we've terminated the backend
    // above, the row may already be gone via the autoreap on the next
    // Acquire — DELETE here is idempotent.
    if _, err := conn.Exec(ctx, `DELETE FROM index_locks WHERE lock_id = $1`, LockID); err != nil {
        return err
    }
    return nil
}
```

**Why not `pg_advisory_unlock_all` on a fresh connection?** It only
releases locks held by that fresh connection (which has none). The
holder's session is unaffected, so a subsequent `Acquire` would still
see `pg_try_advisory_lock` return false. Terminating the backend is the
only mechanism short of `pg_cancel_backend` + connection close that
forces a holder to relinquish a session-scoped advisory lock from
outside its own session.

### Migration: `migrations/005_writer_lock.up.sql`

```sql
CREATE TABLE index_locks (
    id          SERIAL PRIMARY KEY,
    lock_id     BIGINT NOT NULL,
    client_pid  INTEGER NOT NULL,   -- os.Getpid() of the operator-visible process
    backend_pid INTEGER NOT NULL,   -- pg_backend_pid() — used for liveness reaping
    hostname    TEXT NOT NULL,
    cmd         TEXT NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (lock_id)
);

CREATE INDEX idx_index_locks_backend_pid ON index_locks(backend_pid);
```

> **Migrator note:** the existing migrator records applied migrations in
> `schema_migrations` automatically. The `.up.sql` file does NOT
> hand-insert into `schema_migrations` (rev 1 had a stray `INSERT` —
> removed in rev 2 to match the migrator contract).

The `UNIQUE (lock_id)` constraint guarantees at most one row per
`LockID`; combined with the advisory lock it forms a belt-and-braces
serialization point.

### CLI integration

A single shared helper in `cmd/projectlens/main.go`. The helper has to
hand the wrapped command body everything it could otherwise have
loaded for itself (`cmd`, `cfg`, `repoPath`, `db`) — otherwise the
refactor cannot be mechanical for commands that depend on flags
beyond `--db`.

```go
// LockedCmd is the body shape produced by the wrap. It receives the
// already-opened DB plus the loaded config and resolved repo path so
// the call site does not duplicate config loading inside its body.
type LockedCmd func(ctx context.Context, cmd *cobra.Command, db *storage.DB, cfg *config.Config, repoPath string) error

// withWriteLock wraps a mutating command's RunE so it acquires the
// writer lock on entry and releases it on exit. ErrBusy results in
// exit code 75 (sysexits.h EX_TEMPFAIL).
func withWriteLock(cmdName string, run LockedCmd) func(*cobra.Command, []string) error {
    return func(cmd *cobra.Command, _ []string) error {
        cfg, repoPath, err := loadCmdConfig(cmd)
        if err != nil {
            return err
        }
        ctx := cmd.Context()
        db, err := storage.Connect(ctx, cfg.DatabaseURL)
        if err != nil {
            return err
        }
        defer db.Close()

        lock, err := writelock.Acquire(ctx, db, cmdName)
        if err != nil {
            if be, ok := err.(writelock.ErrBusy); ok {
                fmt.Fprintln(os.Stderr, be.Error())
                os.Exit(75)
            }
            return err
        }
        defer func() { _ = lock.Release(context.Background()) }()

        return run(ctx, cmd, db, cfg, repoPath)
    }
}

// withWriteLockAfterMigrate is the bootstrap variant. bootstrap is the
// command that *creates* the index_locks table, so it must run
// migrations BEFORE attempting Acquire. After migrations succeed, the
// remainder runs under the writer lock. This is the only command that
// uses this variant.
func withWriteLockAfterMigrate(cmdName string, migrationsDir string, run LockedCmd) func(*cobra.Command, []string) error {
    return func(cmd *cobra.Command, _ []string) error {
        cfg, repoPath, err := loadCmdConfig(cmd)
        if err != nil {
            return err
        }
        ctx := cmd.Context()
        db, err := storage.Connect(ctx, cfg.DatabaseURL)
        if err != nil {
            return err
        }
        defer db.Close()
        if err := db.Migrate(ctx, migrationsDir); err != nil {
            return fmt.Errorf("bootstrap migrate: %w", err)
        }
        lock, err := writelock.Acquire(ctx, db, cmdName)
        if err != nil {
            if be, ok := err.(writelock.ErrBusy); ok {
                fmt.Fprintln(os.Stderr, be.Error())
                os.Exit(75)
            }
            return err
        }
        defer func() { _ = lock.Release(context.Background()) }()
        return run(ctx, cmd, db, cfg, repoPath)
    }
}
```

Wrapped commands:

| Command            | cmdName argument | Wrapper variant |
|--------------------|------------------|-----------------|
| `bootstrap`        | `"bootstrap"`    | `withWriteLockAfterMigrate` |
| `reindex`          | `"reindex"`      | `withWriteLock` |
| `index-datastore`  | `"index-datastore"` | `withWriteLock` |
| `index-history`    | `"index-history"` | `withWriteLock` |
| `index-embed`      | `"index-embed"`  | `withWriteLock` |
| `index-summarize`  | `"index-summarize"` | `withWriteLock` |
| `index-all`        | `"index-all"`    | `withWriteLock` |

Unwrapped (read-only):

- `census`, `status`, `query`, `inspect-symbol`, `inspect-package`
- `knowledge list`, `knowledge show`, `knowledge search`
- `knowledge delete` — see invariant below.
- All MCP server tools (the MCP daemon never enters this code path).

**`knowledge delete` invariant.** The command deletes a single
`knowledge_entries` row plus its paired chunk/embedding/edge rows, all
keyed by knowledge id. The indexer's writer pipeline never reads or
writes `knowledge_entries` rows; `index-embed` reads
`chunks WHERE embedding IS NULL` and a deleted chunk simply leaves the
candidate set. There is no read-modify-write race against indexer
writes, so the delete is safe outside the writer lock. This invariant
is asserted by `internal/storage/writelock/knowledge_race_test.go`
(Task 11.5 in the implementation plan).

The bodies of the wrapped RunE functions move into a `func(ctx context.Context, db *storage.DB) error` shape; today most already accept `(ctx, *storage.DB)` after some local plumbing, so the change is mechanical.

### New `unlock` command

```go
func newUnlockCmd() *cobra.Command {
    var force bool
    cmd := &cobra.Command{
        Use:   "unlock",
        Short: "release the indexer write lock (use --force to override)",
        Long: "Releases the writer lock and deletes its bookkeeping row. " +
            "Auto-recovery handles crashed processes; use --force only when " +
            "auto-recovery has failed (e.g. a recycled PID looks alive in " +
            "pg_stat_activity).",
        RunE: func(cmd *cobra.Command, _ []string) error {
            if !force {
                return fmt.Errorf("refusing to unlock without --force")
            }
            cfg, _, err := loadCmdConfig(cmd)
            if err != nil {
                return err
            }
            ctx := cmd.Context()
            db, err := storage.Open(ctx, cfg.DatabaseURL)
            if err != nil {
                return err
            }
            defer db.Close()
            return writelock.ForceUnlock(ctx, db)
        },
    }
    cmd.Flags().BoolVar(&force, "force", false, "required acknowledgement")
    return cmd
}
```

Registered in `main.go` alongside the other commands.

## Error handling + edge cases

| Case | Behaviour |
|------|-----------|
| Concurrent CLI + TUI run | Second to acquire gets `ErrBusy` with first's identity; exit 75; first runs normally. |
| Holder process killed mid-run (kill -9, OOM, panic) | Connection drops → advisory lock auto-released by Postgres → next `Acquire`'s stale-row sweep deletes the orphaned `index_locks` row. No manual intervention. |
| Holder host/network drops connection | Same as kill: conn closes, lock releases, next acquire reaps row. |
| pid recycled before stale sweep runs | Sweep uses `pg_stat_activity`; recycled pid that's a fresh DB connection looks alive → row stays. Resolved via `projectlens unlock --force`. |
| Migration `005_writer_lock.up.sql` not applied | `Acquire` fails on missing table; clear error `"writelock: ... relation \"index_locks\" does not exist"`. `bootstrap` auto-runs migrations as today. |
| `unlock --force` while a healthy holder runs | The healthy holder's commits still go through (advisory lock is advisory; no row-level locks affected), but a *second* writer can now race in. Logged as a warning in the indexer log. Documented as a footgun in the help text. |
| `Release` racing with process exit | Idempotent guard + deferred run cover normal paths; if the process is SIGKILL'd before `Release` runs, the connection drops and the next acquirer reaps the row. |
| Pool exhausted before `Acquire` can grab a conn | `pgxpool.Acquire` returns context-cancelled or pool-closed error; surfaced as plain error to the user. Not a lock-specific concern. |
| Long-running `bootstrap` blocks `index-embed` | Expected behaviour. Per-stage parallelism is explicitly out of scope. |

## TUI integration (downstream consumer)

The TUI Phase 2 runner detects exit code 75 and surfaces the captured
stderr line in its drawer (`another writer holds the lock: pid=…`). No
TUI-side lock awareness beyond that — the runner stays a generic
subprocess executor.

The Phase 2 implementation plan (`2026-04-30-tui-phase2-implementation.md`)
Task 0 verifies this prerequisite has merged before any TUI Phase 2
code lands.

## Testing strategy

### Unit (no DB)

- `lock_test.go`:
  - `LockID` is a stable non-zero constant.
  - `ErrBusy.Error()` formats with all four fields and quoted command.
  - `(*Lock).Release` is idempotent (call twice, second is a no-op, no
    panic, no double-close on the pool conn). Mock the pool conn via a
    small interface.

### Integration (`//go:build integration`)

Each test runs against a real Postgres pool created by the existing
test harness in `internal/storage/`.

- `acquire_release_test.go`:
  - Fresh DB → `Acquire` returns a `*Lock`; `index_locks` has one row
    with the current PID, hostname, and the `cmdName` argument.
  - `Release` removes the row and the advisory lock is no longer held
    (verified by re-acquiring on a separate connection).
  - Sequential `Acquire/Release/Acquire/Release` works without leaking
    rows or connections.
- `contention_test.go`:
  - Two goroutines call `Acquire` concurrently. Exactly one returns
    `*Lock`; the other returns `ErrBusy` with the winner's PID, host,
    and `cmdName`.
  - Calling `Acquire` again after the winner's `Release` succeeds.
- `stale_cleanup_test.go`:
  - Manually insert a row with `pid = 99999` (no such PID in
    `pg_stat_activity`); calling `Acquire` reaps that row and
    proceeds.
- `force_unlock_test.go`:
  - `Acquire` from connection A; `ForceUnlock` from connection B drops
    the row and calls `pg_advisory_unlock_all`. A subsequent `Acquire`
    on connection C succeeds. Connection A's `Release` is harmless
    (advisory unlock fails silently; row already gone).
- `cli_integration_test.go`:
  - Spawn two CLI invocations of a hidden `projectlens debug-hold-lock
    --hold 3s` subcommand. The first acquires the writer lock and
    sleeps; the second must observe `ErrBusy` deterministically. One
    exits 0, the other exits 75 with the `another writer holds the
    lock` text on stderr. Using a dedicated test command instead of
    `index-embed` avoids the nondeterministic "both finished before
    they overlapped" outcome on an empty queue. The hidden command is
    gated by an env var (`PROJECTLENS_DEBUG_HOLD_LOCK=1`) so it cannot be
    invoked in a production binary by accident.

### Manual smoke

- The 1-arg bigint advisory lock splits across `classid` and `objid`
  in `pg_locks` as the high/low int32 of the bigint. Use the bigint
  cast helper (Postgres ≥ 9.0):

  ```sql
  SELECT * FROM pg_locks
  WHERE locktype = 'advisory'
    AND ((classid::bigint << 32) | (objid::bigint & 4294967295)) = 9876543210;
  ```

  while a `bootstrap` runs → exactly one row. (The naive
  `objid = 9876543210` query in rev 1 is incorrect because `objid` only
  holds the low 32 bits.)
- Alternative: open a second psql session and run
  `SELECT pg_try_advisory_lock(9876543210);` — it must return `f` while
  a holder is active.
- `projectlens unlock --force` while idle → exits 0, logs
  `no active holder`.

### Not tested

- Pool-exhaustion behaviour (depends on host-specific config).
- pid-recycle race (deterministic reproduction needs OS cooperation;
  documented as a manual-only test).

## Risks

| Risk | Mitigation |
|------|------------|
| Pinning a connection per write reduces effective pool size | Indexer write workloads are dominant during a run; subordinate write queries borrow other pool slots. Document the contract in the package doc. |
| Stale-row sweep has a false negative when `pg_stat_activity` lags | Acceptable: a stale row only blocks the *next* acquire, not the running holder. `unlock --force` is the documented escape. |
| `os.Exit(75)` in `withWriteLock` short-circuits `defer`s in `main` | The wrapper only exits 75 *before* it has acquired any other resource than the DB pool; deferred close runs because `os.Exit` is invoked from within `RunE`'s closure where `db.Close()` is already deferred. (Verified in the implementation plan with an explicit test.) |
| Adding a NEW table in production-like environments without coordination | `005_writer_lock.up.sql` is additive; existing rows untouched; existing queries unaffected. |
| `LockID` collision with another tool sharing the database | ProjectLens owns the DB; no other tool touches it. Documented as an invariant. |

## Success criteria

1. After this lands, two concurrent `projectlens reindex` invocations
   against the same DB serialize: one runs to completion, the other
   exits 75 with a useful stderr line. Verified by `cli_integration_test.go`.
2. SIGKILLing the active holder leaves no manual cleanup; the next
   `projectlens reindex` proceeds without intervention. Verified by
   `stale_cleanup_test.go` + manual SIGKILL test.
3. `projectlens unlock --force` exits 0 from a healthy idle DB and from a
   DB with a stale row.
4. Read-only commands (`status`, `query`, `inspect-*`, `census`,
   `knowledge` subcommands) and the MCP server are unaffected — no new
   latency, no failures introduced.
5. Phase 2 TUI implementation can begin (Task 0 verification passes).

## Out-of-scope follow-ups

- Per-stage advisory locks if profiling shows embed/summarize would
  benefit from running in parallel and the API providers can keep up.
- Lock metrics in the TUI Storage section (`SELECT * FROM index_locks`).
- A pg_stat_activity-driven "holder is alive but stuck" monitor.
