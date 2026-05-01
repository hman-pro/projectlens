// Package confirmmodal renders a centered yes/no or typed-phrase
// confirmation modal. Used by the Phase 2 action flow to gate every
// mutating subprocess invocation.
package confirmmodal

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type kind int

const (
	kindYesNo kind = iota
	kindTyped
)

// ConfirmedMsg is dispatched when the user confirms. Token lets the
// app match the dispatch back to the originating spec.
type ConfirmedMsg struct{ Token string }

// Model is the modal state machine.
type Model struct {
	kind     kind
	headline string
	phrase   string
	token    string
	typed    string
	done     bool
	confirm  bool
}

func NewYesNo(headline, token string) Model {
	return Model{kind: kindYesNo, headline: headline, token: token}
}

func NewTyped(headline, phrase, token string) Model {
	return Model{kind: kindTyped, headline: headline, phrase: phrase, token: token}
}

func (m Model) Done() bool      { return m.done }
func (m Model) Confirmed() bool { return m.confirm }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if key.Type == tea.KeyEsc {
		m.done = true
		return m, nil
	}
	switch m.kind {
	case kindYesNo:
		switch key.String() {
		case "y", "Y":
			m.done, m.confirm = true, true
			return m, dispatch(m.token)
		default:
			m.done = true
			return m, nil
		}
	case kindTyped:
		if key.Type == tea.KeyEnter {
			if m.typed == m.phrase {
				m.done, m.confirm = true, true
				return m, dispatch(m.token)
			}
			return m, nil
		}
		if key.Type == tea.KeyBackspace {
			if len(m.typed) > 0 {
				m.typed = m.typed[:len(m.typed)-1]
			}
			return m, nil
		}
		if key.Type == tea.KeyRunes && len(key.Runes) > 0 {
			m.typed += string(key.Runes)
		}
	}
	return m, nil
}

func (m Model) View() string {
	body := m.headline
	if m.kind == kindTyped {
		body += fmt.Sprintf("\n> %s_", m.typed)
	} else {
		body += "\n[y/N]"
	}
	body += "\nesc cancel"
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Render(body)
}

func dispatch(token string) tea.Cmd {
	return func() tea.Msg { return ConfirmedMsg{Token: token} }
}
