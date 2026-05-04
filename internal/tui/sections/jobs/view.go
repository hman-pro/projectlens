package jobs

import (
	"fmt"
	"strings"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/theme"
)

func (m *Model) View() string {
	if m.status == sections.StatusError {
		return theme.StatusStyle("error").Render("error: ") + m.err.Error() + "\n\npress r to retry"
	}
	if m.status == sections.StatusIdle {
		return theme.MutedStyle().Render("(loading…)")
	}
	if len(m.runs) == 0 {
		return theme.MutedStyle().Render("no jobs yet — trigger one with R/E/S/H/D/A")
	}
	var b strings.Builder
	b.WriteString(m.tbl.View())
	b.WriteString("\n")
	b.WriteString(theme.TitleStyle().Render(previewHeader(m)))
	b.WriteString("\n")
	b.WriteString(theme.MutedStyle().Render(m.preview.View()))
	return b.String()
}

func previewHeader(m *Model) string {
	idx := m.tbl.Cursor()
	if idx < 0 || idx >= len(m.runs) {
		return "─ Log preview ─"
	}
	r := m.runs[idx]
	// Show only the filename — full paths overflow the panel.
	name := r.LogPath
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return fmt.Sprintf("─ Log preview · %s ─", name)
}
