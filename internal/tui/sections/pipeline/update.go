package pipeline

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func (m *Model) Update(msg tea.Msg) (sections.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshedMsg:
		if msg.Gen != m.gen {
			return m, nil
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
		m.tbl.SetRows(toRows(m.snap))
		return m, nil
	case sections.SizeMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.w, m.h = msg.W, msg.H
		m.tbl.SetHeight(msg.H - 2)
		return m, nil
	case sections.FocusMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.focused = msg.Focused
		if m.focused {
			m.tbl.Focus()
		} else {
			m.tbl.Blur()
		}
		return m, nil
	case tea.KeyMsg:
		if !m.focused {
			return m, nil
		}
		var cmd tea.Cmd
		m.tbl, cmd = m.tbl.Update(msg)
		return m, cmd
	}
	return m, nil
}

func toRows(snap store.PipelineSnapshot) []table.Row {
	out := make([]table.Row, 0, len(snap.Stages))
	for _, s := range snap.Stages {
		dur := "—"
		if s.Duration > 0 {
			dur = s.Duration.Round(time.Second).String()
		}
		started := "—"
		if !s.LastRunStartedAt.IsZero() {
			started = s.LastRunStartedAt.UTC().Format("2006-01-02 15:04:05")
		}
		out = append(out, table.Row{
			s.Name, started, s.Status,
			fmt.Sprintf("%d", s.FilesProcessed), dur,
		})
	}
	return out
}
