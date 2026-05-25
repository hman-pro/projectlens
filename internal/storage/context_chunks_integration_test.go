//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestContextChunkUpsert(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	marker := fmt.Sprintf("ctx-ch-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupContextMarker(t, db, marker) })

	src := &ContextSourceRecord{SourceType: "github", Namespace: "f", DisplayName: "i", ExternalKey: "github:" + marker, Metadata: []byte(`{}`), Enabled: true}
	if err := db.UpsertContextSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	item := &ContextItemRecord{SourceID: src.ID, ItemType: "github_pr", ExternalID: marker + "#1", Metadata: []byte(`{}`)}
	if err := db.UpsertContextItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	ver := &ContextItemVersionRecord{ItemID: item.ID, ContentHash: marker + "-h1", BodyText: "body", Redaction: []byte(`{}`), Metadata: []byte(`{}`)}
	if _, err := db.UpsertContextItemVersion(ctx, ver); err != nil {
		t.Fatal(err)
	}

	sourceAnchor := "github.pr.example-org/ingest." + marker
	chunkAnchor := sourceAnchor + "#ordinal/0"

	c := &ContextChunkRecord{
		ItemVersionID:  ver.ID,
		ChunkKey:       "ord-0",
		ChunkAnchorID:  chunkAnchor,
		SourceAnchorID: sourceAnchor,
		ChunkIndex:     0,
		ContentHash:    marker + "-ch-1",
		TokenCount:     42,
		Metadata:       []byte(`{}`),
	}
	if err := db.UpsertContextChunk(ctx, c); err != nil {
		t.Fatalf("upsert chunk: %v", err)
	}
	if c.ID == 0 {
		t.Fatal("chunk id missing")
	}

	// Re-upsert by chunk_anchor_id; same row, different token_count.
	c2 := &ContextChunkRecord{
		ItemVersionID:  ver.ID,
		ChunkKey:       "ord-0",
		ChunkAnchorID:  chunkAnchor,
		SourceAnchorID: sourceAnchor,
		ChunkIndex:     0,
		ContentHash:    marker + "-ch-2",
		TokenCount:     99,
		Metadata:       []byte(`{}`),
	}
	if err := db.UpsertContextChunk(ctx, c2); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if c2.ID != c.ID {
		t.Fatalf("chunk id changed: %d -> %d", c.ID, c2.ID)
	}

	// Confirm only one row for this anchor.
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM context_chunks WHERE chunk_anchor_id = $1`, chunkAnchor).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 chunk row, got %d", n)
	}

	// Duplicate (item_version_id, chunk_key) with a different anchor must fail.
	dup := &ContextChunkRecord{
		ItemVersionID:  ver.ID,
		ChunkKey:       "ord-0",
		ChunkAnchorID:  chunkAnchor + "-other",
		SourceAnchorID: sourceAnchor,
		ChunkIndex:     0,
		ContentHash:    marker + "-ch-3",
		Metadata:       []byte(`{}`),
	}
	if err := db.UpsertContextChunk(ctx, dup); err == nil {
		t.Fatalf("expected unique violation on (item_version_id, chunk_key)")
	}
}
