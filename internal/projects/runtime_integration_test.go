//go:build integration

package projects_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hman-pro/projectlens/internal/projects"
	"github.com/hman-pro/projectlens/internal/storage"
)

func TestResolveOpensScopedPool(t *testing.T) {
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
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_rt_test CASCADE`)
	})

	migrationsDir := findMigrationsDirForTest(t)
	if err := root.MigrateInSchema(ctx, migrationsDir, "ri_rt_test"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	regPath := filepath.Join(dir, "projects.yaml")
	_ = os.WriteFile(regPath, []byte(`
database_url: `+url+`
projects:
  - slug: rt
    storage_schema: ri_rt_test
    repo_path: /tmp/x
`), 0o644)

	reg, err := projects.LoadRegistry(regPath)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := projects.Resolve(ctx, reg, "rt")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defer rt.Close()
	if rt.StorageSchema != "ri_rt_test" {
		t.Errorf("StorageSchema=%q", rt.StorageSchema)
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
