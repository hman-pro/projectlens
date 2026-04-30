package health

import (
	"fmt"
	"strings"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/theme"
)

func (m *Model) View() string {
	if m.status == sections.StatusError {
		return theme.StatusStyle("error").Render("error: ") + m.err.Error() + "\n\npress r to retry"
	}
	if m.status == sections.StatusIdle {
		return theme.MutedStyle().Render("(loading…)")
	}

	s := m.snap
	commit := shortCommit(s.CommitSHA)
	completed := "—"
	if s.CompletedAt != nil {
		completed = s.CompletedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	dur := "—"
	if d := s.Duration(); d > 0 {
		dur = d.Round(time.Second).String()
	}
	headPart := "(unknown)"
	if s.HeadCommit != "" {
		headPart = "vs HEAD " + shortCommit(s.HeadCommit)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Started:    %s\n", s.StartedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "Completed:  %s\n", completed)
	fmt.Fprintf(&b, "Commit:     %s\n", commit)
	fmt.Fprintf(&b, "Stage:      %s\n", s.Stage)
	fmt.Fprintf(&b, "Status:     %s\n", theme.StatusStyle(s.Status).Render(s.Status))
	fmt.Fprintf(&b, "Duration:   %s\n", dur)
	fmt.Fprintf(&b, "Files: %d   Symbols: %d   Edges: %d\n", s.FilesProcessed, s.SymbolsExtracted, s.EdgesCreated)
	fmt.Fprintf(&b, "Staleness:  %s %s\n", humanDuration(s.Staleness), headPart)
	return b.String()
}

func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
