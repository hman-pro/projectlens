package embeddings

import (
	"context"
	"errors"
	"testing"

	"github.com/hman-pro/projectlens/internal/providers/identity"
)

// mockEmbedder records calls and returns deterministic vectors.
type mockEmbedder struct {
	calls int
	dim   int
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	m.calls++
	results := make([][]float32, len(texts))
	for i := range results {
		results[i] = make([]float32, m.dim)
		results[i][0] = float32(i) // unique marker
	}
	return results, nil
}

func (m *mockEmbedder) EmbedIdentity() identity.ProviderIdentity { return identity.ProviderIdentity{} }

// errorEmbedder always returns an error.
type errorEmbedder struct {
	err error
}

func (e *errorEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, e.err
}

func (e *errorEmbedder) EmbedIdentity() identity.ProviderIdentity {
	return identity.ProviderIdentity{}
}

func TestEmbedChunks_SmallBatch(t *testing.T) {
	mock := &mockEmbedder{dim: 3072}
	chunks := []string{"a", "b", "c", "d", "e"}

	results, err := EmbedChunks(context.Background(), mock, chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.calls != 1 {
		t.Errorf("expected 1 batch call, got %d", mock.calls)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}
	for i, r := range results {
		if r.ChunkIndex != i {
			t.Errorf("result[%d].ChunkIndex = %d, want %d", i, r.ChunkIndex, i)
		}
		if r.Vector[0] != float32(i) {
			t.Errorf("result[%d].Vector[0] = %f, want %f", i, r.Vector[0], float32(i))
		}
	}
}

func TestEmbedChunks_MultipleBatches(t *testing.T) {
	mock := &mockEmbedder{dim: 3072}
	chunks := make([]string, 150)
	for i := range chunks {
		chunks[i] = "chunk"
	}

	results, err := EmbedChunks(context.Background(), mock, chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedBatches := (150 + batchSize - 1) / batchSize
	if mock.calls != expectedBatches {
		t.Errorf("expected %d batch calls, got %d", expectedBatches, mock.calls)
	}
	if len(results) != 150 {
		t.Fatalf("expected 150 results, got %d", len(results))
	}

	// Verify indices span full range.
	for i, r := range results {
		if r.ChunkIndex != i {
			t.Errorf("result[%d].ChunkIndex = %d, want %d", i, r.ChunkIndex, i)
		}
	}
}

func TestEmbedChunks_Empty(t *testing.T) {
	mock := &mockEmbedder{dim: 3072}

	results, err := EmbedChunks(context.Background(), mock, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.calls != 0 {
		t.Errorf("expected 0 batch calls, got %d", mock.calls)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestEmbedChunks_Error(t *testing.T) {
	embedErr := errors.New("openai unavailable")
	emb := &errorEmbedder{err: embedErr}
	chunks := []string{"a", "b"}

	results, err := EmbedChunks(context.Background(), emb, chunks)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, embedErr) {
		t.Errorf("expected wrapped error containing %v, got %v", embedErr, err)
	}
	if results != nil {
		t.Errorf("expected nil results on error, got %d results", len(results))
	}
}

func TestEmbedChunks_VectorDimensions(t *testing.T) {
	mock := &mockEmbedder{dim: 3072}
	chunks := []string{"hello"}

	results, err := EmbedChunks(context.Background(), mock, chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].Vector) != 3072 {
		t.Errorf("expected vector dimension 3072, got %d", len(results[0].Vector))
	}
}
