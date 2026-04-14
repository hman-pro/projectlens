package storage

import (
	"context"
	"fmt"
	"time"
)

// FileRecord maps to a row in the files table.
type FileRecord struct {
	ID               int64     `json:"id"`
	Path             string    `json:"path"`
	PackageName      string    `json:"package_name"`
	Checksum         string    `json:"checksum"`
	Language         string    `json:"language"`
	IsGenerated      bool      `json:"is_generated"`
	IsTest           bool      `json:"is_test"`
	LineCount        int       `json:"line_count"`
	HeuristicSummary *string   `json:"heuristic_summary,omitempty"`
	CommitSHA        string    `json:"commit_sha"`
	IndexedAt        time.Time `json:"indexed_at"`
}

// UpsertFile inserts a new file record or updates it if the path already exists.
// It returns the id of the upserted row.
func (db *DB) UpsertFile(ctx context.Context, f *FileRecord) (int64, error) {
	const query = `
		INSERT INTO files (path, package_name, checksum, language, is_generated, is_test, line_count, heuristic_summary, commit_sha)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (path) DO UPDATE SET
			package_name      = EXCLUDED.package_name,
			checksum          = EXCLUDED.checksum,
			language          = EXCLUDED.language,
			is_generated      = EXCLUDED.is_generated,
			is_test           = EXCLUDED.is_test,
			line_count        = EXCLUDED.line_count,
			heuristic_summary = EXCLUDED.heuristic_summary,
			commit_sha        = EXCLUDED.commit_sha,
			indexed_at        = NOW()
		RETURNING id
	`
	var id int64
	err := db.Pool.QueryRow(ctx, query,
		f.Path, f.PackageName, f.Checksum, f.Language,
		f.IsGenerated, f.IsTest, f.LineCount,
		f.HeuristicSummary, f.CommitSHA,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("storage: upsert file: %w", err)
	}
	return id, nil
}

// GetFileByPath retrieves a single file record by its path.
// Returns nil, nil if no row is found.
func (db *DB) GetFileByPath(ctx context.Context, path string) (*FileRecord, error) {
	const query = `
		SELECT id, path, package_name, checksum, language, is_generated, is_test,
		       line_count, heuristic_summary, commit_sha, indexed_at
		FROM files WHERE path = $1
	`
	f := &FileRecord{}
	err := db.Pool.QueryRow(ctx, query, path).Scan(
		&f.ID, &f.Path, &f.PackageName, &f.Checksum, &f.Language,
		&f.IsGenerated, &f.IsTest, &f.LineCount,
		&f.HeuristicSummary, &f.CommitSHA, &f.IndexedAt,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get file by path: %w", err)
	}
	return f, nil
}

// ListFiles returns all file records.
func (db *DB) ListFiles(ctx context.Context) ([]FileRecord, error) {
	const query = `
		SELECT id, path, package_name, checksum, language, is_generated, is_test,
		       line_count, heuristic_summary, commit_sha, indexed_at
		FROM files ORDER BY path
	`
	rows, err := db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("storage: list files: %w", err)
	}
	defer rows.Close()

	var files []FileRecord
	for rows.Next() {
		var f FileRecord
		if err := rows.Scan(
			&f.ID, &f.Path, &f.PackageName, &f.Checksum, &f.Language,
			&f.IsGenerated, &f.IsTest, &f.LineCount,
			&f.HeuristicSummary, &f.CommitSHA, &f.IndexedAt,
		); err != nil {
			return nil, fmt.Errorf("storage: list files scan: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// DeleteStaleFiles removes files whose paths are NOT in the given list.
// It returns the number of rows deleted.
func (db *DB) DeleteStaleFiles(ctx context.Context, currentPaths []string) (int64, error) {
	if len(currentPaths) == 0 {
		// If no current paths, delete everything.
		tag, err := db.Pool.Exec(ctx, "DELETE FROM files")
		if err != nil {
			return 0, fmt.Errorf("storage: delete stale files: %w", err)
		}
		return tag.RowsAffected(), nil
	}

	const query = `DELETE FROM files WHERE path != ALL($1)`
	tag, err := db.Pool.Exec(ctx, query, currentPaths)
	if err != nil {
		return 0, fmt.Errorf("storage: delete stale files: %w", err)
	}
	return tag.RowsAffected(), nil
}
