//go:build integration

package storage

import (
	"context"
	"testing"
)

// cleanupContextMarker deletes every row created by a Phase 1 context-graph
// test that embedded `marker` into a marker-bearing column. Cascade FKs
// handle child rows. Call once per test:
//
//	marker := fmt.Sprintf("ctx-test-%s-%d", t.Name(), time.Now().UnixNano())
//	t.Cleanup(func() { cleanupContextMarker(t, db, marker) })
//
// Safe to call on a shared dev DB: it only touches rows whose marker-bearing
// columns contain the substring. Logs (does not fail) on per-table errors so
// one failure does not hide another.
func cleanupContextMarker(t *testing.T, db *DB, marker string) {
	t.Helper()
	ctx := context.Background()
	like := "%" + marker + "%"

	// Order: participants → chunks → versions → items → identities → people → sources.
	// FKs from items, versions, chunks, participants cascade, so deleting items
	// first removes the dependent rows automatically. We still target each
	// table directly in case a test inserted rows but never linked them.
	stmts := []struct{ table, where string }{
		{"context_participants", "metadata::text LIKE $1"},
		{"context_chunks", "chunk_anchor_id LIKE $1 OR source_anchor_id LIKE $1"},
		{"context_item_versions", "content_hash LIKE $1"},
		{"context_items", "external_id LIKE $1"},
		{"person_identities", "external_account_id LIKE $1"},
		{"people", "display_name LIKE $1 OR primary_email_hash LIKE $1"},
		{"context_source_state", "metadata::text LIKE $1"},
		{"context_sources", "external_key LIKE $1"},
	}
	for _, s := range stmts {
		if _, err := db.Pool.Exec(ctx,
			"DELETE FROM "+s.table+" WHERE "+s.where, like,
		); err != nil {
			t.Logf("cleanup %s: %v", s.table, err)
		}
	}
}

// TestContextGraphTablesExist verifies migration 009 created every Phase 1 table.
// We do not migrate down on a shared dev DB; this only asserts presence.
func TestContextGraphTablesExist(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	wantTables := []string{
		"context_sources",
		"context_source_state",
		"people",
		"person_identities",
		"context_items",
		"context_item_versions",
		"context_chunks",
		"context_participants",
	}

	for _, tbl := range wantTables {
		var exists bool
		err := db.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema='public' AND table_name=$1)`,
			tbl,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("query %s: %v", tbl, err)
		}
		if !exists {
			t.Fatalf("table %s missing — did migration 009 run?", tbl)
		}
	}
}
