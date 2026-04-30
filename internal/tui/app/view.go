package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/hman-pro/projectlens/internal/tui/components"
	"github.com/hman-pro/projectlens/internal/tui/theme"
)

func (m Model) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	if m.tooSmall {
		return lipgloss.NewStyle().
			Width(m.w).Height(m.h).
			Align(lipgloss.Center, lipgloss.Center).
			Render(fmt.Sprintf("terminal too small (need ≥ %d×%d)", minW, minH))
	}

	header := m.renderHeader()
	footer := m.renderFooter()

	sidebar := components.Panel("", m.sidebar.View(), m.sidebarWidth(), m.h-4)
	dw, dh := m.detailSize()
	body := m.sections[m.focused].View()
	detail := components.Panel(m.sections[m.focused].Title(), body, dw+2, dh+2)

	row := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, detail)
	if m.showHelp {
		overlay := theme.MutedStyle().Render(strings.Join([]string{
			"  ↑/k     up",
			"  ↓/j     down",
			"  enter   focus detail",
			"  esc/h   back to sidebar",
			"  tab     next section",
			"  s+tab   previous section",
			"  r       refresh focused",
			"  ?       toggle help",
			"  q/^C    quit",
		}, "\n"))
		return lipgloss.JoinVertical(lipgloss.Left, header, overlay, footer)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, row, footer)
}

func (m Model) renderHeader() string {
	left := theme.TitleStyle().Render(" projectlens · dashboard ")
	right := ""
	if d := m.since(); d > 0 {
		right = theme.MutedStyle().Render(fmt.Sprintf(" refreshed %s ago ", durationShort(d)))
	}
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	return left + lipgloss.NewStyle().Width(gap).Render("") + right
}

func (m Model) renderFooter() string {
	const hint = "↑/↓ select  enter focus  esc back  r refresh  ? help  q quit"
	return theme.MutedStyle().Width(m.w).Render(" " + hint + " ")
}

func durationShort(d interface{ Seconds() float64 }) string {
	s := int(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	default:
		return fmt.Sprintf("%dh", s/3600)
	}
}
