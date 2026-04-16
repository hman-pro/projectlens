package retrieval

import (
	"context"
	"fmt"

	"github.com/hman-pro/projectlens/internal/storage"
)

// QueryEmbedder generates embedding vectors for text queries.
type QueryEmbedder interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// SemanticSearch embeds the query text and performs a cosine-similarity search
// via pgvector. Results are returned as SearchResult with Source = "semantic"
// and Score = 1 - distance (converting cosine distance to similarity).
func SemanticSearch(ctx context.Context, db *storage.DB, embedder QueryEmbedder, query string, topK int) ([]SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("retrieval: semantic search: query must not be empty")
	}
	if topK <= 0 {
		return nil, fmt.Errorf("retrieval: semantic search: topK must be positive")
	}

	vectors, err := embedder.EmbedBatch(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("retrieval: semantic search: embed query: %w", err)
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("retrieval: semantic search: embedder returned empty vector")
	}

	ssResults, err := db.SemanticSearch(ctx, vectors[0], topK)
	if err != nil {
		return nil, fmt.Errorf("retrieval: semantic search: %w", err)
	}

	results := make([]SearchResult, 0, len(ssResults))
	for _, r := range ssResults {
		results = append(results, semanticResultToSearchResult(r))
	}
	return results, nil
}

// semanticResultToSearchResult converts a storage.SemanticSearchResult to a
// SearchResult with Source = "semantic" and Score = 1 - distance.
func semanticResultToSearchResult(r storage.SemanticSearchResult) SearchResult {
	var sourceURI string
	if r.SourceURI != nil {
		sourceURI = *r.SourceURI
	}
	return SearchResult{
		SymbolName:  r.SymbolName,
		Kind:        r.SymbolKind,
		PackageName: r.PackageName,
		FilePath:    r.FilePath,
		LineStart:   r.LineStart,
		LineEnd:     r.LineEnd,
		Score:       1.0 - r.Distance,
		Source:      "semantic",
		SourceType:  r.SourceType,
		SourceURI:   sourceURI,
	}
}
