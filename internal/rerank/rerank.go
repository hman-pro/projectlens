// Package rerank applies scoring adjustments and sorting to retrieval results.
package rerank

import (
	"sort"
	"strings"
)

// Result holds the fields needed for scoring adjustments. This type is
// intentionally separate from retrieval.SearchResult to avoid an import cycle
// (retrieval imports rerank, so rerank must not import retrieval).
type Result struct {
	SymbolName  string
	PackageName string
	FilePath    string
	Score       float64
}

// generatedFileHints contains path substrings that indicate generated code.
var generatedFileHints = []string{
	".pb.go",
	"_generated",
	".gen.go",
	"_gen.go",
	"zz_generated",
}

// Rank applies scoring adjustments to each result and returns the adjusted
// scores in the same order. The caller is responsible for mapping these back
// to the original result type and sorting.
//
// Adjustments:
//   - Exact name match (case-insensitive query == symbol name): +10.0
//   - Same package as query context (if query contains a package path): +2.0
//   - Test file (path contains _test.go): -3.0 unless isTestQuery
//   - Generated file hint (path contains .pb.go, _generated, etc.): -5.0
func Rank(results []Result, query string, isTestQuery bool) []Result {
	if len(results) == 0 {
		return results
	}

	// Copy to avoid mutating the original.
	ranked := make([]Result, len(results))
	copy(ranked, results)

	queryLower := strings.ToLower(query)
	queryPackage := extractPackagePath(query)

	for i := range ranked {
		adj := 0.0

		// Exact name match (case-insensitive): full query or any query word.
		if strings.EqualFold(ranked[i].SymbolName, query) {
			adj += 10.0
		} else {
			for _, word := range strings.Fields(queryLower) {
				if strings.EqualFold(ranked[i].SymbolName, word) {
					adj += 10.0
					break
				}
			}
		}

		// Same package boost.
		if queryPackage != "" && ranked[i].PackageName == queryPackage {
			adj += 2.0
		}

		// Test file penalty.
		if !isTestQuery && strings.Contains(ranked[i].FilePath, "_test.go") {
			adj -= 3.0
		}

		// Generated file penalty.
		if isGeneratedFile(ranked[i].FilePath) {
			adj -= 5.0
		}

		ranked[i].Score += adj
	}

	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].Score > ranked[j].Score
	})

	return ranked
}

// extractPackagePath looks for a path-like segment in the query (contains '/').
// Returns the first segment that looks like a package path, or empty string.
func extractPackagePath(query string) string {
	for _, word := range strings.Fields(query) {
		if strings.Contains(word, "/") {
			return word
		}
	}
	return ""
}

// isGeneratedFile returns true if the file path suggests generated code.
func isGeneratedFile(path string) bool {
	lower := strings.ToLower(path)
	for _, hint := range generatedFileHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}
