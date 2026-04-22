package storage

import (
	"context"
	"fmt"
	"time"
)

// SymbolHistoryRecord maps to a row in the symbol_history table.
type SymbolHistoryRecord struct {
	ID          int64     `json:"id"`
	SymbolID    int64     `json:"symbol_id"`
	CommitHash  string    `json:"commit_hash"`
	Author      string    `json:"author"`
	CommittedAt time.Time `json:"committed_at"`
	ChangeType  string    `json:"change_type"`
	DiffSnippet *string   `json:"diff_snippet,omitempty"`
}

// FileHistoryRecord maps to a row in the file_history table.
type FileHistoryRecord struct {
	ID          int64     `json:"id"`
	FileID      int64     `json:"file_id"`
	CommitHash  string    `json:"commit_hash"`
	Author      string    `json:"author"`
	CommittedAt time.Time `json:"committed_at"`
	ChangeType  string    `json:"change_type"`
	DiffSnippet *string   `json:"diff_snippet,omitempty"`
}

// InsertSymbolHistory inserts a symbol history record with ON CONFLICT DO NOTHING.
func (db *DB) InsertSymbolHistory(ctx context.Context, r *SymbolHistoryRecord) error {
	const query = `
		INSERT INTO symbol_history (symbol_id, commit_hash, author, committed_at, change_type, diff_snippet)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (symbol_id, commit_hash) DO NOTHING
	`
	_, err := db.Pool.Exec(ctx, query, r.SymbolID, r.CommitHash, r.Author, r.CommittedAt, r.ChangeType, r.DiffSnippet)
	if err != nil {
		return fmt.Errorf("storage: insert symbol history: %w", err)
	}
	return nil
}

// GetSymbolHistory returns the most recent history records for a symbol, up to limit.
func (db *DB) GetSymbolHistory(ctx context.Context, symbolID int64, limit int) ([]SymbolHistoryRecord, error) {
	const query = `
		SELECT id, symbol_id, commit_hash, author, committed_at, change_type, diff_snippet
		FROM symbol_history WHERE symbol_id = $1
		ORDER BY committed_at DESC
		LIMIT $2
	`
	rows, err := db.Pool.Query(ctx, query, symbolID, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: get symbol history: %w", err)
	}
	defer rows.Close()

	var results []SymbolHistoryRecord
	for rows.Next() {
		var r SymbolHistoryRecord
		if err := rows.Scan(&r.ID, &r.SymbolID, &r.CommitHash, &r.Author, &r.CommittedAt, &r.ChangeType, &r.DiffSnippet); err != nil {
			return nil, fmt.Errorf("storage: scan symbol history: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// InsertFileHistory inserts a file history record with ON CONFLICT DO NOTHING.
func (db *DB) InsertFileHistory(ctx context.Context, r *FileHistoryRecord) error {
	const query = `
		INSERT INTO file_history (file_id, commit_hash, author, committed_at, change_type, diff_snippet)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (file_id, commit_hash) DO NOTHING
	`
	_, err := db.Pool.Exec(ctx, query, r.FileID, r.CommitHash, r.Author, r.CommittedAt, r.ChangeType, r.DiffSnippet)
	if err != nil {
		return fmt.Errorf("storage: insert file history: %w", err)
	}
	return nil
}

// GetFileHistory returns the most recent history records for a file, up to limit.
func (db *DB) GetFileHistory(ctx context.Context, fileID int64, limit int) ([]FileHistoryRecord, error) {
	const query = `
		SELECT id, file_id, commit_hash, author, committed_at, change_type, diff_snippet
		FROM file_history WHERE file_id = $1
		ORDER BY committed_at DESC
		LIMIT $2
	`
	rows, err := db.Pool.Query(ctx, query, fileID, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: get file history: %w", err)
	}
	defer rows.Close()

	var results []FileHistoryRecord
	for rows.Next() {
		var r FileHistoryRecord
		if err := rows.Scan(&r.ID, &r.FileID, &r.CommitHash, &r.Author, &r.CommittedAt, &r.ChangeType, &r.DiffSnippet); err != nil {
			return nil, fmt.Errorf("storage: scan file history: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// EvictOldSymbolHistory removes symbol_history entries beyond maxPerSymbol for each symbol.
func (db *DB) EvictOldSymbolHistory(ctx context.Context, maxPerSymbol int) error {
	const query = `
		DELETE FROM symbol_history
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY committed_at DESC) AS rn
				FROM symbol_history
			) ranked
			WHERE rn > $1
		)
	`
	_, err := db.Pool.Exec(ctx, query, maxPerSymbol)
	if err != nil {
		return fmt.Errorf("storage: evict old symbol history: %w", err)
	}
	return nil
}

// GetLatestFileHistoryTimestamp returns the most recent committed_at across
// all file_history rows. Returns (zero, false, nil) if the table is empty.
func (db *DB) GetLatestFileHistoryTimestamp(ctx context.Context) (time.Time, bool, error) {
	const query = `SELECT MAX(committed_at) FROM file_history`
	var ts *time.Time
	if err := db.Pool.QueryRow(ctx, query).Scan(&ts); err != nil {
		return time.Time{}, false, fmt.Errorf("storage: latest file_history timestamp: %w", err)
	}
	if ts == nil {
		return time.Time{}, false, nil
	}
	return *ts, true, nil
}

// EvictOldFileHistory removes file_history entries beyond maxPerFile for each file.
func (db *DB) EvictOldFileHistory(ctx context.Context, maxPerFile int) error {
	const query = `
		DELETE FROM file_history
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY file_id ORDER BY committed_at DESC) AS rn
				FROM file_history
			) ranked
			WHERE rn > $1
		)
	`
	_, err := db.Pool.Exec(ctx, query, maxPerFile)
	if err != nil {
		return fmt.Errorf("storage: evict old file history: %w", err)
	}
	return nil
}
