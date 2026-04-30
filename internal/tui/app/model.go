package app

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
)

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
