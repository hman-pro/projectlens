//go:build integration

// Prerequisites: Postgres on localhost:5433, projectlens database, migrations applied.
// Run: go test ./internal/storage/ -tags integration -run TestKnowledgeRoundtrip -v

package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/pgvector/pgvector-go"
)

func dbURL(t *testing.T) string {
	u := os.Getenv("DATABASE_URL")
	if u == "" {
		u = "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"
	}
	return u
}

func TestKnowledgeRoundtrip(t *testing.T) {
	ctx := context.Background()
	db, err := Connect(ctx, dbURL(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	marker := fmt.Sprintf("knowledge-test-%d", time.Now().UnixNano())

	// 1. seed a fake file + symbol so we have a target to anchor to
	fileID, err := db.UpsertFile(ctx, &FileRecord{
		Path: marker + "/foo.go", PackageName: marker + "_pkg",
		Checksum: "x", Language: "go", LineCount: 1, CommitSHA: "deadbeef",
		IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	scip := "go . " + marker + "_pkg . Foo()"
	if err := db.InsertSymbols(ctx, []SymbolRecord{{
		FileID: fileID, Name: "Foo", Kind: "func", PackageName: marker + "_pkg",
		Signature: "func Foo()", LineStart: 1, LineEnd: 1, Checksum: "y",
		IndexedAt: time.Now(), ScipSymbol: &scip,
	}}); err != nil {
		t.Fatalf("insert symbol: %v", err)
	}

	var symID int64
	if err := db.Pool.QueryRow(ctx,
		`SELECT id FROM symbols WHERE scip_symbol = $1`, scip).Scan(&symID); err != nil {
		t.Fatalf("lookup symbol: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM symbols WHERE file_id = $1`, fileID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM files   WHERE id = $1`, fileID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE title LIKE $1`, marker+"%")
	})

	// 2. insert knowledge entry + chunk
	entry := &KnowledgeEntry{
		Category: "lesson",
		Title:    marker + " title",
		Body:     "Discovered that Foo behaves like X.",
		Tags:     []string{marker, "test"},
	}
	entryID, chunkID, err := db.InsertKnowledgeEntry(ctx, entry)
	if err != nil {
		t.Fatalf("insert knowledge: %v", err)
	}
	if entryID == 0 || chunkID == 0 {
		t.Fatalf("expected non-zero ids, got entry=%d chunk=%d", entryID, chunkID)
	}

	// 3. anchor it to the symbol
	res, err := db.InsertKnowledgeAnchors(ctx, entryID, []AnchorRequest{
		{Type: "symbol", Ref: scip},
		{Type: "symbol", Ref: "doesnotexist"},
	})
	if err != nil {
		t.Fatalf("anchor: %v", err)
	}
	if len(res) != 2 || !res[0].Resolved || res[1].Resolved {
		t.Fatalf("expected first resolved, second not: %+v", res)
	}

	// 4. fake an embedding (1024-dim zeros) so vector search returns the row
	vec := make([]float32, 1024)
	if err := db.UpsertEmbedding(ctx, &EmbeddingRecord{
		ChunkID: chunkID, ModelVersion: "test", Embedding: pgvector.NewHalfVector(vec),
	}); err != nil {
		t.Fatalf("upsert embedding: %v", err)
	}

	// 5. vector search hits it
	hits, err := db.SearchKnowledgeByVector(ctx, vec, "", 10)
	if err != nil {
		t.Fatalf("vector search: %v", err)
	}
	foundVec := false
	for _, h := range hits {
		if h.Entry.ID == entryID {
			foundVec = true
			break
		}
	}
	if !foundVec {
		t.Fatalf("vector search did not return entry %d", entryID)
	}

	// 6. anchor traversal hits it
	anchored, err := db.KnowledgeForAnchor(ctx, AnchorRequest{Type: "symbol", Ref: scip}, 10)
	if err != nil {
		t.Fatalf("anchor search: %v", err)
	}
	if len(anchored) != 1 || anchored[0].ID != entryID {
		t.Fatalf("expected one anchored entry, got %+v", anchored)
	}

	// 7. delete cleans up
	n, err := db.DeleteKnowledgeEntry(ctx, entryID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row deleted, got %d", n)
	}

	var count int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM edges WHERE source_type='knowledge' AND source_id=$1`,
		entryID).Scan(&count); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected anchor edges deleted, found %d", count)
	}
}

// Exercises KnowledgeForSymbolWithPackage and the package-anchor path, plus
// the not-found semantics of GetKnowledgeEntry. Specifically asserts that
// surfaced entries are ordered newest-first.
func TestKnowledgeSymbolWithPackageAndNotFound(t *testing.T) {
	ctx := context.Background()
	db, err := Connect(ctx, dbURL(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	marker := fmt.Sprintf("knowledge-symxpkg-%d", time.Now().UnixNano())
	pkg := marker + "_pkg"

	fileID, err := db.UpsertFile(ctx, &FileRecord{
		Path: marker + "/bar.go", PackageName: pkg,
		Checksum: "x", Language: "go", LineCount: 1, CommitSHA: "deadbeef",
		IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	scip := "go . " + pkg + " . Bar()"
	if err := db.InsertSymbols(ctx, []SymbolRecord{{
		FileID: fileID, Name: "Bar", Kind: "func", PackageName: pkg,
		Signature: "func Bar()", LineStart: 1, LineEnd: 1, Checksum: "y",
		IndexedAt: time.Now(), ScipSymbol: &scip,
	}}); err != nil {
		t.Fatalf("insert symbol: %v", err)
	}
	var symID int64
	if err := db.Pool.QueryRow(ctx,
		`SELECT id FROM symbols WHERE scip_symbol = $1`, scip).Scan(&symID); err != nil {
		t.Fatalf("lookup symbol: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM symbols WHERE file_id = $1`, fileID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM files   WHERE id = $1`, fileID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE title LIKE $1`, marker+"%")
	})

	// Older entry, anchored to the package.
	older := &KnowledgeEntry{
		Category: "convention",
		Title:    marker + " older",
		Body:     "older body",
	}
	olderID, _, err := db.InsertKnowledgeEntry(ctx, older)
	if err != nil {
		t.Fatalf("insert older: %v", err)
	}
	if _, err := db.InsertKnowledgeAnchors(ctx, olderID,
		[]AnchorRequest{{Type: "package", Ref: pkg}}); err != nil {
		t.Fatalf("anchor older: %v", err)
	}

	// Force a measurable created_at delta.
	time.Sleep(20 * time.Millisecond)

	// Newer entry, anchored directly to the symbol.
	newer := &KnowledgeEntry{
		Category: "lesson",
		Title:    marker + " newer",
		Body:     "newer body",
	}
	newerID, _, err := db.InsertKnowledgeEntry(ctx, newer)
	if err != nil {
		t.Fatalf("insert newer: %v", err)
	}
	if _, err := db.InsertKnowledgeAnchors(ctx, newerID,
		[]AnchorRequest{{Type: "symbol", Ref: scip}}); err != nil {
		t.Fatalf("anchor newer: %v", err)
	}

	// KnowledgeForSymbolWithPackage should see both entries (one symbol, one
	// package) and order newest-first.
	hits, err := db.KnowledgeForSymbolWithPackage(ctx, symID, 10)
	if err != nil {
		t.Fatalf("symbol+package search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(hits), hits)
	}
	if hits[0].ID != newerID || hits[1].ID != olderID {
		t.Fatalf("expected newer (%d) before older (%d), got [%d, %d]",
			newerID, olderID, hits[0].ID, hits[1].ID)
	}

	// Package-anchor path of KnowledgeForAnchor.
	pkgHits, err := db.KnowledgeForAnchor(ctx, AnchorRequest{Type: "package", Ref: pkg}, 10)
	if err != nil {
		t.Fatalf("package anchor search: %v", err)
	}
	if len(pkgHits) != 1 || pkgHits[0].ID != olderID {
		t.Fatalf("expected only older (%d) on package anchor, got %+v", olderID, pkgHits)
	}

	// GetKnowledgeEntry returns (nil, nil) for missing ids.
	got, err := db.GetKnowledgeEntry(ctx, 0)
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing id, got %+v", got)
	}
}
