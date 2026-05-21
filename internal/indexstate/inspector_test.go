package indexstate

import (
	"context"
	"errors"
	"testing"
)

type stubEmbedder struct {
	provider string
	ok       bool
	err      error
}

func (s stubEmbedder) ProbeEmbedder(_ context.Context) (string, bool, error) {
	return s.provider, s.ok, s.err
}

type stubSummarizer struct {
	provider string
	state    string
	err      error
}

func (s stubSummarizer) ProbeSummarizer(_ context.Context) (string, string, error) {
	return s.provider, s.state, s.err
}

func TestProbeProviders_ReachableAndConfigured(t *testing.T) {
	insp := &DefaultInspector{
		Embedder:   stubEmbedder{provider: "ollama", ok: true},
		Summarizer: stubSummarizer{provider: "anthropic", state: "configured"},
	}
	got := insp.ProbeProviders(context.Background())
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[0].Role != "embedder" || got[0].State != "reachable" || got[0].Provider != "ollama" {
		t.Errorf("embedder mismatch: %+v", got[0])
	}
	if got[1].Role != "summarizer" || got[1].State != "configured" || got[1].Provider != "anthropic" {
		t.Errorf("summarizer mismatch: %+v", got[1])
	}
}

func TestProbeProviders_NotConfiguredAndError(t *testing.T) {
	insp := &DefaultInspector{
		Embedder:   stubEmbedder{ok: false},
		Summarizer: stubSummarizer{state: "error", err: errors.New("boom")},
	}
	got := insp.ProbeProviders(context.Background())
	if got[0].State != "not_configured" {
		t.Errorf("want not_configured, got %s", got[0].State)
	}
	if got[1].State != "error" || got[1].Error != "boom" {
		t.Errorf("want error/boom, got %+v", got[1])
	}
}

func TestProbeProviders_EmbedderProbeError(t *testing.T) {
	insp := &DefaultInspector{
		Embedder: stubEmbedder{provider: "ollama", ok: true, err: errors.New("conn refused")},
	}
	got := insp.ProbeProviders(context.Background())
	if got[0].State != "error" || got[0].Error != "conn refused" {
		t.Errorf("want error/conn refused, got %+v", got[0])
	}
}

func TestProbeProviders_NilDependenciesSkip(t *testing.T) {
	insp := &DefaultInspector{}
	got := insp.ProbeProviders(context.Background())
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestGitHeadAndDirty_NoRepoPath(t *testing.T) {
	insp := &DefaultInspector{}
	gs := insp.GitHeadAndDirty(context.Background())
	if gs.Head != "" || gs.Dirty {
		t.Errorf("want empty zero, got %+v", gs)
	}
}
