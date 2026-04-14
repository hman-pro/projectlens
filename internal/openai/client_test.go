package openai

import (
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

func TestBuildPackageSummaryPrompt_ContainsPackageName(t *testing.T) {
	prompt := BuildPackageSummaryPrompt("mypackage", []string{"Foo", "Bar"})

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

	prompt := BuildPackageSummaryPrompt("config", symbols)

	for _, sym := range symbols {
		if !strings.Contains(prompt, sym) {
			t.Errorf("expected prompt to contain symbol %q, got:\n%s", sym, prompt)
		}
	}
}

func TestBuildPackageSummaryPrompt_ContainsSystemInstruction(t *testing.T) {
	prompt := BuildPackageSummaryPrompt("pkg", []string{"X"})

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
	prompt := BuildPackageSummaryPrompt("test", symbols)

	// Each symbol should be on its own line.
	for _, sym := range symbols {
		if !strings.Contains(prompt, "\n"+sym+"\n") {
			t.Errorf("expected symbol %q to be on its own line in prompt:\n%s", sym, prompt)
		}
	}
}

func TestBuildPackageSummaryPrompt_EmptySymbols(t *testing.T) {
	prompt := BuildPackageSummaryPrompt("empty", nil)

	if !strings.Contains(prompt, "Package: empty") {
		t.Error("expected package name in prompt even with no symbols")
	}
	if !strings.Contains(prompt, "Exported symbols:") {
		t.Error("expected 'Exported symbols:' header even with no symbols")
	}
}
