package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/hman-pro/projectlens/internal/providers/identity"
	"github.com/hman-pro/projectlens/internal/summaries"
)

// Summarizer satisfies summaries.PackageSummarizer using Ollama /api/generate.
type Summarizer struct {
	endpoint string
	model    string
	http     *http.Client
}

func NewSummarizer(endpoint, model string) *Summarizer {
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}
	return &Summarizer{endpoint: endpoint, model: model, http: &http.Client{}}
}

type generateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type generateResponse struct {
	Response string `json:"response"`
}

func (s *Summarizer) GeneratePackageSummary(ctx context.Context, packageName string, exportedSymbols []string) (string, error) {
	prompt := summaries.BuildPackageSummaryPrompt(packageName, exportedSymbols)
	body, err := json.Marshal(generateRequest{Model: s.model, Prompt: prompt, Stream: false})
	if err != nil {
		return "", fmt.Errorf("ollama summarize: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.endpoint+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama summarize: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama summarize: post (is ollama at %s?): %w", s.endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama summarize: %s: %s", resp.Status, string(b))
	}
	var out generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("ollama summarize: decode: %w", err)
	}
	return out.Response, nil
}

func (s *Summarizer) SummaryIdentity() identity.ProviderIdentity {
	return identity.ProviderIdentity{Vendor: "ollama", Model: s.model}
}
