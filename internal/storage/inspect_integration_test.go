//go:build integration

package storage_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/storage"
)

func openIntegration(t *testing.T) *storage.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := storage.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedInspectFixture wipes test tables and inserts a small known graph:
//   - 2 files in package "pkg/a", 1 file in package "pkg/b"
//   - 3 symbols in pkg/a (across both files), 1 symbol in pkg/b
//   - 1 datastore_table public.orders (engine postgres)
//   - 2 reads_table edges from pkg/a symbols, 1 writes_table edge from pkg/b
//   - file_history rows: two files in pkg/a co-changed 3 times
//   - 4 knowledge_entries across 2 categories
func seedInspectFixture(t *testing.T, db *storage.DB) (cleanup func()) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	commit := false
	defer func() {
		if !commit {
			_ = tx.Rollback(ctx)
		}
	}()

	var fA1, fA2, fB1 int64
	const insertFile = `INSERT INTO files(path, package_name, checksum, commit_sha) VALUES ($1, $2, $3, 'testcommit') RETURNING id`
	if err := tx.QueryRow(ctx, insertFile, "pkg/a/x.go", "pkg/a", "x").Scan(&fA1); err != nil {
		t.Fatalf("file A1: %v", err)
	}
	if err := tx.QueryRow(ctx, insertFile, "pkg/a/y.go", "pkg/a", "y").Scan(&fA2); err != nil {
		t.Fatalf("file A2: %v", err)
	}
	if err := tx.QueryRow(ctx, insertFile, "pkg/b/z.go", "pkg/b", "z").Scan(&fB1); err != nil {
		t.Fatalf("file B1: %v", err)
	}

	var sA1, sA2, sA3, sB1 int64
	const insertSymbol = `INSERT INTO symbols(file_id, name, kind, package_name, signature, line_start, line_end, checksum) VALUES ($1, $2, 'func', $3, 'func()', 1, 2, $4) RETURNING id`
	if err := tx.QueryRow(ctx, insertSymbol, fA1, "F1", "pkg/a", "h-F1").Scan(&sA1); err != nil {
		t.Fatalf("sym A1: %v", err)
	}
	if err := tx.QueryRow(ctx, insertSymbol, fA1, "F2", "pkg/a", "h-F2").Scan(&sA2); err != nil {
		t.Fatalf("sym A2: %v", err)
	}
	if err := tx.QueryRow(ctx, insertSymbol, fA2, "F3", "pkg/a", "h-F3").Scan(&sA3); err != nil {
		t.Fatalf("sym A3: %v", err)
	}
	if err := tx.QueryRow(ctx, insertSymbol, fB1, "G1", "pkg/b", "h-G1").Scan(&sB1); err != nil {
		t.Fatalf("sym B1: %v", err)
	}

	var tID int64
	if err := tx.QueryRow(ctx, `INSERT INTO datastore_tables(name, engine, schema_name) VALUES ('orders','postgres','public') RETURNING id`).Scan(&tID); err != nil {
		t.Fatalf("table: %v", err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO edges(source_type, source_id, target_type, target_id, edge_type, properties, confidence) VALUES
		('symbol',$1,'datastore_table',$4,'reads_table','{}',1.0),
		('symbol',$2,'datastore_table',$4,'reads_table','{}',1.0),
		('symbol',$3,'datastore_table',$4,'writes_table','{}',1.0)
	`, sA1, sA3, sB1, tID); err != nil {
		t.Fatalf("edges: %v", err)
	}

	for _, h := range []string{"c1", "c2", "c3"} {
		if _, err := tx.Exec(ctx, `INSERT INTO file_history(file_id, commit_hash, author, committed_at, change_type) VALUES
			($1,$2,'a',NOW(),'M'),($3,$2,'a',NOW(),'M')`, fA1, h, fA2); err != nil {
			t.Fatalf("history %s: %v", h, err)
		}
	}

	if _, err := tx.Exec(ctx, `INSERT INTO knowledge_entries(category,title,body,source,created_at) VALUES
		('lesson','l1','b','test', NOW() - INTERVAL '1 day'),
		('lesson','l2','b','test', NOW() - INTERVAL '2 days'),
		('convention','c1','b','test', NOW()),
		('convention','c2','b','test', NOW() - INTERVAL '3 days')
	`); err != nil {
		t.Fatalf("knowledge: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	commit = true

	return func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE source = 'test'`)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM edges WHERE target_id = $1 AND target_type = 'datastore_table'`, tID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM datastore_tables WHERE id = $1`, tID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM file_history WHERE file_id IN ($1,$2,$3)`, fA1, fA2, fB1)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM symbols WHERE file_id IN ($1,$2,$3)`, fA1, fA2, fB1)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM files WHERE id IN ($1,$2,$3)`, fA1, fA2, fB1)
	}
}

func TestTopPackagesBySymbolCount(t *testing.T) {
	db := openIntegration(t)
	cleanup := seedInspectFixture(t, db)
	t.Cleanup(cleanup)

	// Use a large limit so the fixture packages (3 and 1 symbols) are visible
	// even when the DB already contains real packages with many more symbols.
	got, err := db.TopPackagesBySymbolCount(context.Background(), 100000)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	want := map[string]storage.PackageStat{
		"pkg/a": {ImportPath: "pkg/a", SymbolCount: 3, FileCount: 2},
		"pkg/b": {ImportPath: "pkg/b", SymbolCount: 1, FileCount: 1},
	}
	for _, p := range got {
		w, ok := want[p.ImportPath]
		if !ok {
			continue
		}
		if p != w {
			t.Errorf("pkg %s: got %+v want %+v", p.ImportPath, p, w)
		}
		delete(want, p.ImportPath)
	}
	if len(want) != 0 {
		t.Errorf("missing packages: %+v", want)
	}
}

func TestTopDatastoreTablesByEdgeCount(t *testing.T) {
	db := openIntegration(t)
	cleanup := seedInspectFixture(t, db)
	t.Cleanup(cleanup)

	got, err := db.TopDatastoreTablesByEdgeCount(context.Background(), 10)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var found bool
	for _, ts := range got {
		if ts.Schema == "public" && ts.Name == "orders" {
			found = true
			if ts.Engine != "postgres" {
				t.Errorf("engine: got %s want postgres", ts.Engine)
			}
			if ts.ReadRefs != 2 || ts.WriteRefs != 1 {
				t.Errorf("refs: got R=%d W=%d want 2/1", ts.ReadRefs, ts.WriteRefs)
			}
			if ts.SourceFileCount != 3 {
				t.Errorf("source files: got %d want 3", ts.SourceFileCount)
			}
		}
	}
	if !found {
		t.Errorf("public.orders not in top-N: %+v", got)
	}
}

func TestHighCouplingPairs(t *testing.T) {
	db := openIntegration(t)
	cleanup := seedInspectFixture(t, db)
	t.Cleanup(cleanup)

	// Use a large limit so the fixture pair (co-change=3) appears even when the
	// DB already contains real pairs with higher co-change counts.
	got, err := db.HighCouplingPairs(context.Background(), 100000, 3)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var found bool
	for _, p := range got {
		if p.FileA == "pkg/a/x.go" && p.FileB == "pkg/a/y.go" && p.CoChangeCount == 3 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected co-change pair (x.go, y.go, 3) missing: %+v", got)
	}
}

func TestKnowledgeStatsByCategory(t *testing.T) {
	db := openIntegration(t)
	cleanup := seedInspectFixture(t, db)
	t.Cleanup(cleanup)

	got, err := db.KnowledgeStatsByCategory(context.Background())
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got["lesson"] < 2 || got["convention"] < 2 {
		t.Errorf("counts missing: %+v", got)
	}
}

func TestRecentKnowledgeEntries(t *testing.T) {
	db := openIntegration(t)
	cleanup := seedInspectFixture(t, db)
	t.Cleanup(cleanup)

	// Use a large limit so all fixture entries are included even when the DB
	// already has many real entries with newer timestamps.
	got, err := db.RecentKnowledgeEntries(context.Background(), 100000)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	// Verify overall ordering invariant: results must be DESC by created_at.
	for i := 1; i < len(got); i++ {
		if got[i].CreatedAt.After(got[i-1].CreatedAt) {
			t.Errorf("order broken at %d: %v after %v", i, got[i].CreatedAt, got[i-1].CreatedAt)
		}
	}

	// Collect only our fixture entries (source='test') and verify their relative order.
	// Fixture timestamps (all relative to NOW() in the seeding transaction):
	//   c1: NOW()           — newest
	//   l1: NOW()-1day
	//   l2: NOW()-2days
	//   c2: NOW()-3days     — oldest
	var fixture []storage.KnowledgeSummary
	wantOrder := []string{"c1", "l1", "l2", "c2"}
	wantSet := map[string]bool{"l1": true, "l2": true, "c1": true, "c2": true}
	for _, e := range got {
		if wantSet[e.Title] {
			fixture = append(fixture, e)
		}
	}
	if len(fixture) != 4 {
		t.Fatalf("want 4 fixture entries, got %d: %+v", len(fixture), fixture)
	}
	for i, e := range fixture {
		if e.Title != wantOrder[i] {
			t.Errorf("fixture[%d]: got %s want %s", i, e.Title, wantOrder[i])
		}
	}
	_ = time.Second
}
