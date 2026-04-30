package theme

import "github.com/charmbracelet/lipgloss"

var (
	ColorBorder = lipgloss.AdaptiveColor{Light: "#cccccc", Dark: "#444444"}
	ColorTitle  = lipgloss.AdaptiveColor{Light: "#222222", Dark: "#dddddd"}
	ColorMuted  = lipgloss.AdaptiveColor{Light: "#666666", Dark: "#888888"}
	ColorAccent = lipgloss.AdaptiveColor{Light: "#0066cc", Dark: "#5fafff"}
	ColorOK     = lipgloss.AdaptiveColor{Light: "#007700", Dark: "#5fdd5f"}
	ColorWarn   = lipgloss.AdaptiveColor{Light: "#aa6600", Dark: "#ffaa33"}
	ColorError  = lipgloss.AdaptiveColor{Light: "#aa0000", Dark: "#ff5f5f"}

	Border = lipgloss.RoundedBorder()
)

func TitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ColorTitle).Bold(true)
}

func MutedStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ColorMuted)
}

func StatusStyle(status string) lipgloss.Style {
	c := ColorMuted
	switch status {
	case "ok", "completed":
		c = ColorOK
	case "running":
		c = ColorAccent
	case "failed", "error":
		c = ColorError
	}
	return lipgloss.NewStyle().Foreground(c)
}
