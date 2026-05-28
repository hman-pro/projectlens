package main

import (
	"context"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/providers/ollama"
	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
)

// buildInspector builds an indexstate.Inspector for read-only commands
// (report, export). Mirrors cmd/projectlens-mcp/main.go's wiring so the
// CLI report and index_status see identical provider health. Do NOT use
// buildProviders — that is fail-fast and returns indexer types.
func buildInspector(cfg *config.Config, db *storage.DB, repoPath string) *indexstate.DefaultInspector {
	embedder := ollama.NewClient(cfg.Embeddings.Endpoint, cfg.Embeddings.Model, cfg.Embeddings.Dimensions)
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
// prober reports "configured" (credentials present, no probe run).
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

// disabledSummarizerProber reports the explicit "disabled" state so the
// report and index_status output can render `summarization: disabled`
// without dereferencing a nil summarizer.
type disabledSummarizerProber struct{}

func (disabledSummarizerProber) ProbeSummarizer(_ context.Context) (string, string, error) {
	return "", "disabled", nil
}

// newCLISummarizerProber returns an indexstate.SummarizerProber for the
// configured summarization provider. When summarization is disabled,
// the prober reports state "disabled". Returns nil only when the
// provider is enabled but unknown (caller handles that as "no probe").
func newCLISummarizerProber(cfg *config.Config) indexstate.SummarizerProber {
	if !cfg.Summarization.Enabled {
		return disabledSummarizerProber{}
	}
	if cfg.Summarization.Provider != "ollama" {
		return nil
	}
	return summarizerProberFunc{
		name:       "ollama",
		configured: func() bool { return true },
	}
}
