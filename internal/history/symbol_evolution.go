package history

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// SymbolChange represents one commit that modified a symbol.
type SymbolChange struct {
	Hash        string `json:"hash"`
	Author      string `json:"author"`
	Timestamp   int64  `json:"timestamp"`
	Message     string `json:"message"`
	DiffSnippet string `json:"diff_snippet"` // the relevant hunk(s)
}

// GetSymbolEvolution finds recent commits that modified a specific symbol.
// It runs git log -p on the file and filters diff hunks that overlap the
// symbol's line range or mention the symbol name.
func GetSymbolEvolution(repoPath string, filePath string, symbolName string, lineStart, lineEnd int, maxCommits int) ([]SymbolChange, error) {
	args := []string{
		"-C", repoPath,
		"log",
		"--no-merges",
		fmt.Sprintf("-%d", maxCommits*3), // fetch more than needed, filter later
		"--format=COMMIT:%H|%an|%at|%s",
		"-p",
		"--", filePath,
	}

	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("history: git log -p for %s: %w", filePath, err)
	}

	return parseSymbolChanges(string(out), symbolName, lineStart, lineEnd, maxCommits)
}

// hunkHeaderRe matches unified diff hunk headers like: @@ -10,5 +12,7 @@ func Foo
var hunkHeaderRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// parseSymbolChanges parses git log -p output and filters commits whose diff
// hunks overlap the symbol's line range or mention the symbol name.
func parseSymbolChanges(output string, symbolName string, lineStart, lineEnd int, maxCommits int) ([]SymbolChange, error) {
	if output == "" {
		return nil, nil
	}

	// Split by COMMIT: markers. The first element before the first marker is empty.
	sections := strings.Split(output, "COMMIT:")

	var results []SymbolChange

	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}

		// The first line contains: hash|author|timestamp|message
		// Followed by diff output
		firstNewline := strings.IndexByte(section, '\n')
		var headerLine, diffBody string
		if firstNewline == -1 {
			headerLine = section
		} else {
			headerLine = section[:firstNewline]
			diffBody = section[firstNewline+1:]
		}

		parts := strings.SplitN(headerLine, "|", 4)
		if len(parts) < 4 {
			continue
		}

		ts, _ := strconv.ParseInt(parts[2], 10, 64)
		change := SymbolChange{
			Hash:      parts[0],
			Author:    parts[1],
			Timestamp: ts,
			Message:   parts[3],
		}

		// Parse diff hunks and check for overlap or name match
		matchingHunks := findMatchingHunks(diffBody, symbolName, lineStart, lineEnd)
		if len(matchingHunks) == 0 {
			continue
		}

		change.DiffSnippet = strings.Join(matchingHunks, "\n")
		results = append(results, change)

		if len(results) >= maxCommits {
			break
		}
	}

	return results, nil
}

// findMatchingHunks extracts diff hunks that overlap the symbol's line range
// or contain the symbol name.
func findMatchingHunks(diffBody string, symbolName string, lineStart, lineEnd int) []string {
	if diffBody == "" {
		return nil
	}

	lines := strings.Split(diffBody, "\n")
	var matchingHunks []string
	var currentHunk []string
	var hunkMatches bool

	for _, line := range lines {
		if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
			// Flush previous hunk if it matched
			if hunkMatches && len(currentHunk) > 0 {
				matchingHunks = append(matchingHunks, strings.Join(currentHunk, "\n"))
			}

			// Start new hunk
			currentHunk = []string{line}
			hunkMatches = false

			// Parse newStart and newLen
			newStart, _ := strconv.Atoi(m[1])
			newLen := 1
			if m[2] != "" {
				newLen, _ = strconv.Atoi(m[2])
			}
			hunkEnd := newStart + newLen - 1

			// Check line range overlap: hunkStart <= lineEnd && hunkEnd >= lineStart
			if newStart <= lineEnd && hunkEnd >= lineStart {
				hunkMatches = true
			}
			continue
		}

		// Accumulate lines within a hunk
		if len(currentHunk) > 0 {
			currentHunk = append(currentHunk, line)

			// Check if this line mentions the symbol name
			if !hunkMatches && strings.Contains(line, symbolName) {
				hunkMatches = true
			}
		}
	}

	// Flush the last hunk
	if hunkMatches && len(currentHunk) > 0 {
		matchingHunks = append(matchingHunks, strings.Join(currentHunk, "\n"))
	}

	return matchingHunks
}
