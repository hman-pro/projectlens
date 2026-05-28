package main

import (
	"context"
	"testing"

	"github.com/hman-pro/projectlens/internal/config"
)

// TestIndexSummarizeStage_DisabledIsNoop pins the contract that the
// summarize stage does no work and never dereferences a nil summarizer
// when the config disables summarization. Passing a nil DB is safe
// here because the disabled branch returns before touching it.
func TestIndexSummarizeStage_DisabledIsNoop(t *testing.T) {
	cfg := &config.Config{}
	cfg.Summarization.Enabled = false
	if err := indexSummarizeStage(context.Background(), nil, cfg, "."); err != nil {
		t.Fatalf("disabled stage should return nil, got %v", err)
	}
}
