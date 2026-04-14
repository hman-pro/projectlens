package census

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hman-pro/projectlens/internal/classifier"
)

// helper to create a file inside a temp dir, creating parent dirs as needed.
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

func TestWalk(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "pkg/foo/bar.go", "package foo\n\nfunc Bar() {}\n")
	writeFile(t, dir, "pkg/foo/bar_test.go", "package foo\n")
	writeFile(t, dir, "pkg/foo/gen.pb.go", "// Code generated\npackage foo\n")
	writeFile(t, dir, "vendor/lib/lib.go", "package lib\n")

	result, err := Walk(dir, classifier.DefaultConfig())
	if err != nil {
		t.Fatalf("Walk returned error: %v", err)
	}

	if result.Total != 4 {
		t.Errorf("Total: got %d, want 4", result.Total)
	}
	if result.Handwritten != 1 {
		t.Errorf("Handwritten: got %d, want 1", result.Handwritten)
	}
	if result.Test != 1 {
		t.Errorf("Test: got %d, want 1", result.Test)
	}
	if result.Generated != 1 {
		t.Errorf("Generated: got %d, want 1", result.Generated)
	}
	if result.Excluded != 1 {
		t.Errorf("Excluded: got %d, want 1", result.Excluded)
	}

	if len(result.Files) != 3 {
		t.Fatalf("Files length: got %d, want 3", len(result.Files))
	}

	// Find the handwritten file and verify its fields.
	var hw *FileEntry
	for i := range result.Files {
		if result.Files[i].RelPath == filepath.Join("pkg", "foo", "bar.go") {
			hw = &result.Files[i]
			break
		}
	}
	if hw == nil {
		t.Fatal("handwritten file pkg/foo/bar.go not found in Files")
	}
	if hw.PackageName != "foo" {
		t.Errorf("PackageName: got %q, want %q", hw.PackageName, "foo")
	}
	if hw.Checksum == "" {
		t.Error("Checksum is empty for handwritten file")
	}
	if hw.LineCount != 3 {
		t.Errorf("LineCount: got %d, want 3", hw.LineCount)
	}
}

func TestWalk_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	result, err := Walk(dir, classifier.DefaultConfig())
	if err != nil {
		t.Fatalf("Walk returned error: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("Total: got %d, want 0", result.Total)
	}
	if len(result.Files) != 0 {
		t.Errorf("Files length: got %d, want 0", len(result.Files))
	}
}

func TestWalk_PackageExtraction(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "simple package main",
			content: "package main\n\nfunc main() {}\n",
			want:    "main",
		},
		{
			name:    "package with trailing comment",
			content: "package foo // comment\n",
			want:    "foo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "test.go", tt.content)

			result, err := Walk(dir, classifier.DefaultConfig())
			if err != nil {
				t.Fatalf("Walk returned error: %v", err)
			}
			if len(result.Files) != 1 {
				t.Fatalf("Files length: got %d, want 1", len(result.Files))
			}
			if result.Files[0].PackageName != tt.want {
				t.Errorf("PackageName: got %q, want %q", result.Files[0].PackageName, tt.want)
			}
		})
	}
}
