// Package summaries provides heuristic and LLM-based file summary generation.
package summaries

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/hman-pro/projectlens/internal/parser"
)

// maxTokenEstimate is the approximate word count cap for summaries.
// We use word count as a rough proxy for token count.
const maxTokenEstimate = 500

// maxPackageDocLines is the maximum number of package doc lines to include.
const maxPackageDocLines = 3

// HeuristicFileSummary produces a concise text summary of a Go file from its
// parsed symbols and package doc comment. It lists only exported symbols with
// their kind, signature, and first line of doc comment. Output is capped at
// approximately 500 tokens.
func HeuristicFileSummary(symbols []parser.Symbol, packageDoc string) string {
	var b strings.Builder

	// Write package doc (first 2-3 lines).
	if doc := trimPackageDoc(packageDoc); doc != "" {
		b.WriteString(doc)
		b.WriteString("\n\n")
	}

	// Filter to exported symbols only.
	exported := filterExported(symbols)

	if len(exported) == 0 {
		b.WriteString("No exported symbols.")
		return b.String()
	}

	b.WriteString("Exports:\n")

	wordCount := countWords(b.String())

	for i, sym := range exported {
		line := formatSymbolLine(sym)
		lineWords := countWords(line)

		if wordCount+lineWords > maxTokenEstimate {
			remaining := len(exported) - i
			b.WriteString(fmt.Sprintf("  ... (%d more symbols)", remaining))
			break
		}

		b.WriteString("  ")
		b.WriteString(line)
		b.WriteString("\n")
		wordCount += lineWords
	}

	return strings.TrimRight(b.String(), "\n")
}

// trimPackageDoc returns the first maxPackageDocLines lines of the package doc,
// trimmed of trailing whitespace.
func trimPackageDoc(doc string) string {
	if doc == "" {
		return ""
	}

	lines := strings.Split(strings.TrimRight(doc, "\n"), "\n")
	if len(lines) > maxPackageDocLines {
		lines = lines[:maxPackageDocLines]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// filterExported returns only symbols whose name starts with an uppercase letter.
func filterExported(symbols []parser.Symbol) []parser.Symbol {
	var exported []parser.Symbol
	for _, sym := range symbols {
		if len(sym.Name) > 0 && unicode.IsUpper(rune(sym.Name[0])) {
			exported = append(exported, sym)
		}
	}
	return exported
}

// formatSymbolLine formats a single symbol as: {signature} — {first line of doc}
// or just {signature} if there is no doc comment.
func formatSymbolLine(sym parser.Symbol) string {
	sig := sym.Signature
	if sig == "" {
		sig = sym.Kind + " " + sym.Name
	}

	doc := firstDocLine(sym.DocComment)
	if doc != "" {
		return sig + " — " + doc
	}
	return sig
}

// firstDocLine extracts the first non-empty line from a doc comment.
func firstDocLine(doc string) string {
	if doc == "" {
		return ""
	}
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// countWords counts the number of whitespace-separated words in s.
func countWords(s string) int {
	return len(strings.Fields(s))
}
