package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hman-pro/projectlens/internal/embeddings"
)

func TestNewClient_DefaultEndpoint(t *testing.T) {
	c := NewClient("", "mxbai-embed-large")
	if c.endpoint != "http://localhost:11434" {
		t.Errorf("expected default endpoint http://localhost:11434, got %s", c.endpoint)
	}
	if c.model != "mxbai-embed-large" {
		t.Errorf("expected model mxbai-embed-large, got %s", c.model)
	}
	if c.http == nil {
		t.Error("expected non-nil http client")
	}
}

func TestNewClient_CustomEndpoint(t *testing.T) {
	c := NewClient("http://custom:9999", "nomic-embed-text")
	if c.endpoint != "http://custom:9999" {
		t.Errorf("expected custom endpoint, got %s", c.endpoint)
	}
	if c.model != "nomic-embed-text" {
		t.Errorf("expected model nomic-embed-text, got %s", c.model)
	}
}

// Verify the Client satisfies the embeddings.Embedder interface at compile time.
var _ embeddings.Embedder = (*Client)(nil)

func TestEmbedBatch_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/embed" {
			t.Errorf("expected /api/embed, got %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}

		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.Model != "mxbai-embed-large" {
			t.Errorf("expected model mxbai-embed-large, got %s", req.Model)
		}
		if len(req.Input) != 2 {
			t.Fatalf("expected 2 inputs, got %d", len(req.Input))
		}

		resp := embedResponse{
			Model: "mxbai-embed-large",
			Embeddings: [][]float64{
				{0.1, 0.2, 0.3},
				{0.4, 0.5, 0.6},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "mxbai-embed-large")
	vectors, err := c.EmbedBatch(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(vectors) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vectors))
	}
	if len(vectors[0]) != 3 {
		t.Fatalf("expected 3-dim vector, got %d", len(vectors[0]))
	}

	// Check float64 -> float32 conversion.
	want := []float32{0.1, 0.2, 0.3}
	for i, v := range vectors[0] {
		if diff := v - want[i]; diff > 1e-6 || diff < -1e-6 {
			t.Errorf("vectors[0][%d] = %f, want %f", i, v, want[i])
		}
	}
}

func TestEmbedBatch_EmptyInput(t *testing.T) {
	c := NewClient("http://should-not-be-called:9999", "model")
	vectors, err := c.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vectors != nil {
		t.Errorf("expected nil vectors for empty input, got %v", vectors)
	}

	vectors, err = c.EmbedBatch(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vectors != nil {
		t.Errorf("expected nil vectors for empty slice, got %v", vectors)
	}
}

func TestEmbedBatch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"model not found"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "nonexistent-model")
	_, err := c.EmbedBatch(context.Background(), []string{"test"})
	if err == nil {
		t.Fatal("expected error for HTTP 500 response")
	}
	if got := err.Error(); !contains(got, "500") {
		t.Errorf("expected error to contain status code, got: %s", got)
	}
	if got := err.Error(); !contains(got, "model not found") {
		t.Errorf("expected error to contain response body, got: %s", got)
	}
}

func TestEmbedBatch_MismatchedEmbeddingCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := embedResponse{
			Model:      "mxbai-embed-large",
			Embeddings: [][]float64{{0.1, 0.2}}, // only 1 embedding for 2 inputs
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "mxbai-embed-large")
	_, err := c.EmbedBatch(context.Background(), []string{"one", "two"})
	if err == nil {
		t.Fatal("expected error for mismatched embedding count")
	}
	if got := err.Error(); !contains(got, "expected 2 embeddings, got 1") {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestEmbedBatch_ConnectionRefused(t *testing.T) {
	c := NewClient("http://localhost:1", "model")
	_, err := c.EmbedBatch(context.Background(), []string{"test"})
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if got := err.Error(); !contains(got, "is ollama running") {
		t.Errorf("expected helpful error message, got: %s", got)
	}
}

func TestPing_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/tags" {
			t.Errorf("expected /api/tags, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "model")
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("unexpected ping error: %v", err)
	}
}

func TestPing_Unreachable(t *testing.T) {
	c := NewClient("http://localhost:1", "model")
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if got := err.Error(); !contains(got, "not reachable") {
		t.Errorf("expected 'not reachable' in error, got: %s", got)
	}
}

// contains is a small helper to avoid importing strings in tests.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
