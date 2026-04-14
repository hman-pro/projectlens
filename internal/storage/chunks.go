package storage

import (
	"context"
	"fmt"
)

// ChunkRecord maps to a row in the chunks table.
type ChunkRecord struct {
	ID         int64  `json:"id"`
	SymbolID   int64  `json:"symbol_id"`
	Content    string `json:"content"`
	TokenCount int    `json:"token_count"`
}

// UpsertChunk inserts or updates a chunk keyed by symbol_id.
// Returns the id of the upserted row.
func (db *DB) UpsertChunk(ctx context.Context, c *ChunkRecord) (int64, error) {
	const query = `
		INSERT INTO chunks (symbol_id, content, token_count)
		VALUES ($1, $2, $3)
		ON CONFLICT (symbol_id) DO UPDATE SET
			content     = EXCLUDED.content,
			token_count = EXCLUDED.token_count
		RETURNING id
	`
	var id int64
	err := db.Pool.QueryRow(ctx, query, c.SymbolID, c.Content, c.TokenCount).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("storage: upsert chunk: %w", err)
	}
	return id, nil
}

// GetChunkBySymbolID retrieves a chunk by its associated symbol ID.
// Returns nil, nil if no row is found.
func (db *DB) GetChunkBySymbolID(ctx context.Context, symbolID int64) (*ChunkRecord, error) {
	const query = `
		SELECT id, symbol_id, content, token_count
		FROM chunks WHERE symbol_id = $1
	`
	c := &ChunkRecord{}
	err := db.Pool.QueryRow(ctx, query, symbolID).Scan(
		&c.ID, &c.SymbolID, &c.Content, &c.TokenCount,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get chunk by symbol id: %w", err)
	}
	return c, nil
}
