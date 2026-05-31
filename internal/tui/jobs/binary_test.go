package jobs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hman-pro/projectlens/internal/tui/jobs"
)

func TestResolveBinary_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-bin")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROJECTLENS_BINARY", bin)
	got, err := jobs.ResolveBinary()
	if err != nil {
		t.Fatal(err)
	}
	if got != bin {
		t.Fatalf("got %q, want %q", got, bin)
	}
}

func TestResolveBinary_EnvNotExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "not-exec")
	if err := os.WriteFile(bin, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROJECTLENS_BINARY", bin)
	if _, err := jobs.ResolveBinary(); err == nil {
		t.Fatal("expected error for non-executable PROJECTLENS_BINARY")
	}
}

func TestResolveBinary_NotFound(t *testing.T) {
	t.Setenv("PROJECTLENS_BINARY", "")
	t.Setenv("PATH", t.TempDir())
	_, err := jobs.ResolveBinary()
	if err == nil {
		t.Fatal("expected error when binary cannot be resolved")
	}
}

// TestResolveBinary_PathNamedLens confirms the resolver looks for "lens" on PATH
// (not the old "projectlens" name). The sibling branch (step 2) is not covered
// here because os.Executable() returns the test runner path.
func TestResolveBinary_PathNamedLens(t *testing.T) {
	// A binary named "lens" on PATH should resolve (proves the resolver looks for "lens").
	dir := t.TempDir()
	bin := filepath.Join(dir, "lens")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROJECTLENS_BINARY", "")
	t.Setenv("PATH", dir)
	got, err := jobs.ResolveBinary()
	if err != nil {
		t.Fatal(err)
	}
	if got != bin {
		t.Fatalf("got %q, want %q", got, bin)
	}
}
