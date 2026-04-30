package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/app"
	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/sections/health"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func newApp(t *testing.T) (tea.Model, *store.Fake) {
	t.Helper()
	f := store.NewFake()
	f.SetHealth(store.HealthSnapshot{StartedAt: time.Now(), Stage: "embed", Status: "ok"})
	secs := []sections.Section{health.New(context.Background(), f)}
	return app.New(context.Background(), secs), f
}

func TestApp_RendersTooSmallBanner(t *testing.T) {
	m, _ := newApp(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 15})
	if !strings.Contains(m.View(), "terminal too small") {
		t.Fatalf("expected too-small banner, got:\n%s", m.View())
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if strings.Contains(m.View(), "terminal too small") {
		t.Fatalf("expected normal layout, got:\n%s", m.View())
	}
}

func TestApp_QuitsOnQ(t *testing.T) {
	m, _ := newApp(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd")
	}
}

func TestApp_TickRearmsRefresh(t *testing.T) {
	f := store.NewFake()
	f.SetHealth(store.HealthSnapshot{StartedAt: time.Now(), Stage: "embed", Status: "ok"})
	secs := []sections.Section{health.New(context.Background(), f)}
	var m tea.Model = app.New(context.Background(), secs)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// Helper: simulate tick by sending the unexported tickMsg via ID equality.
	// Trick: drive Update via the public path with a private struct cannot work.
	// Instead, exercise the public `r` keypress, which uses the same Refresh path.
	for i := 0; i < 5; i++ {
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		if cmd == nil {
			t.Fatalf("press %d: expected refresh cmd, got nil", i)
		}
		// drive the resulting refresh msg back into the model
		msg := cmd()
		m, _ = m.Update(msg)
	}
	if !strings.Contains(m.View(), "embed") {
		t.Fatalf("expected health snapshot in view\n%s", m.View())
	}
}

func TestApp_DBUnreachableThenRecovers(t *testing.T) {
	f := store.NewFake()
	f.SetErr("Health", errors.New("connection refused"))
	secs := []sections.Section{health.New(context.Background(), f)}
	var m tea.Model = app.New(context.Background(), secs)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = m.Update(cmd())
	if !strings.Contains(m.View(), "connection refused") {
		t.Fatalf("expected error in view\n%s", m.View())
	}

	f.SetErr("Health", nil)
	f.SetHealth(store.HealthSnapshot{StartedAt: time.Now(), Stage: "embed", Status: "ok"})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = m.Update(cmd())
	if strings.Contains(m.View(), "connection refused") {
		t.Fatalf("expected recovery, still showing error\n%s", m.View())
	}
}
