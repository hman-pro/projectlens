// Package writelock provides cross-process serialization for the
// projectlens indexer. Mutating CLI commands acquire a Postgres
// session-scoped advisory lock (LockID) plus a bookkeeping row in
// index_locks; concurrent attempts get ErrBusy with the holder's
// identity. See docs/plans/2026-04-30-indexer-writer-lock-design.md.
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
// HolderPID is the operator-visible client PID (os.Getpid of the holder
// process), NOT the Postgres backend pid.
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
