package health_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/sections/health"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestHealth_AbsorbsSnapshot(t *testing.T) {
	f := store.NewFake()
	f.SetHealth(store.HealthSnapshot{
		StartedAt:        time.Date(2026, 4, 29, 9, 14, 2, 0, time.UTC),
		CommitSHA:        "ffdfc82deadbeef",
		Stage:            "embed",
		Status:           "ok",
		FilesProcessed:   4150,
		SymbolsExtracted: 28432,
		EdgesCreated:     91000,
		HeadCommit:       "a1b2c3def",
	})
	m := health.New(context.Background(), f)
	cmd := m.Refresh()
	msg := cmd().(health.RefreshedMsg)
	next, _ := m.Update(msg)
	if next.Status() != sections.StatusOK {
		t.Fatalf("status = %v, want StatusOK", next.Status())
	}
	view := next.View()
	for _, want := range []string{"ffdfc82", "embed", "ok", "4150", "28432", "91000", "a1b2c3d"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\nview:\n%s", want, view)
		}
	}
}

func TestHealth_DropsStaleGeneration(t *testing.T) {
	f := store.NewFake()
	f.SetHealth(store.HealthSnapshot{Stage: "first"})
	m := health.New(context.Background(), f)

	gen1 := m.Refresh()() // gen=1
	gen2 := m.Refresh()() // gen=2

	// Deliver newer first, older second.
	_, _ = m.Update(gen2)
	if !strings.Contains(m.View(), "first") {
		t.Fatalf("expected gen2 snap absorbed")
	}
	older, _ := m.Update(gen1)
	if !strings.Contains(older.View(), "first") {
		t.Fatalf("older message should not overwrite newer state")
	}
}

func TestHealth_SurfacesError(t *testing.T) {
	f := store.NewFake()
	f.SetErr("Health", errors.New("boom"))
	m := health.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	if next.Status() != sections.StatusError {
		t.Fatalf("status = %v, want StatusError", next.Status())
	}
	if !strings.Contains(next.View(), "boom") {
		t.Fatalf("view should mention error\nview:\n%s", next.View())
	}
}
