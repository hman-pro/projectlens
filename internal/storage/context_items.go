package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ContextItemRecord maps to a row in context_items.
type ContextItemRecord struct {
	ID           int64           `json:"id"`
	SourceID     int64           `json:"source_id"`
	ItemType     string          `json:"item_type"`
	ExternalID   string          `json:"external_id"`
	ParentItemID *int64          `json:"parent_item_id,omitempty"`
	RootItemID   *int64          `json:"root_item_id,omitempty"`
	URL          *string         `json:"url,omitempty"`
	Title        *string         `json:"title,omitempty"`
	State        *string         `json:"state,omitempty"`
	CreatedAt    *time.Time      `json:"created_at,omitempty"`
	UpdatedAt    *time.Time      `json:"updated_at,omitempty"`
	DeletedAt    *time.Time      `json:"deleted_at,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	FirstSeenAt  time.Time       `json:"first_seen_at"`
	LastSeenAt   time.Time       `json:"last_seen_at"`
}

// UpsertContextItem inserts or updates by (source_id, item_type, external_id).
// last_seen_at always advances to now(). deleted_at is NEVER overwritten here
// — re-seeing a deleted remote item should be handled explicitly by callers.
func (db *DB) UpsertContextItem(ctx context.Context, r *ContextItemRecord) error {
	if len(r.Metadata) == 0 {
		r.Metadata = json.RawMessage(`{}`)
	}
	const query = `
		INSERT INTO context_items (
			source_id, item_type, external_id, parent_item_id, root_item_id,
			url, title, state, created_at, updated_at, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (source_id, item_type, external_id) DO UPDATE SET
			parent_item_id = EXCLUDED.parent_item_id,
			root_item_id   = EXCLUDED.root_item_id,
			url            = EXCLUDED.url,
			title          = EXCLUDED.title,
			state          = EXCLUDED.state,
			created_at     = COALESCE(EXCLUDED.created_at, context_items.created_at),
			updated_at     = COALESCE(EXCLUDED.updated_at, context_items.updated_at),
			metadata       = EXCLUDED.metadata,
			last_seen_at   = now()
		RETURNING id, first_seen_at, last_seen_at, deleted_at
	`
	err := db.Pool.QueryRow(ctx, query,
		r.SourceID, r.ItemType, r.ExternalID, r.ParentItemID, r.RootItemID,
		r.URL, r.Title, r.State, r.CreatedAt, r.UpdatedAt, r.Metadata,
	).Scan(&r.ID, &r.FirstSeenAt, &r.LastSeenAt, &r.DeletedAt)
	if err != nil {
		return fmt.Errorf("storage: upsert context_item: %w", err)
	}
	return nil
}

// SetContextItemRoot is a small helper because root items are inserted before
// they know their own ID. Callers set root_item_id = id immediately after the
// initial UpsertContextItem.
func (db *DB) SetContextItemRoot(ctx context.Context, itemID, rootID int64) error {
	_, err := db.Pool.Exec(ctx, `UPDATE context_items SET root_item_id = $2 WHERE id = $1`, itemID, rootID)
	if err != nil {
		return fmt.Errorf("storage: set context_item root: %w", err)
	}
	return nil
}

// MarkContextItemDeleted records that the remote item is gone.
// Versions remain until retention policy removes them.
func (db *DB) MarkContextItemDeleted(ctx context.Context, itemID int64) error {
	_, err := db.Pool.Exec(ctx, `UPDATE context_items SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, itemID)
	if err != nil {
		return fmt.Errorf("storage: mark context_item deleted: %w", err)
	}
	return nil
}

// GetContextItem returns nil, nil when not found.
func (db *DB) GetContextItem(ctx context.Context, id int64) (*ContextItemRecord, error) {
	const query = `
		SELECT id, source_id, item_type, external_id, parent_item_id, root_item_id,
		       url, title, state, created_at, updated_at, deleted_at, metadata,
		       first_seen_at, last_seen_at
		FROM context_items WHERE id = $1
	`
	r := &ContextItemRecord{}
	err := db.Pool.QueryRow(ctx, query, id).Scan(
		&r.ID, &r.SourceID, &r.ItemType, &r.ExternalID, &r.ParentItemID, &r.RootItemID,
		&r.URL, &r.Title, &r.State, &r.CreatedAt, &r.UpdatedAt, &r.DeletedAt, &r.Metadata,
		&r.FirstSeenAt, &r.LastSeenAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get context_item: %w", err)
	}
	return r, nil
}
