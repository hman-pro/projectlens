package health

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

type Model struct {
	store  store.Store
	appCtx context.Context

	snap     store.HealthSnapshot
	err      error
	status   sections.Status
	gen      uint64 // last issued generation
	lastSeen uint64 // generation of last absorbed result
	last     time.Time
	w, h     int
	focused  bool
}

func New(appCtx context.Context, s store.Store) *Model {
	return &Model{store: s, appCtx: appCtx, status: sections.StatusIdle}
}

func (m *Model) ID() string              { return ID }
func (m *Model) Title() string           { return "Index health" }
func (m *Model) Init() tea.Cmd           { return nil }
func (m *Model) Status() sections.Status { return m.status }
func (m *Model) LastRefresh() time.Time  { return m.last }

// Refresh issues a Health() query and returns a tea.Cmd that resolves to a RefreshedMsg.
func (m *Model) Refresh() tea.Cmd {
	m.gen++
	gen := m.gen
	m.status = sections.StatusLoading
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.appCtx, 5*time.Second)
		defer cancel()
		snap, err := m.store.Health(ctx)
		return RefreshedMsg{Snap: snap, Err: err, Gen: gen}
	}
}
