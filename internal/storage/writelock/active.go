package writelock

import (
	"context"
	"fmt"

	"github.com/hman-pro/projectlens/internal/storage"
)

// IsWriterActive reports whether a live writer currently holds the
// per-schema advisory lock. A row in index_locks alone is not enough —
// its backend_pid must still appear in pg_stat_activity, mirroring the
// liveness check used in Acquire to reap stale rows.
func IsWriterActive(ctx context.Context, db *storage.DB, schema string) (bool, error) {
	const q = `
		SELECT EXISTS(
			SELECT 1
			FROM index_locks l
			WHERE l.lock_id = $1
			  AND l.backend_pid IN (
				SELECT pid FROM pg_stat_activity WHERE pid IS NOT NULL
			  )
		)
	`
	var active bool
	if err := db.Pool.QueryRow(ctx, q, LockIDFor(schema)).Scan(&active); err != nil {
		return false, fmt.Errorf("writelock: is active: %w", err)
	}
	return active, nil
}
