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

func keyPress(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func newActionApp(t *testing.T, target jobs.RunnerTarget, runner *stubRunner) (tea.Model, *stubRunner, *store.Fake) {
	t.Helper()
	f := store.NewFake()
	f.SetChangedFiles(11)
	f.SetEmbedPending(7)
	f.SetSummarizePending(3)
	f.SetHistoryCommits(42)
	registry := []jobs.Spec{
		{
			Key: 'R', Name: "reindex", Args: []string{"reindex"},
			Confirm: jobs.ConfirmYesNo,
			Preflight: func(ctx context.Context, s store.Store) (int, string, error) {
				n, err := s.ChangedFilesSinceLastRun(ctx)
				return n, "", err
			},
			Headline:  func(n int, _ string) string { return "reindex N? [y/N]" },
			RefreshOn: []string{"health"},
		},
		{
			Key: 'F', Name: "reindex --full", Args: []string{"reindex", "--full"},
			Confirm: jobs.ConfirmTyped, Phrase: "reindex",
			Preflight: func(_ context.Context, _ store.Store) (int, string, error) { return 1, "", nil },
			Headline:  func(int, string) string { return "FULL — type 'reindex'" },
		},
	}
	secs := []sections.Section{health.New(context.Background(), f)}
	m := app.New(context.Background(), secs).WithJobs(f, runner, registry, target)
	mm, _ := tea.Model(m).Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return mm, runner, f
}

func drainCmd(t *testing.T, m tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	next, _ := m.Update(msg)
	return next
}

func TestActionKey_OpensConfirmModalAfterPreflight(t *testing.T) {
	m, _, _ := newActionApp(t, jobs.RunnerTarget{BinaryPath: "/bin/projectlens"}, newStubRunner())
	m, cmd := m.Update(keyPress("R"))
	if m.(app.Model).HasConfirmModal() {
		t.Fatal("modal opened before preflight returned")
	}
	if cmd == nil {
		t.Fatal("expected preflight cmd")
	}
	m = drainCmd(t, m, cmd)
	if !m.(app.Model).HasConfirmModal() {
		t.Fatal("modal not opened after PreflightDoneMsg")
	}
}

func TestActionKey_BinaryMissingRefuses(t *testing.T) {
	m, runner, _ := newActionApp(t, jobs.RunnerTarget{BinaryPath: ""}, newStubRunner())
	mNext, _ := m.Update(keyPress("R"))
	if mNext.(app.Model).HasConfirmModal() {
		t.Fatal("binary-missing must not open modal")
	}
	if len(runner.Started()) != 0 {
		t.Fatal("binary-missing must not start runner")
	}
}

func TestYesNo_NCancels(t *testing.T) {
	m, runner, _ := newActionApp(t, jobs.RunnerTarget{BinaryPath: "/bin/projectlens"}, newStubRunner())
	m, cmd := m.Update(keyPress("R"))
	m = drainCmd(t, m, cmd)
	mNext, _ := m.Update(keyPress("n"))
	if mNext.(app.Model).HasConfirmModal() {
		t.Fatal("n should close modal")
	}
	if len(runner.Started()) != 0 {
		t.Fatal("n must not start runner")
	}
}

func TestYesNo_YStartsRunner(t *testing.T) {
	m, runner, _ := newActionApp(t, jobs.RunnerTarget{BinaryPath: "/bin/projectlens"}, newStubRunner())
	m, cmd := m.Update(keyPress("R"))
	m = drainCmd(t, m, cmd)
	m, cmd = m.Update(keyPress("y"))
	_ = drainCmd(t, m, cmd)
	if got := runner.Started(); len(got) != 1 || got[0].Name != "reindex" {
		t.Fatalf("expected runner.Start(reindex), got %+v", got)
	}
}

func TestTyped_RequiresExactPhrase(t *testing.T) {
	m, runner, _ := newActionApp(t, jobs.RunnerTarget{BinaryPath: "/bin/projectlens"}, newStubRunner())
	m, cmd := m.Update(keyPress("F"))
	m = drainCmd(t, m, cmd)
	for _, r := range "rein" {
		m, _ = m.Update(keyPress(string(r)))
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(runner.Started()) != 0 {
		t.Fatal("partial phrase + Enter must not start runner")
	}
	for _, r := range "dex" {
		m, _ = m.Update(keyPress(string(r)))
	}
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = drainCmd(t, m, cmd)
	if len(runner.Started()) != 1 {
		t.Fatalf("exact phrase + Enter must start runner; got %+v", runner.Started())
	}
}

func TestPreflight_StaleResultDropped(t *testing.T) {
	m, _, _ := newActionApp(t, jobs.RunnerTarget{BinaryPath: "/bin/projectlens"}, newStubRunner())
	mAfterR, cmdR := m.Update(keyPress("R"))
	if cmdR == nil {
		t.Fatal("expected preflight cmd from R")
	}
	mAfterF, cmdF := mAfterR.(app.Model).Update(keyPress("F"))
	if cmdF == nil {
		t.Fatal("expected preflight cmd from F")
	}
	// Resolve R's stale cmd first; modal must NOT open.
	mAfterStale := drainCmd(t, mAfterF, cmdR)
	if mAfterStale.(app.Model).HasConfirmModal() {
		t.Fatal("stale R preflight should not have opened a modal")
	}
	// F's resolution still opens its modal.
	mAfterF2 := drainCmd(t, mAfterStale, cmdF)
	if !mAfterF2.(app.Model).HasConfirmModal() {
		t.Fatal("F preflight result should open modal")
	}
}
