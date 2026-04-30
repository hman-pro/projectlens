package app_test

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/app"
	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/sections/health"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func newApp(t *testing.T) (tea.Model, *store.Fake) {
	t.Helper()
	f := store.NewFake()
	f.SetHealth(store.HealthSnapshot{Stage: "embed", Status: "ok"})
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
