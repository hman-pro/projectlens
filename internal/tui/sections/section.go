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

// ActionableSection is the optional sibling interface for sections
// that can trigger actions. The app reads Actions() to render hotkey
// hints; the global jobs registry is the source of truth for actual
// dispatch.
type ActionableSection interface {
	Section
	Actions() []ActionDescriptor
}

// ActionDescriptor is the section-facing summary of a triggerable
// action. Intentionally avoids importing the jobs package (which
// depends on store) so sections stay decoupled.
type ActionDescriptor struct {
	Key         rune
	Label       string
	Description string
}
