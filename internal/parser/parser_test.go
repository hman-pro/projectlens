package parser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile creates a file at root/relPath with the given content.
func writeFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const fooSource = `// Package foo provides test fixtures.
package foo

// MyConst is a test constant.
const MyConst = "hello"

// MyVar is a test variable.
var MyVar int

// MyStruct is a test struct.
type MyStruct struct {
	Field string
}

// MyInterface is a test interface.
type MyInterface interface {
	DoSomething() error
}

// Bar does something.
func Bar(x int) (string, error) {
	return "", nil
}

// Baz is a method on MyStruct.
func (m *MyStruct) Baz() string {
	return m.Field
}
`

func TestParse(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeFile(t, dir, "pkg/foo/foo.go", fooSource)

	ctx := context.Background()
	result, err := Parse(ctx, dir, []string{"./..."})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// We expect at least the foo.go file in results.
	// Filter to only foo.go to avoid counting any other files.
	var fooFile *FileResult
	for i := range result.Files {
		if strings.HasSuffix(result.Files[i].Path, "foo.go") {
			fooFile = &result.Files[i]
			break
		}
	}
	if fooFile == nil {
		t.Fatal("expected to find foo.go in results")
	}

	if fooFile.Package != "foo" {
		t.Errorf("Package: got %q, want %q", fooFile.Package, "foo")
	}

	if len(fooFile.Symbols) != 6 {
		t.Errorf("Symbols count: got %d, want 6", len(fooFile.Symbols))
		for _, s := range fooFile.Symbols {
			t.Logf("  symbol: %s kind=%s", s.Name, s.Kind)
		}
	}

	// Build a lookup map by name.
	syms := make(map[string]Symbol)
	for _, s := range fooFile.Symbols {
		syms[s.Name] = s
	}

	// MyConst
	if c, ok := syms["MyConst"]; !ok {
		t.Error("missing symbol MyConst")
	} else {
		if c.Kind != "const" {
			t.Errorf("MyConst kind: got %q, want %q", c.Kind, "const")
		}
		if c.Package != "foo" {
			t.Errorf("MyConst package: got %q, want %q", c.Package, "foo")
		}
		if !strings.Contains(c.DocComment, "test constant") {
			t.Errorf("MyConst doc: got %q, want something containing 'test constant'", c.DocComment)
		}
	}

	// MyVar
	if v, ok := syms["MyVar"]; !ok {
		t.Error("missing symbol MyVar")
	} else {
		if v.Kind != "var" {
			t.Errorf("MyVar kind: got %q, want %q", v.Kind, "var")
		}
		if !strings.Contains(v.DocComment, "test variable") {
			t.Errorf("MyVar doc: got %q, want something containing 'test variable'", v.DocComment)
		}
	}

	// MyStruct
	if s, ok := syms["MyStruct"]; !ok {
		t.Error("missing symbol MyStruct")
	} else {
		if s.Kind != "struct" {
			t.Errorf("MyStruct kind: got %q, want %q", s.Kind, "struct")
		}
		if !strings.Contains(s.DocComment, "test struct") {
			t.Errorf("MyStruct doc: got %q, want something containing 'test struct'", s.DocComment)
		}
	}

	// MyInterface
	if iface, ok := syms["MyInterface"]; !ok {
		t.Error("missing symbol MyInterface")
	} else {
		if iface.Kind != "interface" {
			t.Errorf("MyInterface kind: got %q, want %q", iface.Kind, "interface")
		}
		if !strings.Contains(iface.DocComment, "test interface") {
			t.Errorf("MyInterface doc: got %q, want something containing 'test interface'", iface.DocComment)
		}
	}

	// Bar (func)
	if fn, ok := syms["Bar"]; !ok {
		t.Error("missing symbol Bar")
	} else {
		if fn.Kind != "func" {
			t.Errorf("Bar kind: got %q, want %q", fn.Kind, "func")
		}
		if fn.Receiver != "" {
			t.Errorf("Bar receiver: got %q, want empty", fn.Receiver)
		}
		if !strings.Contains(fn.Signature, "func Bar(x int)") {
			t.Errorf("Bar signature: got %q, want something containing 'func Bar(x int)'", fn.Signature)
		}
		if fn.LineStart <= 0 || fn.LineEnd <= 0 || fn.LineEnd < fn.LineStart {
			t.Errorf("Bar line range invalid: %d-%d", fn.LineStart, fn.LineEnd)
		}
		if !strings.Contains(fn.DocComment, "does something") {
			t.Errorf("Bar doc: got %q, want something containing 'does something'", fn.DocComment)
		}
		if !strings.Contains(fn.Body, "return") {
			t.Errorf("Bar body: expected to contain 'return', got %q", fn.Body)
		}
	}

	// Baz (method)
	if m, ok := syms["Baz"]; !ok {
		t.Error("missing symbol Baz")
	} else {
		if m.Kind != "method" {
			t.Errorf("Baz kind: got %q, want %q", m.Kind, "method")
		}
		if !strings.Contains(m.Receiver, "*MyStruct") {
			t.Errorf("Baz receiver: got %q, want something containing '*MyStruct'", m.Receiver)
		}
		if m.LineStart <= 0 || m.LineEnd <= 0 || m.LineEnd < m.LineStart {
			t.Errorf("Baz line range invalid: %d-%d", m.LineStart, m.LineEnd)
		}
		if !strings.Contains(m.DocComment, "method on MyStruct") {
			t.Errorf("Baz doc: got %q, want something containing 'method on MyStruct'", m.DocComment)
		}
	}
}

func TestParseInvalidDir(t *testing.T) {
	ctx := context.Background()
	_, err := Parse(ctx, "/nonexistent/dir/that/should/not/exist", []string{"./..."})
	if err == nil {
		t.Error("expected error for nonexistent directory, got nil")
	}
}

func TestParseEmptyModule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/empty\n\ngo 1.26\n")

	ctx := context.Background()
	result, err := Parse(ctx, dir, []string{"./..."})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(result.Files) != 0 {
		t.Errorf("expected 0 files for empty module, got %d", len(result.Files))
	}
}
