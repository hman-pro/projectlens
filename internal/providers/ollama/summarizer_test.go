package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSummarizer_RequestShapeAndSuccess(t *testing.T) {
	var got generateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(generateResponse{Response: "Two sentence summary."})
	}))
	defer srv.Close()

	s := NewSummarizer(srv.URL, "qwen3-coder:30b")
	got2, err := s.GeneratePackageSummary(context.Background(), "config", []string{"func Load(path string) (*Config, error)"})
	if err != nil {
		t.Fatal(err)
	}
	if got2 != "Two sentence summary." {
		t.Errorf("summary = %q", got2)
	}
	if got.Model != "qwen3-coder:30b" {
		t.Errorf("model = %s", got.Model)
	}
	if got.Stream {
		t.Error("stream should be false")
	}
	if !strings.Contains(got.Prompt, "config") {
		t.Errorf("prompt missing package name: %s", got.Prompt)
	}
}

func TestSummarizer_Non200Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	s := NewSummarizer(srv.URL, "qwen3-coder:30b")
	if _, err := s.GeneratePackageSummary(context.Background(), "p", []string{"X"}); err == nil {
		t.Fatal("expected error for non-200")
	}
}

func TestSummarizer_MalformedJSONError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	s := NewSummarizer(srv.URL, "qwen3-coder:30b")
	if _, err := s.GeneratePackageSummary(context.Background(), "p", []string{"X"}); err == nil {
		t.Fatal("expected error for malformed body")
	}
}

func TestSummarizer_Identity(t *testing.T) {
	s := NewSummarizer("", "qwen3-coder:30b")
	id := s.SummaryIdentity()
	if id.Vendor != "ollama" || id.Model != "qwen3-coder:30b" {
		t.Errorf("identity = %+v", id)
	}
}
