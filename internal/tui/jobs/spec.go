// Package jobs runs single-slot subprocess invocations of projectlens
// from inside the TUI. Each Spec describes a triggerable action; the
// Runner executes one Spec at a time and emits Bubbletea messages
// (JobStartedMsg, JobLineMsg, JobTickMsg, JobCompletedMsg, JobBusyMsg)
// for the app to render. See
// docs/plans/2026-04-30-tui-phase2-design.md.
package jobs

import (
	"context"

	"github.com/hman-pro/projectlens/internal/tui/store"
)

// ConfirmKind controls how an action is confirmed before launch.
type ConfirmKind int

const (
	ConfirmYesNo ConfirmKind = iota
	ConfirmTyped
)

func (c ConfirmKind) String() string {
	switch c {
	case ConfirmYesNo:
		return "yesno"
	case ConfirmTyped:
		return "typed"
	}
	return "unknown"
}

// Preflight runs a fast read-only count before the confirm modal opens.
// Returns (count, costDriver, err). costDriver is a short string like
// "openai" or "anthropic" or "" (purely local).
type Preflight func(ctx context.Context, s store.Store) (int, string, error)

// HeadlineFn formats the confirm-modal headline given the preflight result.
type HeadlineFn func(count int, cost string) string

// Spec describes one triggerable action.
type Spec struct {
	Key       rune
	Name      string
	Args      []string
	Confirm   ConfirmKind
	Phrase    string
	RefreshOn []string
	Preflight Preflight
	Headline  HeadlineFn
}

// Valid reports whether the Spec is fully populated.
func (s Spec) Valid() bool {
	return s.Key != 0 && s.Name != "" && len(s.Args) > 0 &&
		s.Preflight != nil && s.Headline != nil
}

// RunnerTarget captures everything a runner must know to invoke a child
// process targeting the same database, repo, and config the TUI is
// currently rendering.
type RunnerTarget struct {
	BinaryPath   string
	ConfigPath   string
	DatabaseURL  string
	RepoPath     string
	ProjectSlug  string // when non-empty, child CLI gets --project <slug>
	ProjectsPath string // when non-empty, child CLI gets --projects <path>
}
