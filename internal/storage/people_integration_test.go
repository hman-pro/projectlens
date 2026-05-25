//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestPersonIdentityUpsertAndLink(t *testing.T) {
	db := connectForIntegration(t)
	ctx := context.Background()

	marker := fmt.Sprintf("ctx-id-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupContextMarker(t, db, marker) })

	// 1. New identity without a person.
	id := &PersonIdentityRecord{
		Provider:          "github",
		ExternalAccountID: marker + "-gh-1",
		Username:          ptr("alice"),
		Metadata:          []byte(`{}`),
	}
	if err := db.UpsertPersonIdentity(ctx, id); err != nil {
		t.Fatalf("upsert id: %v", err)
	}
	if id.ID == 0 {
		t.Fatalf("ID not populated")
	}

	// 2. Re-upsert same (provider, external_account_id) — same row.
	id2 := &PersonIdentityRecord{
		Provider:          "github",
		ExternalAccountID: marker + "-gh-1",
		Username:          ptr("alice-renamed"),
		Metadata:          []byte(`{}`),
	}
	if err := db.UpsertPersonIdentity(ctx, id2); err != nil {
		t.Fatalf("re-upsert id: %v", err)
	}
	if id2.ID != id.ID {
		t.Fatalf("identity ID changed: %d -> %d", id.ID, id2.ID)
	}

	// 3. Create person and link. DisplayName carries marker so cleanup catches it.
	p := &PersonRecord{DisplayName: ptr("Alice " + marker), Metadata: []byte(`{}`)}
	if err := db.InsertPerson(ctx, p); err != nil {
		t.Fatalf("insert person: %v", err)
	}
	if err := db.LinkIdentityToPerson(ctx, id.ID, p.ID); err != nil {
		t.Fatalf("link: %v", err)
	}

	got, err := db.GetPersonIdentity(ctx, "github", marker+"-gh-1")
	if err != nil {
		t.Fatalf("get id: %v", err)
	}
	if got == nil || got.PersonID == nil || *got.PersonID != p.ID {
		t.Fatalf("link not persisted: %+v", got)
	}

	// 4. Two identities sharing an email_hash do NOT auto-merge people
	//    (Phase 1 has no merge logic; we only assert the schema permits
	//    independent identities even when email_hash matches).
	hash := marker + "-hash"
	idA := &PersonIdentityRecord{Provider: "slack", ExternalAccountID: marker + "-sl-1", EmailHash: &hash, Metadata: []byte(`{}`)}
	idB := &PersonIdentityRecord{Provider: "atlassian", ExternalAccountID: marker + "-at-1", EmailHash: &hash, Metadata: []byte(`{}`)}
	if err := db.UpsertPersonIdentity(ctx, idA); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPersonIdentity(ctx, idB); err != nil {
		t.Fatal(err)
	}
	if idA.PersonID != nil || idB.PersonID != nil {
		t.Fatalf("identities auto-linked to a person, but Phase 1 has no auto-merge")
	}
}

func ptr[T any](v T) *T { return &v }
