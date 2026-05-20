package retrieval

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// pingingEmbedder mocks an embedder that exposes a Ping method.
type pingingEmbedder struct {
	name    string
	pingErr error
}

func (p *pingingEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = make([]float32, 1024)
	}
	return vecs, nil
}

func (p *pingingEmbedder) Ping(ctx context.Context) error { return p.pingErr }
func (p *pingingEmbedder) ProviderName() string           { return p.name }

// embedOnlyEmbedder mocks an embedder without a Ping method; the
// router falls back to EmbedQuery for the probe.
type embedOnlyEmbedder struct {
	name     string
	embedErr error
}

func (e *embedOnlyEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if e.embedErr != nil {
		return nil, e.embedErr
	}
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = make([]float32, 1024)
	}
	return vecs, nil
}

func (e *embedOnlyEmbedder) ProviderName() string { return e.name }

func TestProbeEmbedder_NilEmbedder(t *testing.T) {
	r := NewRouter(nil, nil)
	provider, ok, err := r.ProbeEmbedder(context.Background())
	if provider != "" {
		t.Errorf("provider=%q, want empty", provider)
	}
	if ok {
		t.Error("ok=true, want false when no embedder configured")
	}
	if err != nil {
		t.Errorf("err=%v, want nil", err)
	}
}

func TestProbeEmbedder_PingSuccess(t *testing.T) {
	r := NewRouter(nil, &pingingEmbedder{name: "ollama"})
	provider, ok, err := r.ProbeEmbedder(context.Background())
	if provider != "ollama" {
		t.Errorf("provider=%q, want %q", provider, "ollama")
	}
	if !ok {
		t.Error("ok=false, want true")
	}
	if err != nil {
		t.Errorf("err=%v, want nil", err)
	}
}

func TestProbeEmbedder_PingFailure(t *testing.T) {
	pingErr := fmt.Errorf("dial tcp: connection refused")
	r := NewRouter(nil, &pingingEmbedder{name: "ollama", pingErr: pingErr})
	provider, ok, err := r.ProbeEmbedder(context.Background())
	if provider != "ollama" {
		t.Errorf("provider=%q, want %q", provider, "ollama")
	}
	if !ok {
		t.Error("ok=false, want true when ping ran and failed")
	}
	if err == nil {
		t.Fatal("err=nil, want non-nil from ping failure")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("err message lost context, got %v", err)
	}
}

func TestProbeEmbedder_EmbedQueryFallback(t *testing.T) {
	r := NewRouter(nil, &embedOnlyEmbedder{name: "stub"})
	provider, ok, err := r.ProbeEmbedder(context.Background())
	if provider != "stub" {
		t.Errorf("provider=%q, want %q", provider, "stub")
	}
	if !ok {
		t.Error("ok=false, want true after fallback embed succeeded")
	}
	if err != nil {
		t.Errorf("err=%v, want nil", err)
	}
}

func TestProbeEmbedder_EmbedQueryFallbackError(t *testing.T) {
	embedErr := fmt.Errorf("connection refused")
	r := NewRouter(nil, &embedOnlyEmbedder{name: "stub", embedErr: embedErr})
	provider, ok, err := r.ProbeEmbedder(context.Background())
	if provider != "stub" {
		t.Errorf("provider=%q, want %q", provider, "stub")
	}
	if !ok {
		t.Error("ok=false, want true when EmbedQuery ran and failed")
	}
	if err == nil {
		t.Fatal("err=nil, want non-nil from EmbedQuery failure")
	}
}

func TestQueryImplementation_NoEmbedderWarning(t *testing.T) {
	// No DB is needed because LexicalSearch is replaced via mock? The
	// router still calls into storage directly. Skip running this test
	// if you can't set up the DB; otherwise verify with a real DB.
	t.Skip("requires real DB; see TestIntegration_SearchGoContext_StructuredDegraded for the integration coverage")
}

func TestClassifyQuery_ExactSymbol(t *testing.T) {
	tests := []struct {
		query string
		want  QueryType
	}{
		{"ReserveInventory", ExactSymbol},
		{"Handler", ExactSymbol},
		{"NewRouter", ExactSymbol},
		{"DB", ExactSymbol},
	}
	for _, tt := range tests {
		got := ClassifyQuery(tt.query)
		if got != tt.want {
			t.Errorf("ClassifyQuery(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestClassifyQuery_ImplementationSearch(t *testing.T) {
	tests := []struct {
		query string
		want  QueryType
	}{
		{"how does inventory reservation work", ImplementationSearch},
		{"find handler for requests", ImplementationSearch},
		{"search for error handling", ImplementationSearch},
	}
	for _, tt := range tests {
		got := ClassifyQuery(tt.query)
		if got != tt.want {
			t.Errorf("ClassifyQuery(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestClassifyQuery_PackageOverview(t *testing.T) {
	tests := []struct {
		query string
		want  QueryType
	}{
		{"what does pkg/temporal do", PackageOverview},
		{"package storage", PackageOverview},
		{"service/graphql", PackageOverview},
		{"internal/retrieval", PackageOverview},
	}
	for _, tt := range tests {
		got := ClassifyQuery(tt.query)
		if got != tt.want {
			t.Errorf("ClassifyQuery(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestClassifyQuery_DependencyTrace(t *testing.T) {
	tests := []struct {
		query string
		want  QueryType
	}{
		{"what calls ProcessPayment", DependencyTrace},
		{"callers of HandleRequest", DependencyTrace},
		{"depends on DatabaseService", DependencyTrace},
		{"what uses Logger", DependencyTrace},
		{"who calls ProcessOrder", DependencyTrace},
	}
	for _, tt := range tests {
		got := ClassifyQuery(tt.query)
		if got != tt.want {
			t.Errorf("ClassifyQuery(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestClassifyQuery_EdgeCases(t *testing.T) {
	tests := []struct {
		query string
		want  QueryType
	}{
		// Empty string defaults to implementation search.
		{"", ImplementationSearch},
		// Lowercase single word is not a Go symbol (no uppercase start).
		{"handler", ImplementationSearch},
		// Single lowercase word.
		{"foo", ImplementationSearch},
		// Number prefix.
		{"123abc", ImplementationSearch},
	}
	for _, tt := range tests {
		got := ClassifyQuery(tt.query)
		if got != tt.want {
			t.Errorf("ClassifyQuery(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}
