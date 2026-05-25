//go:build integration

// Integration tests for edges storage methods against a live database.
// Run with: go test ./internal/storage/ -tags integration -v
//
// Prerequisites:
//   - Postgres running on localhost:5433 with projectlens database
//   - Migrations applied.
//
// These tests use marker-based isolation so they can run safely against a
// shared dev database (the live DB has 217K+ edges). They do not truncate.
package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestDeleteEdgesByType(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	// Unique marker so we can isolate this test's rows on a shared dev DB.
	// IMPORTANT: the method under test performs a global DELETE keyed only on
	// (source_type, target_type, edge_type). The live DB already contains real
	// co_changes (file→file) edges (~741 rows), so if we invoked the method
	// with the literal production triple we would destroy them. Instead, we
	// use marker-scoped edge_type values. This still exercises the method's
	// behavior (scoped delete by the triple) without touching production rows.
	marker := fmt.Sprintf("test-de-bt-%d", time.Now().UnixNano())
	pathA := "/tmp/" + marker + "-a.go"
	pathB := "/tmp/" + marker + "-b.go"
	pathC := "/tmp/" + marker + "-c.go"

	// Marker-scoped edge types — not used anywhere in production.
	testCoChangesType := "test_co_changes_" + marker
	testCallsType := "test_calls_" + marker

	// Seed three files. Note: polymorphic edges table has no FK to files, so
	// we could use arbitrary IDs — but using real file IDs from UpsertFile
	// keeps the test closer to production behavior.
	fileAID, err := db.UpsertFile(ctx, &FileRecord{
		Path:        pathA,
		PackageName: "testpkg",
		Checksum:    "checksum-a-" + marker,
		Language:    "go",
		CommitSHA:   "commit-" + marker,
	})
	if err != nil {
		t.Fatalf("UpsertFile a: %v", err)
	}
	fileBID, err := db.UpsertFile(ctx, &FileRecord{
		Path:        pathB,
		PackageName: "testpkg",
		Checksum:    "checksum-b-" + marker,
		Language:    "go",
		CommitSHA:   "commit-" + marker,
	})
	if err != nil {
		t.Fatalf("UpsertFile b: %v", err)
	}
	fileCID, err := db.UpsertFile(ctx, &FileRecord{
		Path:        pathC,
		PackageName: "testpkg",
		Checksum:    "checksum-c-" + marker,
		Language:    "go",
		CommitSHA:   "commit-" + marker,
	})
	if err != nil {
		t.Fatalf("UpsertFile c: %v", err)
	}

	// Cleanup: delete any edges this test owns, then the files.
	// We must remove edges before the files because, while the edges table
	// itself has no FK to files, we still want a scoped cleanup tied to the
	// marker-owned IDs.
	t.Cleanup(func() {
		// Clean up by marker-scoped edge_type first (primary), then by our
		// file IDs (backstop for anything inserted with another type).
		if _, err := db.Pool.Exec(ctx,
			`DELETE FROM edges WHERE edge_type LIKE $1`, "test_%_"+marker,
		); err != nil {
			t.Logf("cleanup edges by marker: %v", err)
		}
		if _, err := db.Pool.Exec(ctx,
			`DELETE FROM edges WHERE source_type = 'file' AND source_id = ANY($1)`,
			[]int64{fileAID, fileBID, fileCID},
		); err != nil {
			t.Logf("cleanup edges: %v", err)
		}
		if _, err := db.Pool.Exec(ctx,
			`DELETE FROM files WHERE path = ANY($1)`,
			[]string{pathA, pathB, pathC},
		); err != nil {
			t.Logf("cleanup files: %v", err)
		}
	})

	// Insert three edges:
	//  - A -> B (file→file) of the coupling-like test type (the type we'll delete)
	//  - A -> C (file→file) of a different test type       (same source_type/target_type, different edge_type; must survive)
	//  - A -> symbol(fakeSymbolID) (file→symbol) with SAME edge_type as the deleted one
	//    (tests target_type discrimination — if the method accidentally used
	//    source_type as both predicates, this would also get deleted.)
	// The edges table is polymorphic with no FK to symbols, so a made-up
	// target_id is fine.
	const fakeSymbolID int64 = 999999
	conf := float32(0.5)
	if err := db.InsertEdges(ctx, []EdgeRecord{
		{
			SourceType: "file",
			SourceID:   fileAID,
			TargetType: "file",
			TargetID:   fileBID,
			EdgeType:   testCoChangesType,
			Confidence: &conf,
		},
		{
			SourceType: "file",
			SourceID:   fileAID,
			TargetType: "file",
			TargetID:   fileCID,
			EdgeType:   testCallsType,
			Confidence: &conf,
		},
		{
			SourceType: "file",
			SourceID:   fileAID,
			TargetType: "symbol",
			TargetID:   fakeSymbolID,
			EdgeType:   testCoChangesType,
			Confidence: &conf,
		},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Sanity check: both edges should exist before delete.
	var preCoChange, preCalls int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM edges
		 WHERE source_type='file' AND source_id=$1 AND target_type='file' AND target_id=$2 AND edge_type=$3`,
		fileAID, fileBID, testCoChangesType,
	).Scan(&preCoChange); err != nil {
		t.Fatalf("pre-count co_changes: %v", err)
	}
	if preCoChange != 1 {
		t.Fatalf("precondition: expected 1 %s edge A->B, got %d", testCoChangesType, preCoChange)
	}
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM edges
		 WHERE source_type='file' AND source_id=$1 AND target_type='file' AND target_id=$2 AND edge_type=$3`,
		fileAID, fileCID, testCallsType,
	).Scan(&preCalls); err != nil {
		t.Fatalf("pre-count calls: %v", err)
	}
	if preCalls != 1 {
		t.Fatalf("precondition: expected 1 %s edge A->C, got %d", testCallsType, preCalls)
	}

	// Act: delete only edges matching (file, file, testCoChangesType).
	// This exercises DeleteEdgesByType exactly as IndexHistory will use it:
	// scoped to the triple (source_type, target_type, edge_type).
	removed, err := db.DeleteEdgesByType(ctx, "file", "file", testCoChangesType)
	if err != nil {
		t.Fatalf("DeleteEdgesByType: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected RowsAffected=1 for (file,file,%s), got %d", testCoChangesType, removed)
	}

	// Assert: our testCoChangesType A->B edge is gone.
	var postCoChange int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM edges
		 WHERE source_type='file' AND source_id=$1 AND target_type='file' AND target_id=$2 AND edge_type=$3`,
		fileAID, fileBID, testCoChangesType,
	).Scan(&postCoChange); err != nil {
		t.Fatalf("post-count co_changes: %v", err)
	}
	if postCoChange != 0 {
		t.Errorf("expected %s edge removed, still present: count=%d", testCoChangesType, postCoChange)
	}

	// Assert: our testCallsType A->C edge survives (different edge_type).
	var postCalls int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM edges
		 WHERE source_type='file' AND source_id=$1 AND target_type='file' AND target_id=$2 AND edge_type=$3`,
		fileAID, fileCID, testCallsType,
	).Scan(&postCalls); err != nil {
		t.Fatalf("post-count calls: %v", err)
	}
	if postCalls != 1 {
		t.Errorf("expected %s edge to survive, got count=%d", testCallsType, postCalls)
	}

	// Assert: the file->symbol edge with the SAME edge_type survives.
	// This verifies target_type is an actual predicate (not a copy-paste of source_type).
	var postSymbol int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM edges
		 WHERE source_type='file' AND source_id=$1 AND target_type='symbol' AND target_id=$2 AND edge_type=$3`,
		fileAID, fakeSymbolID, testCoChangesType,
	).Scan(&postSymbol); err != nil {
		t.Fatalf("post-count file->symbol: %v", err)
	}
	if postSymbol != 1 {
		t.Errorf("expected file->symbol %s edge to survive (target_type discrimination), got count=%d",
			testCoChangesType, postSymbol)
	}

	// Negative path: deleting a non-existent (file, file, edge_type) triple
	// should return (0, nil) — no rows matched is not an error.
	t.Run("no_rows_matched", func(t *testing.T) {
		n, err := db.DeleteEdgesByType(ctx, "file", "file", "nonexistent_"+marker)
		if err != nil {
			t.Fatalf("DeleteEdgesByType (negative path) returned error: %v", err)
		}
		if n != 0 {
			t.Errorf("expected RowsAffected=0 for nonexistent edge_type, got %d", n)
		}
	})
}

func TestInsertEdgesProvenance(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	marker := fmt.Sprintf("test-prov-%d", time.Now().UnixNano())
	pathA := "/tmp/" + marker + "-a.go"
	pathB := "/tmp/" + marker + "-b.go"

	fileAID, err := db.UpsertFile(ctx, &FileRecord{
		Path: pathA, PackageName: "testpkg", Checksum: "ca-" + marker, Language: "go", CommitSHA: "c-" + marker,
	})
	if err != nil {
		t.Fatalf("UpsertFile A: %v", err)
	}
	fileBID, err := db.UpsertFile(ctx, &FileRecord{
		Path: pathB, PackageName: "testpkg", Checksum: "cb-" + marker, Language: "go", CommitSHA: "c-" + marker,
	})
	if err != nil {
		t.Fatalf("UpsertFile B: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Pool.Exec(ctx,
			`DELETE FROM edges WHERE source_type='file' AND source_id = ANY($1)`,
			[]int64{fileAID, fileBID},
		); err != nil {
			t.Logf("cleanup edges: %v", err)
		}
		if _, err := db.Pool.Exec(ctx,
			`DELETE FROM files WHERE path = ANY($1)`,
			[]string{pathA, pathB},
		); err != nil {
			t.Logf("cleanup files: %v", err)
		}
	})

	edgeType := "test_prov_calls_" + marker
	conf := float32(0.75)
	if err := db.InsertEdges(ctx, []EdgeRecord{
		{
			SourceType: "file", SourceID: fileAID,
			TargetType: "file", TargetID: fileBID,
			EdgeType:        edgeType,
			Confidence:      &conf,
			Provenance:      "callgraph",
			ConfidenceClass: "inferred",
		},
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	var prov, class string
	var gotConf float32
	if err := db.Pool.QueryRow(ctx, `
		SELECT provenance, confidence_class, confidence
		FROM edges WHERE source_id=$1 AND target_id=$2 AND edge_type=$3
	`, fileAID, fileBID, edgeType).Scan(&prov, &class, &gotConf); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if prov != "callgraph" || class != "inferred" || gotConf != 0.75 {
		t.Errorf("round-trip mismatch: prov=%q class=%q conf=%v", prov, class, gotConf)
	}

	// Upsert path: changing class on conflict should propagate via EXCLUDED.
	if err := db.InsertEdges(ctx, []EdgeRecord{
		{
			SourceType: "file", SourceID: fileAID,
			TargetType: "file", TargetID: fileBID,
			EdgeType:        edgeType,
			Confidence:      &conf,
			Provenance:      "callgraph",
			ConfidenceClass: "ambiguous",
		},
	}); err != nil {
		t.Fatalf("InsertEdges (upsert): %v", err)
	}
	if err := db.Pool.QueryRow(ctx, `
		SELECT confidence_class FROM edges WHERE source_id=$1 AND target_id=$2 AND edge_type=$3
	`, fileAID, fileBID, edgeType).Scan(&class); err != nil {
		t.Fatalf("scan upsert: %v", err)
	}
	if class != "ambiguous" {
		t.Errorf("upsert did not update confidence_class: got %q want %q", class, "ambiguous")
	}

	// Empty provenance + class round-trip as NULL (not empty string).
	edgeType2 := "test_prov_null_" + marker
	if err := db.InsertEdges(ctx, []EdgeRecord{
		{SourceType: "file", SourceID: fileAID, TargetType: "file", TargetID: fileBID, EdgeType: edgeType2},
	}); err != nil {
		t.Fatalf("InsertEdges (null): %v", err)
	}
	var nullProv, nullClass *string
	if err := db.Pool.QueryRow(ctx, `
		SELECT provenance, confidence_class FROM edges WHERE source_id=$1 AND target_id=$2 AND edge_type=$3
	`, fileAID, fileBID, edgeType2).Scan(&nullProv, &nullClass); err != nil {
		t.Fatalf("scan null: %v", err)
	}
	if nullProv != nil || nullClass != nil {
		t.Errorf("expected NULL provenance/class, got prov=%v class=%v", nullProv, nullClass)
	}

	// CHECK constraint rejects invalid class.
	bad := EdgeRecord{
		SourceType: "file", SourceID: fileAID, TargetType: "file", TargetID: fileBID,
		EdgeType: "test_prov_bad_" + marker, ConfidenceClass: "definitely",
	}
	if err := db.InsertEdges(ctx, []EdgeRecord{bad}); err == nil {
		t.Errorf("expected CHECK violation for invalid confidence_class")
	}
}

func TestBackfillProvenance_PartialRepair(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	marker := fmt.Sprintf("test-backfill-%d", time.Now().UnixNano())
	pathA := "/tmp/" + marker + "-a.go"
	pathB := "/tmp/" + marker + "-b.go"
	fileAID, err := db.UpsertFile(ctx, &FileRecord{
		Path: pathA, PackageName: "testpkg", Checksum: "ca-" + marker, Language: "go", CommitSHA: "c-" + marker,
	})
	if err != nil {
		t.Fatalf("UpsertFile A: %v", err)
	}
	fileBID, err := db.UpsertFile(ctx, &FileRecord{
		Path: pathB, PackageName: "testpkg", Checksum: "cb-" + marker, Language: "go", CommitSHA: "c-" + marker,
	})
	if err != nil {
		t.Fatalf("UpsertFile B: %v", err)
	}

	// Use a marker-scoped edge_type to avoid touching production rows.
	edgeType := "test_backfill_" + marker
	t.Cleanup(func() {
		if _, err := db.Pool.Exec(ctx,
			`DELETE FROM edges WHERE edge_type = $1`, edgeType,
		); err != nil {
			t.Logf("cleanup edges: %v", err)
		}
		if _, err := db.Pool.Exec(ctx,
			`DELETE FROM files WHERE path = ANY($1)`, []string{pathA, pathB},
		); err != nil {
			t.Logf("cleanup files: %v", err)
		}
	})

	// Three rows: NULL/NULL, prov-only, class-only — partial rows must be
	// repaired by the backfill without overwriting set values.
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO edges (source_type, source_id, target_type, target_id, edge_type, provenance, confidence_class)
		VALUES
		    ('file', $1, 'file', $2, $3, NULL,         NULL),
		    ('file', $2, 'file', $1, $3, 'callgraph',  NULL),
		    ('file', $1, 'file', $2, $3 || '-x', NULL, 'extracted')
	`, fileAID, fileBID, edgeType); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Backfill the base edgeType with defaults that should fill missing values
	// but leave the already-set 'callgraph' provenance untouched.
	n, err := db.BackfillProvenance(ctx, edgeType, "history", "inferred")
	if err != nil {
		t.Fatalf("backfill base: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 rows updated for %q, got %d", edgeType, n)
	}

	// Backfill the -x variant: row already has class, only prov needs filling.
	if _, err := db.BackfillProvenance(ctx, edgeType+"-x", "callgraph", "ambiguous"); err != nil {
		t.Fatalf("backfill x: %v", err)
	}

	// Verify final state.
	rows, err := db.Pool.Query(ctx, `
		SELECT source_id, target_id, edge_type, provenance, confidence_class
		FROM edges WHERE edge_type = ANY($1)
		ORDER BY edge_type, source_id, target_id
	`, []string{edgeType, edgeType + "-x"})
	if err != nil {
		t.Fatalf("verify query: %v", err)
	}
	defer rows.Close()
	type row struct {
		src, tgt  int64
		etype     string
		prov, cls *string
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.src, &r.tgt, &r.etype, &r.prov, &r.cls); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}

	for _, r := range got {
		if r.prov == nil || r.cls == nil {
			t.Errorf("row %+v still has NULL: prov=%v cls=%v", r, r.prov, r.cls)
		}
	}
	// The prov-only seed row must keep 'callgraph', not be overwritten with 'history'.
	for _, r := range got {
		if r.etype == edgeType && r.src == fileBID && r.tgt == fileAID {
			if r.prov == nil || *r.prov != "callgraph" {
				t.Errorf("expected preserved provenance=callgraph on prov-only row, got %v", r.prov)
			}
		}
	}
	// Class-only seed row must keep 'extracted', not be overwritten with 'ambiguous'.
	for _, r := range got {
		if r.etype == edgeType+"-x" {
			if r.cls == nil || *r.cls != "extracted" {
				t.Errorf("expected preserved confidence_class=extracted on class-only row, got %v", r.cls)
			}
		}
	}

	// Re-running on a fully-filled set is a no-op.
	n2, err := db.BackfillProvenance(ctx, edgeType, "history", "inferred")
	if err != nil {
		t.Fatalf("backfill rerun: %v", err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 rows on idempotent rerun, got %d", n2)
	}
}
