package jobs

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
)

const previewLines = 200

type Model struct {
	appCtx     context.Context
	dirOverride string

	runs    []JobRun
	err     error
	status  sections.Status
	gen     uint64
	last    time.Time
	w, h    int
	focused bool

	tbl        table.Model
	preview    viewport.Model
	currentLog string
	cachedTail []string

	live *LiveStateMsg
}

func New(appCtx context.Context) *Model {
	cols := []table.Column{
		{Title: "Started", Width: 20},
		{Title: "Action", Width: 18},
		{Title: "Status", Width: 10},
		{Title: "Duration", Width: 10},
	}
	return &Model{
		appCtx:  appCtx,
		status:  sections.StatusIdle,
		tbl:     table.New(table.WithColumns(cols)),
		preview: viewport.New(0, 0),
	}
}

// WithDir wires a fixed directory; production code uses ResolveDir at
// refresh time so env var changes are honoured live.
func (m *Model) WithDir(dir string) *Model { m.dirOverride = dir; return m }

func (m *Model) ID() string              { return ID }
func (m *Model) Title() string           { return "Jobs" }
func (m *Model) Init() tea.Cmd           { return nil }
func (m *Model) Status() sections.Status { return m.status }
func (m *Model) LastRefresh() time.Time  { return m.last }

func (m *Model) Refresh() tea.Cmd {
	m.gen++
	gen := m.gen
	m.status = sections.StatusLoading
	dir := m.dirOverride
	if dir == "" {
		dir = ResolveDir()
	}
	return func() tea.Msg {
		runs, err := ListRuns(dir)
		return RefreshedMsg{Runs: runs, Err: err, Gen: gen}
	}
}
