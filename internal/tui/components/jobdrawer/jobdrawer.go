// Package jobdrawer renders the bottom-of-screen strip that streams
// the currently-running job's tail and surfaces completion status.
package jobdrawer

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// State is the snapshot the drawer renders.
type State struct {
	Status   string // idle | running | cancelling | succeeded | failed | cancelled
	Spec     string
	Started  time.Time
	Duration time.Duration
	Tail     []string
	LogPath  string
}

// Model is the drawer component.
type Model struct {
	state  State
	hidden bool
	w, h   int
}

func New() *Model { return &Model{} }

func (m *Model) SetState(s State, w, h int) {
	m.state = s
	m.w = w
	m.h = h
}

func (m *Model) Toggle() { m.hidden = !m.hidden }

func (m *Model) View() string {
	if m.state.Status == "" || m.state.Status == "idle" {
		return ""
	}
	if m.hidden {
		return lipgloss.NewStyle().Faint(true).Render(
			fmt.Sprintf("[%s %s · drawer hidden — j to show]",
				m.state.Spec, elapsed(m.state)))
	}
	header := fmt.Sprintf("%s · %s · %s", m.state.Spec, elapsed(m.state), headerStatus(m.state))
	if m.state.LogPath != "" && m.state.Status != "running" && m.state.Status != "cancelling" {
		header += " · log: " + m.state.LogPath
	}
	tail := strings.Join(m.state.Tail, "\n")
	body := header + "\n" + tail
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)
	if m.w > 2 {
		style = style.Width(m.w - 2)
	}
	if m.h > 0 {
		style = style.Height(m.h)
	}
	return style.Render(body)
}

func elapsed(s State) string {
	if s.Status == "running" || s.Status == "cancelling" {
		return time.Since(s.Started).Round(time.Second).String()
	}
	return s.Duration.Round(100 * time.Millisecond).String()
}

func headerStatus(s State) string {
	switch s.Status {
	case "succeeded":
		return "ok"
	case "failed":
		return "FAILED"
	case "cancelled":
		return "cancelled"
	case "cancelling":
		return "cancelling…"
	default:
		return s.Status
	}
}
