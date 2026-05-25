package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ContextSourceRecord maps to a row in context_sources.
type ContextSourceRecord struct {
	ID          int64           `json:"id"`
	SourceType  string          `json:"source_type"`
	Namespace   string          `json:"namespace"`
	DisplayName string          `json:"display_name"`
	BaseURL     *string         `json:"base_url,omitempty"`
	ExternalKey string          `json:"external_key"`
	ConfigHash  *string         `json:"config_hash,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	Enabled     bool            `json:"enabled"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// ContextSourceStateRecord maps to a row in context_source_state.
type ContextSourceStateRecord struct {
	ID                  int64           `json:"id"`
	SourceID            int64           `json:"source_id"`
	Stream              string          `json:"stream"`
	CursorKind          string          `json:"cursor_kind"`
	CursorValue         *string         `json:"cursor_value,omitempty"`
	LastSuccessfulRunID *int64          `json:"last_successful_run_id,omitempty"`
	LastSuccessfulAt    *time.Time      `json:"last_successful_at,omitempty"`
	Metadata            json.RawMessage `json:"metadata,omitempty"`
}

// UpsertContextSource inserts or updates a source keyed by (source_type, external_key).
// On success the record's ID, CreatedAt, and UpdatedAt are populated.
func (db *DB) UpsertContextSource(ctx context.Context, r *ContextSourceRecord) error {
	if len(r.Metadata) == 0 {
		r.Metadata = json.RawMessage(`{}`)
	}
	const query = `
		INSERT INTO context_sources (source_type, namespace, display_name, base_url, external_key, config_hash, metadata, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source_type, external_key) DO UPDATE SET
			namespace = EXCLUDED.namespace,
			display_name = EXCLUDED.display_name,
			base_url = EXCLUDED.base_url,
			config_hash = EXCLUDED.config_hash,
			metadata = EXCLUDED.metadata,
			enabled = EXCLUDED.enabled,
			updated_at = now()
		RETURNING id, created_at, updated_at
	`
	err := db.Pool.QueryRow(ctx, query,
		r.SourceType, r.Namespace, r.DisplayName, r.BaseURL, r.ExternalKey,
		r.ConfigHash, r.Metadata, r.Enabled,
	).Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("storage: upsert context_source: %w", err)
	}
	return nil
}

// GetContextSourceByExternalKey returns nil, nil when not found.
func (db *DB) GetContextSourceByExternalKey(ctx context.Context, sourceType, externalKey string) (*ContextSourceRecord, error) {
	const query = `
		SELECT id, source_type, namespace, display_name, base_url, external_key, config_hash, metadata, enabled, created_at, updated_at
		FROM context_sources WHERE source_type = $1 AND external_key = $2
	`
	r := &ContextSourceRecord{}
	err := db.Pool.QueryRow(ctx, query, sourceType, externalKey).Scan(
		&r.ID, &r.SourceType, &r.Namespace, &r.DisplayName, &r.BaseURL,
		&r.ExternalKey, &r.ConfigHash, &r.Metadata, &r.Enabled,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get context_source: %w", err)
	}
	return r, nil
}

// UpsertContextSourceState writes the cursor/watermark row for one (source, stream).
// Per the spec, callers should only update this after a successful run.
func (db *DB) UpsertContextSourceState(ctx context.Context, r *ContextSourceStateRecord) error {
	if len(r.Metadata) == 0 {
		r.Metadata = json.RawMessage(`{}`)
	}
	const query = `
		INSERT INTO context_source_state (source_id, stream, cursor_kind, cursor_value, last_successful_run_id, last_successful_at, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (source_id, stream) DO UPDATE SET
			cursor_kind = EXCLUDED.cursor_kind,
			cursor_value = EXCLUDED.cursor_value,
			last_successful_run_id = EXCLUDED.last_successful_run_id,
			last_successful_at = EXCLUDED.last_successful_at,
			metadata = EXCLUDED.metadata
		RETURNING id
	`
	err := db.Pool.QueryRow(ctx, query,
		r.SourceID, r.Stream, r.CursorKind, r.CursorValue,
		r.LastSuccessfulRunID, r.LastSuccessfulAt, r.Metadata,
	).Scan(&r.ID)
	if err != nil {
		return fmt.Errorf("storage: upsert context_source_state: %w", err)
	}
	return nil
}

// GetContextSourceState returns nil, nil when not found.
func (db *DB) GetContextSourceState(ctx context.Context, sourceID int64, stream string) (*ContextSourceStateRecord, error) {
	const query = `
		SELECT id, source_id, stream, cursor_kind, cursor_value, last_successful_run_id, last_successful_at, metadata
		FROM context_source_state WHERE source_id = $1 AND stream = $2
	`
	r := &ContextSourceStateRecord{}
	err := db.Pool.QueryRow(ctx, query, sourceID, stream).Scan(
		&r.ID, &r.SourceID, &r.Stream, &r.CursorKind, &r.CursorValue,
		&r.LastSuccessfulRunID, &r.LastSuccessfulAt, &r.Metadata,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get context_source_state: %w", err)
	}
	return r, nil
}
