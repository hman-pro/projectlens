package chunks

import (
	"strings"
	"testing"

	"github.com/hman-pro/projectlens/internal/parser"
)

func TestCreate_FunctionWithDocAndBody(t *testing.T) {
	sym := parser.Symbol{
		Name:       "DoStuff",
		Kind:       "func",
		Package:    "mypackage",
		Signature:  "func DoStuff(x int) error",
		DocComment: "DoStuff does important stuff.\n",
		Body:       "func DoStuff(x int) error {\n\treturn nil\n}",
		LineStart:  10,
		LineEnd:    12,
		FilePath:   "/tmp/test.go",
	}
	packageDoc := "Package mypackage provides utilities for testing."

	chunk := Create(sym, packageDoc)

	if chunk.SymbolName != "DoStuff" {
		t.Errorf("SymbolName = %q, want %q", chunk.SymbolName, "DoStuff")
	}
	if chunk.Package != "mypackage" {
		t.Errorf("Package = %q, want %q", chunk.Package, "mypackage")
	}

	// Should contain the package line with first line of packageDoc.
	if !strings.Contains(chunk.Content, "// Package mypackage — Package mypackage provides utilities for testing.") {
		t.Errorf("Content missing package line, got:\n%s", chunk.Content)
	}

	// Should contain the doc comment.
	if !strings.Contains(chunk.Content, "DoStuff does important stuff.") {
		t.Errorf("Content missing doc comment, got:\n%s", chunk.Content)
	}

	// Should contain the signature.
	if !strings.Contains(chunk.Content, "func DoStuff(x int) error") {
		t.Errorf("Content missing signature, got:\n%s", chunk.Content)
	}

	// Should contain the body.
	if !strings.Contains(chunk.Content, "return nil") {
		t.Errorf("Content missing body, got:\n%s", chunk.Content)
	}

	if chunk.TokenCount <= 0 {
		t.Errorf("TokenCount = %d, want > 0", chunk.TokenCount)
	}
}

func TestCreate_FunctionWithoutDocComment(t *testing.T) {
	sym := parser.Symbol{
		Name:      "helper",
		Kind:      "func",
		Package:   "pkg",
		Signature: "func helper() string",
		Body:      "func helper() string {\n\treturn \"ok\"\n}",
		LineStart: 5,
		LineEnd:   7,
		FilePath:  "/tmp/test.go",
	}
	packageDoc := "Package pkg does things."

	chunk := Create(sym, packageDoc)

	// Should have the package line.
	if !strings.Contains(chunk.Content, "// Package pkg") {
		t.Errorf("Content missing package line, got:\n%s", chunk.Content)
	}

	// The content between package line and signature should NOT have a doc comment.
	lines := strings.Split(chunk.Content, "\n")
	foundPackageLine := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "// Package") {
			foundPackageLine = true
			continue
		}
		if foundPackageLine {
			// The next non-empty line after the package line should be the signature or body,
			// not a doc comment line.
			if strings.HasPrefix(trimmed, "//") {
				t.Errorf("Found unexpected comment line after package line: %q", trimmed)
			}
			break
		}
	}
}

func TestCreate_MethodWithReceiver(t *testing.T) {
	sym := parser.Symbol{
		Name:       "Close",
		Kind:       "method",
		Package:    "db",
		Receiver:   "*Connection",
		Signature:  "func (c *Connection) Close() error",
		DocComment: "Close releases the connection.\n",
		Body:       "func (c *Connection) Close() error {\n\tc.conn.Close()\n\treturn nil\n}",
		LineStart:  20,
		LineEnd:    23,
		FilePath:   "/tmp/db.go",
	}

	chunk := Create(sym, "")

	// Should include the method signature with receiver.
	if !strings.Contains(chunk.Content, "func (c *Connection) Close() error") {
		t.Errorf("Content missing method signature with receiver, got:\n%s", chunk.Content)
	}
}

func TestCreate_Struct(t *testing.T) {
	sym := parser.Symbol{
		Name:       "Config",
		Kind:       "struct",
		Package:    "app",
		Signature:  "type Config struct {\n\tHost string\n\tPort int\n}",
		DocComment: "Config holds application configuration.\n",
		Body:       "Config struct {\n\tHost string\n\tPort int\n}",
		LineStart:  1,
		LineEnd:    4,
		FilePath:   "/tmp/config.go",
	}
	packageDoc := "Package app is the main application."

	chunk := Create(sym, packageDoc)

	// Should contain type definition.
	if !strings.Contains(chunk.Content, "type Config struct") {
		t.Errorf("Content missing struct type definition, got:\n%s", chunk.Content)
	}

	if !strings.Contains(chunk.Content, "Host string") {
		t.Errorf("Content missing struct fields, got:\n%s", chunk.Content)
	}
}

