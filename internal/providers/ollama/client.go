// Package ollama provides an embedding client that talks to a local Ollama
// instance, satisfying the embeddings.Embedder interface.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client implements the Embedder interface using a local Ollama instance.
type Client struct {
	endpoint string
	model    string
	http     *http.Client
}

// NewClient creates an Ollama client. Endpoint defaults to http://localhost:11434 if empty.
func NewClient(endpoint, model string) *Client {
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}
	return &Client{
		endpoint: endpoint,
		model:    model,
		http:     &http.Client{},
	}
}

// embedRequest is the JSON body for POST /api/embed.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedResponse is the JSON response from POST /api/embed.
type embedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float64 `json:"embeddings"`
}

// EmbedBatch generates embeddings for the given texts using the Ollama API.
// This satisfies the embeddings.Embedder interface.
func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	body, err := json.Marshal(embedRequest{
		Model: c.model,
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: request failed (is ollama running at %s?): %w", c.endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama: %s: %s", resp.Status, string(respBody))
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}

	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama: expected %d embeddings, got %d", len(texts), len(result.Embeddings))
	}

	// Convert float64 to float32.
	vectors := make([][]float32, len(result.Embeddings))
	for i, emb := range result.Embeddings {
		vec := make([]float32, len(emb))
		for j, v := range emb {
			vec[j] = float32(v)
		}
		vectors[i] = vec
	}

	return vectors, nil
}

// Ping checks if the Ollama server is reachable.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.endpoint+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("ollama: create ping request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: not reachable at %s: %w", c.endpoint, err)
	}
	resp.Body.Close()
	return nil
}
