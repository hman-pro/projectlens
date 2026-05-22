package storage

import (
	"context"
	"fmt"
	"strings"
)

// EdgeRecord maps to a row in the edges table.
type EdgeRecord struct {
	ID              int64    `json:"id"`
	SourceType      string   `json:"source_type"`
	SourceID        int64    `json:"source_id"`
	TargetType      string   `json:"target_type"`
	TargetID        int64    `json:"target_id"`
	EdgeType        string   `json:"edge_type"`
	Properties      *[]byte  `json:"properties,omitempty"`
	Confidence      *float32 `json:"confidence,omitempty"`
	Provenance      string   `json:"provenance,omitempty"`
	ConfidenceClass string   `json:"confidence_class,omitempty"`
}

// nullableString returns nil for an empty string so that the column is
// inserted as SQL NULL rather than an empty literal. Used by edge writers
// that may legitimately leave provenance/confidence_class unset.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// EdgeResult is returned by graph traversal queries and includes the related
// symbol information via a JOIN.
type EdgeResult struct {
	EdgeID          int64  `json:"edge_id"`
	EdgeType        string `json:"edge_type"`
	SymbolID        int64  `json:"symbol_id"`
	SymbolName      string `json:"symbol_name"`
	SymbolKind      string `json:"symbol_kind"`
	PackageName     string `json:"package_name"`
	FilePath        string `json:"file_path"`
	LineStart       int    `json:"line_start"`
	LineEnd         int    `json:"line_end"`
	Provenance      string `json:"provenance,omitempty"`
	ConfidenceClass string `json:"confidence_class,omitempty"`
}

// InsertEdges batch-inserts edge records with ON CONFLICT DO NOTHING.
// Inserts are batched to stay within PostgreSQL's 65535 parameter limit.
func (db *DB) InsertEdges(ctx context.Context, edges []EdgeRecord) error {
	if len(edges) == 0 {
		return nil
	}

	// Deduplicate edges within the input to avoid "ON CONFLICT DO UPDATE
	// command cannot affect row a second time" errors.
	seen := make(map[string]int) // key → index in deduped slice
	deduped := make([]EdgeRecord, 0, len(edges))
	for _, e := range edges {
		key := fmt.Sprintf("%s:%d:%s:%d:%s", e.SourceType, e.SourceID, e.TargetType, e.TargetID, e.EdgeType)
		if _, ok := seen[key]; !ok {
			seen[key] = len(deduped)
			deduped = append(deduped, e)
		}
	}
	edges = deduped

	const cols = 9
	const maxBatch = 65535 / cols // 7281 edges per batch

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
				"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9,
			))
			args = append(args,
				e.SourceType, e.SourceID, e.TargetType, e.TargetID, e.EdgeType,
				e.Properties, e.Confidence, nullableString(e.Provenance), nullableString(e.ConfidenceClass),
			)
		}

		query := fmt.Sprintf(`
			INSERT INTO edges (source_type, source_id, target_type, target_id, edge_type, properties, confidence, provenance, confidence_class)
			VALUES %s
			ON CONFLICT (source_type, source_id, target_type, target_id, edge_type) DO UPDATE SET
				properties = EXCLUDED.properties,
				confidence = EXCLUDED.confidence,
				provenance = EXCLUDED.provenance,
				confidence_class = EXCLUDED.confidence_class
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
		       f.path, s.line_start, s.line_end,
		       COALESCE(e.provenance, ''), COALESCE(e.confidence_class, '')
		FROM edges e
		JOIN symbols s ON s.id = e.source_id
		JOIN files f ON f.id = s.file_id
		WHERE e.target_type = 'symbol' AND e.target_id = $1
		  AND e.source_type = 'symbol'
		ORDER BY s.name
	`
	return db.scanEdgeResults(ctx, query, symbolID)
}

// GetCallees returns symbols that the given symbol calls (outgoing edges where
// the source is symbolID).
func (db *DB) GetCallees(ctx context.Context, symbolID int64) ([]EdgeResult, error) {
	const query = `
		SELECT e.id, e.edge_type, s.id, s.name, s.kind, s.package_name,
		       f.path, s.line_start, s.line_end,
		       COALESCE(e.provenance, ''), COALESCE(e.confidence_class, '')
		FROM edges e
		JOIN symbols s ON s.id = e.target_id
		JOIN files f ON f.id = s.file_id
		WHERE e.source_type = 'symbol' AND e.source_id = $1
		  AND e.target_type = 'symbol'
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
		       f.path, s.line_start, s.line_end,
		       COALESCE(e.provenance, ''), COALESCE(e.confidence_class, '')
		FROM edges e
		JOIN symbols s ON s.id = e.source_id
		JOIN files f ON f.id = s.file_id
		WHERE e.target_type = 'symbol' AND e.target_id = $1
		  AND e.source_type = 'symbol'
		  AND e.edge_type = 'implements'
		ORDER BY s.name
	`
	return db.scanEdgeResults(ctx, query, symbolID)
}

