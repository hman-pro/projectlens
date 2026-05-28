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
