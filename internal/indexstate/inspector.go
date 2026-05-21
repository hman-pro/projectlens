package indexstate

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// EmbedderProber matches retrieval.Router.ProbeEmbedder.
type EmbedderProber interface {
	ProbeEmbedder(ctx context.Context) (provider string, ok bool, err error)
}

// SummarizerProber matches the existing summarizer probe contract:
// returns (provider, state, err) where state is one of the standard
// ProviderHealth.State values.
type SummarizerProber interface {
	ProbeSummarizer(ctx context.Context) (provider, state string, err error)
}

// Inspector is the abstraction the report and index_status share.
// Implementations are expected to be cheap to call multiple times.
type Inspector interface {
	ProbeProviders(ctx context.Context) []ProviderHealth
	GitHeadAndDirty(ctx context.Context) GitState
}

// DefaultInspector probes the configured providers and shells out to
// `git -C <repoPath>` for head/dirty state.
type DefaultInspector struct {
	Embedder   EmbedderProber   // optional
	Summarizer SummarizerProber // optional
	RepoPath   string           // may be empty
	Timeout    time.Duration    // per-probe; defaults to 3s when zero
}

func (d *DefaultInspector) ProbeProviders(ctx context.Context) []ProviderHealth {
	timeout := d.Timeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	out := make([]ProviderHealth, 0, 2)

	if d.Embedder != nil {
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		provider, ok, err := d.Embedder.ProbeEmbedder(probeCtx)
		cancel()
		ph := ProviderHealth{Role: "embedder", Provider: provider}
		switch {
		case !ok:
			ph.State = "not_configured"
			ph.Error = "no embedder configured"
		case err != nil:
			ph.State = "error"
			ph.Error = err.Error()
		default:
			ph.State = "reachable"
		}
		out = append(out, ph)
	}

	if d.Summarizer != nil {
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		provider, state, err := d.Summarizer.ProbeSummarizer(probeCtx)
		cancel()
		ph := ProviderHealth{Role: "summarizer", Provider: provider, State: state}
		switch state {
		case "error":
			if err != nil {
				ph.Error = err.Error()
			}
		case "not_configured":
			ph.Error = "summarizer credentials missing"
		}
		out = append(out, ph)
	}

	return out
}

func (d *DefaultInspector) GitHeadAndDirty(ctx context.Context) GitState {
	if d.RepoPath == "" {
		return GitState{}
	}
	headOut, err := exec.CommandContext(ctx, "git", "-C", d.RepoPath, "rev-parse", "HEAD").Output()
	if err != nil {
		return GitState{}
	}
	head := strings.TrimSpace(string(headOut))
	statusOut, err := exec.CommandContext(ctx, "git", "-C", d.RepoPath, "status", "--porcelain").Output()
	if err != nil {
		return GitState{Head: head}
	}
	return GitState{Head: head, Dirty: strings.TrimSpace(string(statusOut)) != ""}
}
