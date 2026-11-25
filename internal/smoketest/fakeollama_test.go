package smoketest

import (
	"context"
	"math"
	"testing"

	"github.com/hman-pro/projectlens/internal/providers/ollama"
)

func TestFakeOllama_RealClientRoundTrip(t *testing.T) {
	srv := NewFakeOllama(1024)
	defer srv.Close()

	c := ollama.NewClient(srv.URL, "qwen3-embedding:0.6b", 1024)
	ctx := context.Background()

	vecs, err := c.EmbedBatch(ctx, []string{"a", "b", "a"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if got := srv.EmbedCalls(); got != 1 {
		t.Fatalf("EmbedCalls = %d, want 1", got)
	}
	if len(vecs) != 3 {
		t.Fatalf("got %d vectors, want 3", len(vecs))
	}
	if len(vecs[0]) != 1024 {
		t.Fatalf("got %d dims, want 1024", len(vecs[0]))
	}
	differs := false
	for i := range vecs[0] {
		if vecs[0][i] != vecs[2][i] {
			t.Fatalf("vecs[0] != vecs[2] at %d: not deterministic", i)
		}
		if vecs[0][i] != vecs[1][i] {
			differs = true
		}
	}
	if !differs {
		t.Fatal("vecs[0] equals vecs[1]; distinct texts must differ")
	}
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestFakeVector_UnitLength(t *testing.T) {
	var sum float64
	for _, v := range FakeVector("x", 1024) {
		sum += v * v
	}
	if math.Abs(sum-1.0) > 1e-6 {
		t.Fatalf("sum of squares = %v, want 1.0", sum)
	}
}
