package projects

import (
	"strings"
	"testing"
)

func TestLoadRegistryValid(t *testing.T) {
	reg, err := LoadRegistry("testdata/valid.yaml")
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if reg.DatabaseURL == "" {
		t.Error("expected database_url")
	}
	if len(reg.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(reg.Projects))
	}
	if _, err := reg.Find("ingest"); err != nil {
		t.Errorf("Find(ingest): %v", err)
	}
	if _, err := reg.Find("nope"); err == nil {
		t.Error("Find(nope) should error")
	}
	if reg.DefaultProject != "ingest" {
		t.Errorf("default_project=%q", reg.DefaultProject)
	}
}

func TestLoadRegistryDuplicateSlug(t *testing.T) {
	_, err := LoadRegistry("testdata/dup_slug.yaml")
	if err == nil || !strings.Contains(err.Error(), "duplicate slug") {
		t.Fatalf("expected duplicate slug error, got %v", err)
	}
}

func TestLoadRegistryDuplicateSchema(t *testing.T) {
	_, err := LoadRegistry("testdata/dup_schema.yaml")
	if err == nil || !strings.Contains(err.Error(), "duplicate storage_schema") {
		t.Fatalf("expected duplicate storage_schema error, got %v", err)
	}
}

func TestLoadRegistryMissingRepoPath(t *testing.T) {
	_, err := LoadRegistry("testdata/missing_repo.yaml")
	if err == nil || !strings.Contains(err.Error(), "repo_path") {
		t.Fatalf("expected repo_path error, got %v", err)
	}
}
