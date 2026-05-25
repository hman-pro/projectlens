package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ContextParticipantRecord maps to a row in context_participants.
type ContextParticipantRecord struct {
	ID         int64      `json:"id"`
	ItemID     int64      `json:"item_id"`
	PersonID   *int64     `json:"person_id,omitempty"`
	IdentityID *int64     `json:"identity_id,omitempty"`
	Role       string     `json:"role"`
	SourceRole *string    `json:"source_role,omitempty"`
	OccurredAt *time.Time `json:"occurred_at,omitempty"`
	// IsCurrent is *bool so the SQL `NOT NULL DEFAULT TRUE` applies when nil.
	// Set to &falseVar to explicitly write false.
	IsCurrent   *bool           `json:"is_current,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	FirstSeenAt time.Time       `json:"first_seen_at"`
	LastSeenAt  time.Time       `json:"last_seen_at"`
}

// UpsertContextParticipant inserts or refreshes a participant row keyed by
// (item_id, identity_id, person_id, role, source_role) with NULLS NOT DISTINCT.
// Absent source_role is normalised to "" so it matches the column default.
// IsCurrent left nil falls back to the column default TRUE.
func (db *DB) UpsertContextParticipant(ctx context.Context, r *ContextParticipantRecord) error {
	if len(r.Metadata) == 0 {
		r.Metadata = json.RawMessage(`{}`)
	}
	if r.PersonID == nil && r.IdentityID == nil {
		return fmt.Errorf("storage: upsert context_participant: person_id or identity_id required")
	}
	srcRole := ""
	if r.SourceRole != nil {
		srcRole = *r.SourceRole
	}
	const query = `
		INSERT INTO context_participants (
			item_id, person_id, identity_id, role, source_role,
			occurred_at, is_current, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7, TRUE), $8)
		ON CONFLICT ON CONSTRAINT context_participants_uniq DO UPDATE SET
			person_id    = COALESCE(EXCLUDED.person_id, context_participants.person_id),
			occurred_at  = COALESCE(EXCLUDED.occurred_at, context_participants.occurred_at),
			is_current   = EXCLUDED.is_current,
			metadata     = EXCLUDED.metadata,
			last_seen_at = now()
		RETURNING id, first_seen_at, last_seen_at, is_current
	`
	var isCurrent bool
	err := db.Pool.QueryRow(ctx, query,
		r.ItemID, r.PersonID, r.IdentityID, r.Role, srcRole,
		r.OccurredAt, r.IsCurrent, r.Metadata,
	).Scan(&r.ID, &r.FirstSeenAt, &r.LastSeenAt, &isCurrent)
	if err != nil {
		return fmt.Errorf("storage: upsert context_participant: %w", err)
	}
	r.IsCurrent = &isCurrent
	return nil
}
