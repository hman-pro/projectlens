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
