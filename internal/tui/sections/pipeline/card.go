package pipeline

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/hman-pro/projectlens/internal/tui/store"
	"github.com/hman-pro/projectlens/internal/tui/theme"
)

// stageDef declares one card on the Pipeline page. Order here is the
// rendered order. Hotkey is "" for cards driven by section-level keys
// (Code) or planned cards (Datastore, Docs).
type stageDef struct {
	ID      string
	Title   string
	Hotkey  string
	Planned bool
	Sub     func(StageStat) string
}

var stageOrder = []stageDef{
	{ID: "code", Title: "Code", Sub: subCode},
	{ID: "embed", Title: "Embed", Hotkey: "E", Sub: subEmbed},
	{ID: "summarize", Title: "Summarize", Hotkey: "S", Sub: subSummarize},
	{ID: "history", Title: "History", Hotkey: "H", Sub: subHistory},
	{ID: "datastore", Title: "Datastore", Hotkey: "D", Sub: subDatastore},
	{ID: "docs", Title: "Docs", Planned: true},
}

// StageStat is a thin alias matching the snapshot row, exposed so
// subtitle helpers don't import store directly.
type StageStat = store.StageStat

func subCode(s StageStat) string {
	if s.FilesProcessed == 0 {
		return ""
	}
	return fmt.Sprintf("%d files indexed", s.FilesProcessed)
}

func subEmbed(s StageStat) string {
	if s.FilesProcessed == 0 {
		return "missing chunks → press E to embed"
	}
	return ""
}

func subSummarize(s StageStat) string {
	if s.FilesProcessed == 0 {
		return "missing summaries → press S to summarize"
	}
	return ""
}

func subHistory(s StageStat) string {
	if s.FilesProcessed == 0 {
		return "no commits indexed yet → press H to start"
	}
	return ""
}

func subDatastore(s StageStat) string {
	if s.FilesProcessed == 0 {
		return "scan migrations + SQL → press D to index"
	}
	return ""
}

// renderCard renders one card to a string. width is the card's outer
// width (border included). A zero/negative width falls back to 60.
func renderCard(def stageDef, stat StageStat, hasData bool, selected, focused bool, width int) string {
	if width < 20 {
		width = 60
	}

	border := lipgloss.NewStyle().
		Border(theme.Border).
		BorderForeground(theme.ColorBorder).
		Padding(0, 1).
		Width(width - 2)
	if selected && focused {
		border = border.BorderForeground(theme.ColorAccent)
	}

	innerW := width - 4

	titleLeft := theme.TitleStyle().Render(def.Title)
	titleRight := ""
	switch {
	case def.Planned:
		titleRight = theme.MutedStyle().Render("planned")
	case def.Hotkey != "":
		hk := theme.MutedStyle().Render("[" + def.Hotkey + " run]")
		if selected && focused {
			hk = lipgloss.NewStyle().Foreground(theme.ColorAccent).Bold(true).Render("[" + def.Hotkey + " run]")
		}
		titleRight = hk
	}
	titleRow := padBetween(titleLeft, titleRight, innerW)

	var lines []string
	lines = append(lines, titleRow)

	if def.Planned {
		lines = append(lines, theme.MutedStyle().Render("not yet implemented"))
	} else {
		lines = append(lines, statusRow(stat, hasData))
		if def.Sub != nil {
			if sub := def.Sub(stat); sub != "" {
				lines = append(lines, theme.MutedStyle().Render(sub))
			}
		}
	}

	return border.Render(strings.Join(lines, "\n"))
}

// statusRow renders the dot + key facts line: "● ok    last: 2h ago    files: N    dur: 1m04s".
func statusRow(s StageStat, hasData bool) string {
	if !hasData {
		return theme.MutedStyle().Render("○ never run")
	}
	dot := statusDot(s.Status)
	parts := []string{
		dot + " " + theme.StatusStyle(s.Status).Render(displayStatus(s.Status)),
	}
	if !s.LastRunStartedAt.IsZero() {
		parts = append(parts, theme.MutedStyle().Render("last: "+humanAge(time.Since(s.LastRunStartedAt))))
	}
	if s.FilesProcessed > 0 {
		parts = append(parts, theme.MutedStyle().Render(fmt.Sprintf("files: %d", s.FilesProcessed)))
	}
	if s.Duration > 0 {
		parts = append(parts, theme.MutedStyle().Render("dur: "+s.Duration.Round(time.Second).String()))
	}
	return strings.Join(parts, "    ")
}

func statusDot(status string) string {
	switch status {
	case "ok", "completed":
		return lipgloss.NewStyle().Foreground(theme.ColorOK).Render("●")
	case "running":
		return lipgloss.NewStyle().Foreground(theme.ColorAccent).Render("●")
	case "failed", "error":
		return lipgloss.NewStyle().Foreground(theme.ColorError).Render("●")
	default:
		return theme.MutedStyle().Render("○")
	}
}

func displayStatus(s string) string {
	if s == "completed" {
		return "ok"
	}
	if s == "" {
		return "—"
	}
	return s
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// padBetween joins left and right with spaces so the rendered width
// reaches innerW (visible width, not byte count).
func padBetween(left, right string, innerW int) string {
	gap := max(1, innerW-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}
