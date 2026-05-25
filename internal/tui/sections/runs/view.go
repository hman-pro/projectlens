package runs

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/sections"
	"github.com/hman-pro/projectlens/internal/tui/store"
	"github.com/hman-pro/projectlens/internal/tui/theme"
)

func (m *Model) View() string {
	if m.status == sections.StatusError {
		return theme.StatusStyle("error").Render("error: ") + m.err.Error() + "\n\npress r to retry"
	}
	if m.status == sections.StatusIdle {
		return theme.MutedStyle().Render("(loading…)")
	}
	if len(m.snap.Runs) == 0 {
		return theme.MutedStyle().Render("no runs yet — run \"projectlens bootstrap\"")
	}

	var b strings.Builder
	b.WriteString(m.tbl.View())
	if m.focused {
		idx := m.tbl.Cursor()
		if idx >= 0 && idx < len(m.snap.Runs) {
			b.WriteString("\n")
			b.WriteString(m.detailPanel(m.snap.Runs[idx]))
		}
	}
	return b.String()
}

func (m *Model) detailPanel(r store.IndexRun) string {
	var b strings.Builder
	b.WriteString(theme.TitleStyle().Render("─ Run detail ─\n"))
	fmt.Fprintf(&b, "ID:        %d\n", r.ID)
	fmt.Fprintf(&b, "Started:   %s\n", r.StartedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	if r.CompletedAt != nil {
		fmt.Fprintf(&b, "Completed: %s\n", r.CompletedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
		fmt.Fprintf(&b, "Duration:  %s\n", r.Duration().Round(time.Second))
	} else {
		fmt.Fprintf(&b, "Completed: —\n")
	}
	commit := r.CommitSHA
	commit = commit[:min(len(commit), 7)]
	fmt.Fprintf(&b, "Commit:    %s   Stage: %s   Status: %s\n", commit, r.Stage, r.Status)
	fmt.Fprintf(&b, "Files: %d   Symbols: %d   Edges: %d\n", r.FilesProcessed, r.SymbolsExtracted, r.EdgesCreated)

	// Providers line
	if r.ProviderEmbed != "" || r.ProviderSummarize != "" {
		var parts []string
		if r.ProviderEmbed != "" {
			parts = append(parts, "embed="+r.ProviderEmbed)
		}
		if r.ProviderSummarize != "" {
			parts = append(parts, "sum="+r.ProviderSummarize)
		}
		fmt.Fprintf(&b, "Providers: %s\n", strings.Join(parts, " "))
	}

	// Metrics line
	if len(r.Metrics) > 0 {
		keys := make([]string, 0, len(r.Metrics))
		for k := range r.Metrics {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var kvs []string
		for _, k := range keys {
			kvs = append(kvs, fmt.Sprintf("%s=%v", k, r.Metrics[k]))
		}
		fmt.Fprintf(&b, "Metrics:   %s\n", strings.Join(kvs, " "))
	}

	// Error line
	if r.ErrorText != "" {
		errLine := r.ErrorText
		maxWidth := m.w - len("Error:    ")
		if maxWidth > 10 && len(errLine) > maxWidth {
			errLine = errLine[:maxWidth] + "…"
		}
		b.WriteString(theme.StatusStyle("error").Render("Error:    "+errLine) + "\n")
	}

	return b.String()
}