// GetEdgesTargetingDatastoreTable returns symbols that read or write a given datastore table.
func (db *DB) GetEdgesTargetingDatastoreTable(ctx context.Context, tableID int64, edgeType string) ([]EdgeResult, error) {
	const query = `
		SELECT e.id, e.edge_type, s.id, s.name, s.kind, s.package_name,
		       f.path, s.line_start, s.line_end,
		       COALESCE(e.provenance, ''), COALESCE(e.confidence_class, '')
		FROM edges e
		JOIN symbols s ON s.id = e.source_id
		JOIN files f ON f.id = s.file_id
		WHERE e.target_type = 'datastore_table' AND e.target_id = $1
		  AND e.source_type = 'symbol'
		  AND e.edge_type = $2
		ORDER BY s.name
	`
	return db.scanEdgeResults(ctx, query, tableID, edgeType)
}

// DeleteEdgesByType removes all edges matching (source_type, target_type, edge_type).
// Used by the incremental history indexer to clear stale coupling edges before
// recomputing them from the current window.
// This is an unscoped delete: it removes every matching row across the table.
// Returns the number of rows removed.
func (db *DB) DeleteEdgesByType(ctx context.Context, sourceType, targetType, edgeType string) (int64, error) {
	const query = `
		DELETE FROM edges
		WHERE source_type = $1 AND target_type = $2 AND edge_type = $3
	`
	tag, err := db.Pool.Exec(ctx, query, sourceType, targetType, edgeType)
	if err != nil {
		return 0, fmt.Errorf("storage: delete edges by type: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteEdgesBySymbolID removes all edges where the given symbol is either
// source or target.
func (db *DB) DeleteEdgesBySymbolID(ctx context.Context, symbolID int64) error {
	const query = `
		DELETE FROM edges
		WHERE (source_type = 'symbol' AND source_id = $1)
		   OR (target_type = 'symbol' AND target_id = $1)
	`
	_, err := db.Pool.Exec(ctx, query, symbolID)
	if err != nil {
		return fmt.Errorf("storage: delete edges by symbol id: %w", err)
	}
	return nil
}

// CouplingResult represents a co-change coupling edge with the coupled
// file path. Provenance + ConfidenceClass carry the trust class of the
// underlying edge row (typically history/inferred).
type CouplingResult struct {
	FilePath        string  `json:"file_path"`
	Strength        float32 `json:"strength"`
	Provenance      string  `json:"provenance,omitempty"`
	ConfidenceClass string  `json:"confidence_class,omitempty"`
}

// GetCouplingEdges returns files that co-change with the given file.
func (db *DB) GetCouplingEdges(ctx context.Context, fileID int64, minStrength float32) ([]CouplingResult, error) {
	const query = `
		SELECT f.path,
		       COALESCE(e.confidence, 0) as strength,
		       COALESCE(e.provenance, '') AS provenance,
		       COALESCE(e.confidence_class, '') AS confidence_class
		FROM edges e
		JOIN files f ON f.id = CASE
			WHEN e.source_id = $1 AND e.source_type = 'file' THEN e.target_id
			WHEN e.target_id = $1 AND e.target_type = 'file' THEN e.source_id
		END
		WHERE e.edge_type = 'co_changes'
		  AND ((e.source_type = 'file' AND e.source_id = $1) OR (e.target_type = 'file' AND e.target_id = $1))
		  AND COALESCE(e.confidence, 0) >= $2
		ORDER BY strength DESC
	`
	rows, err := db.Pool.Query(ctx, query, fileID, minStrength)
	if err != nil {
		return nil, fmt.Errorf("storage: get coupling edges: %w", err)
	}
	defer rows.Close()

	var results []CouplingResult
	for rows.Next() {
		var r CouplingResult
		if err := rows.Scan(&r.FilePath, &r.Strength, &r.Provenance, &r.ConfidenceClass); err != nil {
			return nil, fmt.Errorf("storage: scan coupling result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// BackfillProvenance fills (provenance, confidence_class) on rows where
// either column is NULL. COALESCE preserves any already-set value so the
// operation is partial-field repair, not blanket overwrite. Returns the
// number of rows touched. Used by `projectlens index-backfill-provenance`
// to apply per-type defaults to edges written before migration 006 and
// to repair partial rows left by older/broken writers.
func (db *DB) BackfillProvenance(ctx context.Context, edgeType, provenance, class string) (int64, error) {
	const query = `
		UPDATE edges
		SET provenance = COALESCE(provenance, $2),
		    confidence_class = COALESCE(confidence_class, $3)
		WHERE edge_type = $1
		  AND (provenance IS NULL OR confidence_class IS NULL)
	`
	tag, err := db.Pool.Exec(ctx, query, edgeType, provenance, class)
	if err != nil {
		return 0, fmt.Errorf("storage: backfill provenance %s: %w", edgeType, err)
	}
	return tag.RowsAffected(), nil
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
			&r.Provenance, &r.ConfidenceClass,
		); err != nil {
			return nil, fmt.Errorf("storage: scan edge result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
