package storage

import (
	"context"
	"fmt"

	"github.com/pgvector/pgvector-go"
)

// EmbeddingRecord maps to a row in the embeddings table.
type EmbeddingRecord struct {
	ID           int64             `json:"id"`
	ChunkID      int64             `json:"chunk_id"`
	ModelVersion string            `json:"model_version"`
	Embedding    pgvector.HalfVector `json:"embedding"`
}

// SemanticSearchResult is returned by semantic search queries.
type SemanticSearchResult struct {
	ChunkID     int64   `json:"chunk_id"`
	SymbolName  string  `json:"symbol_name"`
	SymbolKind  string  `json:"symbol_kind"`
	PackageName string  `json:"package_name"`
	FilePath    string  `json:"file_path"`
	LineStart   int     `json:"line_start"`
	LineEnd     int     `json:"line_end"`
	Distance    float64 `json:"distance"`
	SourceType  string  `json:"source_type"`
	SourceURI   *string `json:"source_uri,omitempty"`
}

// UpsertEmbedding inserts or updates an embedding keyed by (chunk_id, model_version).
func (db *DB) UpsertEmbedding(ctx context.Context, e *EmbeddingRecord) error {
	const query = `
		INSERT INTO embeddings (chunk_id, model_version, embedding)
		VALUES ($1, $2, $3)
		ON CONFLICT (chunk_id, model_version) DO UPDATE SET
			embedding = EXCLUDED.embedding
	`
	_, err := db.Pool.Exec(ctx, query, e.ChunkID, e.ModelVersion, e.Embedding)
	if err != nil {
		return fmt.Errorf("storage: upsert embedding: %w", err)
	}
	return nil
}

// SemanticSearch performs a cosine-distance nearest-neighbour search using
// pgvector's <=> operator and returns the top-K results joined with symbol
// and file metadata.
func (db *DB) SemanticSearch(ctx context.Context, queryVector []float32, topK int) ([]SemanticSearchResult, error) {
	const query = `
		SELECT e.chunk_id,
		       COALESCE(s.name, '') AS symbol_name,
		       COALESCE(s.kind, '') AS symbol_kind,
		       COALESCE(s.package_name, '') AS package_name,
		       COALESCE(f.path, '') AS file_path,
		       COALESCE(s.line_start, 0) AS line_start,
		       COALESCE(s.line_end, 0) AS line_end,
		       e.embedding <=> $1 AS distance,
		       c.source_type,
		       c.source_uri
		FROM embeddings e
		JOIN chunks c ON c.id = e.chunk_id
		LEFT JOIN symbols s ON s.id = c.symbol_id
		LEFT JOIN files f ON f.id = s.file_id
		ORDER BY distance
		LIMIT $2
	`
	hv := pgvector.NewHalfVector(queryVector)

	rows, err := db.Pool.Query(ctx, query, hv, topK)
	if err != nil {
		return nil, fmt.Errorf("storage: semantic search: %w", err)
	}
	defer rows.Close()

	var results []SemanticSearchResult
	for rows.Next() {
		var r SemanticSearchResult
		if err := rows.Scan(
			&r.ChunkID, &r.SymbolName, &r.SymbolKind, &r.PackageName,
			&r.FilePath, &r.LineStart, &r.LineEnd, &r.Distance,
			&r.SourceType, &r.SourceURI,
		); err != nil {
			return nil, fmt.Errorf("storage: semantic search scan: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
