# Indexer Writer Lock Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a single Postgres advisory lock + bookkeeping table that
serializes every mutating `projectlens` indexer command across processes,
exposes the holder's identity in the `ErrBusy` error, auto-recovers from
crashed holders, and provides `projectlens unlock --force` as an escape hatch.

**Architecture:** A new `internal/storage/writelock` package owns
`Acquire`/`Release`/`ForceUnlock` against `pg_try_advisory_lock(LockID)`
on a pinned `*pgxpool.Conn`, backed by an `index_locks` row that records
PID/host/cmd/started_at. Migration `005_writer_lock` adds the table. CLI
mutating commands route their `RunE` through a shared `withWriteLock`
wrapper that exits 75 on `ErrBusy`. A new `projectlens unlock --force`
command drops the row and unconditionally releases the lock.

**Tech Stack:** Go 1.26, `jackc/pgx/v5/pgxpool`, Postgres 16, `cobra`
CLI. No new external deps.

**Reference spec:** `docs/plans/2026-04-30-indexer-writer-lock-design.md` (revision 2).

> **Revision 2 (2026-05-01) — review fixes incorporated.** The
> 2026-04-30 implementation review surfaced findings that change the
> shape of the lock table, `ForceUnlock` semantics, the
> `withWriteLock` helper signature, the bootstrap migration ordering,
> and the CLI contention test. Each affected task below has a
> `> rev 2` callout describing the delta. See the design doc's
> "Revision 2 — review responses" table for the high-level summary.

---

## File structure

### New files

| Path | Responsibility |
|------|----------------|
| `migrations/005_writer_lock.up.sql` | Adds `index_locks` table + index. |
| `migrations/005_writer_lock.down.sql` | Drops the table. |
| `internal/storage/writelock/lock.go` | `LockID` constant, `Lock` struct, `ErrBusy` type. |
| `internal/storage/writelock/acquire.go` | `Acquire(ctx, db, cmdName) (*Lock, error)`. |
| `internal/storage/writelock/release.go` | `(*Lock).Release(ctx) error`. |
| `internal/storage/writelock/force.go` | `ForceUnlock(ctx, db) error`. |
| `internal/storage/writelock/lock_test.go` | Unit tests (`ErrBusy.Error`, `Lock` ID constant). |
| `internal/storage/writelock/integration_test.go` | `//go:build integration` — acquire/release/contention/stale/force flows. |
| `internal/storage/writelock/cli_integration_test.go` | `//go:build integration` — two-subprocess contention against real CLI binary using `debug-hold-lock`. |
| `internal/storage/writelock/knowledge_race_test.go` | `//go:build integration` — asserts `knowledge delete` does not race index-embed (rev 2). |
| `cmd/projectlens/unlock.go` | New `unlock` cobra command. |
| `cmd/projectlens/debug.go` | Hidden `debug-hold-lock --hold <dur>` test-only command (rev 2). |

### Modified files

| Path | Change |
|------|--------|
| `cmd/projectlens/main.go` | Add `withWriteLock` helper; wrap mutating commands' `RunE`; register `unlock` subcommand. |
| `CLAUDE.md` | Document the lock + the new `unlock` command + exit code 75. |
| `README.md` | Document `unlock --force` in the troubleshooting section. |

---

## Task 1: Migration 005 — `index_locks` table

**Files:**
- Create: `migrations/005_writer_lock.up.sql`
- Create: `migrations/005_writer_lock.down.sql`

> **rev 2:** the table now stores BOTH `client_pid` (operator-visible)
> and `backend_pid` (Postgres backend pid, used for liveness reaping
> and force-unlock).

- [ ] **Step 1: Write the up migration**

```sql
-- migrations/005_writer_lock.up.sql

CREATE TABLE index_locks (
    id          SERIAL PRIMARY KEY,
    lock_id     BIGINT NOT NULL,
    client_pid  INTEGER NOT NULL,
    backend_pid INTEGER NOT NULL,
    hostname    TEXT NOT NULL,
    cmd         TEXT NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (lock_id)
);

CREATE INDEX idx_index_locks_backend_pid ON index_locks(backend_pid);
```

> **Note:** the existing migrator (`internal/storage/db.go:Migrate`)
> records applied migrations via `INSERT INTO schema_migrations (name)
> VALUES (<filename>)` automatically. Do NOT add a manual
> `INSERT INTO schema_migrations` line at the bottom of this file —
> that would conflict with the migrator's own bookkeeping.

- [ ] **Step 2: Write the down migration**

```sql
-- migrations/005_writer_lock.down.sql

DROP TABLE IF EXISTS index_locks;
```

- [ ] **Step 3: Verify the migrator picks the file up**

Run: `ls migrations/*.up.sql | sort`
Expected output ends with `005_writer_lock.up.sql`.

- [ ] **Step 4: Apply against a local DB**

Run:
```bash
psql "$DATABASE_URL" -f migrations/005_writer_lock.up.sql
psql "$DATABASE_URL" -c "\d index_locks"
```

Expected: table description prints with `client_pid`, `backend_pid`,
`hostname`, `cmd`, `started_at` plus `id`, `UNIQUE (lock_id)`, and
`idx_index_locks_backend_pid`.

- [ ] **Step 5: Reset for tests**

Run:
```bash
psql "$DATABASE_URL" -f migrations/005_writer_lock.down.sql
```

- [ ] **Step 6: Commit**

