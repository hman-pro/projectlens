package storage

import (
	"fmt"
	"sort"
	"strings"

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
	if len(m.snap.Tables) == 0 {
		return theme.MutedStyle().Render("no tables found — schema not migrated?")
	}
	var b strings.Builder
	b.WriteString(m.tbl.View())
	b.WriteString("\n")
	b.WriteString(theme.TitleStyle().Render("Chunks "))
	b.WriteString(theme.MutedStyle().Render(fmt.Sprintf("(%d total, %d embedded)\n", m.snap.Chunks.Total, m.snap.Chunks.Embedded)))

	keys := make([]string, 0, len(m.snap.Chunks.ByType))
	for k := range m.snap.Chunks.ByType {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "  %-12s %d\n", k, m.snap.Chunks.ByType[k])
	}
	return b.String()
}
