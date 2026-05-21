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
