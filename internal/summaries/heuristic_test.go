package summaries

import (
	"strings"
	"testing"

	"github.com/hman-pro/projectlens/internal/parser"
)

func TestHeuristicFileSummary_WithPackageDocAndMixedSymbols(t *testing.T) {
	symbols := []parser.Symbol{
		{
			Name:       "Bar",
			Kind:       "func",
			Signature:  "func Bar(x int) (string, error)",
			DocComment: "Bar does something useful.\nMore details here.\n",
		},
		{
			Name:       "helper",
			Kind:       "func",
			Signature:  "func helper()",
			DocComment: "helper is unexported.\n",
		},
		{
			Name:       "MyStruct",
			Kind:       "struct",
			Signature:  "type MyStruct struct { ... }",
			DocComment: "MyStruct is a test struct.\n",
		},
		{
			Name:       "internal",
			Kind:       "var",
			Signature:  "var internal int",
			DocComment: "internal is private.\n",
		},
		{
			Name:       "MyConst",
			Kind:       "const",
			Signature:  `const MyConst = "hello"`,
			DocComment: "MyConst is a test constant.\n",
		},
	}

	summary := HeuristicFileSummary(symbols, "Package foo provides utilities for bar processing.\n")

	// Should include package doc.
	if !strings.Contains(summary, "Package foo provides utilities for bar processing.") {
		t.Errorf("expected package doc in summary, got:\n%s", summary)
	}

	// Should include exported symbols.
	if !strings.Contains(summary, "func Bar(x int) (string, error)") {
		t.Errorf("expected Bar in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "type MyStruct struct { ... }") {
		t.Errorf("expected MyStruct in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, `const MyConst = "hello"`) {
		t.Errorf("expected MyConst in summary, got:\n%s", summary)
	}

	// Should NOT include unexported symbols.
	if strings.Contains(summary, "helper") {
		t.Errorf("expected no unexported symbol 'helper' in summary, got:\n%s", summary)
	}
	if strings.Contains(summary, "var internal") {
		t.Errorf("expected no unexported symbol 'internal' in summary, got:\n%s", summary)
	}

	// Doc comments should be first line only.
	if strings.Contains(summary, "More details here") {
		t.Errorf("expected only first line of doc comment, got:\n%s", summary)
	}

	// Should have Exports header.
	if !strings.Contains(summary, "Exports:") {
		t.Errorf("expected 'Exports:' header, got:\n%s", summary)
	}
}

func TestHeuristicFileSummary_WithMethodSymbol(t *testing.T) {
	symbols := []parser.Symbol{
		{
			Name:       "Baz",
			Kind:       "method",
			Receiver:   "*MyStruct",
			Signature:  "func (m *MyStruct) Baz() string",
			DocComment: "Baz is a method on MyStruct.\n",
		},
	}

	summary := HeuristicFileSummary(symbols, "")

	if !strings.Contains(summary, "func (m *MyStruct) Baz() string") {
		t.Errorf("expected method Baz in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "Baz is a method on MyStruct.") {
		t.Errorf("expected doc comment for Baz, got:\n%s", summary)
	}
}

func TestHeuristicFileSummary_NoPackageDoc(t *testing.T) {
	symbols := []parser.Symbol{
		{
			Name:      "Foo",
			Kind:      "func",
			Signature: "func Foo()",
		},
	}

	summary := HeuristicFileSummary(symbols, "")

	// Should not have a blank line at top or "Package" reference.
	if strings.HasPrefix(summary, "\n") {
		t.Errorf("expected no leading newline when no package doc, got:\n%q", summary)
	}

	// Should still have the export.
	if !strings.Contains(summary, "func Foo()") {
		t.Errorf("expected Foo in summary, got:\n%s", summary)
	}
}

func TestHeuristicFileSummary_NoExportedSymbols(t *testing.T) {
	symbols := []parser.Symbol{
		{
			Name:      "helper",
			Kind:      "func",
			Signature: "func helper()",
		},
		{
			Name:      "doStuff",
			Kind:      "func",
			Signature: "func doStuff()",
		},
	}

	summary := HeuristicFileSummary(symbols, "Package foo does stuff.\n")

	if !strings.Contains(summary, "No exported symbols.") {
		t.Errorf("expected 'No exported symbols.' when all symbols are unexported, got:\n%s", summary)
	}
}

func TestHeuristicFileSummary_NoExportedSymbols_NoPackageDoc(t *testing.T) {
	symbols := []parser.Symbol{
		{
			Name:      "private",
			Kind:      "func",
			Signature: "func private()",
		},
	}

	summary := HeuristicFileSummary(symbols, "")

	if !strings.Contains(summary, "No exported symbols.") {
		t.Errorf("expected 'No exported symbols.', got:\n%s", summary)
	}
}

func TestHeuristicFileSummary_LargeFileTruncation(t *testing.T) {
	// Create 60 exported symbols with reasonably long signatures.
	var symbols []parser.Symbol
	for i := 0; i < 60; i++ {
		name := "ExportedFunc" + string(rune('A'+i%26)) + strings.Repeat("x", i%5)
		symbols = append(symbols, parser.Symbol{
			Name:       name,
			Kind:       "func",
			Signature:  "func " + name + "(ctx context.Context, input string) (Result, error)",
			DocComment: name + " does something important for the system.\n",
		})
	}

	summary := HeuristicFileSummary(symbols, "Package bigpkg provides a large number of exports.\n")

	// Should be truncated.
	if !strings.Contains(summary, "... (") {
		t.Errorf("expected truncation marker '... (N more symbols)', got:\n%s", summary)
	}
	if !strings.Contains(summary, "more symbols)") {
		t.Errorf("expected 'more symbols)' in truncation marker, got:\n%s", summary)
	}

	// Word count should be roughly under 500 tokens (using word count as proxy).
	wordCount := len(strings.Fields(summary))
	if wordCount > 600 {
		t.Errorf("expected summary to be roughly ≤500 tokens (word count %d), got:\n%s", wordCount, summary)
	}
}

func TestHeuristicFileSummary_DocCommentFirstLineOnly(t *testing.T) {
	symbols := []parser.Symbol{
		{
			Name:       "Multi",
			Kind:       "func",
			Signature:  "func Multi()",
			DocComment: "Multi is the first line.\nThis is the second line.\nThis is the third line.\n",
		},
	}

	summary := HeuristicFileSummary(symbols, "")

	if !strings.Contains(summary, "Multi is the first line.") {
		t.Errorf("expected first line of doc comment, got:\n%s", summary)
	}
	if strings.Contains(summary, "second line") {
		t.Errorf("expected only first line of doc, but found 'second line' in:\n%s", summary)
	}
	if strings.Contains(summary, "third line") {
		t.Errorf("expected only first line of doc, but found 'third line' in:\n%s", summary)
	}
}

func TestHeuristicFileSummary_NoDocComment(t *testing.T) {
	symbols := []parser.Symbol{
		{
			Name:      "NoDocs",
			Kind:      "func",
			Signature: "func NoDocs(x int) error",
		},
	}

	summary := HeuristicFileSummary(symbols, "")

	// Should have the signature without a dash separator.
	if !strings.Contains(summary, "func NoDocs(x int) error") {
		t.Errorf("expected signature without doc, got:\n%s", summary)
	}
	// Should not have a trailing dash.
	lines := strings.Split(summary, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, "—") || strings.HasSuffix(trimmed, "— ") {
			t.Errorf("should not have trailing em-dash when no doc comment, got line: %q", line)
		}
	}
}

func TestHeuristicFileSummary_PackageDocTruncatedToThreeLines(t *testing.T) {
	longDoc := "Package big does a lot.\nLine two of docs.\nLine three of docs.\nLine four should be cut.\nLine five too.\n"
	symbols := []parser.Symbol{
		{
			Name:      "X",
			Kind:      "func",
			Signature: "func X()",
		},
	}

	summary := HeuristicFileSummary(symbols, longDoc)

	if !strings.Contains(summary, "Line three of docs.") {
		t.Errorf("expected first 3 lines of package doc, got:\n%s", summary)
	}
	if strings.Contains(summary, "Line four") {
		t.Errorf("expected package doc truncated at 3 lines, got:\n%s", summary)
	}
}

func TestHeuristicFileSummary_EmptyInput(t *testing.T) {
	summary := HeuristicFileSummary(nil, "")
	if !strings.Contains(summary, "No exported symbols.") {
		t.Errorf("expected 'No exported symbols.' for nil symbols, got:\n%s", summary)
	}
}

func TestHeuristicFileSummary_InterfaceSymbol(t *testing.T) {
	symbols := []parser.Symbol{
		{
			Name:       "MyInterface",
			Kind:       "interface",
			Signature:  "type MyInterface interface { ... }",
			DocComment: "MyInterface defines the contract.\n",
		},
	}

	summary := HeuristicFileSummary(symbols, "")

	if !strings.Contains(summary, "type MyInterface interface { ... }") {
		t.Errorf("expected interface in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "MyInterface defines the contract.") {
		t.Errorf("expected doc comment for interface, got:\n%s", summary)
	}
}
