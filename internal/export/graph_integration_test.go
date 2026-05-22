//go:build integration

package export_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/export"
	"github.com/hman-pro/projectlens/internal/storage"
)

func openIntegration(t *testing.T) *storage.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := storage.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestExportGraph_ClosureInvariant(t *testing.T) {
	db := openIntegration(t)
	ctx := context.Background()

	var buf bytes.Buffer
	if err := export.NewGraphExporter(db, nil).Export(ctx, &buf, export.Options{}); err != nil {
		t.Fatalf("export: %v", err)
	}

	var doc struct {
		SchemaVersion string `json:"schema_version"`
		Nodes         []struct {
			ID string `json:"id"`
		} `json:"nodes"`
		Edges []struct {
			Source string `json:"source"`
			Target string `json:"target"`
			Type   string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if doc.SchemaVersion != export.SchemaVersion {
		t.Errorf("schema_version: got %s want %s", doc.SchemaVersion, export.SchemaVersion)
	}

	ids := map[string]struct{}{}
	for _, n := range doc.Nodes {
		ids[n.ID] = struct{}{}
	}
	for _, e := range doc.Edges {
		if _, ok := ids[e.Source]; !ok {
			t.Errorf("edge source %q (type=%s) missing from node set", e.Source, e.Type)
		}
		if _, ok := ids[e.Target]; !ok {
			t.Errorf("edge target %q (type=%s) missing from node set", e.Target, e.Type)
		}
	}
}

func TestExportGraph_EdgeFilter(t *testing.T) {
	db := openIntegration(t)
	ctx := context.Background()
	var buf bytes.Buffer
	if err := export.NewGraphExporter(db, nil).Export(ctx, &buf, export.Options{Edges: []string{"calls"}}); err != nil {
		t.Fatalf("export: %v", err)
	}
	var doc struct {
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, e := range doc.Edges {
		if e.Type != "calls" {
			t.Errorf("filter leaked %s", e.Type)
		}
	}
}

// TestExportGraph_EdgeProvenance verifies that v2 edges carry top-level
// provenance + confidence_class fields. Skips when the live DB has no
// edges (fresh install / empty fixture).
func TestExportGraph_EdgeProvenance(t *testing.T) {
	db := openIntegration(t)
	ctx := context.Background()
	var buf bytes.Buffer
	if err := export.NewGraphExporter(db, nil).Export(ctx, &buf, export.Options{}); err != nil {
		t.Fatalf("export: %v", err)
	}
	var doc struct {
		SchemaVersion string `json:"schema_version"`
		Edges         []struct {
			Source          string `json:"source"`
			Target          string `json:"target"`
			Type            string `json:"type"`
			Provenance      string `json:"provenance"`
			ConfidenceClass string `json:"confidence_class"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if doc.SchemaVersion != "projectlens-graph/v2" {
		t.Errorf("schema_version: got %s want projectlens-graph/v2", doc.SchemaVersion)
	}
	if len(doc.Edges) == 0 {
		t.Skip("no edges in current index — skipping provenance assertion")
	}

	// Every edge must carry both fields after backfill + writer changes.
	for i, e := range doc.Edges {
		if e.Provenance == "" || e.ConfidenceClass == "" {
			t.Errorf("edge %d (type=%s) missing provenance/class: prov=%q class=%q",
				i, e.Type, e.Provenance, e.ConfidenceClass)
		}
	}

	// Verify known type → provenance mappings for whichever edge types are
	// present in the current index. Track which mappings actually got
	// exercised so a sparse fixture (no datastore stage, no knowledge
	// anchors, …) does not silently bypass the assertion.
	want := map[string]string{
		"calls":           "callgraph",
		"implements":      "parser",
		"imports":         "parser",
		"co_changes":      "history",
		"knowledge_about": "knowledge",
		"reads_table":     "sql_scanner",
		"writes_table":    "sql_scanner",
	}
	seen := map[string]struct{}{}
	for _, e := range doc.Edges {
		exp, ok := want[e.Type]
		if !ok {
			continue
		}
		seen[e.Type] = struct{}{}
		if e.Provenance != exp {
			t.Errorf("edge type=%s: provenance=%q want %q", e.Type, e.Provenance, exp)
		}
	}
	if len(seen) == 0 {
		t.Fatalf("no known edge types found in export (%d edges total); expected at least one of %v", len(doc.Edges), keysOf(want))
	}
	var unseen []string
	for k := range want {
		if _, ok := seen[k]; !ok {
			unseen = append(unseen, k)
		}
	}
	if len(unseen) > 0 {
		t.Logf("known type→provenance mappings not exercised by this index (sparse data, not a failure): %v", unseen)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
