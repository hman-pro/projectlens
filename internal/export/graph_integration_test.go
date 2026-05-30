//go:build integration

package export_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/export"
	"github.com/hman-pro/projectlens/internal/storage"
)

func openIntegration(t *testing.T) *storage.DB {
	t.Helper()
	url := os.Getenv("PROJECTLENS_DATABASE_URL")
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
	if _, err := export.NewGraphExporter(db, nil).Export(ctx, &buf, export.Options{}); err != nil {
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
	if _, err := export.NewGraphExporter(db, nil).Export(ctx, &buf, export.Options{Edges: []string{"calls"}}); err != nil {
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
	if _, err := export.NewGraphExporter(db, nil).Export(ctx, &buf, export.Options{}); err != nil {
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

// TestExportGraph_TableAnchorEdge is a self-contained fixture proving that a
// knowledge anchor of type "table" (save_knowledge stores target_type='table')
// survives export: it must resolve to the same node id as the datastore_table
// node and must NOT land in diagnostics.skipped_edges. This guards the JOIN +
// edgeEndpoint regression where table anchors became unknown:table:<id> and
// were silently dropped.
func TestExportGraph_TableAnchorEdge(t *testing.T) {
	db := openIntegration(t)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	tableName := "pl_export_fixture_" + suffix
	schema := "public"
	engine := "postgres"

	if err := db.UpsertDatastoreTable(ctx, &storage.DatastoreTableRecord{
		Name:       tableName,
		Engine:     engine,
		SchemaName: &schema,
	}); err != nil {
		t.Fatalf("upsert table: %v", err)
	}

	entry := &storage.KnowledgeEntry{
		Category: "convention",
		Title:    "export fixture knowledge " + suffix,
		Body:     "fixture body for table-anchor export test",
		Source:   "test",
	}
	entryID, _, err := db.InsertKnowledgeEntry(ctx, entry)
	if err != nil {
		t.Fatalf("insert knowledge: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		if _, err := db.DeleteKnowledgeEntry(c, entryID); err != nil {
			t.Logf("cleanup knowledge entry %d: %v", entryID, err)
		}
		db.Pool.Exec(c, `DELETE FROM datastore_tables WHERE name=$1 AND engine=$2`, tableName, engine)
	})

	res, err := db.InsertKnowledgeAnchors(ctx, entryID, []storage.AnchorRequest{
		{Type: "table", Ref: tableName},
	})
	if err != nil {
		t.Fatalf("insert anchor: %v", err)
	}
	if len(res) != 1 || !res[0].Resolved {
		t.Fatalf("table anchor did not resolve: %+v", res)
	}

	var buf bytes.Buffer
	diag, err := export.NewGraphExporter(db, nil).Export(ctx, &buf, export.Options{})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	wantTarget := fmt.Sprintf("table:%s:%s.%s", engine, schema, tableName)
	wantSource := fmt.Sprintf("knowledge:%d", entryID)

	var doc struct {
		Nodes []struct {
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

	nodeIDs := map[string]struct{}{}
	for _, n := range doc.Nodes {
		nodeIDs[n.ID] = struct{}{}
	}
	if _, ok := nodeIDs[wantTarget]; !ok {
		t.Fatalf("table node %q missing from export", wantTarget)
	}

	found := false
	for _, e := range doc.Edges {
		if e.Source == wantSource && e.Target == wantTarget && e.Type == "knowledge_about" {
			found = true
		}
	}
	if !found {
		t.Errorf("knowledge_about edge %s -> %s missing from export", wantSource, wantTarget)
	}

	for _, s := range diag.SkippedEdges {
		if s.Source == wantSource && s.Target == wantTarget {
			t.Errorf("table-anchor edge was skipped: %+v", s)
		}
		if strings.HasPrefix(s.Target, "unknown:table:") {
			t.Errorf("table anchor produced unknown endpoint (regression): %+v", s)
		}
	}
}

// TestExportGraph_IncludeEvidence proves the evidence-stripping toggle on a
// self-contained fixture: a knowledge_about edge carrying an "evidence" blob
// in its properties must drop that blob when --include-evidence is off and
// retain it when on, while other properties (source_attr) survive both ways.
func TestExportGraph_IncludeEvidence(t *testing.T) {
	db := openIntegration(t)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	tableName := "pl_evidence_fixture_" + suffix
	schema := "public"
	engine := "postgres"

	if err := db.UpsertDatastoreTable(ctx, &storage.DatastoreTableRecord{
		Name:       tableName,
		Engine:     engine,
		SchemaName: &schema,
	}); err != nil {
		t.Fatalf("upsert table: %v", err)
	}
	entryID, _, err := db.InsertKnowledgeEntry(ctx, &storage.KnowledgeEntry{
		Category: "convention",
		Title:    "evidence fixture " + suffix,
		Body:     "fixture body",
		Source:   "test",
	})
	if err != nil {
		t.Fatalf("insert knowledge: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		if _, err := db.DeleteKnowledgeEntry(c, entryID); err != nil {
			t.Logf("cleanup knowledge entry %d: %v", entryID, err)
		}
		db.Pool.Exec(c, `DELETE FROM datastore_tables WHERE name=$1 AND engine=$2`, tableName, engine)
	})

	var tableID int64
	if err := db.Pool.QueryRow(ctx,
		`SELECT id FROM datastore_tables WHERE name=$1 AND engine=$2`, tableName, engine).Scan(&tableID); err != nil {
		t.Fatalf("lookup table id: %v", err)
	}

	props := []byte(`{"evidence":"sensitive blob","source_attr":"fixture"}`)
	conf := float32(1.0)
	if err := db.InsertEdges(ctx, []storage.EdgeRecord{{
		SourceType:      "knowledge",
		SourceID:        entryID,
		TargetType:      "table",
		TargetID:        tableID,
		EdgeType:        "knowledge_about",
		Properties:      &props,
		Confidence:      &conf,
		Provenance:      "knowledge",
		ConfidenceClass: "extracted",
	}}); err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	wantSource := fmt.Sprintf("knowledge:%d", entryID)
	wantTarget := fmt.Sprintf("table:%s:%s.%s", engine, schema, tableName)

	findEdge := func(includeEvidence bool) map[string]interface{} {
		var buf bytes.Buffer
		if _, err := export.NewGraphExporter(db, nil).Export(ctx, &buf, export.Options{IncludeEvidence: includeEvidence}); err != nil {
			t.Fatalf("export (evidence=%v): %v", includeEvidence, err)
		}
		var doc struct {
			Edges []struct {
				Source     string                 `json:"source"`
				Target     string                 `json:"target"`
				Type       string                 `json:"type"`
				Properties map[string]interface{} `json:"properties"`
			} `json:"edges"`
		}
		if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, e := range doc.Edges {
			if e.Source == wantSource && e.Target == wantTarget && e.Type == "knowledge_about" {
				return e.Properties
			}
		}
		t.Fatalf("knowledge_about edge %s -> %s not found (evidence=%v)", wantSource, wantTarget, includeEvidence)
		return nil
	}

	stripped := findEdge(false)
	if _, ok := stripped["evidence"]; ok {
		t.Errorf("evidence present with --include-evidence off: %v", stripped)
	}
	if stripped["source_attr"] != "fixture" {
		t.Errorf("non-evidence property dropped: %v", stripped)
	}

	kept := findEdge(true)
	if kept["evidence"] != "sensitive blob" {
		t.Errorf("evidence missing with --include-evidence on: %v", kept)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
