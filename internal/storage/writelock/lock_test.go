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
	// LockIDFor is deterministic; calling it twice for the same schema
	// must return identical values so a second process keys onto the
	// same advisory lock.
	if writelock.LockIDFor("public") != writelock.LockID {
		t.Errorf("LockID drifted: LockID=%d LockIDFor(public)=%d",
			writelock.LockID, writelock.LockIDFor("public"))
	}
	if writelock.LockIDFor("project_a") == writelock.LockIDFor("project_b") {
		t.Error("LockIDFor produced collision across distinct schemas")
	}
	if writelock.LockIDFor("project_a") == writelock.LockIDFor("public") {
		t.Error("LockIDFor produced collision against public")
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
