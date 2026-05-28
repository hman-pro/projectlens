// Package embeddings provides a pipeline for converting text chunks into
// vector embeddings via a local Ollama embedding model.
package embeddings

import (
	"context"
	"fmt"

	"github.com/hman-pro/projectlens/internal/logger"
	"github.com/hman-pro/projectlens/internal/providers/identity"
)

// batchSize is the maximum number of texts sent per EmbedBatch call.
// Kept small for local models (Ollama) where each text uses significant memory.
const batchSize = 10

// maxCharsPerChunk is the approximate character limit for embedding input.
// qwen3-embedding:0.6b has a 32K-token context. With ~3 chars/token for code,
// 80K chars is a generous safe floor; we keep 8000 to bound memory per batch.
const maxCharsPerChunk = 8000

// EmbeddingResult pairs a chunk index with its embedding vector.
type EmbeddingResult struct {
	ChunkIndex int       // index into the input chunks slice
	Vector     []float32 // embedding vector (dimensions depend on model)
}

// Embedder is the interface for generating text embeddings. The ollama.Client
// type satisfies this interface implicitly via its EmbedBatch method.
type Embedder interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	EmbedIdentity() identity.ProviderIdentity
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
		batch := make([]string, end-start)
		copy(batch, chunkContents[start:end])
		batchNum := start/batchSize + 1

		// Truncate oversized chunks to stay within model token limits.
		for i, text := range batch {
			if len(text) > maxCharsPerChunk {
				batch[i] = text[:maxCharsPerChunk]
			}
		}

		logger.Progress("embedding batches", batchNum, totalBatches, "chunks", len(batch))

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
