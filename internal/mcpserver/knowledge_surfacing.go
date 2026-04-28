package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/hman-pro/projectlens/internal/storage"
)

const surfacingLimit = 3

// surfaceKnowledgeForSymbol returns up to 3 entries anchored to symbolID
// (or its enclosing package), formatted as a short text block.
// Returns "" when nothing is anchored — callers append unconditionally.
func (s *Server) surfaceKnowledgeForSymbol(ctx context.Context, symbolID int64) string {
	entries, err := s.db.KnowledgeForSymbolWithPackage(ctx, symbolID, surfacingLimit)
	if err != nil || len(entries) == 0 {
		return ""
	}
	return formatSurfacedKnowledge(entries)
}

// surfaceKnowledgeForPackage returns up to 3 entries anchored to a package.
func (s *Server) surfaceKnowledgeForPackage(ctx context.Context, packageName string) string {
	entries, err := s.db.KnowledgeForAnchor(ctx,
		storage.AnchorRequest{Type: "package", Ref: packageName}, surfacingLimit)
	if err != nil || len(entries) == 0 {
		return ""
	}
	return formatSurfacedKnowledge(entries)
}

func formatSurfacedKnowledge(entries []storage.KnowledgeEntry) string {
	var b strings.Builder
	b.WriteString("\n## Related knowledge\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "- [%s] (#%d) **%s**\n", e.Category, e.ID, e.Title)
		// 1-line summary: first line of body, capped at 200 chars.
		firstLine := e.Body
		if i := strings.IndexByte(firstLine, '\n'); i >= 0 {
			firstLine = firstLine[:i]
		}
		if len(firstLine) > 200 {
			firstLine = firstLine[:200] + "…"
		}
		fmt.Fprintf(&b, "  %s\n", firstLine)
	}
	return b.String()
}
