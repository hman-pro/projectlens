package storage

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SymbolRecord maps to a row in the symbols table.
type SymbolRecord struct {
	ID          int64     `json:"id"`
	FileID      int64     `json:"file_id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	PackageName string    `json:"package_name"`
	Receiver    *string   `json:"receiver,omitempty"`
	Signature   string    `json:"signature"`
	DocComment  *string   `json:"doc_comment,omitempty"`
	LineStart   int       `json:"line_start"`
	LineEnd     int       `json:"line_end"`
	Checksum    string    `json:"checksum"`
	IndexedAt   time.Time `json:"indexed_at"`
}

// InsertSymbols batch-inserts the provided symbol records using a multi-row INSERT.
// Inserts are batched to stay within PostgreSQL's 65535 parameter limit.
func (db *DB) InsertSymbols(ctx context.Context, symbols []SymbolRecord) error {
	if len(symbols) == 0 {
		return nil
	}

	const cols = 10 // number of columns per row
	const maxBatch = 65535 / cols // 6553 symbols per batch

	for start := 0; start < len(symbols); start += maxBatch {
		end := start + maxBatch
		if end > len(symbols) {
			end = len(symbols)
		}
		batch := symbols[start:end]

		valueStrings := make([]string, 0, len(batch))
		args := make([]any, 0, len(batch)*cols)

		for i, s := range batch {
			base := i * cols
			valueStrings = append(valueStrings, fmt.Sprintf(
				"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base+1, base+2, base+3, base+4, base+5,
				base+6, base+7, base+8, base+9, base+10,
			))
			args = append(args,
				s.FileID, s.Name, s.Kind, s.PackageName, s.Receiver,
				s.Signature, s.DocComment, s.LineStart, s.LineEnd, s.Checksum,
			)
		}

		query := fmt.Sprintf(`
			INSERT INTO symbols (file_id, name, kind, package_name, receiver, signature, doc_comment, line_start, line_end, checksum)
			VALUES %s
		`, strings.Join(valueStrings, ", "))

		_, err := db.Pool.Exec(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("storage: insert symbols: %w", err)
		}
	}
	return nil
}

// GetSymbolByName returns all symbols matching the given name.
func (db *DB) GetSymbolByName(ctx context.Context, name string) ([]SymbolRecord, error) {
	const query = `
		SELECT id, file_id, name, kind, package_name, receiver, signature,
		       doc_comment, line_start, line_end, checksum, indexed_at
		FROM symbols WHERE name = $1
	`
	return db.scanSymbols(ctx, query, name)
}

// GetSymbolsByFileID returns all symbols belonging to the given file.
func (db *DB) GetSymbolsByFileID(ctx context.Context, fileID int64) ([]SymbolRecord, error) {
	const query = `
		SELECT id, file_id, name, kind, package_name, receiver, signature,
		       doc_comment, line_start, line_end, checksum, indexed_at
		FROM symbols WHERE file_id = $1 ORDER BY line_start
	`
	return db.scanSymbols(ctx, query, fileID)
}

// GetSymbolsByPackage returns all symbols in the given package.
func (db *DB) GetSymbolsByPackage(ctx context.Context, packageName string) ([]SymbolRecord, error) {
	const query = `
		SELECT id, file_id, name, kind, package_name, receiver, signature,
		       doc_comment, line_start, line_end, checksum, indexed_at
		FROM symbols WHERE package_name = $1 ORDER BY name
	`
	return db.scanSymbols(ctx, query, packageName)
}

// DeleteSymbolsByFileID removes all symbols belonging to a file.
func (db *DB) DeleteSymbolsByFileID(ctx context.Context, fileID int64) error {
	_, err := db.Pool.Exec(ctx, "DELETE FROM symbols WHERE file_id = $1", fileID)
	if err != nil {
		return fmt.Errorf("storage: delete symbols by file id: %w", err)
	}
	return nil
}

// scanSymbols is a helper that executes a query and scans rows into SymbolRecord slices.
func (db *DB) scanSymbols(ctx context.Context, query string, args ...any) ([]SymbolRecord, error) {
	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: query symbols: %w", err)
	}
	defer rows.Close()

	var symbols []SymbolRecord
	for rows.Next() {
		var s SymbolRecord
		if err := rows.Scan(
			&s.ID, &s.FileID, &s.Name, &s.Kind, &s.PackageName,
			&s.Receiver, &s.Signature, &s.DocComment,
			&s.LineStart, &s.LineEnd, &s.Checksum, &s.IndexedAt,
		); err != nil {
			return nil, fmt.Errorf("storage: scan symbol: %w", err)
		}
		symbols = append(symbols, s)
	}
	return symbols, rows.Err()
}
