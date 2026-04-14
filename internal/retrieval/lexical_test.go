package retrieval

import (
	"sort"
	"testing"
)

func TestDeduplicateBySymbolID_KeepsHigherScore(t *testing.T) {
	input := []SearchResult{
		{SymbolID: 1, SymbolName: "Foo", Score: 10.0, Source: "lexical"},
		{SymbolID: 1, SymbolName: "Foo", Score: 5.0, Source: "lexical"},
		{SymbolID: 2, SymbolName: "Bar", Score: 3.0, Source: "lexical"},
	}

	got := deduplicateBySymbolID(input)
	if len(got) != 2 {
		t.Fatalf("expected 2 results after dedup, got %d", len(got))
	}

	// Find symbol 1 result and verify it kept the higher score.
	for _, r := range got {
		if r.SymbolID == 1 && r.Score != 10.0 {
			t.Errorf("symbol 1: expected score 10.0, got %f", r.Score)
		}
	}
}

func TestDeduplicateBySymbolID_NoDuplicates(t *testing.T) {
	input := []SearchResult{
		{SymbolID: 1, SymbolName: "Foo", Score: 10.0},
		{SymbolID: 2, SymbolName: "Bar", Score: 5.0},
		{SymbolID: 3, SymbolName: "Baz", Score: 3.0},
	}

	got := deduplicateBySymbolID(input)
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
}

func TestDeduplicateBySymbolID_Empty(t *testing.T) {
	got := deduplicateBySymbolID(nil)
	if len(got) != 0 {
		t.Fatalf("expected 0 results, got %d", len(got))
	}
}

func TestSearchResultSorting_ByScoreDescending(t *testing.T) {
	results := []SearchResult{
		{SymbolID: 1, Score: 3.0},
		{SymbolID: 2, Score: 10.0},
		{SymbolID: 3, Score: 5.0},
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	expected := []float64{10.0, 5.0, 3.0}
	for i, exp := range expected {
		if results[i].Score != exp {
			t.Errorf("index %d: expected score %f, got %f", i, exp, results[i].Score)
		}
	}
}

func TestTopKLimiting(t *testing.T) {
	results := []SearchResult{
		{SymbolID: 1, Score: 10.0},
		{SymbolID: 2, Score: 8.0},
		{SymbolID: 3, Score: 5.0},
		{SymbolID: 4, Score: 3.0},
		{SymbolID: 5, Score: 1.0},
	}

	topK := 3
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > topK {
		results = results[:topK]
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Score != 10.0 {
		t.Errorf("first result: expected score 10.0, got %f", results[0].Score)
	}
	if results[2].Score != 5.0 {
		t.Errorf("last result: expected score 5.0, got %f", results[2].Score)
	}
}

func TestSearchResultSource(t *testing.T) {
	r := SearchResult{Source: "lexical"}
	if r.Source != "lexical" {
		t.Errorf("expected source 'lexical', got %q", r.Source)
	}
}

func TestDeduplicateBySymbolID_MultipleOverlaps(t *testing.T) {
	// Same symbol appears in all three query tiers.
	input := []SearchResult{
		{SymbolID: 42, SymbolName: "Handler", Score: 10.0, Source: "lexical"},
		{SymbolID: 42, SymbolName: "Handler", Score: 5.0, Source: "lexical"},
		{SymbolID: 42, SymbolName: "Handler", Score: 3.0, Source: "lexical"},
		{SymbolID: 99, SymbolName: "Router", Score: 5.0, Source: "lexical"},
		{SymbolID: 99, SymbolName: "Router", Score: 3.0, Source: "lexical"},
	}

	got := deduplicateBySymbolID(input)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}

	for _, r := range got {
		switch r.SymbolID {
		case 42:
			if r.Score != 10.0 {
				t.Errorf("symbol 42: expected score 10.0, got %f", r.Score)
			}
		case 99:
			if r.Score != 5.0 {
				t.Errorf("symbol 99: expected score 5.0, got %f", r.Score)
			}
		default:
			t.Errorf("unexpected symbol ID: %d", r.SymbolID)
		}
	}
}
