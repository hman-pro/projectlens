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
//
// The acquire flow:
//  1. Reap stale rows whose backend_pid no longer appears in
//     pg_stat_activity (crash recovery).
//  2. SELECT pg_try_advisory_lock(LockID); on false return ErrBusy
//     populated from the holder row.
//  3. INSERT bookkeeping row with client_pid (os.Getpid) and
//     backend_pid (pg_backend_pid()).
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

	// 1. Reap stale rows. Match against backend_pid (the Postgres
	// backend PID) — pg_stat_activity.pid is comparable to
	// pg_backend_pid(), not to client os.Getpid().
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
		// Best-effort identity read. Display client_pid (operator-
		// visible), not backend_pid.
		_ = conn.QueryRow(ctx, `
			SELECT client_pid, hostname, cmd, started_at FROM index_locks
			WHERE lock_id = $1`, LockID).
			Scan(&b.HolderPID, &b.HolderHost, &b.HolderCmd, &b.HolderStartedAt)
		return nil, b
	}

	// 3. Insert holder row.
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
