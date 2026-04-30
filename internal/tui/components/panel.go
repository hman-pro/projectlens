package components

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/hman-pro/projectlens/internal/tui/theme"
)

// Panel renders a bordered, titled box of fixed inner dimensions.
// title may be empty.
func Panel(title, body string, w, h int) string {
	style := lipgloss.NewStyle().
		Border(theme.Border).
		BorderForeground(theme.ColorBorder).
		Width(w - 2).Height(h - 2)
	if title != "" {
		header := theme.TitleStyle().Render(" " + title + " ")
		body = header + "\n" + body
	}
	return style.Render(body)
}
