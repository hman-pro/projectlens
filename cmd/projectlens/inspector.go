package main

import (
	"context"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/providers/anthropic"
	"github.com/hman-pro/projectlens/internal/providers/ollama"
	"github.com/hman-pro/projectlens/internal/providers/openai"
	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
)

// buildInspector builds an indexstate.Inspector for read-only commands
// (report, export). Mirrors cmd/projectlens-mcp/main.go's wiring so the
// CLI report and index_status see identical provider health. Do NOT use
// buildProviders — that is fail-fast and returns indexer types.
func buildInspector(cfg *config.Config, db *storage.DB, repoPath string) *indexstate.DefaultInspector {
	var embedder retrieval.QueryEmbedder
	switch cfg.Embeddings.Provider {
	case "ollama":
		embedder = ollama.NewClient(cfg.Embeddings.Endpoint, cfg.Embeddings.Model)
	case "openai":
		if cfg.OpenAIKey != "" {
			if cfg.Embeddings.Dimensions > 0 {
				embedder = openai.NewClientWithDims(cfg.OpenAIKey, cfg.Embeddings.Dimensions)
			} else {
				embedder = openai.NewClient(cfg.OpenAIKey)
			}
		}
	}
	router := retrieval.NewRouter(db, embedder)

	return &indexstate.DefaultInspector{
		Embedder:   router,
		Summarizer: newCLISummarizerProber(cfg),
		RepoPath:   repoPath,
	}
}

// summarizerProberFunc adapts the configured summarization provider to
// the indexstate.SummarizerProber interface. configured is mandatory;
// ping is optional. When ping is nil and configured returns true the
// prober reports "configured" (credentials present, no probe run) —
// suitable for providers where probing costs tokens (e.g. Anthropic).
type summarizerProberFunc struct {
	name       string
	configured func() bool
	ping       func(ctx context.Context) error
}

func (f summarizerProberFunc) ProbeSummarizer(ctx context.Context) (string, string, error) {
	if !f.configured() {
		return f.name, "not_configured", nil
	}
	if f.ping == nil {
		return f.name, "configured", nil
	}
	if err := f.ping(ctx); err != nil {
		return f.name, "error", err
	}
	return f.name, "reachable", nil
}

// newCLISummarizerProber returns an indexstate.SummarizerProber backed by
// the provider named in cfg.Summarization.Provider. Returns nil when
// no provider is configured.
func newCLISummarizerProber(cfg *config.Config) indexstate.SummarizerProber {
	switch cfg.Summarization.Provider {
	case "anthropic":
		client := anthropic.NewClient(cfg.Summarization.Model)
		return summarizerProberFunc{
			name:       "anthropic",
			configured: client.Configured,
		}
	case "openai":
		if cfg.OpenAIKey == "" {
			return summarizerProberFunc{
				name:       "openai",
				configured: func() bool { return false },
			}
		}
		client := openai.NewClient(cfg.OpenAIKey)
		return summarizerProberFunc{
			name:       "openai",
			configured: func() bool { return true },
			ping:       client.Ping,
		}
	default:
		return nil
	}
}
