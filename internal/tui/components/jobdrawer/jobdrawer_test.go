package jobdrawer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/components/jobdrawer"
)

func TestRunningRendersElapsed(t *testing.T) {
	d := jobdrawer.New()
	d.SetState(jobdrawer.State{
		Status:  "running",
		Spec:    "reindex",
		Started: time.Now().Add(-3 * time.Second),
		Tail:    []string{"INFO indexing"},
		LogPath: "/tmp/x.log",
	}, 80, 8)
	v := d.View()
	if !strings.Contains(v, "running") || !strings.Contains(v, "reindex") {
		t.Fatalf("missing fields: %q", v)
	}
}

func TestSucceededShowsLogPath(t *testing.T) {
	d := jobdrawer.New()
	d.SetState(jobdrawer.State{
		Status:   "succeeded",
		Spec:     "reindex",
		LogPath:  "/var/log/r.log",
		Duration: 2100 * time.Millisecond,
		Tail:     []string{"done"},
	}, 80, 8)
	v := d.View()
	for _, want := range []string{"ok", "/var/log/r.log", "2.1"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}

func TestHiddenWhenIdle(t *testing.T) {
	d := jobdrawer.New()
	d.SetState(jobdrawer.State{Status: "idle"}, 80, 8)
	if d.View() != "" {
		t.Fatalf("expected empty view when idle, got %q", d.View())
	}
}
