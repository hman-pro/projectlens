//go:build integration

package storage_test

import (
	"context"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/storage"
)

func TestScopedPoolsDoNotSeeAcrossSchemas(t *testing.T) {
	url := os.Getenv("PROJECTLENS_DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()

	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_iso_a CASCADE`)
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_iso_b CASCADE`)
	})

	dir := findMigrationsDirForTest(t)
	for _, s := range []string{"ri_iso_a", "ri_iso_b"} {
		if err := root.MigrateInSchema(ctx, dir, s); err != nil {
			t.Fatalf("migrate %s: %v", s, err)
		}
	}

	dbA, err := storage.ConnectScoped(ctx, url, "ri_iso_a")
	if err != nil {
		t.Fatal(err)
	}
	defer dbA.Close()
	dbB, err := storage.ConnectScoped(ctx, url, "ri_iso_b")
	if err != nil {
		t.Fatal(err)
	}
	defer dbB.Close()

	if _, err := dbA.Pool.Exec(ctx, `INSERT INTO files(path, package_name, checksum, commit_sha) VALUES ('a.go','pkg/a','xxx','c')`); err != nil {
		t.Fatalf("insert into A: %v", err)
	}

	var nB int
	if err := dbB.Pool.QueryRow(ctx, `SELECT count(*) FROM files`).Scan(&nB); err != nil {
		t.Fatalf("count from B: %v", err)
	}
	if nB != 0 {
		t.Fatalf("expected 0 files visible from B, got %d", nB)
	}
}

func TestConnectionReuseKeepsScope(t *testing.T) {
	url := os.Getenv("PROJECTLENS_DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_reuse CASCADE`)
	})
	if err := root.MigrateInSchema(ctx, findMigrationsDirForTest(t), "ri_reuse"); err != nil {
		t.Fatal(err)
	}
	db, err := storage.ConnectScoped(ctx, url, "ri_reuse")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for i := 0; i < 5; i++ {
		c, err := db.Pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var sp string
		if err := c.QueryRow(ctx, `SHOW search_path`).Scan(&sp); err != nil {
			c.Release()
			t.Fatal(err)
		}
		c.Release()
		// Postgres normalizes SHOW search_path output (may drop quotes, may add space).
		// Accept the equivalence class; what matters is the schema appears first then public.
		ok := map[string]bool{
			`"ri_reuse", public`: true,
			`"ri_reuse",public`:  true,
			`ri_reuse, public`:   true,
			`ri_reuse,public`:    true,
		}
		if !ok[sp] {
			t.Fatalf("iter %d: search_path=%q lost scope", i, sp)
		}
	}
}

func TestSymbolsIndexRunsKnowledgeDoNotCrossSchemas(t *testing.T) {
	url := os.Getenv("PROJECTLENS_DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_iso2_a CASCADE`)
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_iso2_b CASCADE`)
	})
	dir := findMigrationsDirForTest(t)
	for _, s := range []string{"ri_iso2_a", "ri_iso2_b"} {
		if err := root.MigrateInSchema(ctx, dir, s); err != nil {
			t.Fatal(err)
		}
	}
	dbA, err := storage.ConnectScoped(ctx, url, "ri_iso2_a")
	if err != nil {
		t.Fatal(err)
	}
	defer dbA.Close()
	dbB, err := storage.ConnectScoped(ctx, url, "ri_iso2_b")
	if err != nil {
		t.Fatal(err)
	}
	defer dbB.Close()

	// Insert a file in A (required for symbol FK), then a symbol.
	// symbols requires package_name and checksum (NOT NULL) in this schema.
	var fileID int64
	if err := dbA.Pool.QueryRow(ctx,
		`INSERT INTO files(path, package_name, checksum, commit_sha) VALUES ('s.go','pkg/a','x','c') RETURNING id`,
	).Scan(&fileID); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	if _, err := dbA.Pool.Exec(ctx,
		`INSERT INTO symbols(file_id, name, kind, package_name, signature, line_start, line_end, checksum) VALUES ($1,'F','func','pkg/a','',1,1,'sx')`,
		fileID); err != nil {
		t.Fatalf("insert symbol: %v", err)
	}
	if _, err := dbA.Pool.Exec(ctx,
		`INSERT INTO index_runs(commit_sha, status, started_at, stage) VALUES ('c','running', NOW(), 'parse')`); err != nil {
		t.Fatalf("insert index_runs: %v", err)
	}
	// knowledge_entries.category has a CHECK constraint; use an allowed value.
	if _, err := dbA.Pool.Exec(ctx,
		`INSERT INTO knowledge_entries(category, title, body) VALUES ('lesson','t','b')`); err != nil {
		t.Fatalf("insert knowledge: %v", err)
	}

	for _, tbl := range []string{"symbols", "index_runs", "knowledge_entries"} {
		var n int
		if err := dbB.Pool.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n); err != nil {
			t.Fatalf("count %s from B: %v", tbl, err)
		}
		if n != 0 {
			t.Errorf("expected 0 %s visible from B, got %d", tbl, n)
		}
	}
}
