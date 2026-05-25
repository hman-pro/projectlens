//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestContextSourceUpsertAndState(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	marker := fmt.Sprintf("ctx-src-%d", time.Now().UnixNano())
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
		t.Fatalf("upsert: %v", err)
	}
	if src.ID == 0 {
		t.Fatalf("ID not populated")
	}

	got, err := db.GetContextSourceByExternalKey(ctx, "github", src.ExternalKey)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.ID != src.ID {
		t.Fatalf("round-trip mismatch: got=%+v", got)
	}

	// Re-upsert with new display name; ID stable.
	src.DisplayName = "ingest-renamed"
	if err := db.UpsertContextSource(ctx, src); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got2, _ := db.GetContextSourceByExternalKey(ctx, "github", src.ExternalKey)
	if got2.ID != src.ID {
		t.Fatalf("ID changed on re-upsert: was %d now %d", src.ID, got2.ID)
	}
	if got2.DisplayName != "ingest-renamed" {
		t.Fatalf("display_name not updated: %q", got2.DisplayName)
	}

	// State write/read.
	now := time.Now().UTC().Truncate(time.Microsecond)
	cur := "2026-05-25T00:00:00Z"
	st := &ContextSourceStateRecord{
		SourceID:         src.ID,
		Stream:           "prs",
		CursorKind:       "updated_at",
		CursorValue:      &cur,
		LastSuccessfulAt: &now,
		Metadata:         []byte(`{}`),
	}
	if err := db.UpsertContextSourceState(ctx, st); err != nil {
		t.Fatalf("upsert state: %v", err)
	}

	gotSt, err := db.GetContextSourceState(ctx, src.ID, "prs")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if gotSt == nil || gotSt.CursorKind != "updated_at" || gotSt.CursorValue == nil || *gotSt.CursorValue != cur {
		t.Fatalf("state mismatch: %+v", gotSt)
	}
}
