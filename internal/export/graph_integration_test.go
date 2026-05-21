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