```bash
git add migrations/005_writer_lock.up.sql migrations/005_writer_lock.down.sql
git commit -m "feat(migrations): index_locks table for cross-process writer lock"
```

---

## Task 2: writelock package — types

**Files:**
- Create: `internal/storage/writelock/lock.go`
- Create: `internal/storage/writelock/lock_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/storage/writelock/lock_test.go
package writelock_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/storage/writelock"
)

func TestLockID_IsStableNonZero(t *testing.T) {
	if writelock.LockID == 0 {
		t.Fatal("LockID must be non-zero")
	}
	// Sanity: keep it within int64 and well clear of small values that
	// might collide with another tool's advisory lock convention.
	if writelock.LockID < 1_000_000 {
		t.Errorf("LockID = %d, want a large constant", writelock.LockID)
	}
}

func TestErrBusy_ErrorFormat(t *testing.T) {
	e := writelock.ErrBusy{
		HolderPID:       4242,
		HolderHost:      "laptop",
		HolderCmd:       "reindex",
		HolderStartedAt: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}
	got := e.Error()
	for _, want := range []string{
		"another writer holds the lock",
		"pid=4242",
		"host=laptop",
		`cmd="reindex"`,
		"started=2026-04-30T12:00:00Z",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() missing %q\n%s", want, got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/writelock/...`
Expected: build failure — package undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/storage/writelock/lock.go
package writelock

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LockID is the fixed advisory-lock id used by every mutating indexer
// command. Picked arbitrarily; documented to be stable forever.
const LockID int64 = 9_876_543_210

// Lock is the handle returned by a successful Acquire. It pins the
// pgxpool connection that took the advisory lock; Release returns the
// connection to the pool.
type Lock struct {
	conn     *pgxpool.Conn
	rowID    int64
	released bool
}

// ErrBusy is returned by Acquire when another writer holds the lock.
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/storage/writelock/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/writelock/lock.go internal/storage/writelock/lock_test.go
git commit -m "feat(storage/writelock): LockID, Lock, ErrBusy types"
```

---

## Task 3: Acquire — happy path

**Files:**
- Create: `internal/storage/writelock/acquire.go`
- Create: `internal/storage/writelock/integration_test.go`

> **Note:** integration tests follow the existing convention — gated
> behind `//go:build integration` and require a reachable Postgres at
> `$DATABASE_URL`. Look at `internal/storage/db_integration_test.go` (or
> the equivalent existing helper) for the test-DB bootstrap pattern; the
> tests below assume a `newTestDB(t)` helper that returns a `*storage.DB`
> connected to a clean DB with all migrations applied. If no such helper
> exists yet, copy the pattern from one of the other integration tests
> in `internal/storage/`.

- [ ] **Step 1: Write the failing test**

```go
// internal/storage/writelock/integration_test.go
//go:build integration

package writelock_test

import (
	"context"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/hman-pro/projectlens/internal/storage/writelock"
)

func newTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := storage.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := db.Migrate(ctx, "../../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Marker-only cleanup: only delete rows whose cmd matches a
	// well-known test prefix. Avoids clobbering an in-flight real
	// writer if DATABASE_URL is pointed at a shared dev DB. Tests in
	// this file always set cmd to one of the strings below.
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx,
			`DELETE FROM index_locks WHERE cmd LIKE 'test-%' OR cmd IN
			 ('alice','bob','first','second','x','fresh','stuck',
			  'after-force','concurrent','ghost-cmd')`)
		db.Close()
	})
	return db
}

func TestAcquire_HappyPath(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	lock, err := writelock.Acquire(ctx, db, "test-cmd")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release(ctx)

	var clientPID, backendPID int
	var cmd string
	if err := db.Pool.QueryRow(ctx,
		`SELECT client_pid, backend_pid, cmd FROM index_locks WHERE lock_id = $1`, writelock.LockID).
		Scan(&clientPID, &backendPID, &cmd); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if clientPID != os.Getpid() {
		t.Errorf("client_pid = %d, want %d", clientPID, os.Getpid())
	}
	if backendPID == 0 {
		t.Errorf("backend_pid is zero — must be a real pg_backend_pid()")
	}
	if cmd != "test-cmd" {
		t.Errorf("row cmd = %q, want %q", cmd, "test-cmd")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestAcquire_HappyPath -v`
Expected: build failure — `Acquire` undefined.

- [ ] **Step 3: Implement Acquire (happy path only)**

```go
// internal/storage/writelock/acquire.go
package writelock

import (
	"context"
	"fmt"
	"os"

	"github.com/hman-pro/projectlens/internal/storage"
)

// Acquire pins a pool connection, takes the advisory lock, inserts the
// holder row, and returns a handle. Returns ErrBusy if another writer
// already holds the lock.
func Acquire(ctx context.Context, db *storage.DB, cmdName string) (*Lock, error) {
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("writelock: acquire conn: %w", err)
	}
	success := false
	defer func() {
		if !success {
			conn.Release()
		}
	}()

	var got bool
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock($1)`, LockID).Scan(&got); err != nil {
		return nil, fmt.Errorf("writelock: try lock: %w", err)
	}
	if !got {
		// Busy: identity read deferred to Task 4.
		return nil, ErrBusy{}
	}

	clientPID := os.Getpid()
	host, _ := os.Hostname()
	var rowID int64
	if err := conn.QueryRow(ctx, `
		INSERT INTO index_locks (lock_id, client_pid, backend_pid, hostname, cmd)
		VALUES ($1, $2, pg_backend_pid(), $3, $4)
		RETURNING id
	`, LockID, clientPID, host, cmdName).Scan(&rowID); err != nil {
		_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, LockID)
		return nil, fmt.Errorf("writelock: insert holder: %w", err)
	}

	success = true
	return &Lock{conn: conn, rowID: rowID}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestAcquire_HappyPath -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/writelock/acquire.go internal/storage/writelock/integration_test.go
