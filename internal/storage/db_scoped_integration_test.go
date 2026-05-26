//go:build integration

package storage_test

import (
	"context"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/storage"
)

func TestConnectScopedSetsSearchPath(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()

	// Manually create schema "ri_scopetest" for this test.
	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect root: %v", err)
	}
	defer root.Close()
	if _, err := root.Pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS ri_scopetest`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_scopetest CASCADE`)
	})

	db, err := storage.ConnectScoped(ctx, url, "ri_scopetest")
	if err != nil {
		t.Fatalf("ConnectScoped: %v", err)
	}
	defer db.Close()

	var sp string
	if err := db.Pool.QueryRow(ctx, `SHOW search_path`).Scan(&sp); err != nil {
		t.Fatalf("show search_path: %v", err)
	}
	// Postgres normalizes SHOW search_path: it drops quotes around identifiers
	// that don't need them and may add a space after the comma. Accept all
	// equivalent forms — what matters is the schema appears first then public.
	want := map[string]bool{
		`"ri_scopetest", public`: true,
		`"ri_scopetest",public`:  true,
		`ri_scopetest, public`:   true,
		`ri_scopetest,public`:    true,
	}
	if !want[sp] {
		t.Fatalf("search_path = %q; want ri_scopetest first then public", sp)
	}
}

func TestConnectScopedFailsOnMissingSchema(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	_, err := storage.ConnectScoped(ctx, url, "ri_does_not_exist_xyz")
	if err == nil {
		t.Fatal("ConnectScoped did not error on missing schema")
	}
}
