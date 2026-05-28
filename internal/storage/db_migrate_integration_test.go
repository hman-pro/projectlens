//go:build integration

package storage_test

import (
	"context"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/storage"
)

func TestMigrateInSchemaIsolatesTwoProjects(t *testing.T) {
	url := os.Getenv("PROJECTLENS_DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()

	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect root: %v", err)
	}
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_proj_a CASCADE`)
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_proj_b CASCADE`)
	})

	dir := findMigrationsDirForTest(t)
	if err := root.MigrateInSchema(ctx, dir, "ri_proj_a"); err != nil {
		t.Fatalf("migrate proj_a: %v", err)
	}
	if err := root.MigrateInSchema(ctx, dir, "ri_proj_b"); err != nil {
		t.Fatalf("migrate proj_b: %v", err)
	}

	var nA, nB int
	if err := root.Pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema='ri_proj_a' AND table_name='files'`).Scan(&nA); err != nil {
		t.Fatalf("count A: %v", err)
	}
	if err := root.Pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema='ri_proj_b' AND table_name='files'`).Scan(&nB); err != nil {
		t.Fatalf("count B: %v", err)
	}
	if nA != 1 || nB != 1 {
		t.Fatalf("expected files in both schemas: nA=%d nB=%d", nA, nB)
	}

	var mA, mB int
	_ = root.Pool.QueryRow(ctx, `SELECT count(*) FROM ri_proj_a.schema_migrations`).Scan(&mA)
	_ = root.Pool.QueryRow(ctx, `SELECT count(*) FROM ri_proj_b.schema_migrations`).Scan(&mB)
	if mA == 0 || mB == 0 {
		t.Fatalf("expected schema_migrations rows in each schema, got A=%d B=%d", mA, mB)
	}
}

func findMigrationsDirForTest(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"../../migrations", "../../../migrations"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatal("migrations dir not found")
	return ""
}
