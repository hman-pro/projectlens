package app_test

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/app"
	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/sections/health"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

// TestQuit_DuringRunDrainsBeforeExit asserts that pressing q while a
// job is in flight does NOT quit immediately. The runner is asked to
// Cancel; tea.Quit only fires when JobCompletedMsg arrives. No
// detach modal in rev 2.
func TestQuit_DuringRunDrainsBeforeExit(t *testing.T) {
	f := store.NewFake()
	runner := newStubRunner()
	runner.SetStatus("running")
	secs := []sections.Section{health.New(context.Background(), f)}
	registry := []jobs.Spec{
		{
			Key: 'R', Name: "reindex", Args: []string{"reindex"},
			Confirm:   jobs.ConfirmYesNo,
			Preflight: func(_ context.Context, _ store.Store) (int, string, error) { return 0, "", nil },
			Headline:  func(int, string) string { return "" },
		},
	}
	target := jobs.RunnerTarget{BinaryPath: "/bin/projectlens"}
	var m tea.Model = app.New(context.Background(), secs).WithJobs(f, runner, registry, target)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// q while running: must NOT produce a quit cmd; must request
	// Cancel on the runner.
	mNext, cmd := m.Update(keyPress("q"))
	if cmd != nil {
		// The cmd may carry batched values; check none equal tea.QuitMsg.
		// (tea.Quit returns a tea.QuitMsg when invoked.)
		if msg := cmd(); msg == tea.QuitMsg(struct{}{}) {
			t.Fatal("first q should not quit while job runs")
		}
	}
	if !runner.cancelled {
		t.Fatal("first q must trigger runner.Cancel()")
	}

	// Simulate completion: status flips to cancelled and
	// JobCompletedMsg arrives.
	runner.SetStatus("cancelled")
	mNext, cmd = mNext.Update(jobs.JobCompletedMsg{
		Spec:     registry[0],
		Status:   "cancelled",
		ExitCode: -1,
	})
	if cmd == nil {
		t.Fatal("post-drain JobCompletedMsg should produce tea.Quit cmd")
	}
	_ = mNext
}
