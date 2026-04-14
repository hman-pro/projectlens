package census

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hman-pro/projectlens/internal/classifier"
)

// FileEntry holds metadata about a single Go file discovered during the census walk.
type FileEntry struct {
	Path           string
	RelPath        string
	PackageName    string
	Checksum       string // SHA-256 hex
	Classification classifier.Classification
	LineCount      int
}

// Result aggregates the census counts and file list.
type Result struct {
	Total       int
	Handwritten int
	Test        int
	Generated   int
	Excluded    int
	Files       []FileEntry // only non-excluded files
}

// Walk scans repoPath for .go files, classifies each one, and returns a Result.
func Walk(repoPath string, cfg classifier.Config) (*Result, error) {
	res := &Result{}

	err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		relPath, err := filepath.Rel(repoPath, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", relPath, err)
		}

		contentStr := string(content)
		cls := classifier.Classify(relPath, contentStr, cfg)

		res.Total++

		if cls.Excluded {
			res.Excluded++
			return nil
		}

		entry := FileEntry{
			Path:           path,
			RelPath:        relPath,
			PackageName:    extractPackageName(contentStr),
			Checksum:       sha256Hex(content),
			Classification: cls,
			LineCount:      countLines(contentStr),
		}

		switch {
		case cls.IsGenerated:
			res.Generated++
		case cls.IsTest:
			res.Test++
		default:
			res.Handwritten++
		}

		res.Files = append(res.Files, entry)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", repoPath, err)
	}

	return res, nil
}

// extractPackageName pulls the package name from Go source content.
func extractPackageName(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			// "package foo // comment" → take only "foo"
			rest := strings.TrimPrefix(line, "package ")
			// Split on whitespace or comment markers.
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}

// sha256Hex returns the hex-encoded SHA-256 checksum of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// countLines counts the number of lines in content.
// A trailing newline does not add an extra empty line.
func countLines(content string) int {
	if content == "" {
		return 0
	}
	n := strings.Count(content, "\n")
	// If the content doesn't end with a newline, the last line still counts.
	if !strings.HasSuffix(content, "\n") {
		n++
	}
	return n
}
