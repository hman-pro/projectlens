package report

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

// MarkdownRenderer writes the Report as Markdown intended for direct
// reading or commit alongside the target repo.
type MarkdownRenderer struct{}

func (MarkdownRenderer) Render(w io.Writer, r *Report) error {
	var b strings.Builder
	b.WriteString("# ProjectLens Report\n\n")
	if !r.GeneratedAt.IsZero() {
		fmt.Fprintf(&b, "**Generated:** %s\n", r.GeneratedAt.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if r.RepoPath != "" {
		fmt.Fprintf(&b, "**Repo:** %s\n", r.RepoPath)
	}
	if r.Git.Head != "" {
		dirty := ""
		if r.Git.Dirty {
			dirty = " (dirty)"
		}
		fmt.Fprintf(&b, "**Git HEAD:** %s%s\n", r.Git.Head, dirty)
	}
	fmt.Fprintf(&b, "**Writer active:** %s\n\n", yesNo(r.WriterActive))

	b.WriteString("## Stages\n\n")
	b.WriteString("| Stage | Status | Last Run (UTC) | Age | Provider | Metrics | Error |\n")
	b.WriteString("|-------|--------|----------------|-----|----------|---------|-------|\n")
	// Render known stages first in canonical order, then any extras alphabetically.
	knownOrder := []string{"code", "embed", "summarize", "history", "datastore"}
	seen := make(map[string]bool)
	var extraStages []string
	for _, s := range knownOrder {
		seen[s] = true
	}
	for s := range r.Stages {
		if !seen[s] {
			extraStages = append(extraStages, s)
		}
	}
	sort.Strings(extraStages)
	allStages := append(knownOrder, extraStages...)
	for _, s := range allStages {
		st, ok := r.Stages[s]
		if !ok {
			fmt.Fprintf(&b, "| %s | (none) | | | | | |\n", s)
			continue
		}
		lastRun := ""
		if st.CompletedAt != "" {
			if t, err := time.Parse(time.RFC3339, st.CompletedAt); err == nil {
				lastRun = t.UTC().Format("2006-01-02 15:04 MST")
			} else {
				lastRun = st.CompletedAt
			}
		}
		age := formatAge(st.AgeMinutes)
		provider := formatStageProviders(st.Providers)
		metrics := formatMetrics(st.Metrics)
		errCell := formatError(st.Error)
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			s, st.Status, lastRun, age, provider, metrics, errCell)
	}
	b.WriteString("\n")

	b.WriteString("## Providers\n\n")
	b.WriteString("| Role | Provider | State |\n|------|----------|-------|\n")
	for _, p := range r.Providers {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", p.Role, p.Provider, p.State)
	}
	b.WriteString("\n")

	b.WriteString("## Top Packages (by symbol count)\n\n")
	b.WriteString("| Package | Symbols | Files |\n|---------|---------|-------|\n")
	for _, p := range r.TopPackages {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", p.ImportPath, p.SymbolCount, p.FileCount)
	}
	b.WriteString("\n")

	b.WriteString("## Top Datastore Tables (by edge count)\n\n")
	b.WriteString("| Table | Engine | Reads | Writes | Source Files |\n|-------|--------|-------|--------|--------------|\n")
	for _, t := range r.TopTables {
		name := t.Name
		if t.Schema != "" {
			name = t.Schema + "." + t.Name
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d |\n", name, t.Engine, t.ReadRefs, t.WriteRefs, t.SourceFileCount)
	}
	b.WriteString("\n")

	b.WriteString("## High-Coupling File Pairs (co-change)\n\n")
	b.WriteString("| File A | File B | Co-changes |\n|--------|--------|------------|\n")
	for _, c := range r.HighCoupling {
		fmt.Fprintf(&b, "| %s | %s | %d |\n", c.FileA, c.FileB, c.CoChangeCount)
	}
	b.WriteString("\n")

	b.WriteString("## Edge Trust (provenance + confidence)\n\n")
	if len(r.EdgeTrust) == 0 {
		b.WriteString("No edges indexed.\n\n")
	} else {
		b.WriteString("| Edge type | Provenance | Extracted | Inferred | Ambiguous | Unknown | Total |\n")
		b.WriteString("|-----------|------------|-----------|----------|-----------|---------|-------|\n")
		for _, s := range r.EdgeTrust {
			prov := s.Provenance
			if prov == "" {
				prov = "—"
			}
			fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %d | %d |\n",
				s.EdgeType, prov, s.Extracted, s.Inferred, s.Ambiguous, s.Unknown, s.Total)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Knowledge Inventory\n\n")
	fmt.Fprintf(&b, "- Total entries: %d\n", r.Knowledge.TotalEntries)
	cats := make([]string, 0, len(r.Knowledge.CountsByCategory))
	for c := range r.Knowledge.CountsByCategory {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	if len(cats) > 0 {
		b.WriteString("- By category: ")
		parts := make([]string, len(cats))
		for i, c := range cats {
			parts[i] = fmt.Sprintf("%s %d", c, r.Knowledge.CountsByCategory[c])
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("\n")
	}
	if len(r.Knowledge.RecentEntries) > 0 {
		b.WriteString("- Recent entries:\n")
		for _, e := range r.Knowledge.RecentEntries {
			fmt.Fprintf(&b, "  - [%s] %s (%s)\n", e.Category, e.Title, e.CreatedAt.UTC().Format("2006-01-02"))
		}
	}
	b.WriteString("\n")

	b.WriteString("## Degraded / Missing\n\n")
	if len(r.Degraded) == 0 {
		b.WriteString("None.\n\n")
	} else {
		for _, d := range r.Degraded {
			fmt.Fprintf(&b, "- `%s`: %s", d.Stage, d.Reason)
			if d.SuggestedAction != "" {
				fmt.Fprintf(&b, " — suggested: `%s`", d.SuggestedAction)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Suggested Agent Questions\n\n")
	if len(r.Suggestions) == 0 {
		b.WriteString("None.\n")
	} else {
		for _, s := range r.Suggestions {
			fmt.Fprintf(&b, "- %s → `%s`\n", s.Topic, s.Example)
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// formatAge converts AgeMinutes to a human-readable string like "12m", "2h", "3d".
// Returns an empty string when age is zero (stage not completed yet).
func formatAge(ageMinutes float64) string {
	if ageMinutes <= 0 {
		return ""
	}
	mins := int(math.Round(ageMinutes))
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	hours := mins / 60
	if hours < 48 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dd", hours/24)
}

// formatStageProviders renders the provider cell. Returns "-" when both fields
// are empty.
func formatStageProviders(p StageProviders) string {
	var parts []string
	if p.Embed != "" {
		parts = append(parts, "embed="+p.Embed)
	}
	if p.Summarize != "" {
		parts = append(parts, "sum="+p.Summarize)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

// formatMetrics renders the metrics map as sorted "key=value" pairs joined by
// spaces. Returns "-" when the map is nil or empty.
func formatMetrics(m map[string]any) string {
	if len(m) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%v", k, m[k])
	}
	return strings.Join(parts, " ")
}

// formatError truncates error text to 200 chars with an ellipsis suffix.
// Returns an empty string when errText is empty.
func formatError(errText string) string {
	if errText == "" {
		return ""
	}
	const maxLen = 200
	if len(errText) <= maxLen {
		return errText
	}
	return errText[:maxLen] + "…"
}
