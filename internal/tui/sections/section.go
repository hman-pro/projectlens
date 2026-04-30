package sections

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Status reflects the freshness/health of a section's last refresh.
type Status int

const (
	StatusIdle Status = iota
	StatusLoading
	StatusOK
	StatusError
)

func (s Status) String() string {
	switch s {
	case StatusLoading:
		return "loading"
	case StatusOK:
		return "ok"
	case StatusError:
		return "error"
	default:
		return "idle"
	}
}

// SizeMsg is dispatched by the app to a focused section to declare its
// available rendering area (inside the panel border).
type SizeMsg struct {
	SectionID string
	W, H      int
}

// FocusMsg toggles a section's focused/summary rendering mode.
type FocusMsg struct {
	SectionID string
	Focused   bool
}

// Section is the interface every dashboard panel implements.
//
// Update returns a Section (not tea.Model) so the app router can store the
// result back without runtime type assertions.
type Section interface {
	ID() string
	Title() string
	Init() tea.Cmd
	Update(msg tea.Msg) (Section, tea.Cmd)
	View() string
	Refresh() tea.Cmd
	Status() Status
	LastRefresh() time.Time
}
