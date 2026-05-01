package pipeline

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

type Model struct {
	store  store.Store
	appCtx context.Context

	snap     store.PipelineSnapshot
	err      error
	status   sections.Status
	gen      uint64
	last     time.Time
	w, h     int
	focused  bool
	selected int
	vp       viewport.Model
}

func New(appCtx context.Context, s store.Store) *Model {
	vp := viewport.New(0, 0)
	return &Model{
		store:  s,
		appCtx: appCtx,
		status: sections.StatusIdle,
		vp:     vp,
	}
}

func (m *Model) ID() string              { return ID }
func (m *Model) Title() string           { return "Pipeline" }
func (m *Model) Init() tea.Cmd           { return nil }
func (m *Model) Status() sections.Status { return m.status }
func (m *Model) LastRefresh() time.Time  { return m.last }

// Actions are still surfaced for global hotkey registration. R/F are
// section-level (full pipeline), E/S/H map to per-stage cards.
func (m *Model) Actions() []sections.ActionDescriptor {
	return []sections.ActionDescriptor{
		{Key: 'R', Label: "reindex", Description: "incremental"},
		{Key: 'F', Label: "reindex --full", Description: "full"},
		{Key: 'E', Label: "embed", Description: "missing chunks"},
		{Key: 'S', Label: "summarize", Description: "missing packages"},
		{Key: 'H', Label: "history", Description: "new commits"},
	}
}

func (m *Model) Refresh() tea.Cmd {
	m.gen++
	gen := m.gen
	m.status = sections.StatusLoading
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.appCtx, 5*time.Second)
		defer cancel()
		snap, err := m.store.Pipeline(ctx)
		return RefreshedMsg{Snap: snap, Err: err, Gen: gen}
	}
}
