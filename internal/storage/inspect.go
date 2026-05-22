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

// EdgeConfidenceStat is one row of EdgeConfidenceBreakdown: per
// edge_type counts split by confidence_class. Empty class column maps
// to the "unknown" bucket (rows missing class — should be zero after
// the backfill but tracked for visibility).
type EdgeConfidenceStat struct {
	EdgeType   string
	Provenance string
	Extracted  int
	Inferred   int
	Ambiguous  int
	Unknown    int
	Total      int
}

// EdgeConfidenceBreakdown returns per-edge_type confidence_class counts
// across the edges table. Rows are sorted by total descending.
func (db *DB) EdgeConfidenceBreakdown(ctx context.Context) ([]EdgeConfidenceStat, error) {
	const q = `
		SELECT
		    edge_type,
		    COALESCE(provenance, '') AS provenance,
		    COUNT(*) FILTER (WHERE confidence_class = 'extracted') AS extracted,
		    COUNT(*) FILTER (WHERE confidence_class = 'inferred')  AS inferred,
		    COUNT(*) FILTER (WHERE confidence_class = 'ambiguous') AS ambiguous,
		    COUNT(*) FILTER (WHERE confidence_class IS NULL)       AS unknown,
		    COUNT(*) AS total
		FROM edges
		GROUP BY edge_type, COALESCE(provenance, '')
		ORDER BY total DESC, edge_type ASC
	`
	rows, err := db.Pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("storage: edge confidence breakdown: %w", err)
	}
	defer rows.Close()
	var out []EdgeConfidenceStat
	for rows.Next() {
		var s EdgeConfidenceStat
		if err := rows.Scan(&s.EdgeType, &s.Provenance, &s.Extracted, &s.Inferred, &s.Ambiguous, &s.Unknown, &s.Total); err != nil {
			return nil, fmt.Errorf("storage: scan edge confidence stat: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
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
		ORDER BY (SUM(CASE WHEN re.edge_type = 'reads_table'  THEN 1 ELSE 0 END) +
			  SUM(CASE WHEN re.edge_type = 'writes_table' THEN 1 ELSE 0 END)) DESC, t.name ASC
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

// HighCouplingPairs returns up to N symmetric co-change pairs from
// file_history. Files are paired by shared commit_hash; only the
// canonical (lower file_id < higher file_id) direction is emitted.
// Pairs with fewer than minCount shared commits are filtered out.
func (db *DB) HighCouplingPairs(ctx context.Context, limit, minCount int) ([]CouplingPair, error) {
	if minCount < 1 {
		minCount = 1
	}
	const q = `
		WITH pairs AS (
			SELECT h1.file_id AS a,
			       h2.file_id AS b,
			       COUNT(*)   AS cnt
			FROM file_history h1
			JOIN file_history h2
			  ON h1.commit_hash = h2.commit_hash
			 AND h1.file_id < h2.file_id
			GROUP BY h1.file_id, h2.file_id
			HAVING COUNT(*) >= $2
		)
		SELECT fa.path, fb.path, p.cnt
		FROM pairs p
		JOIN files fa ON fa.id = p.a
		JOIN files fb ON fb.id = p.b
		ORDER BY p.cnt DESC, fa.path ASC, fb.path ASC
		LIMIT $1
	`
	rows, err := db.Pool.Query(ctx, q, limit, minCount)
	if err != nil {
		return nil, fmt.Errorf("storage: coupling: %w", err)
	}
	defer rows.Close()
	var out []CouplingPair
	for rows.Next() {
		var c CouplingPair
		if err := rows.Scan(&c.FileA, &c.FileB, &c.CoChangeCount); err != nil {
			return nil, fmt.Errorf("storage: coupling: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// KnowledgeStatsByCategory returns total counts per category. Categories
// with zero rows are omitted; callers can render missing keys as 0.
func (db *DB) KnowledgeStatsByCategory(ctx context.Context) (map[string]int, error) {
	const q = `SELECT category, COUNT(*) FROM knowledge_entries GROUP BY category`
	rows, err := db.Pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("storage: knowledge stats: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var cat string
		var n int
		if err := rows.Scan(&cat, &n); err != nil {
			return nil, fmt.Errorf("storage: knowledge stats: scan: %w", err)
		}
		out[cat] = n
	}
	return out, rows.Err()
}

// RecentKnowledgeEntries returns the N most recently created entries,
// ordered by created_at DESC. updated_at is not used so edits don't
// surface as "new".
func (db *DB) RecentKnowledgeEntries(ctx context.Context, limit int) ([]KnowledgeSummary, error) {
	const q = `
		SELECT id, title, category, source, created_at
		FROM knowledge_entries
		ORDER BY created_at DESC
		LIMIT $1
	`
	rows, err := db.Pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: recent knowledge: %w", err)
	}
	defer rows.Close()
	var out []KnowledgeSummary
	for rows.Next() {
		var k KnowledgeSummary
		if err := rows.Scan(&k.ID, &k.Title, &k.Category, &k.Source, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("storage: recent knowledge: scan: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
