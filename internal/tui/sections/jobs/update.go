package jobs

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hman-pro/projectlens/internal/tui/sections"
)

func (m *Model) Update(msg tea.Msg) (sections.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case LiveStateMsg:
		live := msg
		m.live = &live
		m.applyLayout()
		return m, nil
	case LiveClearMsg:
		m.live = nil
		m.applyLayout()
		return m, nil
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
	// Reserve one column so wrapped lines never collide with the panel
	// border when bubbletea adds its own padding.
	m.preview.Width = max(20, m.w-1)
	if m.live != nil {
		// Live mode: the running/just-completed job owns the pane.
		// Reserve 2 lines for the live header.
		m.preview.Height = max(3, m.h-2)
		m.tbl.SetHeight(0)
		m.refreshPreviewContent()
		return
	}
	tableH := max(3, (m.h-2)*6/10)
	previewH := max(2, m.h-2-tableH-1)
	m.tbl.SetHeight(tableH)
	m.preview.Height = previewH
	m.refreshPreviewContent()
}

func (m *Model) syncPreview() {
	idx := m.tbl.Cursor()
	if idx < 0 || idx >= len(m.runs) {
		m.currentLog = ""
		m.cachedTail = nil
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
		m.cachedTail = []string{fmt.Sprintf("error reading log: %v", err)}
		m.refreshPreviewContent()
		return
	}
	m.cachedTail = tail
	m.refreshPreviewContent()
	m.preview.GotoTop()
}

// refreshPreviewContent re-applies wrapping to either the live tail
// (when a job is running) or the cached selected-row tail. Called on
// new selection, live update, and resize.
func (m *Model) refreshPreviewContent() {
	src := m.cachedTail
	gotoBottom := false
	if m.live != nil {
		src = m.live.Tail
		gotoBottom = true
	}
	if len(src) == 0 {
		m.preview.SetContent("")
		return
	}
	w := max(10, m.preview.Width)
	style := lipgloss.NewStyle().Width(w)
	cleaned := make([]string, 0, len(src))
	for _, ln := range src {
		cleaned = append(cleaned, style.Render(stripStreamPrefix(ln)))
	}
	m.preview.SetContent(strings.Join(cleaned, "\n"))
	if gotoBottom {
		m.preview.GotoBottom()
	}
}

// stripStreamPrefix removes the leading "stdout\t" / "stderr\t" tag
// the runner writes, so the preview shows the raw command output.
func stripStreamPrefix(s string) string {
	if rest, ok := strings.CutPrefix(s, "stdout\t"); ok {
		return rest
	}
	if rest, ok := strings.CutPrefix(s, "stderr\t"); ok {
		return rest
	}
	return s
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
