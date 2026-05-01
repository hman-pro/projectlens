package pipeline

import (
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
	b.WriteString(m.vp.View())
	b.WriteString("\n")
	b.WriteString(theme.MutedStyle().Render(footerHints()))
	return b.String()
}

func footerHints() string {
	return "↑/↓ select · A index-all · R reindex · F reindex --full · c cancel · j drawer"
}
