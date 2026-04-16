package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// DocumentRecord maps to a row in the documents table.
type DocumentRecord struct {
	ID           int64           `json:"id"`
	SourceType   string          `json:"source_type"`
	ExternalID   string          `json:"external_id"`
	Title        string          `json:"title"`
	URL          *string         `json:"url,omitempty"`
	BodyText     *string         `json:"body_text,omitempty"`
	LastSyncedAt time.Time       `json:"last_synced_at"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

// UpsertDocument inserts or updates a document keyed by (source_type, external_id).
func (db *DB) UpsertDocument(ctx context.Context, r *DocumentRecord) error {
	const query = `
		INSERT INTO documents (source_type, external_id, title, url, body_text, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (source_type, external_id) DO UPDATE SET
			title = EXCLUDED.title,
			url = EXCLUDED.url,
			body_text = EXCLUDED.body_text,
			metadata = EXCLUDED.metadata,
			last_synced_at = NOW()
	`
	_, err := db.Pool.Exec(ctx, query, r.SourceType, r.ExternalID, r.Title, r.URL, r.BodyText, r.Metadata)
	if err != nil {
		return fmt.Errorf("storage: upsert document: %w", err)
	}
	return nil
}

// GetDocumentByExternalID returns a document by source type and external ID.
// Returns nil, nil if no row is found.
func (db *DB) GetDocumentByExternalID(ctx context.Context, sourceType, externalID string) (*DocumentRecord, error) {
	const query = `
		SELECT id, source_type, external_id, title, url, body_text, last_synced_at, metadata
		FROM documents WHERE source_type = $1 AND external_id = $2
	`
	r := &DocumentRecord{}
	err := db.Pool.QueryRow(ctx, query, sourceType, externalID).Scan(
		&r.ID, &r.SourceType, &r.ExternalID, &r.Title, &r.URL, &r.BodyText, &r.LastSyncedAt, &r.Metadata,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get document: %w", err)
	}
	return r, nil
}

// ListDocuments returns documents filtered by source type. If sourceType is empty, returns all.
func (db *DB) ListDocuments(ctx context.Context, sourceType string) ([]DocumentRecord, error) {
	var query string
	var args []any
	if sourceType != "" {
		query = `SELECT id, source_type, external_id, title, url, body_text, last_synced_at, metadata
				 FROM documents WHERE source_type = $1 ORDER BY title`
		args = []any{sourceType}
	} else {
		query = `SELECT id, source_type, external_id, title, url, body_text, last_synced_at, metadata
				 FROM documents ORDER BY title`
	}

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list documents: %w", err)
	}
	defer rows.Close()

	var results []DocumentRecord
	for rows.Next() {
		var r DocumentRecord
		if err := rows.Scan(&r.ID, &r.SourceType, &r.ExternalID, &r.Title, &r.URL, &r.BodyText, &r.LastSyncedAt, &r.Metadata); err != nil {
			return nil, fmt.Errorf("storage: scan document: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