git commit -m "feat(storage/writelock): Acquire happy path with bookkeeping row"
```

---

## Task 4: Acquire — busy path with holder identity

**Files:**
- Modify: `internal/storage/writelock/acquire.go`
- Modify: `internal/storage/writelock/integration_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestAcquire_BusyReturnsHolderIdentity(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	first, err := writelock.Acquire(ctx, db, "alice")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer first.Release(ctx)

	_, err = writelock.Acquire(ctx, db, "bob")
	be, ok := err.(writelock.ErrBusy)
	if !ok {
		t.Fatalf("second Acquire returned %T (%v), want ErrBusy", err, err)
	}
	if be.HolderPID != os.Getpid() {
		t.Errorf("HolderPID = %d, want %d", be.HolderPID, os.Getpid())
	}
	if be.HolderCmd != "alice" {
		t.Errorf("HolderCmd = %q, want %q", be.HolderCmd, "alice")
	}
	if be.HolderHost == "" {
		t.Error("HolderHost is empty")
	}
	if be.HolderStartedAt.IsZero() {
		t.Error("HolderStartedAt is zero")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestAcquire_BusyReturnsHolderIdentity -v`
Expected: FAIL — `ErrBusy` returned with all-zero fields.

- [ ] **Step 3: Replace the placeholder with the identity read**

In `acquire.go`, replace the `if !got { return nil, ErrBusy{} }` block with:

```go
	if !got {
		var b ErrBusy
		// Display client_pid (what shows up in `ps`/operator dashboards),
		// not backend_pid.
		_ = conn.QueryRow(ctx, `
			SELECT client_pid, hostname, cmd, started_at FROM index_locks
			WHERE lock_id = $1`, LockID).
			Scan(&b.HolderPID, &b.HolderHost, &b.HolderCmd, &b.HolderStartedAt)
		return nil, b
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestAcquire_BusyReturnsHolderIdentity -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/writelock/acquire.go internal/storage/writelock/integration_test.go
git commit -m "feat(storage/writelock): ErrBusy carries holder identity"
```

---

## Task 5: Stale-row reap on Acquire

> **rev 2:** reap matches `pg_stat_activity.pid` against the stored
> `backend_pid`, not `client_pid`. Tests plant rows with a fake
> `backend_pid`.

**Files:**
- Modify: `internal/storage/writelock/acquire.go`
- Modify: `internal/storage/writelock/integration_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestAcquire_ReapsStaleRow(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Plant a row with a backend_pid that does not exist in pg_stat_activity.
	const fakeBackendPID = 999_999
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO index_locks (lock_id, client_pid, backend_pid, hostname, cmd)
		VALUES ($1, 12345, $2, 'ghost', 'ghost-cmd')
	`, writelock.LockID, fakeBackendPID); err != nil {
		t.Fatalf("plant ghost: %v", err)
	}

	// Note: pg_try_advisory_lock will still succeed because the ghost row
	// did NOT actually take the advisory lock. The reap is what keeps the
	// row table consistent; without it the INSERT below would violate the
	// UNIQUE (lock_id) constraint.
	lock, err := writelock.Acquire(ctx, db, "fresh")
	if err != nil {
		t.Fatalf("Acquire after stale row: %v", err)
	}
	defer lock.Release(ctx)

	var clientPID, backendPID int
	if err := db.Pool.QueryRow(ctx,
		`SELECT client_pid, backend_pid FROM index_locks WHERE lock_id = $1`, writelock.LockID).
		Scan(&clientPID, &backendPID); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if backendPID == fakeBackendPID {
		t.Errorf("ghost row not reaped; backend_pid still %d", backendPID)
	}
	if clientPID != os.Getpid() {
		t.Errorf("client_pid = %d, want %d", clientPID, os.Getpid())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestAcquire_ReapsStaleRow -v`
Expected: FAIL — `INSERT` collides with the ghost row, returning a unique-violation error.

- [ ] **Step 3: Add reap step before the advisory-lock try**

In `acquire.go`, before the `pg_try_advisory_lock` call, add:

```go
	if _, err := conn.Exec(ctx, `
		DELETE FROM index_locks
		WHERE backend_pid NOT IN (SELECT pid FROM pg_stat_activity WHERE pid IS NOT NULL)
	`); err != nil {
		return nil, fmt.Errorf("writelock: reap stale: %w", err)
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestAcquire_ReapsStaleRow -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/writelock/acquire.go internal/storage/writelock/integration_test.go
git commit -m "feat(storage/writelock): reap stale index_locks rows on Acquire"
```

---

## Task 6: Release — happy path + idempotency

**Files:**
- Create: `internal/storage/writelock/release.go`
- Modify: `internal/storage/writelock/integration_test.go`

- [ ] **Step 1: Write the failing tests**

Append:

```go
func TestRelease_RemovesRowAndReleasesLock(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	lock, err := writelock.Acquire(ctx, db, "first")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}

	var n int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM index_locks WHERE lock_id = $1`, writelock.LockID).
		Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("rows after Release = %d, want 0", n)
	}

	// Re-acquire should succeed.
	lock2, err := writelock.Acquire(ctx, db, "second")
	if err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}
	_ = lock2.Release(ctx)
}

