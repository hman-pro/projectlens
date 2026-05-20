package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMigrationFiles(t *testing.T) {
	// Create a temporary directory with some migration files.
	dir := t.TempDir()

	files := map[string]string{
		"001_initial.up.sql": "CREATE TABLE test1 (id INT);",
		"002_second.up.sql":  "CREATE TABLE test2 (id INT);",
		"003_third.down.sql": "DROP TABLE test2;", // should be ignored
		"README.md":          "not a migration",   // should be ignored
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write file %s: %v", name, err)
		}
	}

	mfs, err := ReadMigrationFiles(dir)
	if err != nil {
		t.Fatalf("ReadMigrationFiles: %v", err)
	}

	if len(mfs) != 2 {
		t.Fatalf("expected 2 migration files, got %d", len(mfs))
	}

	if mfs[0].Name != "001_initial.up.sql" {
		t.Errorf("expected first file to be 001_initial.up.sql, got %s", mfs[0].Name)
	}
	if mfs[1].Name != "002_second.up.sql" {
		t.Errorf("expected second file to be 002_second.up.sql, got %s", mfs[1].Name)
	}

	if mfs[0].SQL != "CREATE TABLE test1 (id INT);" {
		t.Errorf("unexpected SQL for first file: %s", mfs[0].SQL)
	}
	if mfs[1].SQL != "CREATE TABLE test2 (id INT);" {
		t.Errorf("unexpected SQL for second file: %s", mfs[1].SQL)
	}
}

func TestReadMigrationFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	mfs, err := ReadMigrationFiles(dir)
	if err != nil {
		t.Fatalf("ReadMigrationFiles: %v", err)
	}

	if len(mfs) != 0 {
		t.Fatalf("expected 0 migration files, got %d", len(mfs))
	}
}

func TestReadMigrationFiles_NonexistentDir(t *testing.T) {
	_, err := ReadMigrationFiles("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}
