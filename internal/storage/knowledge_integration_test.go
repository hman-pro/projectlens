//go:build integration

// Prerequisites: Postgres on localhost:5433, projectlens database, migrations applied.
// Run: go test ./internal/storage/ -tags integration -run TestKnowledgeRoundtrip -v

package storage

import (
	"context"
	"fmt"
	"os"
	"strings"
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

// Exercises the multi-strategy anchor resolver: symbol short-name fallback
// (unique → resolves; >1 → unresolved + ambiguity reason), package
// import-path fallback, and the "not found" reason.
func TestKnowledgeAnchorResolution(t *testing.T) {
	ctx := context.Background()
	db, err := Connect(ctx, dbURL(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	marker := fmt.Sprintf("anchor-resolve-%d", time.Now().UnixNano())
	uniquePkg := marker + "_unique"
	dupPkg := marker + "_dup"

	uniqueFileID, err := db.UpsertFile(ctx, &FileRecord{
		Path: marker + "/unique.go", PackageName: uniquePkg,
		Checksum: "x", Language: "go", LineCount: 1, CommitSHA: "d",
		IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("upsert unique file: %v", err)
	}
	dupFileA, err := db.UpsertFile(ctx, &FileRecord{
		Path: marker + "/dup_a.go", PackageName: dupPkg,
		Checksum: "x", Language: "go", LineCount: 1, CommitSHA: "d",
		IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("upsert dup file A: %v", err)
	}
	dupFileB, err := db.UpsertFile(ctx, &FileRecord{
		Path: marker + "/dup_b.go", PackageName: dupPkg,
		Checksum: "x", Language: "go", LineCount: 1, CommitSHA: "d",
		IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("upsert dup file B: %v", err)
	}

	uniqueName := marker + "UniqFunc"
	uniqueScip := "go . " + uniquePkg + " . " + uniqueName
	dupName := marker + "Dup"
	dupScipA := "go . " + dupPkg + " . " + dupName + "A"
	dupScipB := "go . " + dupPkg + " . " + dupName + "B"

	if err := db.InsertSymbols(ctx, []SymbolRecord{{
		FileID: uniqueFileID, Name: uniqueName, Kind: "func", PackageName: uniquePkg,
		Signature: "func()", LineStart: 1, LineEnd: 1, Checksum: "y",
		IndexedAt: time.Now(), ScipSymbol: &uniqueScip,
	}, {
		FileID: dupFileA, Name: dupName, Kind: "func", PackageName: dupPkg,
		Signature: "func()", LineStart: 1, LineEnd: 1, Checksum: "y",
		IndexedAt: time.Now(), ScipSymbol: &dupScipA,
	}, {
		FileID: dupFileB, Name: dupName, Kind: "func", PackageName: dupPkg,
		Signature: "func()", LineStart: 1, LineEnd: 1, Checksum: "y",
		IndexedAt: time.Now(), ScipSymbol: &dupScipB,
	}}); err != nil {
		t.Fatalf("insert symbols: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM symbols WHERE file_id IN ($1,$2,$3)`,
			uniqueFileID, dupFileA, dupFileB)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM files WHERE id IN ($1,$2,$3)`,
			uniqueFileID, dupFileA, dupFileB)
	})

	// 1. Unique short-name resolves via name fallback when SCIP miss.
	id, ok, reason, err := db.resolveAnchor(ctx, AnchorRequest{Type: "symbol", Ref: uniqueName})
	if err != nil {
		t.Fatalf("unique resolve: %v", err)
	}
	if !ok || reason != "" {
		t.Fatalf("expected unique short-name to resolve, got ok=%v reason=%q", ok, reason)
	}
	if id == 0 {
		t.Fatalf("expected non-zero id")
	}

	// 2. Exact SCIP id still works (fast path).
	if _, ok, _, err := db.resolveAnchor(ctx, AnchorRequest{Type: "symbol", Ref: uniqueScip}); err != nil || !ok {
		t.Fatalf("scip exact: ok=%v err=%v", ok, err)
	}

	// 3. Ambiguous short name returns unresolved + count reason.
	_, ok, reason, err = db.resolveAnchor(ctx, AnchorRequest{Type: "symbol", Ref: dupName})
	if err != nil {
		t.Fatalf("dup resolve: %v", err)
	}
	if ok {
		t.Fatalf("expected ambiguous to be unresolved")
	}
	if !strings.Contains(reason, "ambiguous") || !strings.Contains(reason, "2") {
		t.Fatalf("expected reason to mention ambiguity and count, got %q", reason)
	}

	// 4. Missing symbol returns "not found".
	_, ok, reason, err = db.resolveAnchor(ctx, AnchorRequest{Type: "symbol", Ref: marker + "Nope"})
	if err != nil {
		t.Fatalf("missing resolve: %v", err)
	}
	if ok || reason != "not found" {
		t.Fatalf("expected not-found, got ok=%v reason=%q", ok, reason)
	}

	// 5. Package import-path fallback: "core/<pkg>" resolves to "<pkg>".
	_, ok, _, err = db.resolveAnchor(ctx, AnchorRequest{Type: "package", Ref: "core/" + uniquePkg})
	if err != nil {
		t.Fatalf("pkg import-path resolve: %v", err)
	}
	if !ok {
		t.Fatalf("expected import-path style package to resolve via last-segment fallback")
	}

	// 6. Package exact match path.
	if _, ok, _, err := db.resolveAnchor(ctx, AnchorRequest{Type: "package", Ref: uniquePkg}); err != nil || !ok {
		t.Fatalf("pkg exact: ok=%v err=%v", ok, err)
	}
}

// Exercises FindRecentDuplicateKnowledge: same (source,title,body) within the
// window returns the existing id; a different body bypasses dedup; window=0
// disables dedup.
func TestFindRecentDuplicateKnowledge(t *testing.T) {
	ctx := context.Background()
	db, err := Connect(ctx, dbURL(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	marker := fmt.Sprintf("dedup-%d", time.Now().UnixNano())
	entry := &KnowledgeEntry{
		Category: "lesson",
		Title:    marker + " title",
		Body:     "exact body content",
		Source:   "test-dedup",
	}
	entryID, _, err := db.InsertKnowledgeEntry(ctx, entry)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE id = $1`, entryID)
	})

	// 1. Exact match within window returns the id.
	hit, err := db.FindRecentDuplicateKnowledge(ctx,
		"test-dedup", entry.Title, entry.Body, 60*time.Second)
	if err != nil {
		t.Fatalf("dedup hit: %v", err)
	}
	if hit != entryID {
		t.Fatalf("expected dedup to return %d, got %d", entryID, hit)
	}

	// 2. Different body bypasses dedup.
	hit, err = db.FindRecentDuplicateKnowledge(ctx,
		"test-dedup", entry.Title, "different body", 60*time.Second)
	if err != nil {
		t.Fatalf("body diff: %v", err)
	}
	if hit != 0 {
		t.Fatalf("expected miss on body diff, got %d", hit)
	}

	// 3. Different source bypasses dedup.
	hit, err = db.FindRecentDuplicateKnowledge(ctx,
		"other-source", entry.Title, entry.Body, 60*time.Second)
	if err != nil {
		t.Fatalf("source diff: %v", err)
	}
	if hit != 0 {
		t.Fatalf("expected miss on source diff, got %d", hit)
	}

	// 4. window=0 disables dedup.
	hit, err = db.FindRecentDuplicateKnowledge(ctx,
		"test-dedup", entry.Title, entry.Body, 0)
	if err != nil {
		t.Fatalf("zero window: %v", err)
	}
	if hit != 0 {
		t.Fatalf("expected zero-window to disable dedup, got %d", hit)
	}
}
