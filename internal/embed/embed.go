package embed

import (
	"context"
	"fmt"
	"time"

	"github.com/hman-pro/projectlens/internal/embeddings"
	"github.com/hman-pro/projectlens/internal/logger"
	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/pgvector/pgvector-go"
)

// Stats holds the counters produced by a single EmbedMissing run.
type Stats struct {
	Chunks  int
	Tokens  int
	Batches int
}

// EmbedMissing finds all chunks without embeddings and embeds them. Returns
// Stats describing the run.
func EmbedMissing(ctx context.Context, db *storage.DB, embedder embeddings.Embedder) (Stats, error) {
	startTime := time.Now()
	logger.Step("Embed missing chunks")

	unembedded, err := db.GetUnembeddedChunks(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("embed: get unembedded chunks: %w", err)
	}

	if len(unembedded) == 0 {
		logger.Info("all chunks already have embeddings — nothing to do")
		return Stats{}, nil
	}

	logger.Info("found chunks missing embeddings", "count", len(unembedded))

	contents := make([]string, len(unembedded))
	for i, c := range unembedded {
		contents[i] = c.Content
	}

	results, err := embeddings.EmbedChunks(ctx, embedder, contents)
	if err != nil {
		return Stats{}, fmt.Errorf("embed: embed chunks: %w", err)
	}

	embedded := 0
	for _, r := range results {
		chunk := unembedded[r.ChunkIndex]
		rec := &storage.EmbeddingRecord{
			ChunkID:      chunk.ID,
			ModelVersion: "embedding-model",
			Embedding:    pgvector.NewHalfVector(r.Vector),
		}
		if err := db.UpsertEmbedding(ctx, rec); err != nil {
			return Stats{Chunks: embedded}, fmt.Errorf("embed: upsert embedding for chunk %d: %w", chunk.ID, err)
		}
		embedded++
	}

	logger.Info("embedded chunks", "count", embedded, "elapsed", time.Since(startTime).Round(time.Millisecond))
	return Stats{Chunks: embedded}, nil
}
