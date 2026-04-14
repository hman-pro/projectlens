package retrieval

import (
	"testing"

	"github.com/hman-pro/projectlens/internal/storage"
)

func TestEdgeResultToSearchResult_Caller(t *testing.T) {
	e := storage.EdgeResult{
		EdgeID:      1,
		EdgeType:    "calls",
		SymbolID:    10,
		SymbolName:  "HandleRequest",
		SymbolKind:  "function",
		PackageName: "server",
		FilePath:    "internal/server/handler.go",
		LineStart:   15,
		LineEnd:     40,
	}

	got := edgeResultToSearchResult(e, 3.0, "caller")

	if got.Source != "graph" {
		t.Errorf("expected source 'graph', got %q", got.Source)
	}
	if got.Relationship != "caller" {
		t.Errorf("expected relationship 'caller', got %q", got.Relationship)
	}
	if got.Score != 3.0 {
		t.Errorf("expected score 3.0, got %f", got.Score)
	}
	if got.SymbolID != 10 {
		t.Errorf("expected symbol ID 10, got %d", got.SymbolID)
	}
	if got.SymbolName != "HandleRequest" {
		t.Errorf("expected name 'HandleRequest', got %q", got.SymbolName)
	}
	if got.Kind != "function" {
		t.Errorf("expected kind 'function', got %q", got.Kind)
	}
	if got.PackageName != "server" {
		t.Errorf("expected package 'server', got %q", got.PackageName)
	}
	if got.FilePath != "internal/server/handler.go" {
		t.Errorf("expected file path 'internal/server/handler.go', got %q", got.FilePath)
	}
}

func TestEdgeResultToSearchResult_Callee(t *testing.T) {
	e := storage.EdgeResult{
		SymbolID:   20,
		SymbolName: "FormatOutput",
	}
	got := edgeResultToSearchResult(e, 3.0, "callee")

	if got.Relationship != "callee" {
		t.Errorf("expected relationship 'callee', got %q", got.Relationship)
	}
	if got.Source != "graph" {
		t.Errorf("expected source 'graph', got %q", got.Source)
	}
}

func TestEdgeResultToSearchResult_Implements(t *testing.T) {
	e := storage.EdgeResult{
		SymbolID:   30,
		SymbolName: "FileStore",
		SymbolKind: "type",
	}
	got := edgeResultToSearchResult(e, 5.0, "implements")

	if got.Relationship != "implements" {
		t.Errorf("expected relationship 'implements', got %q", got.Relationship)
	}
	if got.Score != 5.0 {
		t.Errorf("expected score 5.0, got %f", got.Score)
	}
}

func TestEdgeResultToSearchResult_DependsOn(t *testing.T) {
	e := storage.EdgeResult{
		SymbolID:   40,
		SymbolName: "Connect",
	}
	got := edgeResultToSearchResult(e, 2.0, "depends_on")

	if got.Relationship != "depends_on" {
		t.Errorf("expected relationship 'depends_on', got %q", got.Relationship)
	}
	if got.Score != 2.0 {
		t.Errorf("expected score 2.0, got %f", got.Score)
	}
}

func TestScoreForDepth(t *testing.T) {
	tests := []struct {
		depth    int
		expected float64
	}{
		{1, 3.0},
		{2, 1.0},
		{3, 0.5},
		{10, 0.5},
	}

	for _, tc := range tests {
		got := scoreForDepth(tc.depth)
		if got != tc.expected {
			t.Errorf("scoreForDepth(%d): expected %f, got %f", tc.depth, tc.expected, got)
		}
	}
}

func TestGetCallers_InvalidMaxDepth(t *testing.T) {
	_, err := GetCallers(nil, nil, 1, 0)
	if err == nil {
		t.Fatal("expected error for maxDepth=0")
	}
	_, err = GetCallers(nil, nil, 1, -1)
	if err == nil {
		t.Fatal("expected error for negative maxDepth")
	}
}

func TestGetCallees_InvalidMaxDepth(t *testing.T) {
	_, err := GetCallees(nil, nil, 1, 0)
	if err == nil {
		t.Fatal("expected error for maxDepth=0")
	}
}

func TestGetPackageDeps_EmptyPackageName(t *testing.T) {
	_, err := GetPackageDeps(nil, nil, "")
	if err == nil {
		t.Fatal("expected error for empty package name")
	}
}
