package retrieval

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/hman-pro/projectlens/internal/storage"
)

// SearchResult represents a single retrieval result that can originate from
// lexical, semantic, or graph-based search.
type SearchResult struct {
	SymbolID     int64   `json:"symbol_id"`
	SymbolName   string  `json:"symbol_name"`
	Kind         string  `json:"kind"`
	PackageName  string  `json:"package_name"`
	FilePath     string  `json:"file_path"`
	LineStart    int     `json:"line_start"`
	LineEnd      int     `json:"line_end"`
	Signature    string  `json:"signature"`
	DocComment   string  `json:"doc_comment"`
	Score        float64 `json:"score"`
	Source       string  `json:"source"`       // "lexical", "semantic", "graph"
	Relationship string  `json:"relationship"` // e.g., "caller", "callee", "implements" — empty for non-graph
	SourceType   string  `json:"source_type"`  // "code", "confluence", "jira", "migration"
	SourceURI    string  `json:"source_uri,omitempty"`
}

// LexicalSearch runs three parallel queries against the symbols table:
//  1. Exact match on name (case-insensitive) — score 10.0
//  2. Prefix match on name (ILIKE query%) — score 5.0
//  3. Path/package contains the query string — score 3.0
//
// Results are deduplicated by symbol ID (keeping the highest score), sorted by
// score descending, and limited to topK.
func LexicalSearch(ctx context.Context, db *storage.DB, query string, topK int) ([]SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("retrieval: lexical search: query must not be empty")
	}
	if topK <= 0 {
		return nil, fmt.Errorf("retrieval: lexical search: topK must be positive")
	}

	type queryResult struct {
		results []SearchResult
		err     error
	}

	var wg sync.WaitGroup
	ch := make(chan queryResult, 3)

	// 1. Exact match (case-insensitive)
	wg.Add(1)
	go func() {
		defer wg.Done()
		results, err := lexicalExactMatch(ctx, db, query)
		ch <- queryResult{results: results, err: err}
	}()

	// 2. Prefix match (ILIKE query%)
	wg.Add(1)
	go func() {
		defer wg.Done()
		results, err := lexicalPrefixMatch(ctx, db, query)
		ch <- queryResult{results: results, err: err}
	}()

	// 3. Path/package contains
	wg.Add(1)
	go func() {
		defer wg.Done()
		results, err := lexicalPathPackageMatch(ctx, db, query)
		ch <- queryResult{results: results, err: err}
	}()

	wg.Wait()
	close(ch)

	var all []SearchResult
	for qr := range ch {
		if qr.err != nil {
			return nil, qr.err
		}
		all = append(all, qr.results...)
	}

	merged := deduplicateBySymbolID(all)
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	if len(merged) > topK {
		merged = merged[:topK]
	}
	return merged, nil
}

// lexicalExactMatch finds symbols whose name matches the query exactly (case-insensitive).
func lexicalExactMatch(ctx context.Context, db *storage.DB, query string) ([]SearchResult, error) {
	const q = `
		SELECT s.id, s.name, s.kind, s.package_name, f.path,
		       s.line_start, s.line_end, s.signature, s.doc_comment
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE LOWER(s.name) = LOWER($1)
	`
	return scanLexicalRows(ctx, db, q, 10.0, query)
}

// lexicalPrefixMatch finds symbols whose name starts with the query (case-insensitive).
func lexicalPrefixMatch(ctx context.Context, db *storage.DB, query string) ([]SearchResult, error) {
	const q = `
		SELECT s.id, s.name, s.kind, s.package_name, f.path,
		       s.line_start, s.line_end, s.signature, s.doc_comment
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE s.name ILIKE $1
	`
	pattern := query + "%"
	return scanLexicalRows(ctx, db, q, 5.0, pattern)
}

// lexicalPathPackageMatch finds symbols where the file path or package name
// contains the query string (case-insensitive).
func lexicalPathPackageMatch(ctx context.Context, db *storage.DB, query string) ([]SearchResult, error) {
	const q = `
		SELECT s.id, s.name, s.kind, s.package_name, f.path,
		       s.line_start, s.line_end, s.signature, s.doc_comment
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE f.path ILIKE $1 OR s.package_name ILIKE $1
	`
	pattern := "%" + query + "%"
	return scanLexicalRows(ctx, db, q, 3.0, pattern)
}

// scanLexicalRows executes a query and scans into SearchResult with the given score.
func scanLexicalRows(ctx context.Context, db *storage.DB, query string, score float64, args ...any) ([]SearchResult, error) {
	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("retrieval: lexical query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var docComment *string
		if err := rows.Scan(
			&r.SymbolID, &r.SymbolName, &r.Kind, &r.PackageName, &r.FilePath,
			&r.LineStart, &r.LineEnd, &r.Signature, &docComment,
		); err != nil {
			return nil, fmt.Errorf("retrieval: lexical scan: %w", err)
		}
		if docComment != nil {
			r.DocComment = *docComment
		}
		r.Score = score
		r.Source = "lexical"
		r.SourceType = "code"
		results = append(results, r)
	}
	return results, rows.Err()
}

// deduplicateBySymbolID merges results, keeping the highest score per symbol ID.
func deduplicateBySymbolID(results []SearchResult) []SearchResult {
	best := make(map[int64]SearchResult)
	for _, r := range results {
		if existing, ok := best[r.SymbolID]; !ok || r.Score > existing.Score {
			best[r.SymbolID] = r
		}
	}
	out := make([]SearchResult, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	return out
}

// symbolRecordToSearchResult converts a storage.SymbolRecord to a SearchResult.
// The filePath must be supplied separately since SymbolRecord does not include it.
func symbolRecordToSearchResult(s storage.SymbolRecord, filePath string, score float64, source, relationship string) SearchResult {
	var doc string
	if s.DocComment != nil {
		doc = *s.DocComment
	}
	return SearchResult{
		SymbolID:     s.ID,
		SymbolName:   s.Name,
		Kind:         s.Kind,
		PackageName:  s.PackageName,
		FilePath:     filePath,
		LineStart:    s.LineStart,
		LineEnd:      s.LineEnd,
		Signature:    s.Signature,
		DocComment:   doc,
		Score:        score,
		Source:       source,
		Relationship: relationship,
		SourceType:   "code",
	}
}
