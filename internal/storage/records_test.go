package storage

import (
	"testing"
	"time"

	"github.com/pgvector/pgvector-go"
)

// TestRecordStructs verifies that all record structs can be instantiated
// with expected field types. This is a compile-time + basic sanity check.
func TestRecordStructs(t *testing.T) {
	now := time.Now()
	summary := "test summary"

	_ = FileRecord{
		ID: 1, Path: "/test.go", PackageName: "main", Checksum: "abc",
		Language: "go", IsGenerated: false, IsTest: false, LineCount: 100,
		HeuristicSummary: &summary, CommitSHA: "sha1", IndexedAt: now,
	}

	receiver := "MyType"
	doc := "// Doc comment"
	_ = SymbolRecord{
		ID: 1, FileID: 1, Name: "Foo", Kind: "function", PackageName: "main",
		Receiver: &receiver, Signature: "func Foo()", DocComment: &doc,
		LineStart: 1, LineEnd: 10, Checksum: "abc", IndexedAt: now,
	}

	_ = ChunkRecord{
		ID: 1, SymbolID: 1, Content: "func Foo() {}", TokenCount: 5,
	}

	vec := pgvector.NewHalfVector([]float32{0.1, 0.2, 0.3})
	_ = EmbeddingRecord{
		ID: 1, ChunkID: 1, ModelVersion: "text-embedding-3-large",
		Embedding: vec,
	}

	_ = SemanticSearchResult{
		ChunkID: 1, SymbolName: "Foo", SymbolKind: "function",
		PackageName: "main", FilePath: "/test.go",
		LineStart: 1, LineEnd: 10, Distance: 0.5,
	}

	_ = SummaryRecord{
		ID: 1, PackageName: "main", SummaryText: "a package",
		ModelVersion: "gpt-4o", GeneratedAt: now,
	}

	_ = EdgeRecord{
		ID: 1, SourceSymbolID: 1, TargetSymbolID: 2, EdgeType: "calls",
	}

	_ = EdgeResult{
		EdgeID: 1, EdgeType: "calls", SymbolID: 2, SymbolName: "Bar",
		SymbolKind: "function", PackageName: "main", FilePath: "/test.go",
		LineStart: 20, LineEnd: 30,
	}

	completed := now
	_ = IndexRunRecord{
		ID: 1, StartedAt: now, CompletedAt: &completed, CommitSHA: "sha1",
		FilesProcessed: 10, SymbolsExtracted: 50, EdgesCreated: 20,
		Status: "completed",
	}

	_ = GitRefRecord{
		ID: 1, Branch: "main", CommitSHA: "sha1", IndexedAt: now,
	}
}

func TestHalfVectorRoundTrip(t *testing.T) {
	input := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
	hv := pgvector.NewHalfVector(input)

	got := hv.Slice()
	if len(got) != len(input) {
		t.Fatalf("expected %d elements, got %d", len(input), len(got))
	}
	for i, v := range got {
		if v != input[i] {
			t.Errorf("element %d: expected %f, got %f", i, input[i], v)
		}
	}

	// Test string round trip.
	str := hv.String()
	var parsed pgvector.HalfVector
	if err := parsed.Scan(str); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(parsed.Slice()) != len(input) {
		t.Fatalf("after parse: expected %d elements, got %d", len(input), len(parsed.Slice()))
	}
}
