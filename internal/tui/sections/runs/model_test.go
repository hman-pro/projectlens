package runs_test

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/sections/runs"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestRuns_TableAndDetail(t *testing.T) {
	f := store.NewFake()
	completed := time.Date(2026, 4, 29, 9, 28, 24, 0, time.UTC)
	f.SetRuns(store.RunsSnapshot{Runs: []store.IndexRun{
		{ID: 7, StartedAt: completed.Add(-15 * time.Minute), CompletedAt: &completed, CommitSHA: "abcdef0123", Stage: "embed", Status: "ok", FilesProcessed: 4150, SymbolsExtracted: 28432, EdgesCreated: 91000},
	}})
	m := runs.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	if !strings.Contains(next.View(), "abcdef0") {
		t.Fatalf("commit absent\n%s", next.View())
	}

	// Focus → detail panel renders.
	next, _ = next.Update(sections.SizeMsg{SectionID: runs.ID, W: 100, H: 30})
	next, _ = next.Update(sections.FocusMsg{SectionID: runs.ID, Focused: true})
	next, _ = next.Update(tea.WindowSizeMsg{}) // no-op; ensures interface ok

	v := next.View()
	for _, want := range []string{"Run detail", "Files: 4150", "Symbols: 28432", "Edges: 91000"} {
		if !strings.Contains(v, want) {
			t.Errorf("focused view missing %q\n%s", want, v)
		}
	}
}
