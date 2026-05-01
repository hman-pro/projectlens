package jobs

import (
	"context"
	"fmt"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

// DefaultRegistry returns the canonical list of Phase 2 actions.
// Cost-driver strings (shown in the embed/summarize confirm modal)
// are derived from the loaded config so they match what the
// subprocess will actually use.
func DefaultRegistry(cfg *config.Config) []Spec {
	embedProvider := ""
	summProvider := ""
	if cfg != nil {
		embedProvider = cfg.Embeddings.Provider
		summProvider = cfg.Summarization.Provider
	}
	return []Spec{
		{
			Key: 'R', Name: "reindex", Args: []string{"reindex"},
			Confirm:   ConfirmYesNo,
			RefreshOn: []string{"pipeline", "runs", "storage"},
			Preflight: changedFilesPreflight,
			Headline: func(n int, _ string) string {
				return fmt.Sprintf("reindex %d changed file(s)? [y/N]", n)
			},
		},
		{
			Key: 'F', Name: "reindex --full", Args: []string{"reindex", "--full"},
			Confirm: ConfirmTyped, Phrase: "reindex",
			RefreshOn: []string{"pipeline", "runs", "storage"},
			Preflight: changedFilesPreflight,
			Headline: func(n int, _ string) string {
				return fmt.Sprintf("RE-INDEX %d files (~8 min, rewrites embeddings)\nType 'reindex' to confirm", n)
			},
		},
		{
			Key: 'E', Name: "index-embed", Args: []string{"index-embed"},
			Confirm:   ConfirmYesNo,
			RefreshOn: []string{"pipeline", "runs"},
			Preflight: embedPendingPreflight(embedProvider),
			Headline: func(n int, cost string) string {
				return fmt.Sprintf("embed %d chunk(s) via %s? [y/N]", n, cost)
			},
		},
		{
			Key: 'S', Name: "index-summarize", Args: []string{"index-summarize"},
			Confirm:   ConfirmYesNo,
			RefreshOn: []string{"pipeline", "runs"},
			Preflight: summarizePendingPreflight(summProvider),
			Headline: func(n int, cost string) string {
				return fmt.Sprintf("summarize %d package(s) via %s? [y/N]", n, cost)
			},
		},
		{
			Key: 'H', Name: "index-history", Args: []string{"index-history"},
			Confirm:   ConfirmYesNo,
			RefreshOn: []string{"pipeline", "runs"},
			Preflight: historyCommitsPreflight,
			Headline: func(n int, _ string) string {
				return fmt.Sprintf("ingest %d new commit(s)? [y/N]", n)
			},
		},
		{
			Key: 'D', Name: "index-datastore", Args: []string{"index-datastore"},
			Confirm:   ConfirmYesNo,
			RefreshOn: []string{"pipeline", "runs", "storage"},
			Preflight: datastoreTablesPreflight,
			Headline: func(n int, _ string) string {
				return fmt.Sprintf("rescan datastore (currently %d table(s) indexed)? [y/N]", n)
			},
		},
	}
}

func changedFilesPreflight(ctx context.Context, s store.Store) (int, string, error) {
	n, err := s.ChangedFilesSinceLastRun(ctx)
	return n, "", err
}

func embedPendingPreflight(cost string) Preflight {
	return func(ctx context.Context, s store.Store) (int, string, error) {
		n, err := s.EmbedPending(ctx)
		return n, cost, err
	}
}

func summarizePendingPreflight(cost string) Preflight {
	return func(ctx context.Context, s store.Store) (int, string, error) {
		n, err := s.SummarizePending(ctx)
		return n, cost, err
	}
}

func historyCommitsPreflight(ctx context.Context, s store.Store) (int, string, error) {
	n, err := s.HistoryNewCommits(ctx)
	return n, "", err
}

func datastoreTablesPreflight(ctx context.Context, s store.Store) (int, string, error) {
	n, err := s.DatastoreTableCount(ctx)
	return n, "", err
}
