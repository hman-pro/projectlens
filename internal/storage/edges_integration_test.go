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

	// Insert two edges:
	//  - A -> B of the coupling-like test type (the type we'll delete)
	//  - A -> C of a different test type       (same source_type/target_type, different edge_type; must survive)
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
	if err := db.DeleteEdgesByType(ctx, "file", "file", testCoChangesType); err != nil {
		t.Fatalf("DeleteEdgesByType: %v", err)
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
}
