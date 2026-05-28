// Package anthropic provides a wrapper around the Anthropic API for generating
// package summaries using Claude models.
package anthropic

import (
	"context"
	"fmt"
	"os"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/hman-pro/projectlens/internal/providers/identity"
	"github.com/hman-pro/projectlens/internal/summaries"
)

// Client wraps the Anthropic API client for generating package summaries.
type Client struct {
	client anthropic.Client
	model  string
	apiKey string
}

// NewClient creates a new Anthropic client. The API key is read from
// ANTHROPIC_API_KEY by the SDK automatically.
func NewClient(model string) *Client {
	return &Client{
		client: anthropic.NewClient(),
		model:  model,
		apiKey: os.Getenv("ANTHROPIC_API_KEY"),
	}
}

// Configured reports whether the client captured an API key at
// construction time. Cheap — no network call. Use this for a status
// signal when paying for tokens is not appropriate (e.g. on every
// index_status invocation).
func (c *Client) Configured() bool {
	return c.apiKey != ""
}

// GeneratePackageSummary calls Claude with the same prompt format used by
// the OpenAI client. Returns a 2-4 sentence summary.
func (c *Client) GeneratePackageSummary(ctx context.Context, packageName string, exportedSymbols []string) (string, error) {
	prompt := summaries.BuildPackageSummaryPrompt(packageName, exportedSymbols)

	resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("anthropic: message for package %q: %w", packageName, err)
	}

	if len(resp.Content) == 0 {
		return "", fmt.Errorf("anthropic: no content blocks returned for package %q", packageName)
	}

	block := resp.Content[0]
	if block.Type != "text" {
		return "", fmt.Errorf("anthropic: unexpected content block type %q for package %q", block.Type, packageName)
	}

	return block.Text, nil
}

// SummaryIdentity returns the provider identity for the summarization role.
func (c *Client) SummaryIdentity() identity.ProviderIdentity {
	return identity.ProviderIdentity{
		Vendor: "anthropic",
		Model:  c.model,
	}
}
