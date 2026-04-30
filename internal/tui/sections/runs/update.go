package runs

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
		m.tbl.SetRows(rowsFromRuns(m.snap.Runs))
		return m, nil
	case sections.SizeMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.w, m.h = msg.W, msg.H
		// Detail panel takes ~6 lines; in summary mode the table can use full height-2.
		h := msg.H - 2
		if m.focused {
			h -= 8
		}
		if h < 3 {
			h = 3
		}
		m.tbl.SetHeight(h)
		return m, nil
	case sections.FocusMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.focused = msg.Focused
		if m.focused {
			m.tbl.Focus()
			m.tbl.SetHeight(m.h - 10)
		} else {
			m.tbl.Blur()
			m.tbl.SetHeight(m.h - 2)
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

func rowsFromRuns(runs []store.IndexRun) []table.Row {
	out := make([]table.Row, 0, len(runs))
	for _, r := range runs {
		dur := "—"
		if d := r.Duration(); d > 0 {
			dur = d.Round(time.Second).String()
		}
		out = append(out, table.Row{
			fmt.Sprintf("%d", r.ID),
			r.StartedAt.UTC().Format("2006-01-02 15:04:05"),
			r.Stage, r.Status, dur,
			fmt.Sprintf("%d", r.FilesProcessed),
		})
	}
	return out
}
