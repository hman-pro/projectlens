package app

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
)

const focusRefreshThreshold = 2 * time.Second

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.tooSmall = m.w < minW || m.h < minH
		if !m.tooSmall {
			m.sidebar.SetSize(m.sidebarWidth(), m.h-4)
			dw, dh := m.detailSize()
			id := m.sections[m.focused].ID()
			next, cmd := m.sections[m.focused].Update(sections.SizeMsg{SectionID: id, W: dw, H: dh})
			m.sections[m.focused] = next
			return m, cmd
		}
		return m, nil

	case tickMsg:
		cmd := m.sections[m.focused].Refresh()
		return m, tea.Batch(cmd, tickCmd())

	case tea.KeyMsg:
		if m.tooSmall {
			if key.Matches(msg, m.keys.Quit) {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.mode == ModeSidebar {
			return m.handleSidebarKey(msg)
		}
		return m.handleDetailKey(msg)
	}

	// Route every other message through every section so typed RefreshedMsg
	// reaches its target. Sections ignore messages that aren't their own.
	var cmds []tea.Cmd
	for i, s := range m.sections {
		next, cmd := s.Update(msg)
		m.sections[i] = next
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleSidebarKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Down),
		key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.ShiftTab):
		var cmd tea.Cmd
		switch {
		case key.Matches(msg, m.keys.Tab):
			m.sidebar.CursorDown()
		case key.Matches(msg, m.keys.ShiftTab):
			m.sidebar.CursorUp()
		default:
			m.sidebar, cmd = m.sidebar.Update(msg)
		}
		newIdx := m.sidebar.Index()
		var refresh tea.Cmd
		if newIdx != m.focused {
			m.focused = newIdx
			sec := m.sections[m.focused]
			if sec.LastRefresh().IsZero() || m.since() > focusRefreshThreshold {
				refresh = sec.Refresh()
			}
			dw, dh := m.detailSize()
			id := sec.ID()
			next, sizeCmd := sec.Update(sections.SizeMsg{SectionID: id, W: dw, H: dh})
			m.sections[m.focused] = next
			return m, tea.Batch(cmd, sizeCmd, refresh)
		}
		return m, cmd
	case key.Matches(msg, m.keys.Refresh):
		return m, m.sections[m.focused].Refresh()
	case key.Matches(msg, m.keys.Enter):
		m.mode = ModeDetail
		id := m.sections[m.focused].ID()
		next, cmd := m.sections[m.focused].Update(sections.FocusMsg{SectionID: id, Focused: true})
		m.sections[m.focused] = next
		return m, cmd
	}
	return m, nil
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Esc):
		m.mode = ModeSidebar
		id := m.sections[m.focused].ID()
		next, cmd := m.sections[m.focused].Update(sections.FocusMsg{SectionID: id, Focused: false})
		m.sections[m.focused] = next
		return m, cmd
	case key.Matches(msg, m.keys.Refresh):
		return m, m.sections[m.focused].Refresh()
	}
	next, cmd := m.sections[m.focused].Update(msg)
	m.sections[m.focused] = next
	return m, cmd
}
