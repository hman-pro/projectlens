package jobs

import (
	"fmt"
	"strings"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/theme"
)

func (m *Model) View() string {
	if m.status == sections.StatusError {
		return theme.StatusStyle("error").Render("error: ") + m.err.Error() + "\n\npress r to retry"
	}
	if m.live != nil {
		var b strings.Builder
		b.WriteString(theme.TitleStyle().Render(liveHeader(m.live)))
		b.WriteString("\n")
		b.WriteString(m.preview.View())
		return b.String()
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

// liveHeader renders a one-line status banner for the running job,
// styled by terminal status.
func liveHeader(s *LiveStateMsg) string {
	dur := s.Duration
	if dur == 0 && !s.Started.IsZero() {
		dur = time.Since(s.Started)
	}
	statusBadge := theme.StatusStyle(s.Status).Render(displayStatus(s.Status))
	header := s.Spec + " · " + statusBadge + " · " + dur.Round(time.Second).String()
	if s.Status != "running" && s.Status != "cancelling" && s.LogPath != "" {
		header += " · log: " + filenameOf(s.LogPath)
	}
	return header
}

func displayStatus(s string) string {
	switch s {
	case "running":
		return "running"
	case "cancelling":
		return "cancelling…"
	case "succeeded":
		return "ok"
	case "failed":
		return "FAILED"
	case "cancelled":
		return "cancelled"
	default:
		return s
	}
}

func filenameOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
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
