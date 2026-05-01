//go:build integration

// Integration tests for the writelock package against a live database.
// Run with: go test ./internal/storage/writelock/ -tags integration -v
//
// Prerequisites:
//   - Postgres running with projectlens database
//   - Migration 005_writer_lock applied.
//
// Marker-only cleanup: tests delete only rows whose cmd matches a known
// test prefix to avoid clobbering an in-flight real writer on a shared
// dev DB.
package writelock_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/hman-pro/projectlens/internal/storage/writelock"
)

const testDB = "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"

func newTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = testDB
	}
	ctx := context.Background()
	db, err := storage.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("connect: %v", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		t.Skipf("ping: %v", err)
	}
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

	lock, err := writelock.Acquire(ctx, db, "test-happy")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release(ctx)

	var clientPID, backendPID int
	var cmd string
	if err := db.Pool.QueryRow(ctx,
		`SELECT client_pid, backend_pid, cmd FROM index_locks WHERE lock_id = $1`,
		writelock.LockID).Scan(&clientPID, &backendPID, &cmd); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if clientPID != os.Getpid() {
		t.Errorf("client_pid = %d, want %d", clientPID, os.Getpid())
	}
	if backendPID == 0 {
		t.Errorf("backend_pid is zero — must be a real pg_backend_pid()")
	}
	if cmd != "test-happy" {
		t.Errorf("cmd = %q, want test-happy", cmd)
	}
}

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

func TestAcquire_ReapsStaleRow(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	const fakeBackendPID = 999_999
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO index_locks (lock_id, client_pid, backend_pid, hostname, cmd)
		VALUES ($1, 12345, $2, 'ghost', 'ghost-cmd')
	`, writelock.LockID, fakeBackendPID); err != nil {
		t.Fatalf("plant ghost: %v", err)
	}

	lock, err := writelock.Acquire(ctx, db, "fresh")
	if err != nil {
		t.Fatalf("Acquire after stale row: %v", err)
	}
	defer lock.Release(ctx)

	var clientPID, backendPID int
	if err := db.Pool.QueryRow(ctx,
		`SELECT client_pid, backend_pid FROM index_locks WHERE lock_id = $1`,
		writelock.LockID).Scan(&clientPID, &backendPID); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if backendPID == fakeBackendPID {
		t.Errorf("ghost row not reaped; backend_pid still %d", backendPID)
	}
	if clientPID != os.Getpid() {
		t.Errorf("client_pid = %d, want %d", clientPID, os.Getpid())
	}
}

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
		`SELECT COUNT(*) FROM index_locks WHERE lock_id = $1`,
		writelock.LockID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("rows after Release = %d, want 0", n)
	}

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

func TestAcquire_ContentionSerializes(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	results := make(chan error, 2)
	starter := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-starter
			lock, err := writelock.Acquire(ctx, db, "concurrent")
			if err != nil {
				results <- err
				return
			}
			defer lock.Release(ctx)
			time.Sleep(100 * time.Millisecond)
			results <- nil
		}()
	}
	close(starter)
	wg.Wait()
	close(results)

	wins, busies := 0, 0
	for r := range results {
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
		`SELECT COUNT(*) FROM index_locks WHERE lock_id = $1`,
		writelock.LockID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("rows after ForceUnlock = %d, want 0", n)
	}

	lock2, err := writelock.Acquire(ctx, db, "after-force")
	if err != nil {
		t.Fatalf("Acquire after ForceUnlock: %v", err)
	}
	_ = lock2.Release(ctx)

	// Original Lock's Release sees a dead connection — must not panic.
	_ = lock.Release(ctx)
}

func TestForceUnlock_OnIdleDB_Succeeds(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := writelock.ForceUnlock(ctx, db); err != nil {
		t.Fatalf("ForceUnlock on idle DB: %v", err)
	}
}
