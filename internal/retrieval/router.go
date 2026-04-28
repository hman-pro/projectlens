package retrieval

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/hman-pro/projectlens/internal/rerank"
	"github.com/hman-pro/projectlens/internal/storage"
)

// rankSearchResults applies rerank scoring adjustments to SearchResult copies
// and returns them sorted by final score descending. It bridges between
// retrieval.SearchResult and rerank.Result to avoid an import cycle.
func rankSearchResults(results []SearchResult, query string, isTestQuery bool) []SearchResult {
	if len(results) == 0 {
		return results
	}

	// Convert to rerank.Result with an encoded original index in the Score
	// field's fractional bits. Instead, we use a parallel index approach:
	// rank the rerank.Results, then match each ranked result back to its
	// original SearchResult by finding which original has the same
	// (SymbolName, FilePath, PackageName, original score).

	// Step 1: Build rerank input and remember original scores.
	rr := make([]rerank.Result, len(results))
	origScores := make([]float64, len(results))
	for i, r := range results {
		origScores[i] = r.Score
		rr[i] = rerank.Result{
			SymbolName:  r.SymbolName,
			PackageName: r.PackageName,
			FilePath:    r.FilePath,
			Score:       r.Score,
		}
	}

	ranked := rerank.Rank(rr, query, isTestQuery)

	// Step 2: For each ranked result, find the original SearchResult.
	// We mark used indices to handle duplicates correctly.
	used := make([]bool, len(results))
	out := make([]SearchResult, len(ranked))

	for i, rRes := range ranked {
		// Compute what the original score must have been:
		// We can't easily reverse the adjustment, so instead match on the
		// non-score fields and pick the best unused match.
		for j, orig := range results {
			if used[j] {
				continue
			}
			if orig.SymbolName == rRes.SymbolName &&
				orig.FilePath == rRes.FilePath &&
				orig.PackageName == rRes.PackageName {
				out[i] = orig
				out[i].Score = rRes.Score
				used[j] = true
				break
			}
		}
	}

	return out
}

// QueryType classifies the intent behind a user query.
type QueryType string

const (
	// ExactSymbol indicates the query is a single Go identifier (e.g. "ReserveInventory").
	ExactSymbol QueryType = "exact_symbol"
	// ImplementationSearch indicates a general natural-language query.
	ImplementationSearch QueryType = "implementation_search"
	// PackageOverview indicates the query asks about a package.
	PackageOverview QueryType = "package_overview"
	// DependencyTrace indicates the query asks about callers/callees/dependencies.
	DependencyTrace QueryType = "dependency_trace"
)

// Router orchestrates retrieval by classifying queries and dispatching to the
// appropriate search backends (lexical, semantic, graph).
type Router struct {
	db       *storage.DB
	embedder QueryEmbedder
}

// QueryResult holds the classified query type and the ranked results.
type QueryResult struct {
	QueryType QueryType      `json:"query_type"`
	Results   []SearchResult `json:"results"`
}

// NewRouter creates a Router with the given database and embedder.
func NewRouter(db *storage.DB, embedder QueryEmbedder) *Router {
	return &Router{db: db, embedder: embedder}
}

// dependencyPatterns are phrases that indicate a dependency trace query.
var dependencyPatterns = []string{
	"what calls",
	"callers of",
	"depends on",
	"what uses",
	"who calls",
}

// goIdentifierPattern matches a single CamelCase Go identifier that starts
// with an uppercase letter (exported symbol).
var goIdentifierPattern = regexp.MustCompile(`^[A-Z][a-zA-Z0-9]*$`)

// ClassifyQuery determines the QueryType for a given query string using
// heuristic rules.
func ClassifyQuery(query string) QueryType {
	if query == "" {
		return ImplementationSearch
	}

	queryLower := strings.ToLower(query)

	// Check dependency trace patterns first.
	for _, pattern := range dependencyPatterns {
		if strings.Contains(queryLower, pattern) {
			return DependencyTrace
		}
	}

	// Package overview: contains "package" keyword or looks like a path.
	if strings.Contains(queryLower, "package") {
		return PackageOverview
	}
	// If the query (or a token in it) contains '/', treat as package path.
	for _, word := range strings.Fields(query) {
		if strings.Contains(word, "/") {
			return PackageOverview
		}
	}

	// Exact symbol: single word matching Go exported identifier pattern.
	trimmed := strings.TrimSpace(query)
	if !strings.Contains(trimmed, " ") && isGoIdentifier(trimmed) {
		return ExactSymbol
	}

	return ImplementationSearch
}

// isGoIdentifier checks whether s looks like a Go exported identifier:
// starts with uppercase letter, followed by letters and digits.
func isGoIdentifier(s string) bool {
	if s == "" {
		return false
	}
	if !unicode.IsUpper(rune(s[0])) {
		return false
	}
	return goIdentifierPattern.MatchString(s)
}

// Query classifies the query, dispatches retrieval, ranks, and returns top-K results.
func (r *Router) Query(ctx context.Context, query string, topK int) (*QueryResult, error) {
	qtype := ClassifyQuery(query)
	isTestQuery := strings.Contains(strings.ToLower(query), "test")

	var results []SearchResult
	var err error

	switch qtype {
	case ExactSymbol:
		results, err = r.queryExactSymbol(ctx, query, topK)
	case ImplementationSearch:
		results, err = r.queryImplementation(ctx, query, topK)
	case PackageOverview:
		results, err = r.queryPackageOverview(ctx, query, topK)
	case DependencyTrace:
		results, err = r.queryDependencyTrace(ctx, query, topK)
	default:
		results, err = r.queryImplementation(ctx, query, topK)
	}

	if err != nil {
		return nil, fmt.Errorf("retrieval: router query (%s): %w", qtype, err)
	}

	ranked := rankSearchResults(results, query, isTestQuery)

	if len(ranked) > topK {
		ranked = ranked[:topK]
	}

	return &QueryResult{
		QueryType: qtype,
		Results:   ranked,
	}, nil
}

