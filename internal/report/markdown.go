package report

import (
	"fmt"
	"io"
	"sort"
	"strings"
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

	b.WriteString("## Index Freshness\n\n")
	b.WriteString("| Stage | Status | Completed | Age (min) | Files |\n")
	b.WriteString("|-------|--------|-----------|-----------|-------|\n")
	for _, s := range []string{"code", "summarize", "embed", "history", "datastore"} {
		st, ok := r.Stages[s]
		if !ok {
			fmt.Fprintf(&b, "| %s | (none) | | | |\n", s)
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %.0f | %d |\n",
			st.Stage, st.Status, st.CompletedAt, st.AgeMinutes, st.FilesProcessed)
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
