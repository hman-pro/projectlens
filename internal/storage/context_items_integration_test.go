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