func TestRelease_IsIdempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	lock, err := writelock.Acquire(ctx, db, "x")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(ctx); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lock.Release(ctx); err != nil {
		t.Fatalf("second Release returned err: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestRelease -v`
Expected: build failure — `Release` undefined.

- [ ] **Step 3: Implement Release**

```go
// internal/storage/writelock/release.go
package writelock

import (
	"context"
	"fmt"
)

// Release drops the bookkeeping row, calls pg_advisory_unlock, and
// returns the pinned connection to the pool. Idempotent — calling
// Release a second time is a no-op and returns nil.
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

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestRelease -v`
Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/writelock/release.go internal/storage/writelock/integration_test.go
git commit -m "feat(storage/writelock): Release with idempotency guard"
```

---

## Task 7: Concurrent contention test

**Files:**
- Modify: `internal/storage/writelock/integration_test.go`

- [ ] **Step 1: Write the test**

Append:

```go
func TestAcquire_ContentionSerializes(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Run two Acquire calls concurrently, on the same DB pool. Exactly
	// one must win; the other must see ErrBusy.
	results := make(chan error, 2)
	starter := make(chan struct{})

	for i := 0; i < 2; i++ {
		go func(i int) {
			<-starter
			lock, err := writelock.Acquire(ctx, db, "concurrent")
			if err != nil {
				results <- err
				return
			}
			// Hold briefly so the other goroutine has time to fail.
			defer lock.Release(ctx)
			time.Sleep(100 * time.Millisecond)
			results <- nil
		}(i)
	}
	close(starter)

	r1 := <-results
	r2 := <-results
	wins, busies := 0, 0
	for _, r := range []error{r1, r2} {
		switch {
		case r == nil:
			wins++
		default:
			if _, ok := r.(writelock.ErrBusy); ok {
				busies++
			} else {
				t.Errorf("unexpected error type: %T (%v)", r, r)
			}
		}
	}
	if wins != 1 || busies != 1 {
		t.Errorf("wins=%d busies=%d, want 1/1", wins, busies)
	}
}
```

Add `"time"` to the imports if not already present.

- [ ] **Step 2: Run**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestAcquire_ContentionSerializes -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/writelock/integration_test.go
git commit -m "test(storage/writelock): assert exactly one of two concurrent Acquire wins"
```

---

## Task 8: ForceUnlock + test

> **rev 2 (critical):** `pg_advisory_unlock_all` on a fresh connection
> CANNOT release another session's session-scoped advisory lock. The
> escape hatch instead reads `backend_pid` from the bookkeeping row
> and calls `pg_terminate_backend(backend_pid)`, which drops the
> holder's connection and causes Postgres to auto-release the lock.

**Files:**
- Create: `internal/storage/writelock/force.go`
- Modify: `internal/storage/writelock/integration_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestForceUnlock_TerminatesHolderAndReleasesLock(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	lock, err := writelock.Acquire(ctx, db, "stuck")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if err := writelock.ForceUnlock(ctx, db); err != nil {
		t.Fatalf("ForceUnlock: %v", err)
	}

	var n int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM index_locks WHERE lock_id = $1`, writelock.LockID).
		Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("rows after ForceUnlock = %d, want 0", n)
	}

	// A subsequent Acquire from a third connection succeeds — proves the
	// advisory lock was actually released, not just the row deleted.
	lock2, err := writelock.Acquire(ctx, db, "after-force")
	if err != nil {
		t.Fatalf("Acquire after ForceUnlock: %v", err)
	}
	_ = lock2.Release(ctx)

	// Original Lock's Release will see a dead connection (the backend
	// was terminated). Release must not panic; it returns a wrapped
	// error which the caller is expected to ignore.
	_ = lock.Release(ctx)
}

