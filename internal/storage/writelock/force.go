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
// Postgres session advisory locks can ONLY be released by the session
// that holds them. pg_advisory_unlock_all on a fresh connection only
// releases locks held by that fresh connection — never the holder's.
// Killing the holder's backend is the only way to force a session-
// scoped advisory lock to drop from outside the holder's own session.
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
