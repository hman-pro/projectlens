// Package embeddings provides a pipeline for converting text chunks into
// vector embeddings via an external embedding service (e.g., OpenAI).
package embeddings

import (
	"context"
	"fmt"
	"log"
)

// batchSize is the maximum number of texts sent per EmbedBatch call.
const batchSize = 100

// EmbeddingResult pairs a chunk index with its embedding vector.
type EmbeddingResult struct {
	ChunkIndex int       // index into the input chunks slice
	Vector     []float32 // 3072-dim vector from OpenAI
}

// Embedder is the interface for generating text embeddings. The openai.Client
// type satisfies this interface implicitly via its EmbedBatch method.
type Embedder interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// EmbedChunks takes a list of chunk content strings, calls embedder.EmbedBatch
// in batches of 100, and returns an EmbeddingResult for each chunk with its
// index and vector. If any batch fails, an error is returned immediately with
// no partial results.
func EmbedChunks(ctx context.Context, embedder Embedder, chunkContents []string) ([]EmbeddingResult, error) {
	if len(chunkContents) == 0 {
		return nil, nil
	}

	totalBatches := (len(chunkContents) + batchSize - 1) / batchSize
	results := make([]EmbeddingResult, 0, len(chunkContents))

	for start := 0; start < len(chunkContents); start += batchSize {
		end := start + batchSize
		if end > len(chunkContents) {
			end = len(chunkContents)
		}
		batch := chunkContents[start:end]
		batchNum := start/batchSize + 1

		log.Printf("embedding batch %d of %d (%d chunks)", batchNum, totalBatches, len(batch))

		vectors, err := embedder.EmbedBatch(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("embedding batch %d of %d: %w", batchNum, totalBatches, err)
		}

		for i, vec := range vectors {
			results = append(results, EmbeddingResult{
				ChunkIndex: start + i,
				Vector:     vec,
			})
		}
	}

	return results, nil
}
