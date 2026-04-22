//go:build integration

// Integration tests for file_history/symbol_history storage methods against a
// live database.
// Run with: go test ./internal/storage/ -tags integration -v
//
// Prerequisites:
//   - Postgres running on localhost:5433 with projectlens database
//   - Migrations applied (bootstrap or `projectlens migrate` will do this).
//
// These tests are destructive to the file_history and files tables in the
// rows they insert: they clean up after themselves with explicit deletes.
package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

const testDB = "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"

// connectForIntegration opens a connection to the live test database, skipping
// the test if the database is unavailable.
func connectForIntegration(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()
	db, err := Connect(ctx, testDB)
	if err != nil {
		t.Skipf("cannot connect to test database: %v", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		t.Skipf("cannot ping test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestGetLatestFileHistoryTimestamp(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	// Use a unique marker so we can clean up only what this test created and
	// avoid clobbering indexed data in a shared dev database.
	marker := fmt.Sprintf("test-latest-fh-%d", time.Now().UnixNano())
	filePath := "/tmp/" + marker + ".go"

	// Clean up any rows this test creates (in reverse FK order).
	t.Cleanup(func() {
		// Delete history rows by commit_hash marker prefix, then the file.
		if _, err := db.Pool.Exec(ctx,
			`DELETE FROM file_history WHERE commit_hash LIKE $1`, marker+"-%",
		); err != nil {
			t.Logf("cleanup file_history: %v", err)
		}
		if _, err := db.Pool.Exec(ctx, `DELETE FROM files WHERE path = $1`, filePath); err != nil {
			t.Logf("cleanup files: %v", err)
		}
	})

	// --- Empty-table case ---
	//
	// If the repo's file_history happens to already contain rows (because
	// this is a shared dev DB), we skip the empty-case assertion: the method
	// under test just returns MAX(committed_at). What we need to verify in
	// that case is only that the returned timestamp is monotonic and reflects
	// any later inserts we make. We detect empty-ness via COUNT(*) rather
	// than truncating the table.
	var preCount int64
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM file_history`).Scan(&preCount); err != nil {
		t.Fatalf("count file_history: %v", err)
	}

	if preCount == 0 {
		ts, found, err := db.GetLatestFileHistoryTimestamp(ctx)
		if err != nil {
			t.Fatalf("GetLatestFileHistoryTimestamp empty: %v", err)
		}
		if found {
			t.Errorf("expected found=false on empty table, got found=true ts=%v", ts)
		}
		if !ts.IsZero() {
			t.Errorf("expected zero time on empty table, got %v", ts)
		}
	} else {
		t.Logf("file_history already has %d rows; skipping empty-case assertion", preCount)
	}

	// --- Populated case: insert a file plus two history rows with distinct timestamps ---
	fileID, err := db.UpsertFile(ctx, &FileRecord{
		Path:        filePath,
		PackageName: "testpkg",
		Checksum:    "checksum-" + marker,
		Language:    "go",
		CommitSHA:   "commit-" + marker,
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	earlier := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	later := time.Date(2024, 6, 15, 9, 30, 0, 0, time.UTC)

	if err := db.InsertFileHistory(ctx, &FileHistoryRecord{
		FileID:      fileID,
		CommitHash:  marker + "-earlier",
		Author:      "test-author",
		CommittedAt: earlier,
		ChangeType:  "modified",
	}); err != nil {
		t.Fatalf("InsertFileHistory earlier: %v", err)
	}
	if err := db.InsertFileHistory(ctx, &FileHistoryRecord{
		FileID:      fileID,
		CommitHash:  marker + "-later",
		Author:      "test-author",
		CommittedAt: later,
		ChangeType:  "modified",
	}); err != nil {
		t.Fatalf("InsertFileHistory later: %v", err)
	}

	ts, found, err := db.GetLatestFileHistoryTimestamp(ctx)
	if err != nil {
		t.Fatalf("GetLatestFileHistoryTimestamp populated: %v", err)
	}
	if !found {
		t.Fatal("expected found=true after inserting rows")
	}

	// When the shared DB has pre-existing rows with later timestamps, the
	// result is global MAX — not necessarily our 'later' value. So assert
	// "at least as recent as our later insert".
	if ts.Before(later) {
		t.Errorf("expected latest timestamp >= %v, got %v", later, ts)
	}

	// And if we started empty, our inserts are the only rows, so we can
	// pin down the exact value.
	if preCount == 0 && !ts.Equal(later) {
		t.Errorf("expected latest timestamp == %v (empty starting state), got %v", later, ts)
	}
}

func TestListCommitsInWindow(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	// Unique markers so we can isolate this test's rows on a shared dev DB.
	marker := fmt.Sprintf("test-lciw-%d", time.Now().UnixNano())
	c1Hash := marker + "-c1"
	cOldHash := marker + "-c-old"
	pathA := "/tmp/" + marker + "-a.go"
	pathB := "/tmp/" + marker + "-b.go"

	t.Cleanup(func() {
		if _, err := db.Pool.Exec(ctx,
			`DELETE FROM file_history WHERE commit_hash LIKE $1`, marker+"-%",
		); err != nil {
			t.Logf("cleanup file_history: %v", err)
		}
		if _, err := db.Pool.Exec(ctx,
			`DELETE FROM files WHERE path = ANY($1)`, []string{pathA, pathB},
		); err != nil {
			t.Logf("cleanup files: %v", err)
		}
	})

	// Seed two files.
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

	// c1 is inside the 12-month window (24 hours ago).
	// Use a truncated timestamp so we don't fight Postgres microsecond precision.
	// c1/a and c1/b are deliberately inserted 5 minutes apart so we can assert
	// the returned timestamp is MIN(committed_at) rather than MAX or arbitrary.
	insideTSEarly := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	insideTSLate := insideTSEarly.Add(5 * time.Minute)
	// c_old is outside the window (400 days ago).
	outsideTS := time.Now().Add(-400 * 24 * time.Hour).UTC().Truncate(time.Second)

	// c1 touches both files (two file_history rows, same commit_hash).
	if err := db.InsertFileHistory(ctx, &FileHistoryRecord{
		FileID:      fileAID,
		CommitHash:  c1Hash,
		Author:      "test-author",
		CommittedAt: insideTSEarly,
		ChangeType:  "modified",
	}); err != nil {
		t.Fatalf("InsertFileHistory c1/a: %v", err)
	}
	if err := db.InsertFileHistory(ctx, &FileHistoryRecord{
		FileID:      fileBID,
		CommitHash:  c1Hash,
		Author:      "test-author",
		CommittedAt: insideTSLate,
		ChangeType:  "modified",
	}); err != nil {
		t.Fatalf("InsertFileHistory c1/b: %v", err)
	}
	// c_old touches only file a, outside window.
	if err := db.InsertFileHistory(ctx, &FileHistoryRecord{
		FileID:      fileAID,
		CommitHash:  cOldHash,
		Author:      "test-author",
		CommittedAt: outsideTS,
		ChangeType:  "modified",
	}); err != nil {
		t.Fatalf("InsertFileHistory c_old/a: %v", err)
	}

	all, err := db.ListCommitsInWindow(ctx, 12)
	if err != nil {
		t.Fatalf("ListCommitsInWindow: %v", err)
	}

	// The shared DB has 14K+ rows; filter to just ours by marker prefix.
	var mine []CommitFiles
	for _, c := range all {
		// Defensive: we only care about rows we created.
		if len(c.Hash) >= len(marker) && c.Hash[:len(marker)] == marker {
			mine = append(mine, c)
		}
	}

	if len(mine) != 1 {
		t.Fatalf("expected exactly one commit with marker %q in window, got %d: %+v", marker, len(mine), mine)
	}
	got := mine[0]
	if got.Hash != c1Hash {
		t.Errorf("hash: want %q, got %q", c1Hash, got.Hash)
	}
	// c_old must NOT appear.
	for _, c := range mine {
		if c.Hash == cOldHash {
			t.Errorf("c_old (%q) should have been filtered out by the 12-month window", cOldHash)
		}
	}
	if len(got.Files) != 2 {
		t.Fatalf("expected 2 files for c1, got %d: %+v", len(got.Files), got.Files)
	}
	// ARRAY_AGG is ORDER BY path in the query — so Files should be sorted.
	wantFiles := []string{pathA, pathB} // pathA ends in "-a.go", pathB ends in "-b.go"
	for i, f := range wantFiles {
		if got.Files[i] != f {
			t.Errorf("Files[%d]: want %q, got %q (full: %v)", i, f, got.Files[i], got.Files)
		}
	}
	// Timestamp should match the EARLIER of c1's two inserts — the query uses
	// MIN(committed_at). Within a second tolerance for Postgres truncation /
	// pgx UTC round-trip.
	delta := got.Timestamp.Sub(insideTSEarly)
	if delta < -time.Second || delta > time.Second {
		t.Errorf("timestamp: want ~%v (MIN of c1 inserts), got %v (delta %v)", insideTSEarly, got.Timestamp, delta)
	}

	// --- Empty-window case: months=0 means NOW() - 0 = NOW(), so nothing in
	// the past qualifies. All seeded rows are in the past, so our marker
	// should be absent. (We filter by marker so unrelated rows in the shared
	// DB don't affect the assertion.)
	t.Run("empty_window", func(t *testing.T) {
		empty, err := db.ListCommitsInWindow(ctx, 0)
		if err != nil {
			t.Fatalf("ListCommitsInWindow(0): %v", err)
		}
		for _, c := range empty {
			if len(c.Hash) >= len(marker) && c.Hash[:len(marker)] == marker {
				t.Errorf("expected no rows with marker %q in zero-month window, got %+v", marker, c)
			}
		}
	})
}
