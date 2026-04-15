# Azure OpenAI Support Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Support Azure OpenAI as an alternative to direct OpenAI API, so ProjectLens can use RELEX's Azure AI Platform for embeddings and summaries.

**Architecture:** The `openai-go` SDK (v1.12.0) has a built-in `azure` sub-package. We add Azure config fields, and `NewClient` conditionally constructs a standard or Azure-backed client. No interface changes needed -- callers are unaffected.

**Tech Stack:** `github.com/openai/openai-go/azure` (already bundled in SDK), Azure OpenAI API `2024-12-01-preview`

---

### Task 1: Add Azure fields to config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `configs/index.yaml`

**Step 1: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_AzureFieldsFromYAML(t *testing.T) {
	dir := t.TempDir()
	yaml := `
azure_openai:
  endpoint: "https://aip-dev-eu.openai.azure.com"
  api_key: "test-azure-key"
  api_version: "2024-12-01-preview"
  chat_deployment: "gpt-4o-mini"
  embedding_deployment: "text-embedding-3-large"
`
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.AzureOpenAI.Endpoint != "https://aip-dev-eu.openai.azure.com" {
		t.Errorf("endpoint = %q, want %q", cfg.AzureOpenAI.Endpoint, "https://aip-dev-eu.openai.azure.com")
	}
	if cfg.AzureOpenAI.APIKey != "test-azure-key" {
		t.Errorf("api_key = %q, want %q", cfg.AzureOpenAI.APIKey, "test-azure-key")
	}
	if cfg.AzureOpenAI.APIVersion != "2024-12-01-preview" {
		t.Errorf("api_version = %q, want %q", cfg.AzureOpenAI.APIVersion, "2024-12-01-preview")
	}
	if cfg.AzureOpenAI.ChatDeployment != "gpt-4o-mini" {
		t.Errorf("chat_deployment = %q, want %q", cfg.AzureOpenAI.ChatDeployment, "gpt-4o-mini")
	}
	if cfg.AzureOpenAI.EmbeddingDeployment != "text-embedding-3-large" {
		t.Errorf("embedding_deployment = %q, want %q", cfg.AzureOpenAI.EmbeddingDeployment, "text-embedding-3-large")
	}
}

func TestLoad_AzureEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AZURE_OPENAI_ENDPOINT", "https://from-env.openai.azure.com")
	t.Setenv("AZURE_OPENAI_API_KEY", "env-key")
	t.Setenv("AZURE_OPENAI_API_VERSION", "2024-06-01")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.AzureOpenAI.Endpoint != "https://from-env.openai.azure.com" {
		t.Errorf("endpoint = %q, want %q", cfg.AzureOpenAI.Endpoint, "https://from-env.openai.azure.com")
	}
	if cfg.AzureOpenAI.APIKey != "env-key" {
		t.Errorf("api_key = %q, want %q", cfg.AzureOpenAI.APIKey, "env-key")
	}
	if cfg.AzureOpenAI.APIVersion != "2024-06-01" {
		t.Errorf("api_version = %q, want %q", cfg.AzureOpenAI.APIVersion, "2024-06-01")
	}
}

func TestLoad_AzureIsActiveWhenEndpointSet(t *testing.T) {
	dir := t.TempDir()
	yaml := `
azure_openai:
  endpoint: "https://aip-dev-eu.openai.azure.com"
  api_key: "key"
  api_version: "2024-12-01-preview"
`
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if !cfg.AzureOpenAI.IsActive() {
		t.Error("expected IsActive() = true when endpoint is set")
	}
}

func TestLoad_AzureIsInactiveByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.AzureOpenAI.IsActive() {
		t.Error("expected IsActive() = false when no Azure config is set")
	}
}
```

**Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `cfg.AzureOpenAI` field does not exist

**Step 3: Implement the config changes**

In `internal/config/config.go`, add the `AzureOpenAIConfig` struct and field:

```go
// AzureOpenAIConfig holds Azure OpenAI-specific settings.
type AzureOpenAIConfig struct {
	Endpoint            string `yaml:"endpoint"`
	APIKey              string `yaml:"api_key"`
	APIVersion          string `yaml:"api_version"`
	ChatDeployment      string `yaml:"chat_deployment"`
	EmbeddingDeployment string `yaml:"embedding_deployment"`
}

