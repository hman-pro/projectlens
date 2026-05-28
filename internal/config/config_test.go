package config

import "testing"

func TestNewWithDefaults_SummarizationDisabled(t *testing.T) {
	cfg := NewWithDefaults()
	if cfg.Summarization.Enabled {
		t.Errorf("default summarization should be disabled; got %+v", cfg.Summarization)
	}
	if cfg.Summarization.Provider != "" {
		t.Errorf("default summarization provider should be empty; got %q", cfg.Summarization.Provider)
	}
}

func TestNewWithDefaults_EmbeddingsAreOllamaQwen(t *testing.T) {
	cfg := NewWithDefaults()
	if cfg.Embeddings.Provider != "ollama" {
		t.Errorf("default embed provider = %q, want ollama", cfg.Embeddings.Provider)
	}
	if cfg.Embeddings.Model != "qwen3-embedding:0.6b" {
		t.Errorf("default model = %q", cfg.Embeddings.Model)
	}
	if cfg.Embeddings.Dimensions != 1024 {
		t.Errorf("default dimensions = %d", cfg.Embeddings.Dimensions)
	}
}
