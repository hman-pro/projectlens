package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PersonRecord maps to a row in people.
type PersonRecord struct {
	ID               int64           `json:"id"`
	DisplayName      *string         `json:"display_name,omitempty"`
	PrimaryEmailHash *string         `json:"primary_email_hash,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// PersonIdentityRecord maps to a row in person_identities.
type PersonIdentityRecord struct {
	ID                int64           `json:"id"`
	PersonID          *int64          `json:"person_id,omitempty"`
	Provider          string          `json:"provider"`
	ExternalAccountID string          `json:"external_account_id"`
	Username          *string         `json:"username,omitempty"`
	DisplayName       *string         `json:"display_name,omitempty"`
	EmailHash         *string         `json:"email_hash,omitempty"`
	ProfileURL        *string         `json:"profile_url,omitempty"`
	ConfidenceClass   string          `json:"confidence_class"`
	Metadata          json.RawMessage `json:"metadata,omitempty"`
	FirstSeenAt       time.Time       `json:"first_seen_at"`
	LastSeenAt        time.Time       `json:"last_seen_at"`
}

// InsertPerson creates a sparse canonical person record.
func (db *DB) InsertPerson(ctx context.Context, r *PersonRecord) error {
	if len(r.Metadata) == 0 {
		r.Metadata = json.RawMessage(`{}`)
	}
	const query = `
		INSERT INTO people (display_name, primary_email_hash, metadata)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at
	`
	err := db.Pool.QueryRow(ctx, query, r.DisplayName, r.PrimaryEmailHash, r.Metadata).
		Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("storage: insert person: %w", err)
	}
	return nil
}

// UpsertPersonIdentity inserts or updates an identity keyed by (provider, external_account_id).
// Note: this never overwrites person_id — call LinkIdentityToPerson for that.
func (db *DB) UpsertPersonIdentity(ctx context.Context, r *PersonIdentityRecord) error {
	if len(r.Metadata) == 0 {
		r.Metadata = json.RawMessage(`{}`)
	}
	if r.ConfidenceClass == "" {
		r.ConfidenceClass = "extracted"
	}
	const query = `
		INSERT INTO person_identities (
			person_id, provider, external_account_id, username, display_name,
			email_hash, profile_url, confidence_class, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (provider, external_account_id) DO UPDATE SET
			username = EXCLUDED.username,
			display_name = EXCLUDED.display_name,
			email_hash = EXCLUDED.email_hash,
			profile_url = EXCLUDED.profile_url,
			confidence_class = EXCLUDED.confidence_class,
			metadata = EXCLUDED.metadata,
			last_seen_at = now()
		RETURNING id, person_id, first_seen_at, last_seen_at
	`
	err := db.Pool.QueryRow(ctx, query,
		r.PersonID, r.Provider, r.ExternalAccountID, r.Username, r.DisplayName,
		r.EmailHash, r.ProfileURL, r.ConfidenceClass, r.Metadata,
	).Scan(&r.ID, &r.PersonID, &r.FirstSeenAt, &r.LastSeenAt)
	if err != nil {
		return fmt.Errorf("storage: upsert person_identity: %w", err)
	}
	return nil
}

// LinkIdentityToPerson sets person_id on an identity row.
// Spec rule: merge must be explicit. There is no auto-merge by display name.
func (db *DB) LinkIdentityToPerson(ctx context.Context, identityID, personID int64) error {
	const query = `UPDATE person_identities SET person_id = $2, last_seen_at = now() WHERE id = $1`
	tag, err := db.Pool.Exec(ctx, query, identityID, personID)
	if err != nil {
		return fmt.Errorf("storage: link identity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("storage: link identity: identity %d not found", identityID)
	}
	return nil
}

// GetPersonIdentity returns nil, nil when not found.
func (db *DB) GetPersonIdentity(ctx context.Context, provider, externalAccountID string) (*PersonIdentityRecord, error) {
	const query = `
		SELECT id, person_id, provider, external_account_id, username, display_name,
		       email_hash, profile_url, confidence_class, metadata, first_seen_at, last_seen_at
		FROM person_identities WHERE provider = $1 AND external_account_id = $2
	`
	r := &PersonIdentityRecord{}
	err := db.Pool.QueryRow(ctx, query, provider, externalAccountID).Scan(
		&r.ID, &r.PersonID, &r.Provider, &r.ExternalAccountID, &r.Username, &r.DisplayName,
		&r.EmailHash, &r.ProfileURL, &r.ConfidenceClass, &r.Metadata, &r.FirstSeenAt, &r.LastSeenAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get person_identity: %w", err)
	}
	return r, nil
}
