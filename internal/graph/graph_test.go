package graph

import (
	"context"
	"os"
	"path/filepath"
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

const callerSource = `package caller

import "example.com/test/pkg/callee"

func DoWork() string {
	return callee.Process("hello")
}
`

const calleeSource = `package callee

// Processor is an interface.
type Processor interface {
	Run() string
}

// SimpleProcessor implements Processor.
type SimpleProcessor struct{}

func (s *SimpleProcessor) Run() string {
	return "done"
}

// Process does something.
func Process(input string) string {
	return input + " processed"
}
`

func TestBuild(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeFile(t, dir, "pkg/caller/caller.go", callerSource)
	writeFile(t, dir, "pkg/callee/callee.go", calleeSource)

	ctx := context.Background()
	result, err := Build(ctx, dir, []string{"./..."})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if len(result.Edges) == 0 {
		t.Fatal("expected at least one edge, got 0")
	}

	// Log all edges for debugging.
	for _, e := range result.Edges {
		t.Logf("edge: %s/%s -> %s/%s [%s]",
			e.SourcePackage, e.SourceName,
			e.TargetPackage, e.TargetName,
			e.EdgeType)
	}

	// Should find a "calls" edge from DoWork to Process.
	foundCallEdge := false
	for _, e := range result.Edges {
		if e.EdgeType == "calls" && e.SourceName == "DoWork" && e.TargetName == "Process" {
			foundCallEdge = true
			if e.SourcePackage != "example.com/test/pkg/caller" {
				t.Errorf("call edge SourcePackage: got %q, want %q", e.SourcePackage, "example.com/test/pkg/caller")
			}
			if e.TargetPackage != "example.com/test/pkg/callee" {
				t.Errorf("call edge TargetPackage: got %q, want %q", e.TargetPackage, "example.com/test/pkg/callee")
			}
			break
		}
	}
	if !foundCallEdge {
		t.Error("expected to find a 'calls' edge from DoWork to Process")
	}

	// Should find an "implements" edge from SimpleProcessor to Processor.
	foundImplEdge := false
	for _, e := range result.Edges {
		if e.EdgeType == "implements" && e.SourceName == "SimpleProcessor" && e.TargetName == "Processor" {
			foundImplEdge = true
			if e.SourcePackage != "example.com/test/pkg/callee" {
				t.Errorf("implements edge SourcePackage: got %q, want %q", e.SourcePackage, "example.com/test/pkg/callee")
			}
			if e.TargetPackage != "example.com/test/pkg/callee" {
				t.Errorf("implements edge TargetPackage: got %q, want %q", e.TargetPackage, "example.com/test/pkg/callee")
			}
			break
		}
	}
	if !foundImplEdge {
		t.Error("expected to find an 'implements' edge from SimpleProcessor to Processor")
	}

	// Should find an "imports" edge from caller to callee.
	foundImportEdge := false
	for _, e := range result.Edges {
		if e.EdgeType == "imports" && e.SourcePackage == "example.com/test/pkg/caller" && e.TargetPackage == "example.com/test/pkg/callee" {
			foundImportEdge = true
			break
		}
	}
	if !foundImportEdge {
		t.Error("expected to find an 'imports' edge from caller to callee")
	}

	// Should NOT find edges to standard library functions.
	for _, e := range result.Edges {
		if !isInModule(e.TargetPackage, "example.com/test") {
			t.Errorf("unexpected edge to package outside module: %s/%s -> %s/%s [%s]",
				e.SourcePackage, e.SourceName,
				e.TargetPackage, e.TargetName,
				e.EdgeType)
		}
		if !isInModule(e.SourcePackage, "example.com/test") {
			t.Errorf("unexpected edge from package outside module: %s/%s -> %s/%s [%s]",
				e.SourcePackage, e.SourceName,
				e.TargetPackage, e.TargetName,
				e.EdgeType)
		}
	}
}

func TestBuildInvalidDir(t *testing.T) {
	ctx := context.Background()
	_, err := Build(ctx, "/nonexistent/dir/that/should/not/exist", []string{"./..."})
	if err == nil {
		t.Error("expected error for nonexistent directory, got nil")
	}
}

func TestBuildEmptyModule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/empty\n\ngo 1.26\n")

	ctx := context.Background()
	result, err := Build(ctx, dir, []string{"./..."})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if len(result.Edges) != 0 {
		t.Errorf("expected 0 edges for empty module, got %d", len(result.Edges))
	}
}
