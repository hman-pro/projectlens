package pipeline

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

type Model struct {
	store  store.Store
	appCtx context.Context

	snap    store.PipelineSnapshot
	err     error
	status  sections.Status
	gen     uint64
	last    time.Time
	w, h    int
	focused bool
	tbl     table.Model
}

func New(appCtx context.Context, s store.Store) *Model {
	cols := []table.Column{
		{Title: "Stage", Width: 14},
		{Title: "Started", Width: 22},
		{Title: "Status", Width: 10},
		{Title: "Files", Width: 8},
		{Title: "Duration", Width: 10},
	}
	tbl := table.New(table.WithColumns(cols), table.WithFocused(false))
	return &Model{store: s, appCtx: appCtx, tbl: tbl, status: sections.StatusIdle}
}

func (m *Model) ID() string              { return ID }
func (m *Model) Title() string           { return "Pipeline" }
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
		snap, err := m.store.Pipeline(ctx)
		return RefreshedMsg{Snap: snap, Err: err, Gen: gen}
	}
}
