package retrieval

import (
	"context"
	"fmt"
	"testing"

	"github.com/hman-pro/projectlens/internal/storage"
)

// mockEmbedder implements QueryEmbedder for testing.
type mockEmbedder struct {
	vectors [][]float32
	err     error
}

func (m *mockEmbedder) EmbedBatch(_ context.Context, _ []string) ([][]float32, error) {
	return m.vectors, m.err
}

func TestSemanticResultToSearchResult(t *testing.T) {
	sr := storage.SemanticSearchResult{
		ChunkID:     1,
		SymbolName:  "ParseConfig",
		SymbolKind:  "function",
		PackageName: "config",
		FilePath:    "internal/config/parse.go",
		LineStart:   10,
		LineEnd:     30,
		Distance:    0.25,
	}

	got := semanticResultToSearchResult(sr)

	if got.Source != "semantic" {
		t.Errorf("expected source 'semantic', got %q", got.Source)
	}
	if got.Score != 0.75 {
		t.Errorf("expected score 0.75 (1 - 0.25), got %f", got.Score)
	}
	if got.SymbolName != "ParseConfig" {
		t.Errorf("expected symbol name 'ParseConfig', got %q", got.SymbolName)
	}
	if got.Kind != "function" {
		t.Errorf("expected kind 'function', got %q", got.Kind)
	}
	if got.PackageName != "config" {
		t.Errorf("expected package 'config', got %q", got.PackageName)
	}
	if got.FilePath != "internal/config/parse.go" {
		t.Errorf("expected file path 'internal/config/parse.go', got %q", got.FilePath)
	}
	if got.LineStart != 10 || got.LineEnd != 30 {
		t.Errorf("expected lines 10-30, got %d-%d", got.LineStart, got.LineEnd)
	}
	if got.Relationship != "" {
		t.Errorf("expected empty relationship, got %q", got.Relationship)
	}
}

func TestSemanticResultToSearchResult_ZeroDistance(t *testing.T) {
	sr := storage.SemanticSearchResult{
		Distance: 0.0,
	}
	got := semanticResultToSearchResult(sr)
	if got.Score != 1.0 {
		t.Errorf("expected score 1.0 for zero distance, got %f", got.Score)
	}
}

func TestSemanticResultToSearchResult_MaxDistance(t *testing.T) {
	sr := storage.SemanticSearchResult{
		Distance: 1.0,
	}
	got := semanticResultToSearchResult(sr)
	if got.Score != 0.0 {
		t.Errorf("expected score 0.0 for max distance, got %f", got.Score)
	}
}

func TestSemanticSearch_EmptyQuery(t *testing.T) {
	embedder := &mockEmbedder{}
	_, err := SemanticSearch(context.Background(), nil, embedder, "", 10)
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestSemanticSearch_EmbedderError(t *testing.T) {
	embedder := &mockEmbedder{
		err: fmt.Errorf("model unavailable"),
	}
	_, err := SemanticSearch(context.Background(), nil, embedder, "test query", 10)
	if err == nil {
		t.Fatal("expected error when embedder fails")
	}
}

func TestSemanticSearch_EmptyVector(t *testing.T) {
	embedder := &mockEmbedder{
		vectors: [][]float32{{}},
	}
	_, err := SemanticSearch(context.Background(), nil, embedder, "test query", 10)
	if err == nil {
		t.Fatal("expected error for empty embedding vector")
	}
}

func TestSemanticSearch_NilVectors(t *testing.T) {
	embedder := &mockEmbedder{
		vectors: nil,
	}
	_, err := SemanticSearch(context.Background(), nil, embedder, "test query", 10)
	if err == nil {
		t.Fatal("expected error for nil vectors")
	}
}

func TestSemanticSearch_InvalidTopK(t *testing.T) {
	embedder := &mockEmbedder{}
	_, err := SemanticSearch(context.Background(), nil, embedder, "test", 0)
	if err == nil {
		t.Fatal("expected error for topK=0")
	}
	_, err = SemanticSearch(context.Background(), nil, embedder, "test", -1)
	if err == nil {
		t.Fatal("expected error for negative topK")
	}
}
