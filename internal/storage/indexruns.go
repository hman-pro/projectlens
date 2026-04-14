package storage

import (
	"context"
	"fmt"
	"time"
)

// IndexRunRecord maps to a row in the index_runs table.
type IndexRunRecord struct {
	ID               int64      `json:"id"`
	StartedAt        time.Time  `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	CommitSHA        string     `json:"commit_sha"`
	FilesProcessed   int        `json:"files_processed"`
	SymbolsExtracted int        `json:"symbols_extracted"`
	EdgesCreated     int        `json:"edges_created"`
	Status           string     `json:"status"`
}

// GitRefRecord maps to a row in the git_refs table.
type GitRefRecord struct {
	ID        int64     `json:"id"`
	Branch    string    `json:"branch"`
	CommitSHA string    `json:"commit_sha"`
	IndexedAt time.Time `json:"indexed_at"`
}

// StartRun inserts a new index run with status "running" and returns its id.
func (db *DB) StartRun(ctx context.Context, commitSHA string) (int64, error) {
	const query = `
		INSERT INTO index_runs (commit_sha, status) VALUES ($1, 'running')
		RETURNING id
	`
	var id int64
	err := db.Pool.QueryRow(ctx, query, commitSHA).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("storage: start run: %w", err)
	}
	return id, nil
}

// CompleteRun marks a run as completed and records its statistics.
func (db *DB) CompleteRun(ctx context.Context, runID int64, filesProcessed, symbolsExtracted, edgesCreated int) error {
	const query = `
		UPDATE index_runs SET
			completed_at      = NOW(),
			status            = 'completed',
			files_processed   = $2,
			symbols_extracted = $3,
			edges_created     = $4
		WHERE id = $1
	`
	tag, err := db.Pool.Exec(ctx, query, runID, filesProcessed, symbolsExtracted, edgesCreated)
	if err != nil {
		return fmt.Errorf("storage: complete run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("storage: complete run: run %d not found", runID)
	}
	return nil
}

// FailRun marks a run as failed.
func (db *DB) FailRun(ctx context.Context, runID int64) error {
	const query = `
		UPDATE index_runs SET
			completed_at = NOW(),
			status       = 'failed'
		WHERE id = $1
	`
	tag, err := db.Pool.Exec(ctx, query, runID)
	if err != nil {
		return fmt.Errorf("storage: fail run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("storage: fail run: run %d not found", runID)
	}
	return nil
}

// GetLatestRun returns the most recent index run.
// Returns nil, nil if no runs exist.
func (db *DB) GetLatestRun(ctx context.Context) (*IndexRunRecord, error) {
	const query = `
		SELECT id, started_at, completed_at, commit_sha,
		       files_processed, symbols_extracted, edges_created, status
		FROM index_runs ORDER BY id DESC LIMIT 1
	`
	r := &IndexRunRecord{}
	err := db.Pool.QueryRow(ctx, query).Scan(
		&r.ID, &r.StartedAt, &r.CompletedAt, &r.CommitSHA,
		&r.FilesProcessed, &r.SymbolsExtracted, &r.EdgesCreated, &r.Status,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get latest run: %w", err)
	}
	return r, nil
}

// UpsertGitRef inserts or updates a git ref keyed by branch.
func (db *DB) UpsertGitRef(ctx context.Context, branch, commitSHA string) error {
	const query = `
		INSERT INTO git_refs (branch, commit_sha)
		VALUES ($1, $2)
		ON CONFLICT (branch) DO UPDATE SET
			commit_sha = EXCLUDED.commit_sha,
			indexed_at = NOW()
	`
	_, err := db.Pool.Exec(ctx, query, branch, commitSHA)
	if err != nil {
		return fmt.Errorf("storage: upsert git ref: %w", err)
	}
	return nil
}

// GetGitRef retrieves a git ref by branch name.
// Returns nil, nil if no row is found.
func (db *DB) GetGitRef(ctx context.Context, branch string) (*GitRefRecord, error) {
	const query = `
		SELECT id, branch, commit_sha, indexed_at
		FROM git_refs WHERE branch = $1
	`
	r := &GitRefRecord{}
	err := db.Pool.QueryRow(ctx, query, branch).Scan(
		&r.ID, &r.Branch, &r.CommitSHA, &r.IndexedAt,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get git ref: %w", err)
	}
	return r, nil
}
