package jobs_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestRunner_InitialStateIsIdle(t *testing.T) {
	r := jobs.NewRunner(jobs.RunnerTarget{}, nil)
	if got := r.State().Status; got != "idle" {
		t.Fatalf("status = %q, want %q", got, "idle")
	}
}

func TestRunner_StartRejectsInvalidSpec(t *testing.T) {
	r := jobs.NewRunner(jobs.RunnerTarget{BinaryPath: "/bin/true"}, nil)
	if err := r.Start(jobs.Spec{}); err == nil {
		t.Fatal("expected error for invalid spec")
	}
}

func TestRunner_StartExecutesAndCompletes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROJECTLENS_TUI_RUNS_DIR", dir)
	got := make(chan tea.Msg, 64)
	send := func(m tea.Msg) { got <- m }
	target := jobs.RunnerTarget{
		BinaryPath:  "/bin/sh",
		ConfigPath:  "/dev/null",
		DatabaseURL: "postgres://x",
		RepoPath:    "/tmp",
	}
	r := jobs.NewRunner(target, send)
	spec := jobs.Spec{
		Key: 'X', Name: "echo",
		Args:    []string{"-c", "echo hello && echo world 1>&2"},
		Confirm: jobs.ConfirmYesNo,
		Preflight: func(_ context.Context, _ store.Store) (int, string, error) {
			return 0, "", nil
		},
		Headline: func(int, string) string { return "" },
	}
	if err := r.Start(spec); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	var completed *jobs.JobCompletedMsg
	for completed == nil {
		select {
		case msg := <-got:
			if c, ok := msg.(jobs.JobCompletedMsg); ok {
				completed = &c
			}
		case <-deadline:
			t.Fatal("timeout waiting for JobCompletedMsg")
		}
	}
	if completed.Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", completed.Status)
	}
	if len(completed.Tail) < 2 {
		t.Errorf("tail too short: %v", completed.Tail)
	}
	data, err := os.ReadFile(completed.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "hello") || !strings.Contains(body, "world") {
		t.Errorf("log missing lines: %q", body)
	}
}

func TestRunner_CancelStopsLongRunningJob(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROJECTLENS_TUI_RUNS_DIR", dir)
	got := make(chan tea.Msg, 64)
	send := func(m tea.Msg) { got <- m }
	target := jobs.RunnerTarget{BinaryPath: "/bin/sh", ConfigPath: "/dev/null", DatabaseURL: "postgres://x", RepoPath: "/tmp"}
	r := jobs.NewRunner(target, send)
	spec := jobs.Spec{
		Key: 'X', Name: "sleep",
		// trap '' TERM ignores SIGTERM so we exercise the SIGKILL watchdog.
		Args:      []string{"-c", "trap '' TERM; sleep 30"},
		Confirm:   jobs.ConfirmYesNo,
		Preflight: func(_ context.Context, _ store.Store) (int, string, error) { return 0, "", nil },
		Headline:  func(int, string) string { return "" },
	}
	if err := r.Start(spec); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	r.Cancel()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg := <-got:
			if c, ok := msg.(jobs.JobCompletedMsg); ok {
				if c.Status != "cancelled" {
					t.Errorf("status = %q, want cancelled", c.Status)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout: cancel did not terminate within 10s")
		}
	}
}

func TestRunner_StartReturnsErrJobInFlight(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROJECTLENS_TUI_RUNS_DIR", dir)
	got := make(chan tea.Msg, 64)
	send := func(m tea.Msg) { got <- m }
	target := jobs.RunnerTarget{BinaryPath: "/bin/sh", ConfigPath: "/dev/null", DatabaseURL: "postgres://x", RepoPath: "/tmp"}
	r := jobs.NewRunner(target, send)
	spec := jobs.Spec{
		Key: 'X', Name: "wait",
		Args:      []string{"-c", "sleep 2"},
		Confirm:   jobs.ConfirmYesNo,
		Preflight: func(_ context.Context, _ store.Store) (int, string, error) { return 0, "", nil },
		Headline:  func(int, string) string { return "" },
	}
	if err := r.Start(spec); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(spec); err != jobs.ErrJobInFlight {
		t.Fatalf("second Start = %v, want ErrJobInFlight", err)
	}
	r.Cancel()
	for {
		msg := <-got
		if _, ok := msg.(jobs.JobCompletedMsg); ok {
			return
		}
	}
}
