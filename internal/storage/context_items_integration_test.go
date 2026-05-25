//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestContextItemUpsertAndDelete(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	marker := fmt.Sprintf("ctx-it-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupContextMarker(t, db, marker) })

	src := &ContextSourceRecord{
		SourceType:  "github",
		Namespace:   "example-org",
		DisplayName: "ingest",
		ExternalKey: "github:" + marker + "/ingest",
		Metadata:    []byte(`{}`),
		Enabled:     true,
	}
	if err := db.UpsertContextSource(ctx, src); err != nil {
		t.Fatalf("source: %v", err)
	}

	pr := &ContextItemRecord{
		SourceID:   src.ID,
		ItemType:   "github_pr",
		ExternalID: "example-org/ingest#" + marker,
		URL:        ptr("https://example/pr/1"),
		Title:      ptr("test PR"),
		State:      ptr("open"),
		Metadata:   []byte(`{}`),
	}
	if err := db.UpsertContextItem(ctx, pr); err != nil {
		t.Fatalf("upsert pr: %v", err)
	}
	if pr.ID == 0 {
		t.Fatal("PR id not set")
	}

	// Root self-reference: spec says root items have parent NULL and root = self.
	if err := db.SetContextItemRoot(ctx, pr.ID, pr.ID); err != nil {
		t.Fatalf("set root: %v", err)
	}

	// Child item.
	cmt := &ContextItemRecord{
		SourceID:     src.ID,
		ItemType:     "github_pr_issue_comment",
		ExternalID:   "example-org/ingest#" + marker + "/c1",
		ParentItemID: &pr.ID,
		RootItemID:   &pr.ID,
		Title:        ptr("comment"),
		Metadata:     []byte(`{}`),
	}
	if err := db.UpsertContextItem(ctx, cmt); err != nil {
		t.Fatalf("upsert comment: %v", err)
	}

	// Re-upsert PR: same (source_id, item_type, external_id) → same ID.
	prAgain := &ContextItemRecord{
		SourceID:   src.ID,
		ItemType:   "github_pr",
		ExternalID: pr.ExternalID,
		Title:      ptr("test PR renamed"),
		State:      ptr("merged"),
		Metadata:   []byte(`{}`),
	}
	if err := db.UpsertContextItem(ctx, prAgain); err != nil {
		t.Fatalf("re-upsert pr: %v", err)
	}
	if prAgain.ID != pr.ID {
		t.Fatalf("PR id changed on re-upsert: %d -> %d", pr.ID, prAgain.ID)
	}

	// Delete leaves the row but sets deleted_at.
	if err := db.MarkContextItemDeleted(ctx, cmt.ID); err != nil {
		t.Fatalf("mark deleted: %v", err)
	}
	got, _ := db.GetContextItem(ctx, cmt.ID)
	if got == nil || got.DeletedAt == nil {
		t.Fatalf("deleted_at not set: %+v", got)
	}
}

func TestContextItemCascadeDeletesVersionsAndChunks(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	marker := fmt.Sprintf("ctx-cas-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupContextMarker(t, db, marker) })

	src := &ContextSourceRecord{SourceType: "github", Namespace: "f", DisplayName: "i", ExternalKey: "github:" + marker, Metadata: []byte(`{}`), Enabled: true}
	if err := db.UpsertContextSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	item := &ContextItemRecord{SourceID: src.ID, ItemType: "github_pr", ExternalID: marker + "#1", Metadata: []byte(`{}`)}
	if err := db.UpsertContextItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	ver := &ContextItemVersionRecord{ItemID: item.ID, ContentHash: marker + "-h", BodyText: "b", Redaction: []byte(`{}`), Metadata: []byte(`{}`)}
	if _, err := db.UpsertContextItemVersion(ctx, ver); err != nil {
		t.Fatal(err)
	}
	anchor := "github.pr." + marker
	chunk := &ContextChunkRecord{
		ItemVersionID:  ver.ID,
		ChunkKey:       "ord-0",
		ChunkAnchorID:  anchor + "#ordinal/0",
		SourceAnchorID: anchor,
		ChunkIndex:     0,
		ContentHash:    marker + "-c1",
		Metadata:       []byte(`{}`),
	}
	if err := db.UpsertContextChunk(ctx, chunk); err != nil {
		t.Fatal(err)
	}

	// Hard delete the item to exercise ON DELETE CASCADE on versions and chunks.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM context_items WHERE id=$1`, item.ID); err != nil {
		t.Fatal(err)
	}
	var vCount, cCount int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM context_item_versions WHERE id=$1`, ver.ID).Scan(&vCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM context_chunks WHERE id=$1`, chunk.ID).Scan(&cCount); err != nil {
		t.Fatal(err)
	}
	if vCount != 0 || cCount != 0 {
		t.Fatalf("cascade failed: versions=%d chunks=%d", vCount, cCount)
	}
}

func TestContextParticipantIdentityOnly(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	marker := fmt.Sprintf("ctx-io-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupContextMarker(t, db, marker) })

	src := &ContextSourceRecord{SourceType: "slack", Namespace: "x", DisplayName: "x", ExternalKey: "slack:" + marker, Metadata: []byte(`{}`), Enabled: true}
	if err := db.UpsertContextSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	item := &ContextItemRecord{SourceID: src.ID, ItemType: "slack_thread", ExternalID: marker, Metadata: []byte(`{}`)}
	if err := db.UpsertContextItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	id := &PersonIdentityRecord{Provider: "slack", ExternalAccountID: marker + "-u", Metadata: []byte(`{}`)}
	if err := db.UpsertPersonIdentity(ctx, id); err != nil {
		t.Fatal(err)
	}

	// person_id intentionally nil — ambiguous identity, per spec.
	p := &ContextParticipantRecord{
		ItemID:     item.ID,
		IdentityID: &id.ID,
		Role:       "participant",
		Metadata:   []byte(`{}`),
	}
	if err := db.UpsertContextParticipant(ctx, p); err != nil {
		t.Fatalf("identity-only participant: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("participant id not set")
	}
}
