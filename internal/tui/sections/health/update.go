package health

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
)

func (m *Model) Update(msg tea.Msg) (sections.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshedMsg:
		if msg.Gen != m.gen {
			return m, nil // not the latest issued; drop
		}
		m.last = time.Now()
		if msg.Err != nil {
			m.err = msg.Err
			m.status = sections.StatusError
			return m, nil
		}
		m.snap = msg.Snap
		m.err = nil
		m.status = sections.StatusOK
		return m, nil
	case sections.SizeMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.w, m.h = msg.W, msg.H
		return m, nil
	case sections.FocusMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.focused = msg.Focused
		return m, nil
	}
	return m, nil
}
