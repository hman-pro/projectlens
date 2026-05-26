// Package writelock provides cross-process serialization for the
// projectlens indexer. Mutating CLI commands acquire a Postgres
// session-scoped advisory lock (LockID) plus a bookkeeping row in
// index_locks; concurrent attempts get ErrBusy with the holder's
// identity. See docs/plans/2026-04-30-indexer-writer-lock-design.md.
package writelock

import (
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LockID is the legacy single-tenant advisory lock key, documented to
// be stable forever. LockIDFor("") and LockIDFor("public") MUST return
// this exact value so a new binary co-exists with old public writers
// during rollout and operator/recovery scripts that hard-code the
// constant continue to see the holder.
const LockID int64 = 9_876_543_210

// LockIDFor derives a stable per-schema advisory lock id. Postgres
// advisory locks are database-global, so multi-project writers MUST key
// the lock by storage schema; otherwise project A's writer can block
// project B and B's busy-path read of index_locks in its own schema
// finds nothing.
//
// Non-public schemas hash to LockID ^ fnv64a(schema). The public schema
// (and the empty default) keep the legacy constant unchanged.
func LockIDFor(schema string) int64 {
	if schema == "" || schema == "public" {
		return LockID
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(schema))
	return LockID ^ int64(h.Sum64())
}

// Lock is the handle returned by a successful Acquire. It pins the
// pgxpool connection that took the advisory lock; Release returns the
// connection to the pool.
type Lock struct {
	conn     *pgxpool.Conn
	lockID   int64
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