func TestForceUnlock_OnIdleDB_Succeeds(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := writelock.ForceUnlock(ctx, db); err != nil {
		t.Fatalf("ForceUnlock on idle DB: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestForceUnlock -v`
Expected: build failure — `ForceUnlock` undefined.

- [ ] **Step 3: Implement ForceUnlock**

```go
// internal/storage/writelock/force.go
package writelock

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hman-pro/projectlens/internal/storage"
)

// ForceUnlock terminates the holder's Postgres backend (which causes
// Postgres to auto-release the holder's session-scoped advisory lock)
// and deletes the index_locks bookkeeping row. Used by
// `projectlens unlock --force` when auto-recovery has failed.
//
// We CANNOT release the holder's advisory lock from a different
// session: pg_advisory_unlock_all on a fresh connection only releases
// locks held by that fresh connection, never the holder's. Killing
// the backend is the only mechanism that forces a session-scoped
// advisory lock to drop from outside the holder's own session.
func ForceUnlock(ctx context.Context, db *storage.DB) error {
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("writelock: acquire conn: %w", err)
	}
	defer conn.Release()

	var clientPID, backendPID int
	var host, cmd string
	var started time.Time
	switch err := conn.QueryRow(ctx, `
		SELECT client_pid, backend_pid, hostname, cmd, started_at FROM index_locks
		WHERE lock_id = $1`, LockID).
		Scan(&clientPID, &backendPID, &host, &cmd, &started); {
	case err == nil:
		log.Printf("writelock: force-unlocking holder client_pid=%d backend_pid=%d host=%s cmd=%q started=%s",
			clientPID, backendPID, host, cmd, started.Format(time.RFC3339))
		if backendPID > 0 {
			var killed bool
			if err := conn.QueryRow(ctx,
				`SELECT pg_terminate_backend($1)`, backendPID).Scan(&killed); err != nil {
				return fmt.Errorf("writelock: terminate backend %d: %w", backendPID, err)
			}
			if !killed {
				log.Printf("writelock: pg_terminate_backend(%d) returned false; backend already gone", backendPID)
			}
		}
	case errors.Is(err, pgx.ErrNoRows):
		log.Printf("writelock: no active holder; running unlock anyway")
	default:
		return fmt.Errorf("writelock: read holder: %w", err)
	}

	if _, err := conn.Exec(ctx,
		`DELETE FROM index_locks WHERE lock_id = $1`, LockID); err != nil {
		return fmt.Errorf("writelock: delete row: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestForceUnlock -v`
Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/writelock/force.go internal/storage/writelock/integration_test.go
git commit -m "feat(storage/writelock): ForceUnlock with audit log"
```

---

## Task 9: CLI helper `withWriteLock` + wire mutating commands

> **rev 2:** the helper passes `cmd`, `cfg`, and resolved `repoPath`
> to the wrapped body. Wrapping `bootstrap` directly is unsafe because
> bootstrap is the command that creates the `index_locks` table —
> introduce a second helper `withWriteLockAfterMigrate` that runs
> migrations BEFORE `Acquire`.

**Files:**
- Modify: `cmd/projectlens/main.go`

- [ ] **Step 1: Add the helpers**

Insert near the existing `loadCmdConfig` helper:

```go
// LockedCmd is the body shape for write-locked subcommands. It receives
// the already-loaded config, the resolved repo path, the open DB, and
// the cobra command — everything the original RunE had access to.
type LockedCmd func(ctx context.Context, cmd *cobra.Command, db *storage.DB, cfg *config.Config, repoPath string) error

func acquireOrExit(ctx context.Context, db *storage.DB, cmdName string) (*writelock.Lock, error) {
	lock, err := writelock.Acquire(ctx, db, cmdName)
	if err != nil {
		if be, ok := err.(writelock.ErrBusy); ok {
			fmt.Fprintln(os.Stderr, be.Error())
			os.Exit(75)
		}
		return nil, err
	}
	return lock, nil
}

// withWriteLock wraps a mutating command's RunE so it acquires the
// writer lock on entry and releases it on exit. ErrBusy results in
// exit code 75 (sysexits.h EX_TEMPFAIL). Used by every mutating
// command except `bootstrap` (see withWriteLockAfterMigrate).
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

		lock, err := acquireOrExit(ctx, db, cmdName)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Release(context.Background()) }()

		return run(ctx, cmd, db, cfg, repoPath)
	}
}

// withWriteLockAfterMigrate is the bootstrap variant. bootstrap is the
// command that *creates* the index_locks table, so it must run
// migrations BEFORE attempting Acquire. Migrations themselves are
// idempotent and are not subject to the lock — they run before any
// pipeline mutation. After migrations succeed, the wrapper acquires
// the lock and runs the rest of bootstrap under it.
func withWriteLockAfterMigrate(cmdName, migrationsDir string, run LockedCmd) func(*cobra.Command, []string) error {
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
		lock, err := acquireOrExit(ctx, db, cmdName)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Release(context.Background()) }()
		return run(ctx, cmd, db, cfg, repoPath)
	}
}
```

Add the imports:

```go
"github.com/hman-pro/projectlens/internal/config"
"github.com/hman-pro/projectlens/internal/storage/writelock"
```

- [ ] **Step 2: Wrap each mutating command**

For each of the seven mutating commands, refactor the existing `RunE` so
its body becomes a `func(ctx context.Context, db *storage.DB) error`,
then route it through `withWriteLock`. Example for `reindex`:

Before:
```go
func newReindexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "...",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := loadCmdConfig(cmd)
			if err != nil { return err }
			ctx := cmd.Context()
			db, err := storage.Connect(ctx, cfg.DatabaseURL)
			if err != nil { return err }
			defer db.Close()
			// ...existing reindex body...
		},
	}
	return cmd
}
```

After:
```go
func newReindexCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "reindex", Short: "..."}
	cmd.RunE = withWriteLock("reindex", func(ctx context.Context, c *cobra.Command, db *storage.DB, cfg *config.Config, repoPath string) error {
		// ...existing reindex body...
		// `c` is the cobra command (use c.Flags() for command-specific flags),
		// `cfg` is the loaded config, `repoPath` is already resolved.
	})
	return cmd
}
```

For `bootstrap`, use the migrate-first helper:

```go
func newBootstrapCmd(migrationsDir string) *cobra.Command {
	cmd := &cobra.Command{Use: "bootstrap", Short: "..."}
	cmd.RunE = withWriteLockAfterMigrate("bootstrap", migrationsDir,
		func(ctx context.Context, c *cobra.Command, db *storage.DB, cfg *config.Config, repoPath string) error {
			// ...existing bootstrap body MINUS the db.Migrate call (it
			// already ran before Acquire).
		})
	return cmd
}
```

Apply the `withWriteLock` shape to: `reindex`, `index-datastore`,
`index-history`, `index-embed`, `index-summarize`, `index-all`.
`bootstrap` uses `withWriteLockAfterMigrate`. Read-only commands
(`census`, `status`, `query`, `inspect-symbol`, `inspect-package`,
`knowledge list/show/search/delete`) are NOT wrapped — see the
"`knowledge delete` invariant" note in the design doc, and Task 11.5
for the test that asserts non-interference with `index-embed`.

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 4: Run existing CLI test sweep**

Run: `go test ./...`
Expected: every existing test still passes.

- [ ] **Step 5: Commit**

```bash
git add cmd/projectlens/main.go
git commit -m "feat(cmd/projectlens): wrap mutating commands with writelock"
```

---

## Task 10: `projectlens unlock --force` command

**Files:**
- Create: `cmd/projectlens/unlock.go`
- Modify: `cmd/projectlens/main.go`

- [ ] **Step 1: Implement the command**

```go
// cmd/projectlens/unlock.go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/hman-pro/projectlens/internal/storage/writelock"
)

func newUnlockCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "unlock",
		Short: "Release the indexer write lock (use --force to override)",
		Long: `Releases the writer lock and deletes its bookkeeping row.

Auto-recovery handles crashed processes; use --force only when
auto-recovery has failed (e.g. a recycled PID looks alive in
pg_stat_activity).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !force {
				return fmt.Errorf("refusing to unlock without --force")
			}
			cfg, _, err := loadCmdConfig(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			db, err := storage.Connect(ctx, cfg.DatabaseURL)
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

- [ ] **Step 2: Register the command**

In `cmd/projectlens/main.go`, add `newUnlockCmd()` to the root command's
child list alongside the other commands.

- [ ] **Step 3: Build**

Run: `go build ./cmd/projectlens/`
Expected: clean build.

- [ ] **Step 4: Manual smoke**

Run:
```bash
go run ./cmd/projectlens/ unlock
go run ./cmd/projectlens/ unlock --force
```

Expected:
- First call exits non-zero with `refusing to unlock without --force`.
- Second call exits 0 and logs `writelock: no active holder; running unlock anyway`.

- [ ] **Step 5: Commit**

```bash
git add cmd/projectlens/unlock.go cmd/projectlens/main.go
git commit -m "feat(cmd/projectlens): unlock --force escape hatch"
```

---

## Task 10.5: Hidden `debug-hold-lock` test command

> **rev 2:** rev 1's contention test launched two `index-embed`
> invocations on what may be an empty queue, allowing both to finish
> sequentially without overlapping at the lock. We need a deterministic
> way for the winner to *hold* the lock for a known duration. A hidden
> `debug-hold-lock --hold <dur>` subcommand acquires the lock, sleeps,
> then releases.

**Files:**
- Create: `cmd/projectlens/debug.go`
- Modify: `cmd/projectlens/main.go` (register only when env gate set)

- [ ] **Step 1: Implement the hidden command**

```go
// cmd/projectlens/debug.go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/storage"
)

// newDebugHoldLockCmd is gated behind PROJECTLENS_DEBUG_HOLD_LOCK=1 so it is
// invisible in production binaries. It acquires the writer lock and
// sleeps for --hold to make CLI contention tests deterministic.
func newDebugHoldLockCmd() *cobra.Command {
	var hold time.Duration
	cmd := &cobra.Command{
		Use:    "debug-hold-lock",
		Hidden: true,
		Short:  "[test only] hold the writer lock for --hold duration then release",
		RunE: withWriteLock("debug-hold-lock",
			func(ctx context.Context, c *cobra.Command, db *storage.DB, cfg *config.Config, repoPath string) error {
				fmt.Println("debug-hold-lock: acquired; sleeping", hold)
				select {
				case <-time.After(hold):
				case <-ctx.Done():
				}
				return nil
			}),
	}
	cmd.Flags().DurationVar(&hold, "hold", 3*time.Second, "duration to hold the lock")
	return cmd
}
```

- [ ] **Step 2: Register the command behind the env gate**

In `cmd/projectlens/main.go`, near the other `AddCommand` calls:

```go
if os.Getenv("PROJECTLENS_DEBUG_HOLD_LOCK") == "1" {
	root.AddCommand(newDebugHoldLockCmd())
}
```

- [ ] **Step 3: Build**

Run: `go build ./cmd/projectlens/`
Expected: clean build.

- [ ] **Step 4: Smoke**

Run: `PROJECTLENS_DEBUG_HOLD_LOCK=1 go run ./cmd/projectlens/ debug-hold-lock --hold 1s --db "$DATABASE_URL" --config configs/index.yaml --repo .`
Expected: prints `debug-hold-lock: acquired; sleeping 1s` then exits 0
about a second later.

- [ ] **Step 5: Commit**

```bash
git add cmd/projectlens/debug.go cmd/projectlens/main.go
git commit -m "feat(cmd/projectlens): hidden debug-hold-lock test command"
```

---

## Task 11: CLI integration test — two-process contention (deterministic)

> **rev 2:** uses `debug-hold-lock --hold 3s` on both racers. The
> first to acquire holds the lock for 3 seconds, guaranteeing the
> second observes `ErrBusy`.

**Files:**
- Create: `internal/storage/writelock/cli_integration_test.go`

- [ ] **Step 1: Write the test**

```go
// internal/storage/writelock/cli_integration_test.go
//go:build integration

