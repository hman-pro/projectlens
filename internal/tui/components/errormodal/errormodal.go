// Package errormodal renders a centered, blocking error dialog.
// Used in place of toast for failures the user must acknowledge:
// preflight failures, missing binary, job-start failures, and
// completed jobs with non-zero exit. Toast remains for transient
// non-error infos.
package errormodal

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hman-pro/projectlens/internal/tui/theme"
)

type Model struct {
	title   string
	message string
	hint    string
	done    bool
}

func New(title, message string) Model {
	return Model{title: title, message: message}
}

func (m Model) WithHint(hint string) Model {
	m.hint = hint
	return m
}

func (m Model) Done() bool { return m.done }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyEsc, tea.KeyEnter:
		m.done = true
		return m, nil
	}
	switch key.String() {
	case "q", "Q":
		m.done = true
	}
	return m, nil
}

func (m Model) View() string {
	title := theme.StatusStyle("error").Bold(true).Render(m.title)
	body := title + "\n\n" + m.message
	if m.hint != "" {
		body += "\n\n" + theme.MutedStyle().Render(m.hint)
	}
	body += "\n\n" + theme.MutedStyle().Render("press esc/enter to dismiss")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorError).
		Padding(1, 2).
		Render(body)
}
