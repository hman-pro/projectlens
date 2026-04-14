package classifier

import (
	"path/filepath"
	"strings"
)

// Classification describes how a file should be treated by the indexer.
type Classification struct {
	Language    string
	IsGenerated bool
	IsTest      bool
	Excluded    bool
}

// Config controls which patterns are used for classification.
type Config struct {
	ExcludePatterns  []string
	GeneratedMarkers []string
}

// DefaultConfig returns the standard exclude patterns and generated markers.
func DefaultConfig() Config {
	return Config{
		ExcludePatterns: []string{
			"vendor/",
			"third_party/",
			"testdata/",
			"node_modules/",
		},
		GeneratedMarkers: []string{
			"Code generated",
			"DO NOT EDIT",
			"_generated.go",
			"_gen.go",
			".pb.go",
			"_grpc.go",
			"_string.go",
			"zz_generated",
		},
	}
}

// pathGeneratedMarkers are the markers that make sense to match against file paths.
// Content markers like "Code generated" and "DO NOT EDIT" are only checked in content.
var pathGeneratedMarkers = map[string]bool{
	"_generated.go": true,
	"_gen.go":       true,
	".pb.go":        true,
	"_grpc.go":      true,
	"_string.go":    true,
	"zz_generated":  true,
}

// ClassifyPath classifies a file based on its path alone.
func ClassifyPath(path string, cfg Config) Classification {
	c := Classification{
		Language: languageFromExt(path),
	}

	// Check exclude patterns (substring match).
	for _, pattern := range cfg.ExcludePatterns {
		if strings.Contains(path, pattern) {
			c.Excluded = true
			return c
		}
	}

	// Check test file suffix.
	if strings.HasSuffix(path, "_test.go") {
		c.IsTest = true
	}

	// Check generated markers that apply to paths.
	for _, marker := range cfg.GeneratedMarkers {
		if !pathGeneratedMarkers[marker] {
			continue
		}
		if strings.Contains(path, marker) {
			c.IsGenerated = true
			break
		}
	}

	return c
}

// ClassifyContent classifies a file based on the first ~512 bytes of its content.
func ClassifyContent(content string, cfg Config) Classification {
	var c Classification

	// Only inspect the header (first 512 bytes).
	header := content
	if len(header) > 512 {
		header = header[:512]
	}

	// Check content-based generated markers.
	for _, marker := range cfg.GeneratedMarkers {
		// Skip path-only markers when inspecting content.
		if pathGeneratedMarkers[marker] {
			continue
		}
		if strings.Contains(header, marker) {
			c.IsGenerated = true
			break
		}
	}

	return c
}

// Classify combines path-based and content-based classification.
// If the path indicates exclusion, content is not inspected.
func Classify(path string, content string, cfg Config) Classification {
	c := ClassifyPath(path, cfg)

	// If excluded, return immediately without reading content.
	if c.Excluded {
		return c
	}

	// Augment with content-based classification.
	cc := ClassifyContent(content, cfg)
	if cc.IsGenerated {
		c.IsGenerated = true
	}

	return c
}

// languageFromExt returns a language identifier based on file extension.
func languageFromExt(path string) string {
	ext := filepath.Ext(path)
	switch ext {
	case ".go":
		return "go"
	default:
		return "unknown"
	}
}
