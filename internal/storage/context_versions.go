package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ContextItemVersionRecord maps to a row in context_item_versions.
type ContextItemVersionRecord struct {
	ID              int64           `json:"id"`
	ItemID          int64           `json:"item_id"`
	ExternalVersion *string         `json:"external_version,omitempty"`
	ContentHash     string          `json:"content_hash"`
	BodyText        string          `json:"body_text"`
	Redaction       json.RawMessage `json:"redaction,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	IsCurrent       bool            `json:"is_current"`
	InsertedAt      time.Time       `json:"inserted_at"`
	SupersededAt    *time.Time      `json:"superseded_at,omitempty"`
	RunID           *int64          `json:"run_id,omitempty"`
}

// VersionUpsertResult describes what UpsertContextItemVersion actually did.
type VersionUpsertResult struct {
	VersionID int64 // ID of the current version after the call
	Inserted  bool  // true when a new row was inserted (content changed)
}

// UpsertContextItemVersion implements the spec's content-hash lineage rules:
//   - If a current version exists with the same content_hash, no new row is
//     inserted. The existing version's ID is returned.
//   - If no current version exists, insert one.
//   - If the current version has a different hash, mark it is_current=false
//     and superseded_at=now(), then insert the new current version.
//
// The whole operation runs in a single transaction so the partial-unique index
// `context_item_versions_current_idx` is never violated mid-flight.
func (db *DB) UpsertContextItemVersion(ctx context.Context, r *ContextItemVersionRecord) (VersionUpsertResult, error) {
	if len(r.Redaction) == 0 {
		r.Redaction = json.RawMessage(`{}`)
	}
	if len(r.Metadata) == 0 {
		r.Metadata = json.RawMessage(`{}`)
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return VersionUpsertResult{}, fmt.Errorf("storage: upsert context_item_version: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Look up current version, if any. Lock the row so a concurrent caller
	//    cannot race us between the SELECT and the INSERT.
	var curID int64
	var curHash string
	err = tx.QueryRow(ctx,
		`SELECT id, content_hash FROM context_item_versions
		 WHERE item_id = $1 AND is_current = TRUE
		 FOR UPDATE`,
		r.ItemID,
	).Scan(&curID, &curHash)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// fall through to insert
	case err != nil:
		return VersionUpsertResult{}, fmt.Errorf("storage: upsert context_item_version: lookup current: %w", err)
	default:
		if curHash == r.ContentHash {
			// Unchanged content — touch item last_seen_at, return existing id.
			if _, err := tx.Exec(ctx,
				`UPDATE context_items SET last_seen_at = now() WHERE id = $1`,
				r.ItemID,
			); err != nil {
				return VersionUpsertResult{}, fmt.Errorf("storage: upsert context_item_version: touch item: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return VersionUpsertResult{}, fmt.Errorf("storage: upsert context_item_version: commit: %w", err)
			}
			r.ID = curID
			r.IsCurrent = true
			return VersionUpsertResult{VersionID: curID, Inserted: false}, nil
		}
		// Different hash — supersede.
		if _, err := tx.Exec(ctx,
			`UPDATE context_item_versions
			    SET is_current = FALSE, superseded_at = now()
			  WHERE id = $1`,
			curID,
		); err != nil {
			return VersionUpsertResult{}, fmt.Errorf("storage: upsert context_item_version: supersede: %w", err)
		}
	}

	// 2. Insert new current version.
	err = tx.QueryRow(ctx,
		`INSERT INTO context_item_versions
		   (item_id, external_version, content_hash, body_text, redaction, metadata, is_current, run_id)
		 VALUES ($1, $2, $3, $4, $5, $6, TRUE, $7)
		 RETURNING id, inserted_at`,
		r.ItemID, r.ExternalVersion, r.ContentHash, r.BodyText, r.Redaction, r.Metadata, r.RunID,
	).Scan(&r.ID, &r.InsertedAt)
	if err != nil {
		return VersionUpsertResult{}, fmt.Errorf("storage: upsert context_item_version: insert: %w", err)
	}

	// 3. Touch item.
	if _, err := tx.Exec(ctx, `UPDATE context_items SET last_seen_at = now() WHERE id = $1`, r.ItemID); err != nil {
		return VersionUpsertResult{}, fmt.Errorf("storage: upsert context_item_version: touch item: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return VersionUpsertResult{}, fmt.Errorf("storage: upsert context_item_version: commit: %w", err)
	}
	r.IsCurrent = true
	return VersionUpsertResult{VersionID: r.ID, Inserted: true}, nil
}

// GetCurrentContextItemVersion returns the live version for an item.
// Returns nil, nil when there is no current version.
func (db *DB) GetCurrentContextItemVersion(ctx context.Context, itemID int64) (*ContextItemVersionRecord, error) {
	const query = `
		SELECT id, item_id, external_version, content_hash, body_text, redaction, metadata,
		       is_current, inserted_at, superseded_at, run_id
		FROM context_item_versions
		WHERE item_id = $1 AND is_current = TRUE
	`
	r := &ContextItemVersionRecord{}
	err := db.Pool.QueryRow(ctx, query, itemID).Scan(
		&r.ID, &r.ItemID, &r.ExternalVersion, &r.ContentHash, &r.BodyText, &r.Redaction, &r.Metadata,
		&r.IsCurrent, &r.InsertedAt, &r.SupersededAt, &r.RunID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get current context_item_version: %w", err)
	}
	return r, nil
}
