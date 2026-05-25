//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestContextParticipantUpsert(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	marker := fmt.Sprintf("ctx-p-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupContextMarker(t, db, marker) })

	src := &ContextSourceRecord{SourceType: "github", Namespace: "f", DisplayName: "i", ExternalKey: "github:" + marker, Metadata: []byte(`{}`), Enabled: true}
	if err := db.UpsertContextSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	item := &ContextItemRecord{SourceID: src.ID, ItemType: "github_pr", ExternalID: marker + "#1", Metadata: []byte(`{}`)}
	if err := db.UpsertContextItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	id := &PersonIdentityRecord{Provider: "github", ExternalAccountID: marker + "-acct", Metadata: []byte(`{}`)}
	if err := db.UpsertPersonIdentity(ctx, id); err != nil {
		t.Fatal(err)
	}

	p := &ContextParticipantRecord{
		ItemID:     item.ID,
		IdentityID: &id.ID,
		Role:       "author",
		Metadata:   []byte(`{}`),
	}
	if err := db.UpsertContextParticipant(ctx, p); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	firstID := p.ID

	// Zero-value IsCurrent must land as TRUE (column default applies).
	if p.IsCurrent == nil || !*p.IsCurrent {
		t.Fatalf("expected is_current=true by default, got %v", p.IsCurrent)
	}

	// Explicit false is honoured.
	falseV := false
	pFalse := &ContextParticipantRecord{
		ItemID:     item.ID,
		IdentityID: &id.ID,
		Role:       "reviewer",
		IsCurrent:  &falseV,
		Metadata:   []byte(`{}`),
	}
	if err := db.UpsertContextParticipant(ctx, pFalse); err != nil {
		t.Fatal(err)
	}
	if pFalse.IsCurrent == nil || *pFalse.IsCurrent {
		t.Fatalf("expected explicit is_current=false to persist, got %v", pFalse.IsCurrent)
	}

	// CHECK constraint rejects rows with neither person_id nor identity_id.
	bad := &ContextParticipantRecord{
		ItemID:   item.ID,
		Role:     "mentioned",
		Metadata: []byte(`{}`),
	}
	if err := db.UpsertContextParticipant(ctx, bad); err == nil {
		t.Fatalf("expected error on participant with no person and no identity")
	}

	// Re-upsert same (item, identity, role, source_role=NULL) — same row.
	if err := db.UpsertContextParticipant(ctx, &ContextParticipantRecord{
		ItemID:     item.ID,
		IdentityID: &id.ID,
		Role:       "author",
		Metadata:   []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM context_participants WHERE item_id=$1 AND identity_id=$2 AND role='author'`,
		item.ID, id.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 author row, got %d (firstID=%d)", n, firstID)
	}

	// Different source_role creates a new row.
	if err := db.UpsertContextParticipant(ctx, &ContextParticipantRecord{
		ItemID:     item.ID,
		IdentityID: &id.ID,
		Role:       "author",
		SourceRole: ptr("co-author"),
		Metadata:   []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM context_participants WHERE item_id=$1 AND identity_id=$2 AND role='author'`,
		item.ID, id.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 rows (one per source_role), got %d", n)
	}
}
