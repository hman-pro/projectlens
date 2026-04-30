package runs

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

type Model struct {
	store    store.Store
	appCtx   context.Context
	snap     store.RunsSnapshot
	err      error
	status   sections.Status
	gen      uint64
	lastSeen uint64
	last     time.Time
	w, h     int
	focused  bool
	tbl      table.Model
}

func New(appCtx context.Context, s store.Store) *Model {
	cols := []table.Column{
		{Title: "ID", Width: 6},
		{Title: "Started", Width: 22},
		{Title: "Stage", Width: 10},
		{Title: "Status", Width: 10},
		{Title: "Duration", Width: 10},
		{Title: "Files", Width: 8},
	}
	return &Model{
		store: s, appCtx: appCtx, status: sections.StatusIdle,
		tbl: table.New(table.WithColumns(cols)),
	}
}

func (m *Model) ID() string              { return ID }
func (m *Model) Title() string           { return "Recent runs" }
func (m *Model) Init() tea.Cmd           { return nil }
func (m *Model) Status() sections.Status { return m.status }
func (m *Model) LastRefresh() time.Time  { return m.last }

func (m *Model) Refresh() tea.Cmd {
	m.gen++
	gen := m.gen
	m.status = sections.StatusLoading
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.appCtx, 5*time.Second)
		defer cancel()
		snap, err := m.store.Runs(ctx, store.RunsMaxRows)
		return RefreshedMsg{Snap: snap, Err: err, Gen: gen}
	}
}
