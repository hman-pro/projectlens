package mcpserver

import (
	"strings"
	"testing"
)

// TestProbeRenderer_SummarizationDisabled pins the contract that when
// the summarizer prober reports state "disabled", the index_status
// providers block contains the canonical phrase
// `summarization: disabled` (rather than `summarizer: disabled` or
// dereferencing an absent provider). This is the user-visible signal
// that summarization is off by config — not by an outage.
func TestProbeRenderer_SummarizationDisabled(t *testing.T) {
	providers := []ProviderHealth{
		{Role: "embedder", Provider: "ollama", State: "reachable"},
		{Role: "summarizer", Provider: "", State: "disabled"},
	}

	var b strings.Builder
	renderProviders(&b, providers)

	got := b.String()
	if !strings.Contains(got, "summarization: disabled") {
		t.Fatalf("expected probe text to contain %q, got:\n%s", "summarization: disabled", got)
	}
	if strings.Contains(got, "summarizer: disabled") {
		t.Errorf("expected canonical phrase, but got fallback %q in:\n%s", "summarizer: disabled", got)
	}
}

// TestProbeRenderer_EnabledSummarizer keeps the previous rendering for
// the case where a summarizer is configured and reachable: the line
// reads `summarizer (ollama): reachable`. Adding the "disabled" branch
// must not regress this format.
func TestProbeRenderer_EnabledSummarizer(t *testing.T) {
	providers := []ProviderHealth{
		{Role: "summarizer", Provider: "ollama", State: "reachable"},
	}

	var b strings.Builder
	renderProviders(&b, providers)

	got := b.String()
	if !strings.Contains(got, "summarizer (ollama): reachable") {
		t.Fatalf("expected %q in probe text, got:\n%s", "summarizer (ollama): reachable", got)
	}
}
