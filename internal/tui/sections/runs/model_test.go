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
		{
			ID: 7, StartedAt: completed.Add(-15 * time.Minute), CompletedAt: &completed,
			CommitSHA: "abcdef0123", Stage: "embed", Status: "ok",
			FilesProcessed: 4150, SymbolsExtracted: 28432, EdgesCreated: 91000,
			ProviderEmbed:     "ollama",
			ProviderSummarize: "anthropic",
			Metrics:           map[string]any{"chunks_indexed": float64(312), "tokens_used": float64(4096)},
			ErrorText:         "",
		},
	}})
	m := runs.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	for _, want := range []string{"7", "embed", "ok", "4150"} {
		if !strings.Contains(next.View(), want) {
			t.Fatalf("table view missing %q\n%s", want, next.View())
		}
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

	// New observability fields must render in the detail panel.
	for _, want := range []string{
		"embed=ollama", "sum=anthropic",
		"chunks_indexed=312", "tokens_used=4096",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("focused view missing observability field %q\n%s", want, v)
		}
	}

	// Error text renders when present.
	f.SetRuns(store.RunsSnapshot{Runs: []store.IndexRun{
		{
			ID: 8, StartedAt: completed, CommitSHA: "deadbeef", Stage: "index", Status: "failed",
			ErrorText: "connection refused",
		},
	}})
	errModel := runs.New(context.Background(), f)
	errMsg := errModel.Refresh()()
	errNext, _ := errModel.Update(errMsg)
	errNext, _ = errNext.Update(sections.SizeMsg{SectionID: runs.ID, W: 100, H: 30})
	errNext, _ = errNext.Update(sections.FocusMsg{SectionID: runs.ID, Focused: true})
	if !strings.Contains(errNext.View(), "connection refused") {
		t.Errorf("focused view missing error text\n%s", errNext.View())
	}
}
