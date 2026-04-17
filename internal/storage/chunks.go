package storage

import (
	"context"
	"fmt"
)

// ChunkRecord maps to a row in the chunks table.
type ChunkRecord struct {
	ID         int64   `json:"id"`
	SymbolID   *int64  `json:"symbol_id,omitempty"` // nullable for doc chunks
	Content    string  `json:"content"`
	TokenCount int     `json:"token_count"`
	SourceType string  `json:"source_type"`
	SourceURI  *string `json:"source_uri,omitempty"`
}

// UpsertChunk inserts or updates a chunk keyed by symbol_id.
// Returns the id of the upserted row.
func (db *DB) UpsertChunk(ctx context.Context, c *ChunkRecord) (int64, error) {
	const query = `
		INSERT INTO chunks (symbol_id, content, token_count, source_type, source_uri)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (symbol_id) DO UPDATE SET
			content     = EXCLUDED.content,
			token_count = EXCLUDED.token_count,
			source_type = EXCLUDED.source_type,
			source_uri  = EXCLUDED.source_uri
		RETURNING id
	`
	var id int64
	err := db.Pool.QueryRow(ctx, query, c.SymbolID, c.Content, c.TokenCount, c.SourceType, c.SourceURI).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("storage: upsert chunk: %w", err)
	}
	return id, nil
}

// InsertDocChunk inserts a non-symbol chunk (e.g. Confluence, Jira, migration).
// Doc chunks don't have a symbol_id, so they can't use the ON CONFLICT (symbol_id)
// strategy. Returns the id of the inserted row.
func (db *DB) InsertDocChunk(ctx context.Context, c *ChunkRecord) (int64, error) {
	const query = `
		INSERT INTO chunks (symbol_id, content, token_count, source_type, source_uri)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`
	var id int64
	err := db.Pool.QueryRow(ctx, query, c.SymbolID, c.Content, c.TokenCount, c.SourceType, c.SourceURI).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("storage: insert doc chunk: %w", err)
	}
	return id, nil
}

// UnembeddedChunk is a chunk that has no embedding yet.
type UnembeddedChunk struct {
	ID      int64
	Content string
}

// GetUnembeddedChunks returns all chunks that don't have an embedding.
func (db *DB) GetUnembeddedChunks(ctx context.Context) ([]UnembeddedChunk, error) {
	const query = `
		SELECT c.id, c.content FROM chunks c
		LEFT JOIN embeddings e ON e.chunk_id = c.id
		WHERE e.id IS NULL
		ORDER BY c.id
	`
	rows, err := db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("storage: get unembedded chunks: %w", err)
	}
	defer rows.Close()

	var results []UnembeddedChunk
	for rows.Next() {
		var c UnembeddedChunk
		if err := rows.Scan(&c.ID, &c.Content); err != nil {
			return nil, fmt.Errorf("storage: scan unembedded chunk: %w", err)
		}
		results = append(results, c)
	}
	return results, rows.Err()
}

// GetChunkBySymbolID retrieves a chunk by its associated symbol ID.
// Returns nil, nil if no row is found.
func (db *DB) GetChunkBySymbolID(ctx context.Context, symbolID int64) (*ChunkRecord, error) {
	const query = `
		SELECT id, symbol_id, content, token_count, source_type, source_uri
		FROM chunks WHERE symbol_id = $1
	`
	c := &ChunkRecord{}
	err := db.Pool.QueryRow(ctx, query, symbolID).Scan(
		&c.ID, &c.SymbolID, &c.Content, &c.TokenCount, &c.SourceType, &c.SourceURI,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get chunk by symbol id: %w", err)
	}
	return c, nil
}
