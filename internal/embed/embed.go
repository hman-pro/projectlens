package embed

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hman-pro/projectlens/internal/embeddings"
	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/pgvector/pgvector-go"
)

// EmbedMissing finds all chunks without embeddings and embeds them.
func EmbedMissing(ctx context.Context, db *storage.DB, embedder embeddings.Embedder) error {
	startTime := time.Now()
	log.Println("── Embed missing chunks ──")

	unembedded, err := db.GetUnembeddedChunks(ctx)
	if err != nil {
		return fmt.Errorf("embed: get unembedded chunks: %w", err)
	}

	if len(unembedded) == 0 {
		log.Println("all chunks already have embeddings — nothing to do")
		return nil
	}

	log.Printf("found %d chunks missing embeddings", len(unembedded))

	contents := make([]string, len(unembedded))
	for i, c := range unembedded {
		contents[i] = c.Content
	}

	results, err := embeddings.EmbedChunks(ctx, embedder, contents)
	if err != nil {
		return fmt.Errorf("embed: embed chunks: %w", err)
	}

	for _, r := range results {
		chunk := unembedded[r.ChunkIndex]
		rec := &storage.EmbeddingRecord{
			ChunkID:      chunk.ID,
			ModelVersion: "embedding-model",
			Embedding:    pgvector.NewHalfVector(r.Vector),
		}
		if err := db.UpsertEmbedding(ctx, rec); err != nil {
			return fmt.Errorf("embed: upsert embedding for chunk %d: %w", chunk.ID, err)
		}
	}

	log.Printf("embedded %d chunks (%s)", len(results), time.Since(startTime).Round(time.Millisecond))
	return nil
}
