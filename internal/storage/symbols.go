package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
	ScipSymbol  *string   `json:"scip_symbol,omitempty"`
	Roles       int       `json:"roles"`
}

// InsertSymbols batch-inserts the provided symbol records using a multi-row INSERT.
// Inserts are batched to stay within PostgreSQL's 65535 parameter limit.
func (db *DB) InsertSymbols(ctx context.Context, symbols []SymbolRecord) error {
	if len(symbols) == 0 {
		return nil
	}

	const cols = 12               // number of columns per row
	const maxBatch = 65535 / cols // 5461 symbols per batch

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
				"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base+1, base+2, base+3, base+4, base+5,
				base+6, base+7, base+8, base+9, base+10,
				base+11, base+12,
			))
			args = append(args,
				s.FileID, s.Name, s.Kind, s.PackageName, s.Receiver,
				s.Signature, s.DocComment, s.LineStart, s.LineEnd, s.Checksum,
				s.ScipSymbol, s.Roles,
			)
		}

		query := fmt.Sprintf(`
			INSERT INTO symbols (file_id, name, kind, package_name, receiver, signature, doc_comment, line_start, line_end, checksum, scip_symbol, roles)
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
		       doc_comment, line_start, line_end, checksum, indexed_at,
		       scip_symbol, roles
		FROM symbols WHERE name = $1
	`
	return db.scanSymbols(ctx, query, name)
}

// GetSymbolsByFileID returns all symbols belonging to the given file.
func (db *DB) GetSymbolsByFileID(ctx context.Context, fileID int64) ([]SymbolRecord, error) {
	const query = `
		SELECT id, file_id, name, kind, package_name, receiver, signature,
		       doc_comment, line_start, line_end, checksum, indexed_at,
		       scip_symbol, roles
		FROM symbols WHERE file_id = $1 ORDER BY line_start
	`
	return db.scanSymbols(ctx, query, fileID)
}

// GetSymbolsByPackage returns all symbols in the given package.
func (db *DB) GetSymbolsByPackage(ctx context.Context, packageName string) ([]SymbolRecord, error) {
	const query = `
		SELECT id, file_id, name, kind, package_name, receiver, signature,
		       doc_comment, line_start, line_end, checksum, indexed_at,
		       scip_symbol, roles
		FROM symbols WHERE package_name = $1 ORDER BY name
	`
	return db.scanSymbols(ctx, query, packageName)
}

// GetExportedSymbolsByPackageLimited returns up to `limit` exported symbols
// in the given package. Exported = name starts with an uppercase ASCII
// letter (Go's export rule). Filtering in SQL avoids the trap where a
// post-fetch cap truncates exported names hidden behind many unexported
// ones in the result set.
func (db *DB) GetExportedSymbolsByPackageLimited(ctx context.Context, packageName string, limit int) ([]SymbolRecord, error) {
	if limit <= 0 {
		limit = 500
	}
	const query = `
		SELECT id, file_id, name, kind, package_name, receiver, signature,
		       doc_comment, line_start, line_end, checksum, indexed_at,
		       scip_symbol, roles
		FROM symbols
		WHERE package_name = $1 AND name ~ '^[A-Z]'
		ORDER BY name LIMIT $2
	`
	return db.scanSymbols(ctx, query, packageName, limit)
}

// CountSymbolsByPackage returns the total symbol count for a package
// (exported + unexported). Used by callers that surface a truncation
// flag alongside a capped result list.
func (db *DB) CountSymbolsByPackage(ctx context.Context, packageName string) (int, error) {
	var n int
	err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM symbols WHERE package_name = $1 AND name ~ '^[A-Z]'`,
		packageName).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("storage: count exported symbols by package: %w", err)
	}
	return n, nil
}

// ResolvePackageName maps a user-facing package input to its canonical
// stored form. Resolution order, all deterministic:
//
//  1. Exact match against `package_name`.
//  2. Last path segment of the input (e.g. "core/supplierfunding" →
//     "supplierfunding") — handles agents that pass import-path style.
//
// No fuzzy / suffix matching: ambiguous resolutions ("funding" vs
// "supplierfunding") are an MCP correctness hazard. Returns ("", nil)
// when nothing matches; only real storage/query errors propagate.
func (db *DB) ResolvePackageName(ctx context.Context, input string) (string, error) {
	if input == "" {
		return "", nil
	}
	found, err := db.lookupPackageName(ctx, input)
	if err != nil {
		return "", err
	}
	if found != "" {
		return found, nil
	}
	if i := strings.LastIndexByte(input, '/'); i >= 0 && i < len(input)-1 {
		short := input[i+1:]
		found, err = db.lookupPackageName(ctx, short)
		if err != nil {
			return "", err
		}
		return found, nil
	}
	return "", nil
}

func (db *DB) lookupPackageName(ctx context.Context, name string) (string, error) {
	var found string
	err := db.Pool.QueryRow(ctx,
		`SELECT package_name FROM symbols WHERE package_name = $1 LIMIT 1`,
		name).Scan(&found)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("storage: lookup package name: %w", err)
	}
	return found, nil
}

// DeleteSymbolsByFileID removes all symbols belonging to a file.
func (db *DB) DeleteSymbolsByFileID(ctx context.Context, fileID int64) error {
	_, err := db.Pool.Exec(ctx, "DELETE FROM symbols WHERE file_id = $1", fileID)
	if err != nil {
		return fmt.Errorf("storage: delete symbols by file id: %w", err)
	}
	return nil
}

// GetDistinctPackageNames returns all unique package names from the symbols table.
func (db *DB) GetDistinctPackageNames(ctx context.Context) ([]string, error) {
	const query = `SELECT DISTINCT package_name FROM symbols ORDER BY package_name`
	rows, err := db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("storage: get distinct package names: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("storage: scan package name: %w", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
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
			&s.ScipSymbol, &s.Roles,
		); err != nil {
			return nil, fmt.Errorf("storage: scan symbol: %w", err)
		}
		symbols = append(symbols, s)
	}
	return symbols, rows.Err()
}
