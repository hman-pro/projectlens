//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestContextItemVersionLineage(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	marker := fmt.Sprintf("ctx-v-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupContextMarker(t, db, marker) })

	src := &ContextSourceRecord{SourceType: "github", Namespace: "f", DisplayName: "i", ExternalKey: "github:" + marker, Metadata: []byte(`{}`), Enabled: true}
	if err := db.UpsertContextSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	item := &ContextItemRecord{SourceID: src.ID, ItemType: "github_pr", ExternalID: marker + "#1", Metadata: []byte(`{}`)}
	if err := db.UpsertContextItem(ctx, item); err != nil {
		t.Fatal(err)
	}

	// 1. First version. Content hashes carry marker so cleanup picks them up.
	v1 := &ContextItemVersionRecord{
		ItemID:      item.ID,
		ContentHash: marker + "-hash-a",
		BodyText:    "hello world",
		Redaction:   []byte(`{}`),
		Metadata:    []byte(`{}`),
	}
	res, err := db.UpsertContextItemVersion(ctx, v1)
	if err != nil {
		t.Fatalf("v1: %v", err)
	}
	if !res.Inserted {
		t.Fatalf("v1 should have been inserted")
	}

	// 2. Same hash → no new row.
	v1again := &ContextItemVersionRecord{
		ItemID:      item.ID,
		ContentHash: marker + "-hash-a",
		BodyText:    "hello world",
		Redaction:   []byte(`{}`),
		Metadata:    []byte(`{}`),
	}
	res2, err := db.UpsertContextItemVersion(ctx, v1again)
	if err != nil {
		t.Fatalf("v1again: %v", err)
	}
	if res2.Inserted {
		t.Fatalf("same hash should not insert a new version")
	}
	if res2.VersionID != v1.ID {
		t.Fatalf("expected current version id %d, got %d", v1.ID, res2.VersionID)
	}

	// 3. Different hash → supersede.
	v2 := &ContextItemVersionRecord{
		ItemID:      item.ID,
		ContentHash: marker + "-hash-b",
		BodyText:    "hello again",
		Redaction:   []byte(`{}`),
		Metadata:    []byte(`{}`),
	}
	res3, err := db.UpsertContextItemVersion(ctx, v2)
	if err != nil {
		t.Fatalf("v2: %v", err)
	}
	if !res3.Inserted {
		t.Fatalf("v2 should have been inserted")
	}
	if v2.ID == v1.ID {
		t.Fatalf("v2 reused v1 id")
	}

	// 4. Confirm only one current per item.
	var currentCount int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM context_item_versions WHERE item_id=$1 AND is_current=TRUE`,
		item.ID).Scan(&currentCount); err != nil {
		t.Fatal(err)
	}
	if currentCount != 1 {
		t.Fatalf("expected 1 current version, got %d", currentCount)
	}

	// 5. Old row is now is_current=false and has superseded_at.
	var oldIsCurrent bool
	var oldSuperseded *time.Time
	if err := db.Pool.QueryRow(ctx,
		`SELECT is_current, superseded_at FROM context_item_versions WHERE id=$1`,
		v1.ID).Scan(&oldIsCurrent, &oldSuperseded); err != nil {
		t.Fatal(err)
	}
	if oldIsCurrent || oldSuperseded == nil {
		t.Fatalf("v1 not properly superseded: is_current=%v superseded_at=%v", oldIsCurrent, oldSuperseded)
	}

	// 6. GetCurrentVersion returns v2.
	cur, err := db.GetCurrentContextItemVersion(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur == nil || cur.ID != v2.ID {
		t.Fatalf("current mismatch: %+v", cur)
	}

	// 7. Same-hash reingest still refreshes item metadata when the importer
	//    calls UpsertContextItem first. This is the spec's same-hash rule:
	//    "update item metadata and last_seen_at, but do not insert a new version".
	item.Title = ptr("renamed title")
	item.State = ptr("merged")
	item.Metadata = []byte(`{"label":"important"}`)
	if err := db.UpsertContextItem(ctx, item); err != nil {
		t.Fatalf("refresh item: %v", err)
	}
	// Re-call version upsert with the current hash (marker+"-hash-b") — must not insert.
	vSame := &ContextItemVersionRecord{
		ItemID:      item.ID,
		ContentHash: marker + "-hash-b",
		BodyText:    "hello again",
		Redaction:   []byte(`{}`),
		Metadata:    []byte(`{}`),
	}
	res4, err := db.UpsertContextItemVersion(ctx, vSame)
	if err != nil {
		t.Fatal(err)
	}
	if res4.Inserted {
		t.Fatalf("same-hash reingest should not insert a new version")
	}
	got, _ := db.GetContextItem(ctx, item.ID)
	if got == nil || got.Title == nil || *got.Title != "renamed title" || got.State == nil || *got.State != "merged" {
		t.Fatalf("item metadata not refreshed on same-hash reingest: %+v", got)
	}
}
