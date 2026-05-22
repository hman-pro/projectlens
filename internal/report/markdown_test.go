package report

import (
	"bytes"
	"strings"
	"testing"
)

func TestMarkdownRenderer_SectionsPresent(t *testing.T) {
	var buf bytes.Buffer
	if err := (MarkdownRenderer{}).Render(&buf, fixtureReport()); err != nil {
		t.Fatalf("render: %v", err)
	}
	s := buf.String()
	for _, header := range []string{
		"# ProjectLens Report",
		"## Index Freshness",
		"## Providers",
		"## Top Packages",
		"## Top Datastore Tables",
		"## High-Coupling File Pairs",
		"## Edge Trust (provenance + confidence)",
		"## Knowledge Inventory",
		"## Degraded / Missing",
		"## Suggested Agent Questions",
	} {
		if !strings.Contains(s, header) {
			t.Errorf("missing header %q in:\n%s", header, s)
		}
	}
	for _, frag := range []string{
		"abc123",
		"pkg/a",
		"public.orders",
		"a.go",
		"run projectlens index-embed",
		"| calls | callgraph | 0 | 100 | 0 | 0 | 100 |",
		"| implements | parser | 7 | 0 | 0 | 0 | 7 |",
	} {
		if !strings.Contains(s, frag) {
			t.Errorf("missing fragment %q in:\n%s", frag, s)
		}
	}
}

func TestMarkdownRenderer_EmptyReport(t *testing.T) {
	var buf bytes.Buffer
	if err := (MarkdownRenderer{}).Render(&buf, &Report{}); err != nil {
		t.Fatalf("render empty: %v", err)
	}
	if !strings.Contains(buf.String(), "# ProjectLens Report") {
		t.Errorf("missing header on empty report:\n%s", buf.String())
	}
}
