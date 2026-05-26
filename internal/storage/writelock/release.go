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
		`SELECT pg_advisory_unlock($1)`, l.lockID).Scan(&ok); err != nil {
		return fmt.Errorf("writelock: advisory unlock: %w", err)
	}
	return nil
}
