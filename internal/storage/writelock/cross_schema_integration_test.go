//go:build integration

package writelock_test

import (
	"context"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/hman-pro/projectlens/internal/storage/writelock"
)

// TestAcquire_CrossSchemaProceedsIndependently pins the multi-project
// invariant: writers targeting distinct storage schemas must not
// contend on the global advisory lock. A writer in schema A holding
// its lock must NOT block a concurrent acquire in schema B.
func TestAcquire_CrossSchemaProceedsIndependently(t *testing.T) {
	url := os.Getenv("PROJECTLENS_DATABASE_URL")
	if url == "" {
		url = testDB
	}
	ctx := context.Background()

	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Skipf("connect: %v", err)
	}
	defer root.Close()

	const schemaA = "ri_wlx_a"
	const schemaB = "ri_wlx_b"
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schemaA+` CASCADE`)
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schemaB+` CASCADE`)
	})

	dir := findMigrationsDirForWritelockTest(t)
	for _, s := range []string{schemaA, schemaB} {
		if err := root.MigrateInSchema(ctx, dir, s); err != nil {
			t.Fatalf("migrate %s: %v", s, err)
		}
	}

	dbA, err := storage.ConnectScoped(ctx, url, schemaA)
	if err != nil {
		t.Fatalf("connect A: %v", err)
	}
	defer dbA.Close()
	dbB, err := storage.ConnectScoped(ctx, url, schemaB)
	if err != nil {
		t.Fatalf("connect B: %v", err)
	}
	defer dbB.Close()

	// A acquires and HOLDS its lock across B's attempt.
	lockA, err := writelock.Acquire(ctx, dbA, "writer-A", schemaA)
	if err != nil {
		t.Fatalf("acquire A: %v", err)
	}
	defer lockA.Release(context.Background())

	// Same-schema contention still blocks: a second A-attempt must
	// observe ErrBusy with A's identity, proving the per-schema key
	// did not become global noise.
	if _, err := writelock.Acquire(ctx, dbA, "writer-A-2", schemaA); err == nil {
		t.Errorf("second acquire in schema A succeeded; want ErrBusy")
	} else if _, ok := err.(writelock.ErrBusy); !ok {
		t.Errorf("second acquire in schema A: got %T (%v); want ErrBusy", err, err)
	}

	// B acquires successfully despite A still holding.
	lockB, err := writelock.Acquire(ctx, dbB, "writer-B", schemaB)
	if err != nil {
		t.Fatalf("acquire B blocked by A: %v", err)
	}
	defer lockB.Release(context.Background())

	// Liveness probe is also per-schema.
	activeA, err := writelock.IsWriterActive(ctx, dbA, schemaA)
	if err != nil {
		t.Fatalf("IsWriterActive A: %v", err)
	}
	if !activeA {
		t.Errorf("IsWriterActive A = false; want true (lockA still held)")
	}
	activeB, err := writelock.IsWriterActive(ctx, dbB, schemaB)
	if err != nil {
		t.Fatalf("IsWriterActive B: %v", err)
	}
	if !activeB {
		t.Errorf("IsWriterActive B = false; want true (lockB still held)")
	}

	// Force-unlock B must not touch A.
	if err := writelock.ForceUnlock(ctx, dbB, schemaB); err != nil {
		t.Fatalf("ForceUnlock B: %v", err)
	}
	activeA, err = writelock.IsWriterActive(ctx, dbA, schemaA)
	if err != nil {
		t.Fatalf("IsWriterActive A post B-unlock: %v", err)
	}
	if !activeA {
		t.Errorf("ForceUnlock(B) collaterally cleared A")
	}
}

func findMigrationsDirForWritelockTest(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"../../../migrations", "../../../../migrations"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatal("migrations dir not found")
	return ""
}
