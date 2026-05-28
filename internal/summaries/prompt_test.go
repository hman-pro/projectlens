package summaries

import (
	"strings"
	"testing"
)

func TestBuildPackageSummaryPrompt_ContainsPackageName(t *testing.T) {
	prompt := BuildPackageSummaryPrompt("mypackage", []string{"Foo", "Bar"})
	if !strings.Contains(prompt, "mypackage") {
		t.Errorf("prompt missing package name: %s", prompt)
	}
}

func TestBuildPackageSummaryPrompt_ContainsAllSymbols(t *testing.T) {
	syms := []string{"func New() *Client", "func (c *Client) Do()"}
	prompt := BuildPackageSummaryPrompt("config", syms)
	for _, s := range syms {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt missing symbol %q", s)
		}
	}
}

func TestBuildPackageSummaryPrompt_EmptySymbols(t *testing.T) {
	prompt := BuildPackageSummaryPrompt("empty", nil)
	if prompt == "" {
		t.Error("prompt empty for empty symbols")
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
