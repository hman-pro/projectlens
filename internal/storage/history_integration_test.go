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
