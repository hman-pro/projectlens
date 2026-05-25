package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ContextChunkRecord maps to a row in context_chunks.
type ContextChunkRecord struct {
	ID             int64           `json:"id"`
	ItemVersionID  int64           `json:"item_version_id"`
	ChunkKey       string          `json:"chunk_key"`
	ChunkAnchorID  string          `json:"chunk_anchor_id"`
	SourceAnchorID string          `json:"source_anchor_id"`
	ChunkIndex     int             `json:"chunk_index"`
	Heading        *string         `json:"heading,omitempty"`
	ContentHash    string          `json:"content_hash"`
	TokenCount     int             `json:"token_count"`
	ChunkID        *int64          `json:"chunk_id,omitempty"`
	LightragDocID  *string         `json:"lightrag_doc_id,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// UpsertContextChunk inserts or updates a chunk keyed by chunk_anchor_id.
// chunk_key must stay stable for a given anchor within an item_version, or
// the call fails on the secondary unique index.
func (db *DB) UpsertContextChunk(ctx context.Context, r *ContextChunkRecord) error {
	if len(r.Metadata) == 0 {
		r.Metadata = json.RawMessage(`{}`)
	}
	const query = `
		INSERT INTO context_chunks (
			item_version_id, chunk_key, chunk_anchor_id, source_anchor_id,
			chunk_index, heading, content_hash, token_count, chunk_id, lightrag_doc_id, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (chunk_anchor_id) DO UPDATE SET
			item_version_id  = EXCLUDED.item_version_id,
			chunk_key        = EXCLUDED.chunk_key,
			source_anchor_id = EXCLUDED.source_anchor_id,
			chunk_index      = EXCLUDED.chunk_index,
			heading          = EXCLUDED.heading,
			content_hash     = EXCLUDED.content_hash,
			token_count      = EXCLUDED.token_count,
			chunk_id         = EXCLUDED.chunk_id,
			lightrag_doc_id  = EXCLUDED.lightrag_doc_id,
			metadata         = EXCLUDED.metadata
		RETURNING id, created_at
	`
	err := db.Pool.QueryRow(ctx, query,
		r.ItemVersionID, r.ChunkKey, r.ChunkAnchorID, r.SourceAnchorID,
		r.ChunkIndex, r.Heading, r.ContentHash, r.TokenCount,
		r.ChunkID, r.LightragDocID, r.Metadata,
	).Scan(&r.ID, &r.CreatedAt)
	if err != nil {
		return fmt.Errorf("storage: upsert context_chunk: %w", err)
	}
	return nil
}
