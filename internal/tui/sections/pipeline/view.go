package pipeline

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
	var b strings.Builder
	if len(m.snap.Stages) == 0 {
		b.WriteString(theme.MutedStyle().Render("no runs yet — run \"projectlens bootstrap\""))
	} else {
		b.WriteString(m.tbl.View())
	}
	b.WriteString("\n\n")
	b.WriteString(theme.TitleStyle().Render("Controls"))
	b.WriteString("\n")
	for _, a := range m.Actions() {
		fmt.Fprintf(&b, "  %c %-18s %s\n", a.Key, a.Label, theme.MutedStyle().Render(a.Description))
	}
	return b.String()
}
