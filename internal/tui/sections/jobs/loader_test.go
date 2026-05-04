package jobs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/sections/jobs"
)

func writeLog(t *testing.T, dir, name string, lines []string, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestListRuns_ParsesAndOrders(t *testing.T) {
	dir := t.TempDir()
	t1 := time.Date(2026, 5, 4, 12, 29, 41, 0, time.UTC)
	t2 := time.Date(2026, 5, 4, 13, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 5, 4, 14, 0, 0, 0, time.UTC)
	writeLog(t, dir, t1.Format(time.RFC3339)+"-index-datastore.log",
		[]string{"stdout\thello", "stderr\twarn"}, t1.Add(2*time.Second))
	writeLog(t, dir, t2.Format(time.RFC3339)+"-reindex.log",
		[]string{"stdout\tok", "stderr\tError: boom"}, t2.Add(5*time.Second))
	writeLog(t, dir, t3.Format(time.RFC3339)+"-index-embed.log",
		[]string{"stdout\tdone"}, t3.Add(time.Second))
	writeLog(t, dir, "garbage.log", []string{"x"}, time.Time{})

	runs, err := jobs.ListRuns(dir)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3 (got %+v)", len(runs), runs)
	}
	if !runs[0].Started.Equal(t3) {
		t.Errorf("expected newest first; got %v", runs[0].Started)
	}
	for _, r := range runs {
		switch r.Action {
		case "reindex":
			if r.Status != "failed" {
				t.Errorf("reindex status = %q, want failed", r.Status)
			}
		case "index-datastore", "index-embed":
			if r.Status != "completed" {
				t.Errorf("%s status = %q, want completed", r.Action, r.Status)
			}
		default:
			t.Errorf("unexpected action %q", r.Action)
		}
		if r.Duration <= 0 {
			t.Errorf("expected positive duration for %s, got %v", r.Action, r.Duration)
		}
	}
}

func TestListRuns_MissingDirReturnsEmpty(t *testing.T) {
	runs, err := jobs.ListRuns(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("want empty, got %d", len(runs))
	}
}

func TestReadTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.log")
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString("line\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tail, err := jobs.ReadTail(path, 50)
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}
	if len(tail) != 50 {
		t.Fatalf("len = %d, want 50", len(tail))
	}
}