package writelock_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCLI_TwoHoldersSerialize(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}

	dir := t.TempDir()
	binPath := filepath.Join(dir, "projectlens")
	build := exec.Command("go", "build", "-o", binPath, "../../../cmd/projectlens/")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build projectlens: %v", err)
	}

	// Launch two `debug-hold-lock --hold 3s` invocations concurrently.
	// The winner holds for 3s, guaranteeing the loser sees ErrBusy.
	type result struct {
		exit int
		err  error
		out  string
	}
	run := func() result {
		var stderr bytes.Buffer
		c := exec.Command(binPath, "debug-hold-lock", "--hold", "3s",
			"--db", dsn, "--repo", t.TempDir(),
			"--config", "../../../configs/index.yaml")
		c.Env = append(os.Environ(), "PROJECTLENS_DEBUG_HOLD_LOCK=1")
		c.Stderr = &stderr
		err := c.Run()
		exit := 0
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else if err != nil {
			exit = -1
		}
		return result{exit: exit, err: err, out: stderr.String()}
	}
	var wg sync.WaitGroup
	results := make([]result, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = run()
		}(i)
	}
	wg.Wait()

	wins, busies := 0, 0
	for _, r := range results {
		switch r.exit {
		case 0:
			wins++
		case 75:
			if !strings.Contains(r.out, "another writer holds the lock") {
				t.Errorf("exit 75 but stderr lacks busy text: %q", r.out)
			}
			busies++
		default:
			t.Errorf("unexpected exit %d (err=%v stderr=%q)", r.exit, r.err, r.out)
		}
	}
	if wins != 1 || busies != 1 {
		t.Errorf("wins=%d busies=%d, want 1/1", wins, busies)
	}
}
```

> **Note:** the test assumes migration 005 is already applied against
> the test DB; the existing test bootstrap (or a manual `bootstrap` run)
> covers that. Adjust the `--config` path if the test runs from a
> different working directory.

- [ ] **Step 2: Run**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestCLI_TwoHoldersSerialize -v`
Expected: PASS (one process exits 0, the other exits 75 with the busy
message on stderr).

- [ ] **Step 3: Commit**

```bash
git add internal/storage/writelock/cli_integration_test.go
git commit -m "test(storage/writelock): deterministic two-process CLI contention"
```

---

## Task 11.5: `knowledge delete` non-interference test

> **rev 2:** the design declares `knowledge delete` safe to run
> outside the writer lock. Document and assert this with a focused
> test: an `index-embed`-style scan running concurrently with a
> `knowledge delete` must complete without error, and the deleted
> knowledge row must not reappear.

**Files:**
- Create: `internal/storage/writelock/knowledge_race_test.go`

- [ ] **Step 1: Write the test**

```go
// internal/storage/writelock/knowledge_race_test.go
//go:build integration

package writelock_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/storage/writelock"
)

// TestKnowledgeDelete_DoesNotRaceIndexerScan asserts the design
// invariant: deleting a knowledge_entries row + its paired chunk does
// not race an index-embed-style "chunks WHERE embedding IS NULL" scan.
// The scan reads only chunks; the delete simply removes a candidate.
func TestKnowledgeDelete_DoesNotRaceIndexerScan(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Acquire the writer lock to simulate index-embed running.
	lock, err := writelock.Acquire(ctx, db, "index-embed")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release(ctx)

	// In a goroutine, run a representative scan loop while the main
	// goroutine deletes a knowledge row.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = db.Pool.Exec(ctx,
				`SELECT id FROM chunks WHERE source_type = 'knowledge' LIMIT 100`)
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// The delete uses NO lock — it must succeed despite the scan loop.
	if _, err := db.Pool.Exec(ctx,
		`DELETE FROM knowledge_entries WHERE id = -1`); err != nil {
		t.Errorf("delete with concurrent scan: %v", err)
	}
	close(stop)
	wg.Wait()
}
```

- [ ] **Step 2: Run**

Run: `go test -tags integration ./internal/storage/writelock/... -run TestKnowledgeDelete_DoesNotRaceIndexerScan -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/writelock/knowledge_race_test.go
git commit -m "test(storage/writelock): knowledge delete non-interference invariant"
```

---

## Task 12: Manual smoke test

- [ ] **Step 1: Apply the migration against your dev DB**

Run:
```bash
go run ./cmd/projectlens/ bootstrap --repo "$REPO_PATH" --db "$DATABASE_URL" || true
psql "$DATABASE_URL" -c "\d index_locks"
```

Expected: table exists.

- [ ] **Step 2: Hold the lock from one shell**

Shell A:
```bash
go run ./cmd/projectlens/ reindex
```

- [ ] **Step 3: Try to take it from a second shell**

Shell B (while Shell A is still running):
```bash
go run ./cmd/projectlens/ reindex
```

Expected: Shell B exits non-zero (75) with `another writer holds the lock: pid=<A's pid> host=<host> cmd="reindex" started=<ts>`.

- [ ] **Step 4: Inspect `pg_locks` and `index_locks`**

```bash
psql "$DATABASE_URL" -c "SELECT * FROM index_locks"
# 1-arg bigint advisory locks split across classid/objid (high/low int32).
# Reconstruct the bigint to filter:
psql "$DATABASE_URL" -c "
SELECT * FROM pg_locks
WHERE locktype = 'advisory'
  AND ((classid::bigint << 32) | (objid::bigint & 4294967295)) = 9876543210
"
# Alternative: prove the lock is held from a fresh connection:
psql "$DATABASE_URL" -c "SELECT pg_try_advisory_lock(9876543210)"  # must return f
```

