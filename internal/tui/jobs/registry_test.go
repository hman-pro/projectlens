package jobs_test

import (
	"context"
	"testing"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func testCfg() *config.Config {
	return &config.Config{
		Embeddings:    config.EmbeddingsConfig{Provider: "ollama"},
		Summarization: config.SummarizationConfig{Provider: "anthropic"},
	}
}

func TestRegistry_NoKeyCollisions(t *testing.T) {
	seen := map[rune]string{}
	for _, s := range jobs.DefaultRegistry(testCfg()) {
		if other, ok := seen[s.Key]; ok {
			t.Errorf("key %q used by %s and %s", s.Key, other, s.Name)
		}
		seen[s.Key] = s.Name
	}
}

func TestRegistry_AllSpecsValid(t *testing.T) {
	for _, s := range jobs.DefaultRegistry(testCfg()) {
		if !s.Valid() {
			t.Errorf("spec %q is not Valid: %+v", s.Name, s)
		}
		if s.Confirm == jobs.ConfirmTyped && s.Phrase == "" {
			t.Errorf("spec %q is ConfirmTyped but Phrase is empty", s.Name)
		}
	}
}

func TestRegistry_PreflightUsesStore(t *testing.T) {
	f := store.NewFake()
	f.SetEmbedPending(7)
	f.SetSummarizePending(3)
	f.SetHistoryCommits(42)
	f.SetChangedFiles(11)

	wants := map[string]int{
		"reindex":         11,
		"reindex --full":  11,
		"index-embed":     7,
		"index-summarize": 3,
		"index-history":   42,
	}
	for _, s := range jobs.DefaultRegistry(testCfg()) {
		want, ok := wants[s.Name]
		if !ok {
			t.Fatalf("unexpected spec name %q", s.Name)
		}
		got, _, err := s.Preflight(context.Background(), f)
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		if got != want {
			t.Errorf("%s: count = %d, want %d", s.Name, got, want)
		}
	}
}

func TestRegistry_CostDriverFromConfig(t *testing.T) {
	cfg := &config.Config{
		Embeddings:    config.EmbeddingsConfig{Provider: "openai"},
		Summarization: config.SummarizationConfig{Provider: "openai"},
	}
	f := store.NewFake()
	for _, s := range jobs.DefaultRegistry(cfg) {
		_, cost, err := s.Preflight(context.Background(), f)
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		switch s.Name {
		case "index-embed":
			if cost != "openai" {
				t.Errorf("embed cost = %q, want openai", cost)
			}
		case "index-summarize":
			if cost != "openai" {
				t.Errorf("summarize cost = %q, want openai", cost)
			}
		default:
			if cost != "" {
				t.Errorf("%s cost = %q, want empty", s.Name, cost)
			}
		}
	}
}
