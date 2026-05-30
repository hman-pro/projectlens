package export

import (
	"strings"
	"testing"
)

func strptr(s string) *string { return &s }

// TestEdgeEndpoint_AllShapes covers every endpoint type the exporter can
// produce, including the knowledge "table" anchor type — which save_knowledge
// stores as target_type='table' and must resolve to the same node id format
// as emitted datastore_table nodes (table:<engine>:<schema>.<name>).
func TestEdgeEndpoint_AllShapes(t *testing.T) {
	cases := []struct {
		name     string
		t        string
		id       int64
		engine   *string
		schema   *string
		tname    *string
		pkg      *string
		props    map[string]interface{}
		isSource bool
		want     string
	}{
		{name: "symbol", t: "symbol", id: 42, want: "sym:42"},
		{name: "file", t: "file", id: 7, want: "file:7"},
		{
			name: "datastore_table", t: "datastore_table", id: 3,
			engine: strptr("postgres"), schema: strptr("public"), tname: strptr("orders"),
			want: "table:postgres:public.orders",
		},
		{
			name: "table anchor resolves like datastore_table", t: "table", id: 5,
			engine: strptr("postgres"), schema: strptr("public"), tname: strptr("orders"),
			want: "table:postgres:public.orders",
		},
		{
			name: "table anchor empty schema", t: "table", id: 5,
			engine: strptr("postgres"), schema: strptr(""), tname: strptr("events"),
			want: "table:postgres:.events",
		},
		{name: "knowledge", t: "knowledge", id: 12, want: "knowledge:12"},
		{
			name: "package via file", t: "package", id: 9,
			pkg: strptr("internal/x"), want: "package:internal/x",
		},
		{
			name: "package via target prop fallback", t: "package", id: 9,
			props:    map[string]interface{}{"target_package": "internal/y"},
			isSource: false, want: "package:internal/y",
		},
		{
			name: "package via source prop fallback", t: "package", id: 9,
			props:    map[string]interface{}{"source_package": "internal/z"},
			isSource: true, want: "package:internal/z",
		},
		{name: "unknown type", t: "mystery", id: 4, want: "unknown:mystery:4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := edgeEndpoint(c.t, c.id, c.engine, c.schema, c.tname, c.pkg, c.props, c.isSource)
			if got != c.want {
				t.Errorf("edgeEndpoint: got %q want %q", got, c.want)
			}
		})
	}
}

// TestEdgeSkipReason verifies the closure-invariant classifier surfaces a
// diagnostic for unresolved endpoints instead of silently dropping them.
func TestEdgeSkipReason(t *testing.T) {
	emitted := map[string]struct{}{
		"sym:1":                   {},
		"table:postgres:public.t": {},
	}
	cases := []struct {
		name           string
		source, target string
		wantEmpty      bool
		wantContains   string
	}{
		{name: "both emitted", source: "sym:1", target: "table:postgres:public.t", wantEmpty: true},
		{name: "unknown source type", source: "unknown:mystery:4", target: "sym:1", wantContains: "source"},
		{name: "target not emitted", source: "sym:1", target: "sym:99", wantContains: "target"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := edgeSkipReason(c.source, c.target, emitted)
			if c.wantEmpty {
				if got != "" {
					t.Errorf("want empty reason, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatalf("want non-empty reason mentioning %q", c.wantContains)
			}
			if !strings.Contains(got, c.wantContains) {
				t.Errorf("reason %q does not mention %q", got, c.wantContains)
			}
		})
	}
}

func TestNodeID_AllKinds(t *testing.T) {
	cases := []struct {
		kind nodeKind
		id   int64
		eng  string
		sch  string
		name string
		pkg  string
		want string
	}{
		{kindSymbol, 42, "", "", "", "", "sym:42"},
		{kindFile, 7, "", "", "", "", "file:7"},
		{kindDatastoreTable, 0, "postgres", "public", "orders", "", "table:postgres:public.orders"},
		{kindDatastoreTable, 0, "postgres", "", "events", "", "table:postgres:.events"},
		{kindPackage, 0, "", "", "", "internal/x", "package:internal/x"},
		{kindKnowledge, 12, "", "", "", "", "knowledge:12"},
	}
	for _, c := range cases {
		got := nodeID(c.kind, c.id, c.eng, c.sch, c.name, c.pkg)
		if got != c.want {
			t.Errorf("nodeID(%v): got %q want %q", c, got, c.want)
		}
	}
}

func TestIsValidEdgeType(t *testing.T) {
	for _, e := range AllowedEdgeTypes {
		if !IsValidEdgeType(e) {
			t.Errorf("want valid: %s", e)
		}
	}
	if !IsValidEdgeType("all") {
		t.Errorf("want valid: all")
	}
	if IsValidEdgeType("call") {
		t.Errorf("singular should be invalid")
	}
	if IsValidEdgeType("bogus") {
		t.Errorf("bogus invalid")
	}
}

func TestOptions_ResolveEdges(t *testing.T) {
	if got := (Options{}).resolveEdges(); len(got) != len(AllowedEdgeTypes) {
		t.Errorf("empty: want all (%d), got %d", len(AllowedEdgeTypes), len(got))
	}
	if got := (Options{Edges: []string{"all"}}).resolveEdges(); len(got) != len(AllowedEdgeTypes) {
		t.Errorf("all: want all, got %d", len(got))
	}
	if got := (Options{Edges: []string{"calls"}}).resolveEdges(); len(got) != 1 || got[0] != "calls" {
		t.Errorf("calls: got %v", got)
	}
}
