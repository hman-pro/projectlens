package config

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
	c := m.snap
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", theme.TitleStyle().Render("Embeddings"))
	fmt.Fprintf(&b, "  Provider:  %s\n", c.EmbeddingProvider)
	fmt.Fprintf(&b, "  Model:     %s\n", c.EmbeddingModel)
	fmt.Fprintf(&b, "  Dims:      %d\n", c.EmbeddingDims)
	if c.EmbeddingEndpoint != "" {
		fmt.Fprintf(&b, "  Endpoint:  %s\n", c.EmbeddingEndpoint)
	}
	fmt.Fprintf(&b, "\n%s\n", theme.TitleStyle().Render("Summarization"))
	fmt.Fprintf(&b, "  Provider:  %s\n", c.SummarizationProvider)
	fmt.Fprintf(&b, "  Model:     %s\n", c.SummarizationModel)
	fmt.Fprintf(&b, "\n%s\n", theme.TitleStyle().Render("Database"))
	fmt.Fprintf(&b, "  Host:      %s\n", c.DBHost)
	fmt.Fprintf(&b, "  Database:  %s\n", c.DBName)
	fmt.Fprintf(&b, "\n%s\n", theme.TitleStyle().Render("MCP server"))
	fmt.Fprintf(&b, "  URL:       %s\n", c.MCPURL)
	fmt.Fprintf(&b, "  Status:    %s", renderMCPStatus(c.MCPStatus))
	if c.MCPStatus == "up" && c.MCPLatency > 0 {
		fmt.Fprintf(&b, " (%s)", c.MCPLatency.Round(time.Millisecond))
	}
	b.WriteByte('\n')
	if c.MCPStatus != "up" && c.MCPError != "" {
		fmt.Fprintf(&b, "  Error:     %s\n", c.MCPError)
	}
	return b.String()
}

func renderMCPStatus(s string) string {
	switch s {
	case "up":
		return theme.StatusStyle("ok").Render("up")
	case "down":
		return theme.StatusStyle("error").Render("down")
	default:
		return theme.MutedStyle().Render("unknown")
	}
}
