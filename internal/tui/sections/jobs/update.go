package jobs

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/sections"
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
		m.runs = msg.Runs
		m.err = nil
		m.status = sections.StatusOK
		m.tbl.SetRows(rowsFromRuns(m.runs))
		m.syncPreview()
		return m, nil
	case sections.SizeMsg:
		if msg.SectionID != ID {
			return m, nil
		}
		m.w, m.h = msg.W, msg.H
		m.applyLayout()
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
		m.applyLayout()
		return m, nil
	case tea.KeyMsg:
		if !m.focused {
			return m, nil
		}
		prev := m.tbl.Cursor()
		var cmd tea.Cmd
		m.tbl, cmd = m.tbl.Update(msg)
		if m.tbl.Cursor() != prev {
			m.syncPreview()
		}
		return m, cmd
	}
	return m, nil
}

func (m *Model) applyLayout() {
	if m.h <= 0 {
		return
	}
	tableH := (m.h - 2) * 6 / 10
	if tableH < 3 {
		tableH = 3
	}
	previewH := m.h - 2 - tableH - 1
	if previewH < 2 {
		previewH = 2
	}
	m.tbl.SetHeight(tableH)
	m.preview.Width = m.w
	m.preview.Height = previewH
}

func (m *Model) syncPreview() {
	idx := m.tbl.Cursor()
	if idx < 0 || idx >= len(m.runs) {
		m.currentLog = ""
		m.preview.SetContent("")
		return
	}
	run := m.runs[idx]
	if run.LogPath == m.currentLog {
		return
	}
	m.currentLog = run.LogPath
	tail, err := ReadTail(run.LogPath, previewLines)
	if err != nil {
		m.preview.SetContent(fmt.Sprintf("error reading log: %v", err))
		return
	}
	m.preview.SetContent(strings.Join(tail, "\n"))
	m.preview.GotoTop()
}

func rowsFromRuns(runs []JobRun) []table.Row {
	out := make([]table.Row, 0, len(runs))
	for _, r := range runs {
		dur := "—"
		if r.Duration > 0 {
			dur = r.Duration.Round(time.Second).String()
		}
		out = append(out, table.Row{
			r.Started.UTC().Format("2006-01-02 15:04:05"),
			r.Action,
			r.Status,
			dur,
		})
	}
	return out
}
