package export

import "testing"

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
