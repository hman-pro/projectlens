package history

import (
	"sort"
	"strings"
)

// CouplingPair represents two files that frequently change together.
type CouplingPair struct {
	FileA         string  `json:"file_a"`
	FileB         string  `json:"file_b"`
	CoChangeCount int     `json:"co_change_count"`
	Strength      float64 `json:"strength"`       // co_changes / max(changes_a, changes_b)
	LastCoChange  int64   `json:"last_co_change"` // unix timestamp
}

// ComputeCoupling analyzes commits and returns co-change coupling pairs.
// Commits with more than maxFilesPerCommit files are excluded (noise: refactors, renames).
// Only pairs with at least minCoChanges are returned.
// Results are sorted by strength descending.
func ComputeCoupling(commits []Commit, minCoChanges int, maxFilesPerCommit int) []CouplingPair {
	// Track per-file change count
	fileChanges := make(map[string]int)

	// Track co-change counts for file pairs
	// Key: "fileA\x00fileB" (lexicographically ordered)
	pairCounts := make(map[string]int)
	pairLastChange := make(map[string]int64)

	for _, c := range commits {
		// Skip high-diffusion commits (refactors)
		if len(c.Files) > maxFilesPerCommit || len(c.Files) < 2 {
			// Still count individual file changes for files in small commits
			if len(c.Files) <= maxFilesPerCommit {
				for _, f := range c.Files {
					fileChanges[f]++
				}
			}
			continue
		}

		// Count individual file changes
		for _, f := range c.Files {
			fileChanges[f]++
		}

		// Generate all pairs (sorted to avoid A,B vs B,A duplicates)
		sorted := make([]string, len(c.Files))
		copy(sorted, c.Files)
		sort.Strings(sorted)

		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				key := sorted[i] + "\x00" + sorted[j]
				pairCounts[key]++
				if c.Timestamp > pairLastChange[key] {
					pairLastChange[key] = c.Timestamp
				}
			}
		}
	}

	// Build result, filtering by minimum co-changes
	var pairs []CouplingPair
	for key, count := range pairCounts {
		if count < minCoChanges {
			continue
		}
		parts := strings.SplitN(key, "\x00", 2)
		changesA := fileChanges[parts[0]]
		changesB := fileChanges[parts[1]]
		maxChanges := changesA
		if changesB > maxChanges {
			maxChanges = changesB
		}

		strength := float64(count) / float64(maxChanges)

		pairs = append(pairs, CouplingPair{
			FileA:         parts[0],
			FileB:         parts[1],
			CoChangeCount: count,
			Strength:      strength,
			LastCoChange:  pairLastChange[key],
		})
	}

	// Sort by strength descending
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].Strength > pairs[j].Strength
	})

	return pairs
}
