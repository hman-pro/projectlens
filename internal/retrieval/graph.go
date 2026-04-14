package retrieval

import (
	"context"
	"fmt"

	"github.com/hman-pro/projectlens/internal/storage"
)

// GetCallers returns symbols that call the given symbol, traversing up to
// maxDepth levels via BFS. Depth 1 callers get score 3.0, depth 2 get 1.0.
func GetCallers(ctx context.Context, db *storage.DB, symbolID int64, maxDepth int) ([]SearchResult, error) {
	if maxDepth <= 0 {
		return nil, fmt.Errorf("retrieval: get callers: maxDepth must be positive")
	}
	return bfsTraverse(ctx, db, symbolID, maxDepth, "caller", db.GetCallers)
}

// GetCallees returns symbols that the given symbol calls, traversing up to
// maxDepth levels via BFS. Depth 1 callees get score 3.0, depth 2 get 1.0.
func GetCallees(ctx context.Context, db *storage.DB, symbolID int64, maxDepth int) ([]SearchResult, error) {
	if maxDepth <= 0 {
		return nil, fmt.Errorf("retrieval: get callees: maxDepth must be positive")
	}
	return bfsTraverse(ctx, db, symbolID, maxDepth, "callee", db.GetCallees)
}

// edgeFetcher is a function that fetches edges for a given symbol.
type edgeFetcher func(ctx context.Context, symbolID int64) ([]storage.EdgeResult, error)

// bfsTraverse performs a breadth-first traversal of the call graph up to the
// given depth. It uses the provided fetcher function to get edges at each level.
func bfsTraverse(ctx context.Context, db *storage.DB, rootID int64, maxDepth int, relationship string, fetch edgeFetcher) ([]SearchResult, error) {
	visited := map[int64]bool{rootID: true}
	var results []SearchResult

	// Current frontier: symbol IDs to expand
	frontier := []int64{rootID}

	for depth := 1; depth <= maxDepth && len(frontier) > 0; depth++ {
		score := scoreForDepth(depth)
		var nextFrontier []int64

		for _, symID := range frontier {
			edges, err := fetch(ctx, symID)
			if err != nil {
				return nil, fmt.Errorf("retrieval: graph traversal at depth %d: %w", depth, err)
			}

			for _, e := range edges {
				if visited[e.SymbolID] {
					continue
				}
				visited[e.SymbolID] = true

				results = append(results, edgeResultToSearchResult(e, score, relationship))
				nextFrontier = append(nextFrontier, e.SymbolID)
			}
		}

		frontier = nextFrontier
	}

	return results, nil
}

// scoreForDepth returns the score for a given BFS depth level.
func scoreForDepth(depth int) float64 {
	switch depth {
	case 1:
		return 3.0
	case 2:
		return 1.0
	default:
		return 0.5
	}
}

// GetImplementors returns symbols that implement the given interface symbol.
// All results receive score 5.0.
func GetImplementors(ctx context.Context, db *storage.DB, symbolID int64) ([]SearchResult, error) {
	edges, err := db.GetImplementors(ctx, symbolID)
	if err != nil {
		return nil, fmt.Errorf("retrieval: get implementors: %w", err)
	}

	results := make([]SearchResult, 0, len(edges))
	for _, e := range edges {
		results = append(results, edgeResultToSearchResult(e, 5.0, "implements"))
	}
	return results, nil
}

// GetPackageDeps finds all symbols in the given package and then retrieves
// their depends_on edges.
func GetPackageDeps(ctx context.Context, db *storage.DB, packageName string) ([]SearchResult, error) {
	if packageName == "" {
		return nil, fmt.Errorf("retrieval: get package deps: packageName must not be empty")
	}

	symbols, err := db.GetSymbolsByPackage(ctx, packageName)
	if err != nil {
		return nil, fmt.Errorf("retrieval: get package deps: list symbols: %w", err)
	}

	seen := make(map[int64]bool)
	var results []SearchResult
	for _, sym := range symbols {
		callees, err := db.GetCallees(ctx, sym.ID)
		if err != nil {
			return nil, fmt.Errorf("retrieval: get package deps: get callees for %d: %w", sym.ID, err)
		}
		for _, e := range callees {
			if seen[e.SymbolID] {
				continue
			}
			seen[e.SymbolID] = true
			results = append(results, edgeResultToSearchResult(e, 2.0, "depends_on"))
		}
	}

	return results, nil
}

// edgeResultToSearchResult converts a storage.EdgeResult to a SearchResult.
func edgeResultToSearchResult(e storage.EdgeResult, score float64, relationship string) SearchResult {
	return SearchResult{
		SymbolID:     e.SymbolID,
		SymbolName:   e.SymbolName,
		Kind:         e.SymbolKind,
		PackageName:  e.PackageName,
		FilePath:     e.FilePath,
		LineStart:    e.LineStart,
		LineEnd:      e.LineEnd,
		Score:        score,
		Source:       "graph",
		Relationship: relationship,
	}
}
