// Package chunks creates embeddable text chunks from parsed symbols.
// Each chunk maps 1:1 with a parser.Symbol and contains formatted content
// suitable for embedding via an LLM embedding model.
package chunks

import (
	"strings"

	"github.com/hman-pro/projectlens/internal/parser"
)

// Chunk represents an embeddable text chunk derived from a single symbol.
type Chunk struct {
	SymbolName string // name of the symbol
	Package    string // package the symbol belongs to
	Content    string // the full embeddable text
	TokenCount int    // approximate token count (whitespace-split word count)
}

// Create creates a single Chunk from a Symbol and optional package-level documentation.
//
// Content format:
//
//	// Package {package} — {first line of packageDoc if available}
//
//	{doc comment if present}
//	{signature}
//	{body}
//
// If packageDoc is empty, the package line is omitted.
// If the symbol has no doc comment, that section is omitted.
func Create(sym parser.Symbol, packageDoc string) Chunk {
	var parts []string

	// Package line (only if packageDoc is non-empty).
	if packageDoc != "" {
		firstLine := firstLineOf(packageDoc)
		parts = append(parts, "// Package "+sym.Package+" — "+firstLine)
		parts = append(parts, "") // blank separator
	}

	// Doc comment (only if present).
	if sym.DocComment != "" {
		doc := strings.TrimRight(sym.DocComment, "\n")
		parts = append(parts, doc)
	}

	// Signature.
	if sym.Signature != "" {
		parts = append(parts, sym.Signature)
	}

	// Body.
	if sym.Body != "" {
		parts = append(parts, sym.Body)
	}

	content := strings.Join(parts, "\n")

	return Chunk{
		SymbolName: sym.Name,
		Package:    sym.Package,
		Content:    content,
		TokenCount: countTokens(content),
	}
}

// CreateBatch creates chunks for a list of symbols, all sharing the same packageDoc.
func CreateBatch(symbols []parser.Symbol, packageDoc string) []Chunk {
	chunks := make([]Chunk, len(symbols))
	for i, sym := range symbols {
		chunks[i] = Create(sym, packageDoc)
	}
	return chunks
}

// firstLineOf returns the first non-empty line from s.
func firstLineOf(s string) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// countTokens approximates the token count by splitting on whitespace
// and counting the resulting words. This is rough but fast.
func countTokens(s string) int {
	return len(strings.Fields(s))
}