// queryExactSymbol runs lexical search only for exact symbol queries.
func (r *Router) queryExactSymbol(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return LexicalSearch(ctx, r.db, query, topK)
}

// queryImplementation runs lexical and semantic search in parallel and merges.
func (r *Router) queryImplementation(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	type result struct {
		results []SearchResult
		err     error
	}

	var wg sync.WaitGroup
	ch := make(chan result, 2)

	// Lexical search.
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := LexicalSearch(ctx, r.db, query, topK)
		ch <- result{results: res, err: err}
	}()

	// Semantic search.
	if r.embedder != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := SemanticSearch(ctx, r.db, r.embedder, query, topK)
			ch <- result{results: res, err: err}
		}()
	}

	wg.Wait()
	close(ch)

	var all []SearchResult
	for res := range ch {
		if res.err != nil {
			return nil, res.err
		}
		all = append(all, res.results...)
	}

	// Deduplicate by symbol ID, keeping the highest score.
	merged := deduplicateBySymbolID(all)
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	return merged, nil
}

// queryPackageOverview runs lexical search for the package and includes
// the package summary if available.
func (r *Router) queryPackageOverview(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	// Extract the package path from the query.
	pkg := extractQueryPackage(query)
	if pkg == "" {
		// Fall back to using the query itself.
		pkg = query
	}

	results, err := LexicalSearch(ctx, r.db, pkg, topK)
	if err != nil {
		return nil, err
	}

	// Try to get the package summary. This is informational; we don't fail
	// the whole query if it's not found.
	summary, _ := r.db.GetSummaryByPackage(ctx, pkg)
	if summary != nil {
		// Insert a synthetic result for the summary.
		results = append(results, SearchResult{
			SymbolName:  pkg,
			Kind:        "package_summary",
			PackageName: pkg,
			DocComment:  summary.SummaryText,
			Score:       8.0,
			Source:      "summary",
		})
	}

	return results, nil
}

// queryDependencyTrace finds the symbol via lexical search, then retrieves
// callers and callees from the graph.
func (r *Router) queryDependencyTrace(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	// Extract the symbol name from the dependency query.
	symbolName := extractSymbolFromDependencyQuery(query)
	if symbolName == "" {
		return nil, fmt.Errorf("could not extract symbol name from dependency query: %q", query)
	}

	// Find the symbol via lexical search.
	lexResults, err := LexicalSearch(ctx, r.db, symbolName, 5)
	if err != nil {
		return nil, err
	}
	if len(lexResults) == 0 {
		return nil, nil
	}

	// Use the top result's symbol ID for graph traversal.
	symbolID := lexResults[0].SymbolID

	var all []SearchResult

	// Get callers (depth 2).
	callers, err := GetCallers(ctx, r.db, symbolID, 2)
	if err != nil {
		return nil, fmt.Errorf("get callers: %w", err)
	}
	all = append(all, callers...)

	// Get callees (depth 2).
	callees, err := GetCallees(ctx, r.db, symbolID, 2)
	if err != nil {
		return nil, fmt.Errorf("get callees: %w", err)
	}
	all = append(all, callees...)

	// Include the symbol itself.
	all = append(all, lexResults[0])

	return all, nil
}

// extractQueryPackage extracts a package-like path from a query.
// It looks for tokens containing '/' or the word after "package".
func extractQueryPackage(query string) string {
	words := strings.Fields(query)
	for i, w := range words {
		if strings.Contains(w, "/") {
			return w
		}
		if strings.ToLower(w) == "package" && i+1 < len(words) {
			return words[i+1]
		}
	}
	return ""
}

// EmbedQuery embeds a single query string via the configured embedder and
// returns its 1024-dim float32 vector. Returns an error if no embedder is
// configured or the embedder returns no vectors.
func (r *Router) EmbedQuery(ctx context.Context, q string) ([]float32, error) {
	if r.embedder == nil {
		return nil, fmt.Errorf("retrieval: no embedder configured")
	}
	out, err := r.embedder.EmbedBatch(ctx, []string{q})
	if err != nil {
		return nil, fmt.Errorf("retrieval: embed query: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("retrieval: empty embedding")
	}
	return out[0], nil
}

// extractSymbolFromDependencyQuery extracts the symbol name from a dependency
// trace query like "what calls ProcessPayment" or "callers of HandleRequest".
func extractSymbolFromDependencyQuery(query string) string {
	queryLower := strings.ToLower(query)

	// Try to match known patterns and extract the symbol after them.
	for _, pattern := range dependencyPatterns {
		idx := strings.Index(queryLower, pattern)
		if idx >= 0 {
			after := strings.TrimSpace(query[idx+len(pattern):])
			words := strings.Fields(after)
			if len(words) > 0 {
				return words[0]
			}
		}
	}

	// Fallback: return the last word if it looks like a symbol.
	words := strings.Fields(query)
	if len(words) > 0 {
		last := words[len(words)-1]
		if isGoIdentifier(last) {
			return last
		}
	}

	return ""
}
