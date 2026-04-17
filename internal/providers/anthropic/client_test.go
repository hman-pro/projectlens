package anthropic

import (
	"context"
	"os"
	"strings"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/hman-pro/projectlens/internal/providers/openai"
)

func TestNewClient_CreatesClientWithModel(t *testing.T) {
	c := NewClient("claude-sonnet-4-6")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.model != "claude-sonnet-4-6" {
		t.Errorf("expected model %q, got %q", "claude-sonnet-4-6", c.model)
	}
}

func TestNewClient_CustomModel(t *testing.T) {
	c := NewClient("claude-opus-4-7")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.model != "claude-opus-4-7" {
		t.Errorf("expected model %q, got %q", "claude-opus-4-7", c.model)
	}
}

func TestNewClient_SDKModelConstant(t *testing.T) {
	// Verify the SDK constant matches the expected string.
	if anthropicsdk.ModelClaudeSonnet4_6 != "claude-sonnet-4-6" {
		t.Errorf("expected SDK constant to be %q, got %q", "claude-sonnet-4-6", anthropicsdk.ModelClaudeSonnet4_6)
	}
}

func TestPromptContainsPackageName(t *testing.T) {
	prompt := openai.BuildPackageSummaryPrompt("mypackage", []string{"Foo", "Bar"})
	if !strings.Contains(prompt, "Package: mypackage") {
		t.Errorf("expected prompt to contain package name, got:\n%s", prompt)
	}
}

func TestPromptContainsAllSymbols(t *testing.T) {
	symbols := []string{
		"func NewClient(model string) *Client",
		"func (c *Client) GeneratePackageSummary() (string, error)",
		"type Config struct { ... }",
	}

	prompt := openai.BuildPackageSummaryPrompt("anthropic", symbols)

	for _, sym := range symbols {
		if !strings.Contains(prompt, sym) {
			t.Errorf("expected prompt to contain symbol %q", sym)
		}
	}
}

func TestPromptContainsInstructions(t *testing.T) {
	prompt := openai.BuildPackageSummaryPrompt("pkg", []string{"X"})

	if !strings.Contains(prompt, "Go package documentation expert") {
		t.Error("expected prompt to contain system instruction about Go package documentation expert")
	}
	if !strings.Contains(prompt, "2-4 sentence summary") {
		t.Error("expected prompt to mention 2-4 sentence summary")
	}
}

func TestPromptEmptySymbols(t *testing.T) {
	prompt := openai.BuildPackageSummaryPrompt("empty", nil)

	if !strings.Contains(prompt, "Package: empty") {
		t.Error("expected package name in prompt even with no symbols")
	}
	if !strings.Contains(prompt, "Exported symbols:") {
		t.Error("expected 'Exported symbols:' header even with no symbols")
	}
}

// TestLiveGeneratePackageSummary calls the real Anthropic API.
// Skipped unless ANTHROPIC_API_KEY is set.
//
//	go test ./internal/providers/anthropic/ -v -run TestLive
func TestLiveGeneratePackageSummary(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set — skipping live test")
	}

	client := NewClient("claude-sonnet-4-6")
	symbols := []string{
		"func NewClient(endpoint, model string) *Client",
		"func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)",
		"func (c *Client) Ping(ctx context.Context) error",
	}

	summary, err := client.GeneratePackageSummary(context.Background(), "ollama", symbols)
	if err != nil {
		t.Fatalf("GeneratePackageSummary failed: %v", err)
	}

	if len(summary) < 20 {
		t.Errorf("expected a meaningful summary, got: %q", summary)
	}
	t.Logf("Summary:\n%s", summary)
}
