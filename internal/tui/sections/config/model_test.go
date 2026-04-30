package config_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/sections/config"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestConfig_RendersAllFields(t *testing.T) {
	f := store.NewFake()
	f.SetConfig(store.ConfigSnapshot{
		EmbeddingProvider: "ollama", EmbeddingModel: "mxbai-embed-large", EmbeddingDims: 1024, EmbeddingEndpoint: "http://localhost:11434",
		SummarizationProvider: "anthropic", SummarizationModel: "claude-sonnet-4-6",
		DBHost: "localhost:5433", DBName: "projectlens",
		MCPURL: "http://localhost:8484/mcp", MCPStatus: "up", MCPLatency: 12 * time.Millisecond,
	})
	m := config.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	v := next.View()
	for _, want := range []string{
		"ollama", "mxbai-embed-large", "1024", "anthropic", "claude-sonnet-4-6",
		"localhost:5433", "projectlens",
		"MCP server", "http://localhost:8484/mcp", "up", "12ms",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}

func TestConfig_RendersMCPDownWithError(t *testing.T) {
	f := store.NewFake()
	f.SetConfig(store.ConfigSnapshot{
		EmbeddingProvider: "ollama", EmbeddingModel: "mxbai-embed-large", EmbeddingDims: 1024,
		SummarizationProvider: "anthropic", SummarizationModel: "claude-sonnet-4-6",
		DBHost: "localhost:5433", DBName: "projectlens",
		MCPURL: "http://localhost:8484/mcp", MCPStatus: "down", MCPError: "connection refused",
	})
	m := config.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	v := next.View()
	for _, want := range []string{"down", "connection refused"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}
