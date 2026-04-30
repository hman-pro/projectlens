package storage

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
		m.tbl.SetRows(rowsFromSnap(m.snap))
		return m, nil
	case sections.SizeMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.w, m.h = msg.W, msg.H
		m.tbl.SetHeight(msg.H - 6) // reserve 4 lines for chunks footer
		return m, nil
	case sections.FocusMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.focused = msg.Focused
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

func rowsFromSnap(s store.StorageSnapshot) []table.Row {
	out := make([]table.Row, 0, len(s.Tables))
	for _, t := range s.Tables {
		out = append(out, table.Row{t.Name, fmt.Sprintf("%d", t.EstRows), humanBytes(t.Bytes)})
	}
	return out
}

func humanBytes(n int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case n < kb:
		return fmt.Sprintf("%d B", n)
	case n < mb:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kb))
	case n < gb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gb))
	}
}
