package storage

import (
	"context"
	"fmt"
	"strings"
)

// EdgeRecord maps to a row in the edges table.
type EdgeRecord struct {
	ID             int64  `json:"id"`
	SourceSymbolID int64  `json:"source_symbol_id"`
	TargetSymbolID int64  `json:"target_symbol_id"`
	EdgeType       string `json:"edge_type"`
}

// EdgeResult is returned by graph traversal queries and includes the related
// symbol information via a JOIN.
type EdgeResult struct {
	EdgeID      int64  `json:"edge_id"`
	EdgeType    string `json:"edge_type"`
	SymbolID    int64  `json:"symbol_id"`
	SymbolName  string `json:"symbol_name"`
	SymbolKind  string `json:"symbol_kind"`
	PackageName string `json:"package_name"`
	FilePath    string `json:"file_path"`
	LineStart   int    `json:"line_start"`
	LineEnd     int    `json:"line_end"`
}

// InsertEdges batch-inserts edge records with ON CONFLICT DO NOTHING.
// Inserts are batched to stay within PostgreSQL's 65535 parameter limit.
func (db *DB) InsertEdges(ctx context.Context, edges []EdgeRecord) error {
	if len(edges) == 0 {
		return nil
	}

	const cols = 3
	const maxBatch = 65535 / cols // 21845 edges per batch

	for start := 0; start < len(edges); start += maxBatch {
		end := start + maxBatch
		if end > len(edges) {
			end = len(edges)
		}
		batch := edges[start:end]

		valueStrings := make([]string, 0, len(batch))
		args := make([]any, 0, len(batch)*cols)

		for i, e := range batch {
			base := i * cols
			valueStrings = append(valueStrings, fmt.Sprintf(
				"($%d, $%d, $%d)", base+1, base+2, base+3,
			))
			args = append(args, e.SourceSymbolID, e.TargetSymbolID, e.EdgeType)
		}

		query := fmt.Sprintf(`
			INSERT INTO edges (source_symbol_id, target_symbol_id, edge_type)
			VALUES %s
			ON CONFLICT (source_symbol_id, target_symbol_id, edge_type) DO NOTHING
		`, strings.Join(valueStrings, ", "))

		_, err := db.Pool.Exec(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("storage: insert edges: %w", err)
		}
	}
	return nil
}

// GetCallers returns symbols that call the given symbol (incoming edges where
// the target is symbolID).
func (db *DB) GetCallers(ctx context.Context, symbolID int64) ([]EdgeResult, error) {
	const query = `
		SELECT e.id, e.edge_type, s.id, s.name, s.kind, s.package_name,
		       f.path, s.line_start, s.line_end
		FROM edges e
		JOIN symbols s ON s.id = e.source_symbol_id
		JOIN files f ON f.id = s.file_id
		WHERE e.target_symbol_id = $1
		ORDER BY s.name
	`
	return db.scanEdgeResults(ctx, query, symbolID)
}

// GetCallees returns symbols that the given symbol calls (outgoing edges where
// the source is symbolID).
func (db *DB) GetCallees(ctx context.Context, symbolID int64) ([]EdgeResult, error) {
	const query = `
		SELECT e.id, e.edge_type, s.id, s.name, s.kind, s.package_name,
		       f.path, s.line_start, s.line_end
		FROM edges e
		JOIN symbols s ON s.id = e.target_symbol_id
		JOIN files f ON f.id = s.file_id
		WHERE e.source_symbol_id = $1
		ORDER BY s.name
	`
	return db.scanEdgeResults(ctx, query, symbolID)
}

// GetImplementors returns symbols that implement the given interface symbol.
// It looks for edges with edge_type = 'implements' where the target is the
// interface symbol.
func (db *DB) GetImplementors(ctx context.Context, symbolID int64) ([]EdgeResult, error) {
	const query = `
		SELECT e.id, e.edge_type, s.id, s.name, s.kind, s.package_name,
		       f.path, s.line_start, s.line_end
		FROM edges e
		JOIN symbols s ON s.id = e.source_symbol_id
		JOIN files f ON f.id = s.file_id
		WHERE e.target_symbol_id = $1 AND e.edge_type = 'implements'
		ORDER BY s.name
	`
	return db.scanEdgeResults(ctx, query, symbolID)
}

// DeleteEdgesBySymbolID removes all edges where the given symbol is either
// source or target.
func (db *DB) DeleteEdgesBySymbolID(ctx context.Context, symbolID int64) error {
	const query = `DELETE FROM edges WHERE source_symbol_id = $1 OR target_symbol_id = $1`
	_, err := db.Pool.Exec(ctx, query, symbolID)
	if err != nil {
		return fmt.Errorf("storage: delete edges by symbol id: %w", err)
	}
	return nil
}

// scanEdgeResults is a helper that scans rows into EdgeResult slices.
func (db *DB) scanEdgeResults(ctx context.Context, query string, args ...any) ([]EdgeResult, error) {
	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: query edges: %w", err)
	}
	defer rows.Close()

	var results []EdgeResult
	for rows.Next() {
		var r EdgeResult
		if err := rows.Scan(
			&r.EdgeID, &r.EdgeType, &r.SymbolID, &r.SymbolName,
			&r.SymbolKind, &r.PackageName, &r.FilePath,
			&r.LineStart, &r.LineEnd,
		); err != nil {
			return nil, fmt.Errorf("storage: scan edge result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
