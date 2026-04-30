package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const tickInterval = 30 * time.Second

type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg { return tickMsg{} })
}
