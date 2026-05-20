package history

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Commit represents a parsed git log entry.
type Commit struct {
	Hash      string
	Author    string
	Timestamp int64 // unix timestamp
	Message   string
	Files     []string // relative file paths changed in this commit
}

// ParseGitLog runs git log for the given time window and returns parsed commits.
// It excludes merge commits. Window is a git --since value like "12 months".
func ParseGitLog(repoPath string, window string) ([]Commit, error) {
	args := []string{
		"-C", repoPath,
		"log",
		"--name-only",
		"--no-merges",
		"--format=COMMIT:%H|%an|%at|%s",
	}
	if window != "" {
		args = append(args, "--since="+window)
	}

	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("history: git log: %w", err)
	}

	return parseGitLogOutput(string(out))
}

// ParseGitLogForFile runs git log for a specific file with --follow to track renames.
// Returns up to maxCommits.
func ParseGitLogForFile(repoPath string, filePath string, maxCommits int) ([]Commit, error) {
	args := []string{
		"-C", repoPath,
		"log",
		"--name-only",
		"--no-merges",
		"--follow",
		fmt.Sprintf("-%d", maxCommits),
		"--format=COMMIT:%H|%an|%at|%s",
		"--", filePath,
	}

	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("history: git log for %s: %w", filePath, err)
	}

	return parseGitLogOutput(string(out))
}

// parseGitLogOutput parses the raw git log output into Commit structs.
// Format: lines starting with "COMMIT:" are headers, non-empty lines after are files.
func parseGitLogOutput(output string) ([]Commit, error) {
	var commits []Commit
	var current *Commit

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "COMMIT:") {
			// Save previous commit if exists
			if current != nil {
				commits = append(commits, *current)
			}

			// Parse: COMMIT:hash|author|timestamp|message
			parts := strings.SplitN(line[7:], "|", 4)
			if len(parts) < 4 {
				continue
			}
			ts, _ := strconv.ParseInt(parts[2], 10, 64)
			current = &Commit{
				Hash:      parts[0],
				Author:    parts[1],
				Timestamp: ts,
				Message:   parts[3],
			}
		} else if current != nil {
			// File path line
			current.Files = append(current.Files, line)
		}
	}

	// Don't forget the last commit
	if current != nil {
		commits = append(commits, *current)
	}

	return commits, nil
}
