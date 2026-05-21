//go:build integration

package writelock_test

import (
	"context"
	"testing"

	"github.com/hman-pro/projectlens/internal/storage/writelock"
)

func TestIsWriterActive_TrueWhenHolderLive(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	lock, err := writelock.Acquire(ctx, db, "test-is-active")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lock.Release(ctx)

	active, err := writelock.IsWriterActive(ctx, db)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !active {
		t.Errorf("want active=true while holder live")
	}
}

func TestIsWriterActive_FalseWhenNoRow(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.Pool.Exec(ctx, `DELETE FROM index_locks WHERE lock_id = $1`, writelock.LockID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	active, err := writelock.IsWriterActive(ctx, db)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if active {
		t.Errorf("want active=false with no rows")
	}
}

func TestIsWriterActive_FalseWhenBackendPidDead(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.Pool.Exec(ctx, `DELETE FROM index_locks WHERE lock_id = $1`, writelock.LockID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO index_locks (lock_id, client_pid, backend_pid, hostname, cmd)
		VALUES ($1, 0, 2147483647, 'ghost', 'ghost-cmd')
	`, writelock.LockID); err != nil {
		t.Fatalf("insert ghost: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM index_locks WHERE lock_id = $1`, writelock.LockID)
	})

	active, err := writelock.IsWriterActive(ctx, db)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if active {
		t.Errorf("want active=false with ghost row")
	}
}
