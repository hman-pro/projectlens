package app

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/components/confirmmodal"
	"github.com/hman-pro/projectlens/internal/tui/components/errormodal"
	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

// Runner is the interface the app needs from a job runner. *jobs.Runner
// satisfies this; tests can stub.
type Runner interface {
	Start(spec jobs.Spec) error
	Cancel()
	State() jobs.Snapshot
}

type Mode int

const (
	ModeSidebar Mode = iota
	ModeDetail
)

type Model struct {
	ctx  context.Context
	keys keyMap
	help help.Model

	sections []sections.Section
	focused  int
	mode     Mode

	w, h    int
	sidebar list.Model

	tooSmall bool
	showHelp bool

	// Phase 2 (optional — nil when not wired):
	store         store.Store
	runner        Runner
	registry      []jobs.Spec
	target        jobs.RunnerTarget
	confirm       *confirmmodal.Model
	errorModal    *errormodal.Model
	pendingToken  uint64
	pendingSpec   jobs.Spec
	quitRequested bool
	toastMsg      string
}

const minW, minH = 80, 20

type sectionItem struct{ title string }

func (s sectionItem) Title() string       { return s.title }
func (s sectionItem) Description() string { return "" }
func (s sectionItem) FilterValue() string { return s.title }

// New constructs the root app model. sections must have at least one element.
func New(ctx context.Context, secs []sections.Section) Model {
	items := make([]list.Item, len(secs))
	for i, s := range secs {
		items[i] = sectionItem{title: s.Title()}
	}
	d := list.NewDefaultDelegate()
	d.ShowDescription = false
	sb := list.New(items, d, 24, 10)
	sb.Title = "Sections"
	sb.SetShowHelp(false)
	sb.SetShowStatusBar(false)
	sb.SetShowPagination(false)
	sb.SetFilteringEnabled(false)
	return Model{
		ctx:      ctx,
		keys:     defaultKeys(),
		help:     help.New(),
		sections: secs,
		sidebar:  sb,
	}
}

func (m Model) Init() tea.Cmd {
	if len(m.sections) == 0 {
		return tea.Quit
	}
	return tea.Batch(m.sections[m.focused].Init(), m.sections[m.focused].Refresh(), tickCmd())
}

// helpers
func (m Model) sidebarWidth() int {
	w := m.w / 4
	if w > 24 {
		w = 24
	}
	if w < 18 {
		w = 18
	}
	return w
}

func (m Model) detailSize() (w, h int) {
	return m.w - m.sidebarWidth() - 2, m.h - 4
}

func (m Model) since() time.Duration {
	if t := m.sections[m.focused].LastRefresh(); !t.IsZero() {
		return time.Since(t)
	}
	return 0
}

// WithJobs returns a copy of m wired with a store, runner, registry,
// and runner target. Phase 2 action keys (R/F/E/S/H/D/A/c/J) become
// active. Without WithJobs, the app behaves exactly like Phase 1.
func (m Model) WithJobs(st store.Store, runner Runner, registry []jobs.Spec, target jobs.RunnerTarget) Model {
	m.store = st
	m.runner = runner
	m.registry = registry
	m.target = target
	return m
}

// RunnerStatus is a small accessor used by tests.
func (m Model) RunnerStatus() string {
	if m.runner == nil {
		return ""
	}
	return m.runner.State().Status
}

// HasConfirmModal reports whether a confirm modal is open. Used by
// tests.
func (m Model) HasConfirmModal() bool { return m.confirm != nil }

// HasErrorModal reports whether the blocking error modal is open.
func (m Model) HasErrorModal() bool { return m.errorModal != nil }

// PendingToken exposes the pendingToken counter for tests asserting
// stale-preflight behaviour.
func (m Model) PendingToken() uint64 { return m.pendingToken }
