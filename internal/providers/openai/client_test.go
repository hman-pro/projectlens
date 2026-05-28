package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hman-pro/projectlens/internal/summaries"
)

func TestNewClient_DoesNotPanic(t *testing.T) {
	// NewClient with a valid key string should not panic.
	c := NewClient("sk-test-key-1234567890")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClient_EmptyKey(t *testing.T) {
	// Even an empty key should produce a client without panicking;
	// the API call itself would fail later.
	c := NewClient("")
	if c == nil {
		t.Fatal("expected non-nil client even with empty key")
	}
}

func TestBuildPackageSummaryPrompt_ContainsPackageName(t *testing.T) {
	prompt := summaries.BuildPackageSummaryPrompt("mypackage", []string{"Foo", "Bar"})

	if !strings.Contains(prompt, "Package: mypackage") {
		t.Errorf("expected prompt to contain package name, got:\n%s", prompt)
	}
}

func TestBuildPackageSummaryPrompt_ContainsAllSymbols(t *testing.T) {
	symbols := []string{
		"func NewClient(apiKey string) *Client",
		"func (c *Client) Do() error",
		"type Config struct { ... }",
	}

	prompt := summaries.BuildPackageSummaryPrompt("config", symbols)

	for _, sym := range symbols {
		if !strings.Contains(prompt, sym) {
			t.Errorf("expected prompt to contain symbol %q, got:\n%s", sym, prompt)
		}
	}
}

func TestBuildPackageSummaryPrompt_ContainsSystemInstruction(t *testing.T) {
	prompt := summaries.BuildPackageSummaryPrompt("pkg", []string{"X"})

	if !strings.Contains(prompt, "Go package documentation expert") {
		t.Error("expected prompt to contain system instruction about Go package documentation expert")
	}
	if !strings.Contains(prompt, "2-4 sentence summary") {
		t.Error("expected prompt to mention 2-4 sentence summary")
	}
	if !strings.Contains(prompt, "Exported symbols:") {
		t.Error("expected prompt to contain 'Exported symbols:' header")
	}
	if !strings.Contains(prompt, "concise summary focused on purpose and usage") {
		t.Error("expected prompt to contain closing instruction")
	}
}

func TestBuildPackageSummaryPrompt_SymbolsOnSeparateLines(t *testing.T) {
	symbols := []string{"Alpha", "Beta", "Gamma"}
	prompt := summaries.BuildPackageSummaryPrompt("test", symbols)

	// Each symbol should be on its own line.
	for _, sym := range symbols {
		if !strings.Contains(prompt, "\n"+sym+"\n") {
			t.Errorf("expected symbol %q to be on its own line in prompt:\n%s", sym, prompt)
		}
	}
}

func TestBuildPackageSummaryPrompt_EmptySymbols(t *testing.T) {
	prompt := summaries.BuildPackageSummaryPrompt("empty", nil)

	if !strings.Contains(prompt, "Package: empty") {
		t.Error("expected package name in prompt even with no symbols")
	}
	if !strings.Contains(prompt, "Exported symbols:") {
		t.Error("expected 'Exported symbols:' header even with no symbols")
	}
}

func TestPing_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			http.Error(w, "missing/wrong auth header: "+got, http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	c := NewClient("test-key")
	c.baseURL = server.URL

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: unexpected error: %v", err)
	}
}

func TestPing_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	c := NewClient("test-key")
	c.baseURL = server.URL

	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping: expected error on 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("Ping error should mention status 401, got: %v", err)
	}
}

func TestPing_NetworkError(t *testing.T) {
	c := NewClient("test-key")
	c.baseURL = "http://127.0.0.1:1" // intentionally unreachable

	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping: expected network error, got nil")
	}
}