// IsActive reports whether Azure OpenAI is configured.
func (a AzureOpenAIConfig) IsActive() bool {
	return a.Endpoint != ""
}
```

Add the field to `Config`:

```go
type Config struct {
	RepoPath    string            `yaml:"repo_path"`
	DatabaseURL string            `yaml:"database_url"`
	OpenAIKey   string            `yaml:"openai_api_key"`
	AzureOpenAI AzureOpenAIConfig `yaml:"azure_openai"`
	Index       IndexConfig       `yaml:"index"`
}
```

Add env overrides in `Load()`, after the existing overrides:

```go
// Azure OpenAI env overrides.
if v := os.Getenv("AZURE_OPENAI_ENDPOINT"); v != "" {
	cfg.AzureOpenAI.Endpoint = v
}
if v := os.Getenv("AZURE_OPENAI_API_KEY"); v != "" {
	cfg.AzureOpenAI.APIKey = v
}
if v := os.Getenv("AZURE_OPENAI_API_VERSION"); v != "" {
	cfg.AzureOpenAI.APIVersion = v
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS

**Step 5: Update index.yaml with commented Azure section**

Add to `configs/index.yaml`:

```yaml
# Azure OpenAI configuration (alternative to direct OpenAI API).
# Set these values or use env vars: AZURE_OPENAI_ENDPOINT, AZURE_OPENAI_API_KEY, AZURE_OPENAI_API_VERSION.
# azure_openai:
#   endpoint: "https://aip-dev-eu.openai.azure.com"
#   api_key: ""
#   api_version: "2024-12-01-preview"
#   chat_deployment: "gpt-4o-mini"
#   embedding_deployment: "text-embedding-3-large"
```

**Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go configs/index.yaml
git commit -m "feat: add Azure OpenAI config fields with env var overrides"
```

---

### Task 2: Update OpenAI client to support Azure

**Files:**
- Modify: `internal/openai/client.go`
- Modify: `internal/openai/client_test.go`

**Step 1: Write the failing tests**

Add to `internal/openai/client_test.go`:

```go
func TestNewAzureClient_DoesNotPanic(t *testing.T) {
	c := NewAzureClient("https://test.openai.azure.com", "test-key", "2024-12-01-preview", "", "")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewAzureClient_CustomDeployments(t *testing.T) {
	c := NewAzureClient("https://test.openai.azure.com", "test-key", "2024-12-01-preview", "my-chat", "my-embed")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.chatModel != "my-chat" {
		t.Errorf("chatModel = %q, want %q", c.chatModel, "my-chat")
	}
	if c.embeddingModel != "my-embed" {
		t.Errorf("embeddingModel = %q, want %q", c.embeddingModel, "my-embed")
	}
}

func TestNewClient_DefaultModels(t *testing.T) {
	c := NewClient("sk-test")
	if c.chatModel != string(oai.ChatModelGPT4oMini) {
		t.Errorf("chatModel = %q, want %q", c.chatModel, oai.ChatModelGPT4oMini)
	}
	if c.embeddingModel != string(oai.EmbeddingModelTextEmbedding3Large) {
		t.Errorf("embeddingModel = %q, want %q", c.embeddingModel, oai.EmbeddingModelTextEmbedding3Large)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/openai/ -v`
Expected: FAIL — `NewAzureClient` doesn't exist, `chatModel`/`embeddingModel` fields missing

**Step 3: Implement Azure client support**

Rewrite `internal/openai/client.go`:

```go
package openai

import (
	"context"
	"fmt"
	"strings"

	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/azure"
	"github.com/openai/openai-go/option"
)

const embeddingBatchSize = 100

// Client wraps the OpenAI API client.
type Client struct {
	client         oai.Client
	chatModel      string
	embeddingModel string
}

// NewClient creates a new OpenAI client using the standard OpenAI API.
func NewClient(apiKey string) *Client {
	return &Client{
		client:         oai.NewClient(option.WithAPIKey(apiKey)),
		chatModel:      string(oai.ChatModelGPT4oMini),
		embeddingModel: string(oai.EmbeddingModelTextEmbedding3Large),
	}
}

// NewAzureClient creates a new OpenAI client backed by Azure OpenAI.
// chatDeployment and embeddingDeployment default to "gpt-4o-mini" and
// "text-embedding-3-large" if left empty.
func NewAzureClient(endpoint, apiKey, apiVersion, chatDeployment, embeddingDeployment string) *Client {
	if chatDeployment == "" {
		chatDeployment = string(oai.ChatModelGPT4oMini)
	}
	if embeddingDeployment == "" {
		embeddingDeployment = string(oai.EmbeddingModelTextEmbedding3Large)
	}
	return &Client{
		client: oai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithAPIKey(apiKey),
		),
		chatModel:      chatDeployment,
		embeddingModel: embeddingDeployment,
	}
}
```

Update `GeneratePackageSummary` to use `c.chatModel`:

```go
func (c *Client) GeneratePackageSummary(ctx context.Context, packageName string, exportedSymbols []string) (string, error) {
	prompt := BuildPackageSummaryPrompt(packageName, exportedSymbols)

	resp, err := c.client.Chat.Completions.New(ctx, oai.ChatCompletionNewParams{
		Model: oai.ChatModel(c.chatModel),
		Messages: []oai.ChatCompletionMessageParamUnion{
			oai.UserMessage(prompt),
		},
	})
	// ... rest unchanged
```

Update `EmbedBatch` to use `c.embeddingModel`:

```go
resp, err := c.client.Embeddings.New(ctx, oai.EmbeddingNewParams{
	Model: oai.EmbeddingModel(c.embeddingModel),
	Input: oai.EmbeddingNewParamsInputUnion{
		OfArrayOfStrings: batch,
	},
})
```

**Step 4: Run `go mod tidy` to pull azure sub-package deps**

Run: `go mod tidy`

**Step 5: Run tests to verify they pass**

Run: `go test ./internal/openai/ -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/openai/client.go internal/openai/client_test.go go.mod go.sum
git commit -m "feat: add Azure OpenAI client constructor with configurable deployments"
```

---

### Task 3: Wire Azure client into CLI and MCP entrypoints

**Files:**
- Modify: `cmd/projectlens/main.go`
- Modify: `cmd/projectlens-mcp/main.go`

**Step 1: Write a helper function for client creation**

The pattern repeats 4 times (bootstrap, reindex, query in CLI + MCP server). Extract into a shared helper. Add to `cmd/projectlens/main.go` near the bottom:

```go
// newOpenAIClient creates an OpenAI client from the config. Returns nil if
// neither standard OpenAI nor Azure OpenAI is configured.
func newOpenAIClient(cfg *config.Config) *openai.Client {
	if cfg.AzureOpenAI.IsActive() {
		return openai.NewAzureClient(
			cfg.AzureOpenAI.Endpoint,
			cfg.AzureOpenAI.APIKey,
			cfg.AzureOpenAI.APIVersion,
			cfg.AzureOpenAI.ChatDeployment,
			cfg.AzureOpenAI.EmbeddingDeployment,
		)
	}
	if cfg.OpenAIKey != "" {
		return openai.NewClient(cfg.OpenAIKey)
	}
	return nil
}
```

**Step 2: Replace all 3 client-creation blocks in `cmd/projectlens/main.go`**

In `newBootstrapCmd` (lines 109-112), replace:
```go
var oaiClient *openai.Client
if cfg.OpenAIKey != "" {
	oaiClient = openai.NewClient(cfg.OpenAIKey)
}
```
with:
```go
oaiClient := newOpenAIClient(cfg)
```

In `newReindexCmd` (lines 144-147), same replacement.

In `newQueryCmd` (lines 389-392), replace:
```go
var embedder retrieval.QueryEmbedder
if cfg.OpenAIKey != "" {
	embedder = openai.NewClient(cfg.OpenAIKey)
}
```
with:
```go
var embedder retrieval.QueryEmbedder
if oaiClient := newOpenAIClient(cfg); oaiClient != nil {
	embedder = oaiClient
}
```

**Step 3: Update `cmd/projectlens-mcp/main.go`**

Replace lines 54-57:
```go
var oaiClient *openai.Client
if cfg.OpenAIKey != "" {
	oaiClient = openai.NewClient(cfg.OpenAIKey)
}
```
with:
```go
oaiClient := newOpenAIClient(cfg)
```

Add the same `newOpenAIClient` helper to this file (or extract to a shared internal package if desired — for now, duplicating a 12-line function in 2 entrypoints is fine).

**Step 4: Run full test suite**

Run: `go build ./... && go test ./...`
Expected: PASS — no interface changes, callers unaffected

**Step 5: Commit**

```bash
git add cmd/projectlens/main.go cmd/projectlens-mcp/main.go
git commit -m "feat: wire Azure OpenAI client into CLI and MCP entrypoints"
```

---

### Task 4: Update Docker and environment configuration

**Files:**
- Modify: `.env.example`
- Modify: `docker/docker-compose.yml`

**Step 1: Update `.env.example`**

Add Azure env vars:

```
# Azure OpenAI (alternative to direct OPENAI_API_KEY)
# AZURE_OPENAI_ENDPOINT=https://aip-dev-eu.openai.azure.com
# AZURE_OPENAI_API_KEY=your-azure-key
# AZURE_OPENAI_API_VERSION=2024-12-01-preview
```

**Step 2: Update `docker/docker-compose.yml`**

Add Azure env vars to both `projectlens-mcp` and `projectlens-indexer` services:

```yaml
environment:
  DATABASE_URL: "postgres://projectlens:${PROJECTLENS_DB_PASSWORD:-projectlens}@postgres:5432/projectlens?sslmode=disable"
  OPENAI_API_KEY: ${OPENAI_API_KEY:-}
  AZURE_OPENAI_ENDPOINT: ${AZURE_OPENAI_ENDPOINT:-}
  AZURE_OPENAI_API_KEY: ${AZURE_OPENAI_API_KEY:-}
  AZURE_OPENAI_API_VERSION: ${AZURE_OPENAI_API_VERSION:-}
  REPO_PATH: /repo
```

**Step 3: Commit**

```bash
git add .env.example docker/docker-compose.yml
git commit -m "feat: add Azure OpenAI env vars to Docker and .env.example"
```

---

### Task 5: Update CLAUDE.md documentation

**Files:**
- Modify: `CLAUDE.md`

**Step 1: Update environment variables table**

Add Azure variables to the existing table in CLAUDE.md:

```
| `AZURE_OPENAI_ENDPOINT` | Azure OpenAI endpoint URL | No (use instead of OPENAI_API_KEY) |
| `AZURE_OPENAI_API_KEY` | Azure OpenAI API key | No (required if endpoint set) |
| `AZURE_OPENAI_API_VERSION` | Azure API version (e.g. 2024-12-01-preview) | No |
```

**Step 2: Add a note to Design decisions**

Add:
```
- **Azure OpenAI support** — standard and Azure endpoints use the same SDK (`openai-go/azure`), selected by config
```

**Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: add Azure OpenAI configuration to CLAUDE.md"
```

---

## Verification checklist

After all tasks are complete:

1. `go build ./...` compiles without errors
2. `go test ./...` passes all tests (existing + new)
3. Setting `AZURE_OPENAI_ENDPOINT` + `AZURE_OPENAI_API_KEY` + `AZURE_OPENAI_API_VERSION` env vars uses Azure path
4. Leaving Azure vars empty still uses standard OpenAI path (backward compatible)
5. Docker compose passes Azure env vars through to containers
