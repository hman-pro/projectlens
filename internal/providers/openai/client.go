// Package openai provides a wrapper around the OpenAI API for generating
// package summaries and text embeddings.
package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/hman-pro/projectlens/internal/providers/identity"
)

const defaultOpenAIBaseURL = "https://api.openai.com/v1"

// Default model names used when no override is provided.
const (
	defaultChatModel  = "gpt-4o-mini"
	defaultEmbedModel = "text-embedding-3-large"
)

// embeddingBatchSize is the maximum number of texts per embedding API call.
const embeddingBatchSize = 100

// Client wraps the OpenAI API client.
type Client struct {
	client        oai.Client
	embeddingDims int // if > 0, request this many dimensions from the embedding model
	chatModel     string
	embedModel    string

	apiKey     string
	baseURL    string // overridable in tests
	httpClient *http.Client
}

// NewClient creates a new OpenAI client using the provided API key and default
// models (gpt-4o-mini for chat, text-embedding-3-large for embeddings).
func NewClient(apiKey string) *Client {
	return NewClientWithModels(apiKey, defaultChatModel, defaultEmbedModel, 0)
}

// NewClientWithDims creates a new OpenAI client that requests a specific
// embedding dimension (e.g., 1024 instead of the default 3072).
func NewClientWithDims(apiKey string, dims int) *Client {
	return NewClientWithModels(apiKey, defaultChatModel, defaultEmbedModel, dims)
}

// NewClientWithModels creates a new OpenAI client with explicit chat and
// embedding model names and an optional dimension override (0 = use model
// default). Use this when the caller wants to override the default models.
func NewClientWithModels(apiKey, chatModel, embedModel string, dims int) *Client {
	if chatModel == "" {
		chatModel = defaultChatModel
	}
	if embedModel == "" {
		embedModel = defaultEmbedModel
	}
	return &Client{
		client:        oai.NewClient(option.WithAPIKey(apiKey)),
		embeddingDims: dims,
		chatModel:     chatModel,
		embedModel:    embedModel,
		apiKey:        apiKey,
		baseURL:       defaultOpenAIBaseURL,
		httpClient:    http.DefaultClient,
	}
}

// ProviderName returns the short label "openai" used in
// mcpserver.ProviderHealth.Provider.
func (c *Client) ProviderName() string { return "openai" }

// Ping issues GET {baseURL}/models with the configured API key and
// returns nil on a 2xx response, an error otherwise. Caller controls
// the timeout via ctx. Use this as a cheap health probe; it does not
// hit the chat/embeddings APIs.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("openai: build ping request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("openai: ping %s: %w", c.baseURL+"/models", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("openai: ping returned status %d", resp.StatusCode)
	}
	return nil
}

// BuildPackageSummaryPrompt constructs the prompt used for generating a package
// summary. Exported so it can be tested independently.
func BuildPackageSummaryPrompt(packageName string, exportedSymbols []string) string {
	var b strings.Builder
	b.WriteString("You are a Go package documentation expert. Given the following exported symbols from a Go package, write a 2-4 sentence summary of what this package does, when a developer would use it, and its main responsibilities.\n\n")
	b.WriteString("Package: ")
	b.WriteString(packageName)
	b.WriteString("\n\nExported symbols:\n")
	for _, sym := range exportedSymbols {
		b.WriteString(sym)
		b.WriteString("\n")
	}
	b.WriteString("\nWrite a concise summary focused on purpose and usage, not implementation details.")
	return b.String()
}

// GeneratePackageSummary calls the configured chat model with a prompt built
// from the package name and its exported symbols, returning a 2-4 sentence
// summary.
func (c *Client) GeneratePackageSummary(ctx context.Context, packageName string, exportedSymbols []string) (string, error) {
	prompt := BuildPackageSummaryPrompt(packageName, exportedSymbols)

	resp, err := c.client.Chat.Completions.New(ctx, oai.ChatCompletionNewParams{
		Model: oai.ChatModel(c.chatModel),
		Messages: []oai.ChatCompletionMessageParamUnion{
			oai.UserMessage(prompt),
		},
	})
	if err != nil {
		return "", fmt.Errorf("openai: chat completion for package %q: %w", packageName, err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices returned for package %q", packageName)
	}

	return resp.Choices[0].Message.Content, nil
}

// EmbedBatch calls the configured embedding model to generate embeddings for
// the given texts. Texts are batched into groups of up to 100 per API call.
// Returns one embedding vector ([]float32) per input text.
func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	result := make([][]float32, 0, len(texts))

	for start := 0; start < len(texts); start += embeddingBatchSize {
		end := start + embeddingBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]

		params := oai.EmbeddingNewParams{
			Model: oai.EmbeddingModel(c.embedModel),
			Input: oai.EmbeddingNewParamsInputUnion{
				OfArrayOfStrings: batch,
			},
		}
		if c.embeddingDims > 0 {
			params.Dimensions = oai.Int(int64(c.embeddingDims))
		}
		resp, err := c.client.Embeddings.New(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("openai: embedding batch [%d:%d]: %w", start, end, err)
		}

		for _, emb := range resp.Data {
			vec := make([]float32, len(emb.Embedding))
			for i, v := range emb.Embedding {
				vec[i] = float32(v)
			}
			result = append(result, vec)
		}
	}

	return result, nil
}

// EmbedIdentity returns the provider identity for the embedding role.
func (c *Client) EmbedIdentity() identity.ProviderIdentity {
	return identity.ProviderIdentity{
		Vendor:     "openai",
		Model:      c.embedModel,
		Dimensions: c.embeddingDims,
	}
}

// SummaryIdentity returns the provider identity for the summarization role.
func (c *Client) SummaryIdentity() identity.ProviderIdentity {
	return identity.ProviderIdentity{
		Vendor: "openai",
		Model:  c.chatModel,
	}
}
