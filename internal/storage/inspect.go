package storage

import (
	"context"
	"fmt"
	"time"
)

// PackageStat is one row of TopPackagesBySymbolCount.
type PackageStat struct {
	ImportPath  string
	SymbolCount int
	FileCount   int
}

// TableStat is one row of TopDatastoreTablesByEdgeCount.
type TableStat struct {
	Schema          string
	Name            string
	Engine          string
	ReadRefs        int
	WriteRefs       int
	SourceFileCount int
}

// CouplingPair is one row of HighCouplingPairs.
type CouplingPair struct {
	FileA         string
	FileB         string
	CoChangeCount int
}

// KnowledgeSummary is one row of RecentKnowledgeEntries.
type KnowledgeSummary struct {
	ID        int64
	Title     string
	Category  string
	Source    string
	CreatedAt time.Time
}

// TopPackagesBySymbolCount returns the top N packages by symbol count,
// with the number of distinct files per package.
func (db *DB) TopPackagesBySymbolCount(ctx context.Context, limit int) ([]PackageStat, error) {
	const q = `
		SELECT package_name,
		       COUNT(*) AS symbol_count,
		       COUNT(DISTINCT file_id) AS file_count
		FROM symbols
		GROUP BY package_name
		ORDER BY symbol_count DESC, package_name ASC
		LIMIT $1
	`
	rows, err := db.Pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: top packages: %w", err)
	}
	defer rows.Close()
	var out []PackageStat
	for rows.Next() {
		var p PackageStat
		if err := rows.Scan(&p.ImportPath, &p.SymbolCount, &p.FileCount); err != nil {
			return nil, fmt.Errorf("storage: top packages: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// TopDatastoreTablesByEdgeCount returns the top N tables by total
// read/write edge count. SourceFileCount is the count of distinct
// files containing any read/write reference, resolved via
// symbols.file_id for source_type='symbol' edges (the only producer
// today) and edges.source_id directly for source_type='file' edges.
func (db *DB) TopDatastoreTablesByEdgeCount(ctx context.Context, limit int) ([]TableStat, error) {
	const q = `
		WITH ref_edges AS (
			SELECT e.target_id,
			       e.edge_type,
			       CASE
				   WHEN e.source_type = 'symbol' THEN s.file_id
				   WHEN e.source_type = 'file'   THEN e.source_id
				   ELSE NULL
			       END AS projected_file_id
			FROM edges e
			LEFT JOIN symbols s ON e.source_type = 'symbol' AND s.id = e.source_id
			WHERE e.target_type = 'datastore_table'
			  AND e.edge_type IN ('reads_table', 'writes_table')
		)
		SELECT t.schema_name,
		       t.name,
		       t.engine,
		       SUM(CASE WHEN re.edge_type = 'reads_table'  THEN 1 ELSE 0 END) AS read_refs,
		       SUM(CASE WHEN re.edge_type = 'writes_table' THEN 1 ELSE 0 END) AS write_refs,
		       COUNT(DISTINCT re.projected_file_id) FILTER (WHERE re.projected_file_id IS NOT NULL) AS source_file_count
		FROM datastore_tables t
		JOIN ref_edges re ON re.target_id = t.id
		GROUP BY t.id, t.schema_name, t.name, t.engine
		ORDER BY (read_refs + write_refs) DESC, t.name ASC
		LIMIT $1
	`
	rows, err := db.Pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: top tables: %w", err)
	}
	defer rows.Close()
	var out []TableStat
	for rows.Next() {
		var ts TableStat
		var schema *string
		if err := rows.Scan(&schema, &ts.Name, &ts.Engine, &ts.ReadRefs, &ts.WriteRefs, &ts.SourceFileCount); err != nil {
			return nil, fmt.Errorf("storage: top tables: scan: %w", err)
		}
		if schema != nil {
			ts.Schema = *schema
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}