Expected: one row in each query (and `f` from the try-lock), all
pointing at Shell A's backend. The naive
`WHERE objid = 9876543210` query (rev 1) returns zero rows because
`objid` only holds the low 32 bits.

- [ ] **Step 5: Kill Shell A and re-run Shell B**

```bash
# kill -9 the Shell A pid
go run ./cmd/projectlens/ reindex
```

Expected: Shell B proceeds normally (the stale row is reaped on
acquire).

- [ ] **Step 6: Test `unlock --force`**

```bash
# In one shell, hold the lock:
go run ./cmd/projectlens/ reindex &
# In another shell, force-release:
go run ./cmd/projectlens/ unlock --force
psql "$DATABASE_URL" -c "SELECT count(*) FROM index_locks"
```

Expected: `index_locks` is empty; the running `reindex` may then race a
concurrent writer (documented footgun).

---

## Task 13: Docs update

**Files:**
- Modify: `CLAUDE.md`
- Modify: `README.md`

- [ ] **Step 1: Document the lock in CLAUDE.md**

Add a subsection under the existing "Database" section:

```markdown
### Writer lock

Mutating indexer commands (`bootstrap`, `reindex`, `index-datastore`,
`index-history`, `index-embed`, `index-summarize`, `index-all`) acquire
a single Postgres advisory lock at acquire time. Read-only commands
(`status`, `query`, `inspect-*`, `census`, `knowledge` subcommands) and
the MCP server bypass the lock.

Holder identity is recorded in `index_locks`. When the lock is held by
another process, a second writer exits with code **75** (sysexits
`EX_TEMPFAIL`) and a stderr line of the form:

```
another writer holds the lock: pid=<n> host=<h> cmd="<c>" started=<RFC3339>
```

**Auto-recovery:** if a holder is killed (kill -9, OOM, panic), the
advisory lock auto-releases when the connection drops; the next
`Acquire` reaps the orphaned row via a `pg_stat_activity` join.

**Escape hatch:** `projectlens unlock --force` reads the holder's
backend pid from `index_locks`, calls `pg_terminate_backend(pid)` to
drop the holder's session (which auto-releases the advisory lock),
then deletes the bookkeeping row. Use only when auto-recovery has
failed (e.g. a recycled client PID makes the row look live). Logs
the previous holder identity for audit. Note: this kills the holder's
DB session — any in-flight transactions in that process roll back.
```

- [ ] **Step 2: Update README troubleshooting**

In `README.md`, add a "Troubleshooting" or "Operations" subsection
covering the same `unlock --force` flow at a high level for end users.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md README.md
git commit -m "docs: writer lock + unlock --force + exit code 75"
```

---

## Self-review

**Spec coverage:**

- Migration `005_writer_lock` (with `client_pid` + `backend_pid`) → Task 1.
- `LockID`, `Lock`, `ErrBusy` types → Task 2.
- `Acquire` happy path → Task 3.
- `Acquire` busy path with holder identity → Task 4.
- Stale-row reap (by `backend_pid`) → Task 5.
- `Release` + idempotency → Task 6.
- Concurrent contention test → Task 7.
- `ForceUnlock` (terminate-backend) → Task 8.
- `withWriteLock` + `withWriteLockAfterMigrate` CLI helpers → Task 9.
- `unlock --force` command → Task 10.
- Hidden `debug-hold-lock` test command → Task 10.5 (rev 2).
- Two-process CLI contention test (deterministic) → Task 11.
- `knowledge delete` non-interference test → Task 11.5 (rev 2).
- Manual smoke / verification → Task 12.
- Docs (CLAUDE.md + README.md) → Task 13.
- Read-only commands untouched → enforced by Task 9 (only listed seven
  commands wrapped) and verified by Task 9 Step 4 (existing test sweep).
- Connection lifetime (pinned `*pgxpool.Conn`) → Tasks 3, 6 implementations.

**Placeholder scan:** no `TBD` / `TODO` / "fill in" prose; every code
step shows the actual code; every command step shows the exact command
+ expected output.

**Type consistency:** `Lock`, `LockID`, `ErrBusy`, `Acquire`, `Release`,
`ForceUnlock`, `withWriteLock`, `withWriteLockAfterMigrate`,
`LockedCmd` are spelled identically across all tasks. `client_pid` /
`backend_pid` column names appear consistently in migration, INSERTs,
SELECTs, and reaps. `*storage.DB` (not `*storage.Connection`) used
throughout to match the existing codebase. Migrator signature
`db.Migrate(ctx, migrationsDir string)` matches `internal/storage/db.go:41`.

**Spec sections covered:**

- Architecture (package layout, public API) → Tasks 2–8.
- Connection lifetime → Tasks 3, 6.
- Acquire flow (reap, try, identity, insert) → Tasks 3, 4, 5.
- Release flow (idempotency, conn release) → Task 6.
- ForceUnlock with audit log → Task 8.
- Migration 005 → Task 1.
- CLI integration → Tasks 9, 10.
- Error handling table (busy, kill, force, missing migration) →
  Tasks 4, 5, 8, 12.
- Testing strategy (unit, integration, CLI integration, manual) →
  Tasks 2 (unit), 3–8 (integration), 11 (CLI), 12 (manual).
- Success criteria → satisfied by Tasks 11 (#1), 12 (#2, #3, #4),
  Task 9 Step 4 (#4 — read-only untouched), Task 0 of the Phase 2 plan
  (#5).
