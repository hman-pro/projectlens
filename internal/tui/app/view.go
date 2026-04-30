package app

import "github.com/charmbracelet/lipgloss"

func (m Model) View() string {
	if m.w < 1 || m.h < 1 {
		return ""
	}
	style := lipgloss.NewStyle().
		Width(m.w).Height(m.h).
		Align(lipgloss.Center, lipgloss.Center)
	return style.Render("hello — projectlens tui (press q to quit)")
}
