package rerank

import (
	"testing"
)

func TestRank_ExactNameMatchBoost(t *testing.T) {
	results := []Result{
		{SymbolName: "Handler", Score: 5.0, FilePath: "pkg/server/handler.go"},
		{SymbolName: "Router", Score: 5.0, FilePath: "pkg/server/router.go"},
	}

	ranked := Rank(results, "handler", false)

	if len(ranked) != 2 {
		t.Fatalf("expected 2 results, got %d", len(ranked))
	}
	// "Handler" should get +10.0 for exact name match (case-insensitive).
	if ranked[0].SymbolName != "Handler" {
		t.Errorf("expected Handler first, got %s", ranked[0].SymbolName)
	}
	if ranked[0].Score != 15.0 {
		t.Errorf("expected score 15.0 for exact match, got %f", ranked[0].Score)
	}
	if ranked[1].Score != 5.0 {
		t.Errorf("expected score 5.0 for non-match, got %f", ranked[1].Score)
	}
}

func TestRank_GeneratedCodePenalty(t *testing.T) {
	results := []Result{
		{SymbolName: "Foo", Score: 10.0, FilePath: "pkg/api/types.pb.go"},
		{SymbolName: "Bar", Score: 10.0, FilePath: "pkg/api/handler.go"},
		{SymbolName: "Baz", Score: 10.0, FilePath: "pkg/api/types_generated.go"},
	}

	ranked := Rank(results, "something", false)

	// Non-generated file should be first.
	if ranked[0].SymbolName != "Bar" {
		t.Errorf("expected Bar first (non-generated), got %s", ranked[0].SymbolName)
	}
	if ranked[0].Score != 10.0 {
		t.Errorf("expected score 10.0 for non-generated, got %f", ranked[0].Score)
	}
	// Generated files should have -5.0 penalty.
	for _, r := range ranked[1:] {
		if r.Score != 5.0 {
			t.Errorf("expected score 5.0 for generated file %s, got %f", r.FilePath, r.Score)
		}
	}
}

func TestRank_TestFilePenalty(t *testing.T) {
	results := []Result{
		{SymbolName: "Foo", Score: 10.0, FilePath: "pkg/api/handler_test.go"},
		{SymbolName: "Bar", Score: 10.0, FilePath: "pkg/api/handler.go"},
	}

	ranked := Rank(results, "something", false)

	if ranked[0].SymbolName != "Bar" {
		t.Errorf("expected Bar first (non-test), got %s", ranked[0].SymbolName)
	}
	if ranked[0].Score != 10.0 {
		t.Errorf("expected non-test score 10.0, got %f", ranked[0].Score)
	}
	if ranked[1].Score != 7.0 {
		t.Errorf("expected test file score 7.0, got %f", ranked[1].Score)
	}
}

func TestRank_TestFilePenaltySuppressedForTestQuery(t *testing.T) {
	results := []Result{
		{SymbolName: "TestFoo", Score: 10.0, FilePath: "pkg/api/handler_test.go"},
		{SymbolName: "Bar", Score: 10.0, FilePath: "pkg/api/handler.go"},
	}

	ranked := Rank(results, "TestFoo", true)

	// With isTestQuery=true, the test file penalty should NOT apply.
	// TestFoo gets +10.0 for exact match, Bar stays at 10.0.
	if ranked[0].SymbolName != "TestFoo" {
		t.Errorf("expected TestFoo first when isTestQuery=true, got %s", ranked[0].SymbolName)
	}
	if ranked[0].Score != 20.0 {
		t.Errorf("expected score 20.0 (10+10 exact match, no penalty), got %f", ranked[0].Score)
	}
}

func TestRank_SortingOrder(t *testing.T) {
	results := []Result{
		{SymbolName: "A", Score: 1.0, FilePath: "a.go"},
		{SymbolName: "B", Score: 5.0, FilePath: "b.go"},
		{SymbolName: "C", Score: 3.0, FilePath: "c.go"},
	}

	ranked := Rank(results, "something", false)

	if len(ranked) != 3 {
		t.Fatalf("expected 3 results, got %d", len(ranked))
	}
	for i := 0; i < len(ranked)-1; i++ {
		if ranked[i].Score < ranked[i+1].Score {
			t.Errorf("results not sorted: index %d score %f < index %d score %f",
				i, ranked[i].Score, i+1, ranked[i+1].Score)
		}
	}
}

func TestRank_EmptyInput(t *testing.T) {
	ranked := Rank(nil, "test", false)
	if len(ranked) != 0 {
		t.Fatalf("expected 0 results for nil input, got %d", len(ranked))
	}

	ranked = Rank([]Result{}, "test", false)
	if len(ranked) != 0 {
		t.Fatalf("expected 0 results for empty input, got %d", len(ranked))
	}
}

func TestRank_SamePackageBoost(t *testing.T) {
	results := []Result{
		{SymbolName: "Foo", Score: 5.0, PackageName: "pkg/api", FilePath: "pkg/api/foo.go"},
		{SymbolName: "Bar", Score: 5.0, PackageName: "pkg/server", FilePath: "pkg/server/bar.go"},
	}

	// Query contains a package path hint.
	ranked := Rank(results, "pkg/api Foo", false)

	// Foo should get +2.0 for same package, plus +10.0 for exact name match.
	if ranked[0].SymbolName != "Foo" {
		t.Errorf("expected Foo first (same package + exact match), got %s", ranked[0].SymbolName)
	}
	if ranked[0].Score != 17.0 {
		t.Errorf("expected score 17.0 (5+10+2), got %f", ranked[0].Score)
	}
}

func TestRank_DoesNotMutateOriginal(t *testing.T) {
	original := []Result{
		{SymbolName: "A", Score: 1.0, FilePath: "a.go"},
		{SymbolName: "B", Score: 5.0, FilePath: "b.go"},
	}

	ranked := Rank(original, "something", false)

	// The returned slice should be sorted differently from the original order.
	if ranked[0].SymbolName != "B" {
		t.Errorf("expected ranked[0] to be B, got %s", ranked[0].SymbolName)
	}
	// Original should not be reordered.
	if original[0].SymbolName != "A" {
		t.Errorf("original slice was mutated: expected A first, got %s", original[0].SymbolName)
	}
	// Original scores should not be modified.
	if original[0].Score != 1.0 {
		t.Errorf("original[0].Score was mutated: expected 1.0, got %f", original[0].Score)
	}
}
