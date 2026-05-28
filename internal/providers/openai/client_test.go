package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