func TestCreate_EmptyPackageDoc(t *testing.T) {
	sym := parser.Symbol{
		Name:       "Run",
		Kind:       "func",
		Package:    "main",
		Signature:  "func Run()",
		DocComment: "Run starts the server.\n",
		Body:       "func Run() {\n\tstart()\n}",
		LineStart:  1,
		LineEnd:    3,
		FilePath:   "/tmp/main.go",
	}

	chunk := Create(sym, "")

	// Should NOT have a package line.
	if strings.Contains(chunk.Content, "// Package") {
		t.Errorf("Content should not have package line with empty packageDoc, got:\n%s", chunk.Content)
	}

	// Should still have the doc comment and body.
	if !strings.Contains(chunk.Content, "Run starts the server.") {
		t.Errorf("Content missing doc comment, got:\n%s", chunk.Content)
	}
	if !strings.Contains(chunk.Content, "func Run()") {
		t.Errorf("Content missing signature, got:\n%s", chunk.Content)
	}
}

func TestCreate_TokenCountRoughlyCorrect(t *testing.T) {
	sym := parser.Symbol{
		Name:      "Add",
		Kind:      "func",
		Package:   "math",
		Signature: "func Add(a, b int) int",
		Body:      "func Add(a, b int) int {\n\treturn a + b\n}",
		LineStart: 1,
		LineEnd:   3,
		FilePath:  "/tmp/math.go",
	}

	chunk := Create(sym, "Package math provides math utilities.")

	// Count words manually in the content.
	words := strings.Fields(chunk.Content)
	expected := len(words)

	if chunk.TokenCount != expected {
		t.Errorf("TokenCount = %d, want %d (word count)", chunk.TokenCount, expected)
	}
	if chunk.TokenCount <= 0 {
		t.Errorf("TokenCount = %d, want > 0", chunk.TokenCount)
	}
}

func TestCreateBatch(t *testing.T) {
	symbols := []parser.Symbol{
		{
			Name:      "A",
			Kind:      "func",
			Package:   "pkg",
			Signature: "func A()",
			Body:      "func A() {}",
			LineStart: 1,
			LineEnd:   1,
			FilePath:  "/tmp/a.go",
		},
		{
			Name:      "B",
			Kind:      "func",
			Package:   "pkg",
			Signature: "func B()",
			Body:      "func B() {}",
			LineStart: 3,
			LineEnd:   3,
			FilePath:  "/tmp/b.go",
		},
		{
			Name:       "C",
			Kind:       "struct",
			Package:    "pkg",
			Signature:  "type C struct{}",
			DocComment: "C is a struct.\n",
			Body:       "C struct{}",
			LineStart:  5,
			LineEnd:    5,
			FilePath:   "/tmp/c.go",
		},
	}

	chunks := CreateBatch(symbols, "Package pkg is for testing.")

	if len(chunks) != 3 {
		t.Fatalf("CreateBatch returned %d chunks, want 3", len(chunks))
	}

	if chunks[0].SymbolName != "A" {
		t.Errorf("chunks[0].SymbolName = %q, want %q", chunks[0].SymbolName, "A")
	}
	if chunks[1].SymbolName != "B" {
		t.Errorf("chunks[1].SymbolName = %q, want %q", chunks[1].SymbolName, "B")
	}
	if chunks[2].SymbolName != "C" {
		t.Errorf("chunks[2].SymbolName = %q, want %q", chunks[2].SymbolName, "C")
	}

	// Each chunk should have content and a positive token count.
	for i, c := range chunks {
		if c.Content == "" {
			t.Errorf("chunks[%d].Content is empty", i)
		}
		if c.TokenCount <= 0 {
			t.Errorf("chunks[%d].TokenCount = %d, want > 0", i, c.TokenCount)
		}
		if c.Package != "pkg" {
			t.Errorf("chunks[%d].Package = %q, want %q", i, c.Package, "pkg")
		}
	}
}
