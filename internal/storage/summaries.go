package storage

import (
	"context"
	"fmt"
	"time"
)

// SummaryRecord maps to a row in the summaries table.
type SummaryRecord struct {
	ID           int64     `json:"id"`
	PackageName  string    `json:"package_name"`
	SummaryText  string    `json:"summary_text"`
	ModelVersion string    `json:"model_version"`
	GeneratedAt  time.Time `json:"generated_at"`
}

// UpsertSummary inserts or updates a package summary keyed by package_name.
func (db *DB) UpsertSummary(ctx context.Context, s *SummaryRecord) error {
	const query = `
		INSERT INTO summaries (package_name, summary_text, model_version)
		VALUES ($1, $2, $3)
		ON CONFLICT (package_name) DO UPDATE SET
			summary_text  = EXCLUDED.summary_text,
			model_version = EXCLUDED.model_version,
			generated_at  = NOW()
	`
	_, err := db.Pool.Exec(ctx, query, s.PackageName, s.SummaryText, s.ModelVersion)
	if err != nil {
		return fmt.Errorf("storage: upsert summary: %w", err)
	}
	return nil
}

// GetSummaryByPackage retrieves the summary for a package.
// Returns nil, nil if no row is found.
func (db *DB) GetSummaryByPackage(ctx context.Context, packageName string) (*SummaryRecord, error) {
	const query = `
		SELECT id, package_name, summary_text, model_version, generated_at
		FROM summaries WHERE package_name = $1
	`
	s := &SummaryRecord{}
	err := db.Pool.QueryRow(ctx, query, packageName).Scan(
		&s.ID, &s.PackageName, &s.SummaryText, &s.ModelVersion, &s.GeneratedAt,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get summary by package: %w", err)
	}
	return s, nil
}
