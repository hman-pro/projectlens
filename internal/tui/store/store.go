package store

import "context"

const RunsMaxRows = 100

type Store interface {
	Health(ctx context.Context) (HealthSnapshot, error)
	Pipeline(ctx context.Context) (PipelineSnapshot, error)
	Storage(ctx context.Context) (StorageSnapshot, error)
	Runs(ctx context.Context, limit int) (RunsSnapshot, error)
	Config(ctx context.Context) (ConfigSnapshot, error)

	// Phase 2 preflight counts. Each returns (count, err).
	EmbedPending(ctx context.Context) (int, error)
	SummarizePending(ctx context.Context) (int, error)
	HistoryNewCommits(ctx context.Context) (int, error)
	ChangedFilesSinceLastRun(ctx context.Context) (int, error)
	DatastoreTableCount(ctx context.Context) (int, error)
}
