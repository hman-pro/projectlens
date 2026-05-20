# Agent-Native MCP Responses Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every ProjectLens MCP tool return a typed `StructuredContent` payload alongside the existing human-readable text, with explicit `provenance`, `freshness`, `degraded`, and `evidence` fields so agents can decide whether to trust a result without regex-scraping prose. Also extend `index_status` to report each configured provider's reachability as a tristate.

**Architecture:**
- Introduce a shared `internal/mcpserver/types.go` with reusable response envelopes (`StageFreshness`, `Degradation`, `EvidenceSpan`, `ProviderHealth`).
- Migrate each handler from `mcp.NewToolResultText` to `mcp.NewToolResultStructured(payload, fallbackText)` — the text fallback stays so older agents/clients still see prose.
- Plumb a `summarizer` health probe through `Server` so `index_status` can report it next to `embedder_healthy`.
- Surface the existing `retrieval.QueryResult.Warnings` slice as a structured `degraded` block on `search_go_context`, replacing the prose `warning:` lines.
- Each result that points to a file emits an `evidence` span (`file_path` + `line_start` + `line_end`) so the agent can cheaply re-read the bytes it is being asked to trust.

**Tech Stack:** Go 1.26 · `github.com/mark3labs/mcp-go v0.48.0` (`NewToolResultStructured` already supported) · `pgx` (no schema changes) · existing provider clients (`ollama`, `anthropic`, `openai`).

---

## File Structure

**New files**
- `internal/mcpserver/types.go` — shared response envelopes and per-tool payload structs (one file: handler-shaped types stay close to handler logic).
- `internal/mcpserver/types_test.go` — JSON-shape assertions for the envelopes.

**Modified files**
- `internal/mcpserver/handlers.go` — each handler returns `NewToolResultStructured(payload, fallback)`.
- `internal/mcpserver/server.go` — `Server` gains an optional `summarizer SummarizerProber` field; `New` and a new `WithSummarizer` setter accept it.
- `internal/mcpserver/knowledge_handlers.go` — same migration pattern for `save_knowledge` / `search_knowledge`.
- `internal/mcpserver/handlers_integration_test.go` — every test gains a `decode(t, result, &payload)` assertion against the structured shape; existing text assertions stay as smoke checks.
- `internal/providers/anthropic/client.go` — add `Ping(ctx) error` using a 1-token `messages.create` call gated behind a short timeout.
- `internal/providers/openai/client.go` — add `Ping(ctx) error` hitting `GET /v1/models`.
- `cmd/projectlens-mcp/main.go` — wire the summarizer prober into the server constructor.
- `claude/skills/use-projectlens/SKILL.md` — document the new structured fields agents can rely on.

---

## Task 1: Park backlog (already done in this commit)

The backlog file `docs/plans/backlog.md` was created in the same commit that introduced this plan. No further action — listed here only so the plan is self-describing.

- [x] **Step 1: Verify backlog file exists**

```bash
test -f docs/plans/backlog.md && head -5 docs/plans/backlog.md
```

Expected: prints the first 5 lines of the backlog header.

---

## Task 2: Define shared response types

**Files:**
- Create: `internal/mcpserver/types.go`
- Create: `internal/mcpserver/types_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcpserver/types_test.go`:

```go
package mcpserver

import (
	"encoding/json"
	"testing"
)

func TestEvidenceSpanJSONShape(t *testing.T) {
	e := EvidenceSpan{FilePath: "internal/foo/bar.go", LineStart: 10, LineEnd: 20}
	got, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"file_path":"internal/foo/bar.go","line_start":10,"line_end":20}`
	if string(got) != want {
		t.Fatalf("EvidenceSpan JSON shape:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestDegradationJSONShape(t *testing.T) {
	d := Degradation{Degraded: true, Reason: "embedder unreachable", Fallback: "lexical-only"}
	got, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"degraded":true,"reason":"embedder unreachable","fallback":"lexical-only"}`
	if string(got) != want {
		t.Fatalf("Degradation JSON shape:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestProviderHealthTristate(t *testing.T) {
	cases := []struct {
		name string
		p    ProviderHealth
		want string
	}{
		{"reachable", ProviderHealth{Role: "embedder", Provider: "ollama", State: "reachable"}, `{"role":"embedder","provider":"ollama","state":"reachable"}`},
		{"configured", ProviderHealth{Role: "summarizer", Provider: "anthropic", State: "configured"}, `{"role":"summarizer","provider":"anthropic","state":"configured"}`},
		{"error", ProviderHealth{Role: "embedder", Provider: "ollama", State: "error", Error: "dial tcp: connection refused"}, `{"role":"embedder","provider":"ollama","state":"error","error":"dial tcp: connection refused"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.p)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("ProviderHealth JSON:\n  got:  %s\n  want: %s", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mcpserver/ -run 'TestEvidenceSpanJSONShape|TestDegradationJSONShape|TestProviderHealthTristate' -v
```

Expected: compilation error — `undefined: EvidenceSpan` (and the other three types).

- [ ] **Step 3: Write the type file**

Create `internal/mcpserver/types.go`:

```go
package mcpserver

// EvidenceSpan points at the bytes a structured result is derived from
// so an agent can cheaply re-read and verify before acting on them.
type EvidenceSpan struct {
	FilePath  string `json:"file_path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
}

// Degradation signals that a result is partial because a backend was
// unavailable. Agents should treat results as best-effort when
// Degraded == true.
type Degradation struct {
	Degraded bool   `json:"degraded"`
	Reason   string `json:"reason,omitempty"`
	Fallback string `json:"fallback,omitempty"`
}

// ProviderHealth reports the state of one configured provider. State
// is a tristate:
//   - "reachable": the provider responded to a cheap probe.
//   - "configured": credentials/endpoint are set but no probe was run
//     (or the probe is too expensive/charged to run on every status call).
//   - "error": a probe ran and failed; Error carries the message.
type ProviderHealth struct {
	Role     string `json:"role"`              // "embedder" | "summarizer"
	Provider string `json:"provider"`          // "ollama" | "openai" | "anthropic"
	State    string `json:"state"`             // "reachable" | "configured" | "error"
	Error    string `json:"error,omitempty"`
}

// StageFreshness mirrors the existing stageStatus block but is exported
// for use across the response envelopes. Age is computed at response
// time from CompletedAt.
type StageFreshness struct {
	Stage          string  `json:"stage"`
	Status         string  `json:"status"`
	CommitSHA      string  `json:"commit_sha,omitempty"`
	StartedAt      string  `json:"started_at,omitempty"`
	CompletedAt    string  `json:"completed_at,omitempty"`
	AgeMinutes     float64 `json:"age_minutes,omitempty"`
	FilesProcessed int     `json:"files_processed,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/mcpserver/ -run 'TestEvidenceSpanJSONShape|TestDegradationJSONShape|TestProviderHealthTristate' -v
```

Expected: PASS for all three tests.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/types.go internal/mcpserver/types_test.go docs/plans/2026-05-18-agent-native-mcp-responses.md docs/plans/backlog.md
git commit -m "feat(mcpserver): introduce shared structured response envelopes"
```

---

## Task 3: Add Ping to OpenAI client

**Files:**
- Modify: `internal/providers/openai/client.go`
- Modify: `internal/providers/openai/client_test.go` (add ping test)

- [ ] **Step 1: Write the failing test**

Append to `internal/providers/openai/client_test.go`:

```go
func TestPing_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	c := NewClient("test-key")
	c.baseURL = server.URL // requires baseURL to be exported/test-overridable; see Step 3
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: unexpected error: %v", err)
	}
}

func TestPing_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	c := NewClient("test-key")
	c.baseURL = server.URL
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("Ping: expected error on 401, got nil")
	}
}
```

Add imports `"context"`, `"net/http"`, `"net/http/httptest"` if not already present.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/providers/openai/ -run TestPing -v
```

Expected: compilation error — `c.baseURL undefined` and/or `c.Ping undefined`.

- [ ] **Step 3: Inspect the existing client struct, then add the field + method**

Read `internal/providers/openai/client.go` to confirm the struct fields. Add a `baseURL` field (default `https://api.openai.com/v1`), wire it through `NewClient` / `NewClientWithDims`, then add:

```go
// Ping issues a low-cost GET /models against the configured baseURL to
// confirm the API key + network path are healthy. Returns nil on 2xx,
// an error containing the response status otherwise. Context must
// carry a short timeout (caller's responsibility).
func (c *Client) Ping(ctx context.Context) error {
	url := c.baseURL + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("openai: build ping request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("openai: ping %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("openai: ping returned status %d", resp.StatusCode)
	}
	return nil
}
```

Use the field names already on the struct (`apiKey`, `httpClient`, etc.) — the names above are illustrative. If the upstream SDK is used for chat/embeddings, keep using it, and only hand-roll the `http.Client` call for the ping.

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/providers/openai/ -run TestPing -v
go test ./internal/providers/openai/ -v
```

Expected: both `TestPing_Success` and `TestPing_Failure` PASS, and all pre-existing openai tests still PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/openai/client.go internal/providers/openai/client_test.go
git commit -m "feat(providers/openai): add Ping for health probes"
```

---

## Task 4: Add Ping to Anthropic client

**Files:**
- Modify: `internal/providers/anthropic/client.go`
- Modify: `internal/providers/anthropic/client_test.go`

Anthropic has no dedicated lightweight ping endpoint, and `messages.create` costs tokens. Strategy: a `Ping` that sends a 1-token message *only* when explicitly invoked, plus a `Configured() bool` accessor for the cheap path. `index_status` uses `Configured()`; an opt-in command can use `Ping`.

- [ ] **Step 1: Write the failing test**

Append to `internal/providers/anthropic/client_test.go`:

```go
func TestConfigured(t *testing.T) {
	if (&Client{apiKey: ""}).Configured() {
		t.Fatal("empty apiKey should be Configured()=false")
	}
	if !(&Client{apiKey: "sk-ant-test"}).Configured() {
		t.Fatal("non-empty apiKey should be Configured()=true")
	}
}
```

Use whatever the real field name is on `Client` — `client_test.go` already references it.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/providers/anthropic/ -run TestConfigured -v
```

Expected: compilation error — `Configured undefined`.

- [ ] **Step 3: Add the method**

In `internal/providers/anthropic/client.go`:

```go
// Configured reports whether the client has the credentials needed to
// make a request. Cheap; does not hit the network. Use this when you
// want a status signal without paying for tokens.
func (c *Client) Configured() bool {
	return c.apiKey != ""
}
```

Adjust the field name to match what's already on the struct (`c.apiKey`, `c.key`, etc.). Do **not** add `Ping` here — there's no free probe; if you later need a live check, gate it behind an explicit CLI command.

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/providers/anthropic/ -run TestConfigured -v
go test ./internal/providers/anthropic/ -v
```

Expected: `TestConfigured` PASS and the pre-existing anthropic tests still PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/anthropic/client.go internal/providers/anthropic/client_test.go
git commit -m "feat(providers/anthropic): add Configured() for cheap health signal"
```

---

## Task 5: Plumb summarizer prober into Server

**Files:**
- Modify: `internal/mcpserver/server.go`
- Modify: `cmd/projectlens-mcp/main.go`

- [ ] **Step 1: Define the prober interface and grow the Server**

Edit `internal/mcpserver/server.go`. Add the interface and field:

```go
// SummarizerProber reports whether the configured summarization
// provider is usable. Implementations should be cheap (no network if
// possible). The string is the provider name surfaced in
// ProviderHealth.Provider; bool true means "configured/reachable",
// false means "not configured", and error means "configured but the
// probe failed".
type SummarizerProber interface {
	ProbeSummarizer(ctx context.Context) (provider string, ok bool, err error)
}

// Server wraps the dependencies needed by the MCP tool handlers.
type Server struct {
	db         *storage.DB
	router     *retrieval.Router
	port       int
	repoPath   string
	summarizer SummarizerProber // optional; may be nil
}

// WithSummarizer attaches a SummarizerProber. Returns the same Server
// for chaining at wire-up time.
func (s *Server) WithSummarizer(p SummarizerProber) *Server {
	s.summarizer = p
	return s
}
```

Keep the existing `New` signature unchanged so test fixtures don't need editing.

- [ ] **Step 2: Wire the prober in cmd/projectlens-mcp/main.go**

Read `cmd/projectlens-mcp/main.go` first to see how summarizer/provider config is loaded today. Then, where the `Server` is constructed, add:

```go
prober := newSummarizerProber(cfg) // small helper in the same file
srv := mcpserver.New(db, router, port, repoPath).WithSummarizer(prober)
```

Define `newSummarizerProber` inline in `main.go` (it adapts the configured provider to the interface):

```go
func newSummarizerProber(cfg *config.Config) mcpserver.SummarizerProber {
	switch cfg.Summarization.Provider {
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		client := anthropic.NewClient(key)
		return summarizerProberFunc{name: "anthropic", configured: client.Configured}
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		client := openai.NewClient(key)
		return summarizerProberFunc{name: "openai", configured: func() bool { return key != "" }, ping: client.Ping}
	default:
		return nil
	}
}

type summarizerProberFunc struct {
	name       string
	configured func() bool
	ping       func(ctx context.Context) error // may be nil
}

func (f summarizerProberFunc) ProbeSummarizer(ctx context.Context) (string, bool, error) {
	if !f.configured() {
		return f.name, false, nil
	}
	if f.ping != nil {
		if err := f.ping(ctx); err != nil {
			return f.name, true, err
		}
	}
	return f.name, true, nil
}
```

Adjust import paths to match the repo's module path (`github.com/hman-pro/projectlens/...`).

- [ ] **Step 3: Build to verify wiring compiles**

```bash
make build-mcp
```

Expected: `./bin/projectlens-mcp` builds without errors.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpserver/server.go cmd/projectlens-mcp/main.go
git commit -m "feat(mcpserver): plumb SummarizerProber into Server"
```

---

## Task 6: Extend index_status with embedder + summarizer ProviderHealth

**Files:**
- Modify: `internal/mcpserver/handlers.go` (replace `embedderHealthy` and the `EmbedderHealthy *bool` field)
- Modify: `internal/mcpserver/handlers_integration_test.go` (new structured assertions)

- [ ] **Step 1: Write the failing test**

Add to `internal/mcpserver/handlers_integration_test.go`:

```go
func TestIntegration_IndexStatus_StructuredProviders(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleIndexStatus(ctx, makeRequest(map[string]interface{}{}))
	if err != nil {
		t.Fatalf("handleIndexStatus error: %v", err)
	}
	if result.StructuredContent == nil {
		t.Fatal("expected StructuredContent on index_status, got nil")
	}
	var payload indexStatusPayload
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured: %v", err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal into payload: %v", err)
	}
	if len(payload.Providers) == 0 {
		t.Fatal("expected at least one ProviderHealth entry")
	}
	for _, p := range payload.Providers {
		switch p.State {
		case "reachable", "configured", "error":
		default:
			t.Fatalf("ProviderHealth.State=%q not in tristate {reachable,configured,error}", p.State)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mcpserver/ -tags integration -run TestIntegration_IndexStatus_StructuredProviders -v
```

Expected: FAIL (or skip if no DB) — `payload.Providers undefined` or empty.

- [ ] **Step 3: Replace `EmbedderHealthy` with `Providers []ProviderHealth` and rewrite the probe helpers**

In `internal/mcpserver/handlers.go`, change the `indexStatusPayload` struct:

```go
type indexStatusPayload struct {
	Stages    map[string]StageFreshness `json:"stages"`
	Git       struct {
		Head  string `json:"head,omitempty"`
		Dirty bool   `json:"dirty"`
	} `json:"git"`
	Providers []ProviderHealth `json:"providers"`
}
```

Replace `embedderHealthy` with `probeProviders`:

```go
func (s *Server) probeProviders(ctx context.Context) []ProviderHealth {
	out := make([]ProviderHealth, 0, 2)

	// Embedder.
	if s.router != nil {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err := s.router.EmbedQuery(probeCtx, "ping")
		cancel()
		ph := ProviderHealth{Role: "embedder", Provider: s.embedderProviderName()}
		switch {
		case err == nil:
			ph.State = "reachable"
		case strings.Contains(err.Error(), "no embedder"):
			ph.State = "configured" // no embedder wired; treat as not-checked
			ph.Provider = ""
		default:
			ph.State = "error"
			ph.Error = err.Error()
		}
		out = append(out, ph)
	}

	// Summarizer.
	if s.summarizer != nil {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		provider, ok, err := s.summarizer.ProbeSummarizer(probeCtx)
		cancel()
		ph := ProviderHealth{Role: "summarizer", Provider: provider}
		switch {
		case err != nil:
			ph.State = "error"
			ph.Error = err.Error()
		case ok:
			ph.State = "reachable" // if probe ran without error, treat as reachable
		default:
			ph.State = "configured" // configured=false → not usable → leave as configured + Error empty
			ph.State = "error"
			ph.Error = "summarizer not configured"
		}
		out = append(out, ph)
	}

	return out
}

// embedderProviderName returns a short label for the configured
// embedder. The Server doesn't currently track this directly, so we
// rely on the router exposing it (see Step 4).
func (s *Server) embedderProviderName() string {
	if s.router == nil {
		return ""
	}
	return s.router.EmbedderProvider()
}
```

Then in `handleIndexStatus`, replace the `payload.EmbedderHealthy = ...` line with:

```go
payload.Providers = s.probeProviders(ctx)
```

And update the human-readable section to iterate over `payload.Providers` instead of the old `EmbedderHealthy` line:

```go
for _, p := range payload.Providers {
	fmt.Fprintf(&b, "%s (%s): %s", p.Role, p.Provider, p.State)
	if p.Error != "" {
		fmt.Fprintf(&b, " (%s)", p.Error)
	}
	b.WriteString("\n")
}
```

Replace the existing `mcp.NewToolResultText(b.String())` return with:

```go
return mcp.NewToolResultStructured(payload, b.String()), nil
```

- [ ] **Step 4: Add EmbedderProvider() to retrieval.Router**

In `internal/retrieval/router.go`, add a string field captured at construction (or read from the embedder if it exposes its own name):

```go
// Router struct grows one field:
//     providerName string
// NewRouter signature stays the same; the embedder's name is derived
// from its concrete type via a type assertion, defaulting to "" when
// unknown.

func (r *Router) EmbedderProvider() string {
	if r == nil || r.embedder == nil {
		return ""
	}
	type named interface{ ProviderName() string }
	if n, ok := r.embedder.(named); ok {
		return n.ProviderName()
	}
	return ""
}
```

Then add `ProviderName() string` to `ollama.Client` (returns `"ollama"`) and `openai.Client` (returns `"openai"`). Both are one-line methods.

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/retrieval/ -v
go test ./internal/mcpserver/ -tags integration -run TestIntegration_IndexStatus -v
```

Expected: PASS. If the integration test skips because the DB isn't available, run it once locally against the live DB before commit.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/handlers.go internal/mcpserver/handlers_integration_test.go internal/retrieval/router.go internal/providers/ollama/client.go internal/providers/openai/client.go
git commit -m "feat(mcpserver): report embedder + summarizer health in index_status"
```

---

## Task 7: Structured payload for find_symbol

**Files:**
- Modify: `internal/mcpserver/types.go` (add `FindSymbolPayload`)
- Modify: `internal/mcpserver/handlers.go` (`handleFindSymbol`)
- Modify: `internal/mcpserver/handlers_integration_test.go`

- [ ] **Step 1: Add the payload type to types.go**

```go
// SymbolHit is one structured result row used by find_symbol and other
// tools that return ranked symbols.
type SymbolHit struct {
	Kind        string        `json:"kind"`
	Name        string        `json:"name"`
	Signature   string        `json:"signature,omitempty"`
	PackageName string        `json:"package_name,omitempty"`
	Score       float64       `json:"score"`
	DocComment  string        `json:"doc_comment,omitempty"`
	Evidence    EvidenceSpan  `json:"evidence"`
}

// FindSymbolPayload is the structured response for find_symbol.
type FindSymbolPayload struct {
	Query   string      `json:"query"`
	Kind    string      `json:"kind,omitempty"`
	Hits    []SymbolHit `json:"hits"`
	Total   int         `json:"total"`
}
```

- [ ] **Step 2: Write the failing test**

In `handlers_integration_test.go`:

```go
func TestIntegration_FindSymbol_StructuredShape(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleFindSymbol(ctx, makeRequest(map[string]interface{}{
		"name": "SupplierFunding",
	}))
	if err != nil {
		t.Fatalf("handleFindSymbol error: %v", err)
	}
	if result.StructuredContent == nil {
		t.Fatal("expected StructuredContent on find_symbol")
	}
	var payload FindSymbolPayload
	raw, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Query != "SupplierFunding" {
		t.Errorf("Query=%q, want %q", payload.Query, "SupplierFunding")
	}
	if len(payload.Hits) == 0 {
		t.Skip("no SupplierFunding symbol in test corpus; nothing to assert")
	}
	h := payload.Hits[0]
	if h.Evidence.FilePath == "" || h.Evidence.LineStart == 0 {
		t.Errorf("Evidence missing: %+v", h.Evidence)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/mcpserver/ -tags integration -run TestIntegration_FindSymbol_StructuredShape -v
```

Expected: FAIL — `StructuredContent` is nil.

- [ ] **Step 4: Rewrite handleFindSymbol to emit both prose and struct**

In `internal/mcpserver/handlers.go`, replace the body of `handleFindSymbol`. The fallback prose stays the same; the new code builds `FindSymbolPayload` in parallel and returns `mcp.NewToolResultStructured(payload, b.String())`. Full handler:

```go
func (s *Server) handleFindSymbol(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("find_symbol: missing required argument 'name'"), nil
	}
	kind := req.GetString("kind", "")

	results, err := retrieval.LexicalSearch(ctx, s.db, name, 20)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("find_symbol: search failed", err), nil
	}
	if kind != "" {
		filtered := results[:0]
		for _, r := range results {
			if r.Kind == kind {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	payload := FindSymbolPayload{Query: name, Kind: kind, Total: len(results)}
	payload.Hits = make([]SymbolHit, 0, len(results))
	for _, r := range results {
		payload.Hits = append(payload.Hits, SymbolHit{
			Kind:        r.Kind,
			Name:        r.SymbolName,
			Signature:   formatSignature(r),
			PackageName: r.PackageName,
			Score:       r.Score,
			DocComment:  r.DocComment,
			Evidence:    EvidenceSpan{FilePath: r.FilePath, LineStart: r.LineStart, LineEnd: r.LineEnd},
		})
	}

	var b strings.Builder
	if len(results) == 0 {
		fmt.Fprintf(&b, "No symbols found matching %q.", name)
		return mcp.NewToolResultStructured(payload, b.String()), nil
	}
	fmt.Fprintf(&b, "Found %d symbol(s) matching %q:\n", len(results), name)
	for i, r := range results {
		b.WriteString("\n")
		fmt.Fprintf(&b, "%d. %s %s\n", i+1, r.Kind, formatSignature(r))
		fmt.Fprintf(&b, "   Package: %s\n", r.PackageName)
		fmt.Fprintf(&b, "   File: %s:%d-%d\n", r.FilePath, r.LineStart, r.LineEnd)
		fmt.Fprintf(&b, "   Score: %.2f\n", r.Score)
		if r.DocComment != "" {
			fmt.Fprintf(&b, "   Doc: %s\n", truncateDoc(r.DocComment))
		}
	}

	return mcp.NewToolResultStructured(payload, b.String()), nil
}
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/mcpserver/ -tags integration -run TestIntegration_FindSymbol -v
```

Expected: PASS for both the new structured test and the pre-existing `TestIntegration_FindSymbol_ExactMatch`.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/handlers.go internal/mcpserver/handlers_integration_test.go internal/mcpserver/types.go
git commit -m "feat(mcpserver): structured payload + evidence span for find_symbol"
```

---

## Task 8: Structured payload for search_go_context (with degraded block)

**Files:**
- Modify: `internal/mcpserver/types.go` (`SearchGoContextPayload`)
- Modify: `internal/mcpserver/handlers.go` (`handleSearchGoContext`)
- Modify: `internal/mcpserver/handlers_integration_test.go`

- [ ] **Step 1: Add the payload type**

```go
// SearchGoContextPayload is the structured response for search_go_context.
// Degraded is non-empty when a backend was unavailable; in that case
// Hits is still populated from whatever did succeed.
type SearchGoContextPayload struct {
	Query      string       `json:"query"`
	QueryType  string       `json:"query_type"`
	Hits       []SymbolHit  `json:"hits"`
	Total      int          `json:"total"`
	Degradation Degradation `json:"degradation"`
}
```

- [ ] **Step 2: Write the failing test**

```go
func TestIntegration_SearchGoContext_StructuredDegraded(t *testing.T) {
	srv := setupIntegrationServer(t)
	// Force degradation by clearing the embedder.
	srv.router = retrieval.NewRouter(srv.db, nil)
	ctx := context.Background()

	result, err := srv.handleSearchGoContext(ctx, makeRequest(map[string]interface{}{
		"query": "how does inventory reservation work",
	}))
	if err != nil {
		t.Fatalf("handleSearchGoContext error: %v", err)
	}
	if result.StructuredContent == nil {
		t.Fatal("expected StructuredContent")
	}
	var payload SearchGoContextPayload
	raw, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.Degradation.Degraded {
		t.Errorf("expected Degradation.Degraded=true when embedder missing, got %+v", payload.Degradation)
	}
	if payload.Degradation.Fallback == "" {
		t.Errorf("expected Degradation.Fallback to be set, got empty")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/mcpserver/ -tags integration -run TestIntegration_SearchGoContext_StructuredDegraded -v
```

Expected: FAIL — `StructuredContent` is nil.

- [ ] **Step 4: Rewrite the handler**

Replace `handleSearchGoContext` with:

```go
func (s *Server) handleSearchGoContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("search_go_context: missing required argument 'query'"), nil
	}
	pkgFilter := req.GetString("package_filter", "")
	topK := req.GetInt("top_k", 10)
	if topK <= 0 {
		topK = 10
	}

	qr, err := s.router.Query(ctx, query, topK)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("search_go_context: query failed", err), nil
	}

	results := qr.Results
	if pkgFilter != "" {
		filtered := results[:0]
		for _, r := range results {
			if r.PackageName == pkgFilter {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	payload := SearchGoContextPayload{
		Query:     query,
		QueryType: string(qr.QueryType),
		Total:     len(results),
	}
	payload.Hits = make([]SymbolHit, 0, len(results))
	for _, r := range results {
		payload.Hits = append(payload.Hits, SymbolHit{
			Kind:        r.Kind,
			Name:        r.SymbolName,
			Signature:   formatSignature(r),
			PackageName: r.PackageName,
			Score:       r.Score,
			DocComment:  r.DocComment,
			Evidence:    EvidenceSpan{FilePath: r.FilePath, LineStart: r.LineStart, LineEnd: r.LineEnd},
		})
	}
	if len(qr.Warnings) > 0 {
		payload.Degradation = Degradation{
			Degraded: true,
			Reason:   strings.Join(qr.Warnings, "; "),
			Fallback: "lexical-only",
		}
	}

	var b strings.Builder
	for _, w := range qr.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", w)
	}
	if len(results) == 0 {
		fmt.Fprintf(&b, "No results found for query %q.\n", query)
		return mcp.NewToolResultStructured(payload, b.String()), nil
	}
	fmt.Fprintf(&b, "Found %d result(s) for %q (query type: %s):\n", len(results), query, qr.QueryType)
	for i, r := range results {
		b.WriteString("\n")
		fmt.Fprintf(&b, "%d. [%s] %s %s (score: %.2f, source: %s)\n", i+1, r.SourceType, r.Kind, formatSignature(r), r.Score, r.Source)
		fmt.Fprintf(&b, "   Package: %s\n", r.PackageName)
		fmt.Fprintf(&b, "   File: %s:%d-%d\n", r.FilePath, r.LineStart, r.LineEnd)
		if r.DocComment != "" {
			fmt.Fprintf(&b, "   Doc: %s\n", truncateDoc(r.DocComment))
		}
	}

	seen := map[string]struct{}{}
	var pkgs []string
	for i, r := range results {
		if i >= 5 {
			break
		}
		if r.PackageName == "" {
			continue
		}
		if _, ok := seen[r.PackageName]; ok {
			continue
		}
		seen[r.PackageName] = struct{}{}
		pkgs = append(pkgs, r.PackageName)
	}
	for _, p := range pkgs {
		if extra := s.surfaceKnowledgeForPackage(ctx, p); extra != "" {
			b.WriteString(extra)
		}
	}

	return mcp.NewToolResultStructured(payload, b.String()), nil
}
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/mcpserver/ -tags integration -run TestIntegration_SearchGoContext -v
```

Expected: both the new structured-degraded test and pre-existing search tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/handlers.go internal/mcpserver/handlers_integration_test.go internal/mcpserver/types.go
git commit -m "feat(mcpserver): structured payload + degraded block for search_go_context"
```

---

## Task 9: Structured payload for get_symbol_context

**Files:**
- Modify: `internal/mcpserver/types.go` (`SymbolContextPayload`)
- Modify: `internal/mcpserver/handlers.go` (`handleGetSymbolContext`)
- Modify: `internal/mcpserver/handlers_integration_test.go`

- [ ] **Step 1: Add the payload type**

```go
// SymbolContextPayload is the structured response for get_symbol_context.
type SymbolContextPayload struct {
	Target       SymbolHit    `json:"target"`
	ScipSymbol   string       `json:"scip_symbol,omitempty"`
	Callers      []SymbolHit  `json:"callers,omitempty"`
	Callees      []SymbolHit  `json:"callees,omitempty"`
	Implementors []SymbolHit  `json:"implementors,omitempty"`
}
```

- [ ] **Step 2: Write the failing test**

```go
func TestIntegration_GetSymbolContext_StructuredShape(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleGetSymbolContext(ctx, makeRequest(map[string]interface{}{
		"name": "SupplierFunding",
	}))
	if err != nil {
		t.Fatalf("handleGetSymbolContext error: %v", err)
	}
	if result.StructuredContent == nil {
		t.Fatal("expected StructuredContent")
	}
	var payload SymbolContextPayload
	raw, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Target.Evidence.FilePath == "" {
		t.Errorf("Target.Evidence missing: %+v", payload.Target)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/mcpserver/ -tags integration -run TestIntegration_GetSymbolContext_StructuredShape -v
```

Expected: FAIL — `StructuredContent` is nil.

- [ ] **Step 4: Rewrite the handler**

Edit `handleGetSymbolContext` to build a `SymbolContextPayload` alongside the existing prose `b`. Helper:

```go
func toSymbolHit(r retrieval.SearchResult) SymbolHit {
	return SymbolHit{
		Kind:        r.Kind,
		Name:        r.SymbolName,
		Signature:   formatSignature(r),
		PackageName: r.PackageName,
		Score:       r.Score,
		DocComment:  r.DocComment,
		Evidence:    EvidenceSpan{FilePath: r.FilePath, LineStart: r.LineStart, LineEnd: r.LineEnd},
	}
}
```

Put `toSymbolHit` in `handlers.go` (just above `handleGetSymbolContext`) so other handlers can reuse it. Then inside `handleGetSymbolContext`:

```go
payload := SymbolContextPayload{Target: toSymbolHit(target)}

symRecords, _ := s.db.GetSymbolByName(ctx, target.SymbolName)
for _, sr := range symRecords {
	if sr.ID == target.SymbolID && sr.ScipSymbol != nil {
		payload.ScipSymbol = *sr.ScipSymbol
		break
	}
}

callers, err := retrieval.GetCallers(ctx, s.db, target.SymbolID, 2)
if err == nil {
	for _, c := range callers {
		payload.Callers = append(payload.Callers, toSymbolHit(c))
	}
}

callees, err := retrieval.GetCallees(ctx, s.db, target.SymbolID, 2)
if err == nil {
	for _, c := range callees {
		payload.Callees = append(payload.Callees, toSymbolHit(c))
	}
}

if target.Kind == "interface" {
	implementors, err := retrieval.GetImplementors(ctx, s.db, target.SymbolID)
	if err == nil {
		for _, impl := range implementors {
			payload.Implementors = append(payload.Implementors, toSymbolHit(impl))
		}
	}
}
```

Keep the prose-building block intact, and end with:

```go
return mcp.NewToolResultStructured(payload, b.String()), nil
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/mcpserver/ -tags integration -run TestIntegration_GetSymbolContext -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/handlers.go internal/mcpserver/handlers_integration_test.go internal/mcpserver/types.go
git commit -m "feat(mcpserver): structured payload for get_symbol_context"
```

---

## Task 10: Structured payload for get_package_summary

**Files:**
- Modify: `internal/mcpserver/types.go` (`PackageSummaryPayload`)
- Modify: `internal/mcpserver/handlers.go` (`handleGetPackageSummary`)
- Modify: `internal/mcpserver/handlers_integration_test.go`

- [ ] **Step 1: Add the payload type**

```go
// PackageSummaryPayload is the structured response for get_package_summary.
// GeneratedAt and Stale are computed from the underlying summaries row;
// agents can use Stale=true to decide whether to ask the user to re-run
// the summarize stage before quoting the summary.
type PackageSummaryPayload struct {
	PackageName     string   `json:"package_name"`
	Summary         string   `json:"summary"`
	GeneratedAt     string   `json:"generated_at,omitempty"`
	AgeMinutes      float64  `json:"age_minutes,omitempty"`
	Stale           bool     `json:"stale"`
	ExportedSymbols []string `json:"exported_symbols,omitempty"`
}
```

- [ ] **Step 2: Write the failing test**

```go
func TestIntegration_GetPackageSummary_StructuredShape(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleGetPackageSummary(ctx, makeRequest(map[string]interface{}{
		"package_name": "supplierfunding",
	}))
	if err != nil {
		t.Fatalf("handleGetPackageSummary error: %v", err)
	}
	if result.StructuredContent == nil {
		t.Fatal("expected StructuredContent")
	}
	var payload PackageSummaryPayload
	raw, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.PackageName == "" {
		t.Error("PackageName empty")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/mcpserver/ -tags integration -run TestIntegration_GetPackageSummary_StructuredShape -v
```

Expected: FAIL — `StructuredContent` is nil.

- [ ] **Step 4: Read existing handler and rewrite to emit structured output**

Open `handlers.go` and locate `handleGetPackageSummary` (around line 222). Build the payload from the same DB rows the prose currently uses. Where the handler calls into `storage` for the summary row, capture `CreatedAt` (or `UpdatedAt`) into `payload.GeneratedAt = t.Format(time.RFC3339)` and compute `AgeMinutes = time.Since(t).Minutes()`. Treat `AgeMinutes > 7*24*60` as `Stale = true`. Then return `mcp.NewToolResultStructured(payload, b.String())`.

If `storage.GetPackageSummary` (or its current name) doesn't return a timestamp, leave `GeneratedAt`/`AgeMinutes` zero and `Stale` false for this task — adding a column is a separate change.

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/mcpserver/ -tags integration -run TestIntegration_GetPackageSummary -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/handlers.go internal/mcpserver/handlers_integration_test.go internal/mcpserver/types.go
git commit -m "feat(mcpserver): structured payload + freshness for get_package_summary"
```

---

## Task 11: Structured payload for get_table_context

**Files:**
- Modify: `internal/mcpserver/types.go` (`TableContextPayload`)
- Modify: `internal/mcpserver/handlers.go` (`handleGetTableContext`)
- Modify: `internal/mcpserver/handlers_integration_test.go`

- [ ] **Step 1: Add the payload type**

```go
type TableColumn struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

type TableContextPayload struct {
	TableName  string        `json:"table_name"`
	Columns    []TableColumn `json:"columns,omitempty"`
	Migrations []string      `json:"migrations,omitempty"`
	ReadBy     []SymbolHit   `json:"read_by,omitempty"`
	WrittenBy  []SymbolHit   `json:"written_by,omitempty"`
}
```

- [ ] **Step 2: Write the failing test**

```go
func TestIntegration_GetTableContext_StructuredShape(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleGetTableContext(ctx, makeRequest(map[string]interface{}{
		"table_name": "sets",
	}))
	if err != nil {
		t.Fatalf("handleGetTableContext error: %v", err)
	}
	if result.StructuredContent == nil {
		t.Fatal("expected StructuredContent")
	}
	var payload TableContextPayload
	raw, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.TableName == "" {
		t.Error("TableName empty")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/mcpserver/ -tags integration -run TestIntegration_GetTableContext_StructuredShape -v
```

Expected: FAIL.

- [ ] **Step 4: Rewrite handler**

In `handleGetTableContext`, build a `TableContextPayload` mirroring the prose: copy column rows into `payload.Columns`, migration filenames into `payload.Migrations`, and the `readEdges` / `writeEdges` iteration into `payload.ReadBy` / `payload.WrittenBy` (each edge becomes a `SymbolHit{Kind: e.SymbolKind, Name: e.SymbolName, Evidence: EvidenceSpan{FilePath: e.FilePath, LineStart: e.LineStart}}`).

Return `mcp.NewToolResultStructured(payload, b.String())`.

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/mcpserver/ -tags integration -run TestIntegration_GetTableContext -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/handlers.go internal/mcpserver/handlers_integration_test.go internal/mcpserver/types.go
git commit -m "feat(mcpserver): structured payload for get_table_context"
```

---

## Task 12: Structured payload for get_change_history and get_coupling

**Files:**
- Modify: `internal/mcpserver/types.go`
- Modify: `internal/mcpserver/handlers.go` (`handleGetChangeHistory`, `handleGetCoupling`)
- Modify: `internal/mcpserver/handlers_integration_test.go`

- [ ] **Step 1: Add the payload types**

```go
type ChangeRecord struct {
	Hash       string `json:"hash"`
	ShortHash  string `json:"short_hash"`
	Author     string `json:"author"`
	Date       string `json:"date"`
	ChangeType string `json:"change_type,omitempty"`
	Subject    string `json:"subject,omitempty"`
}

type ChangeHistoryPayload struct {
	Target     string         `json:"target"`           // file path or symbol name
	TargetKind string         `json:"target_kind"`      // "file" | "symbol"
	Evidence   EvidenceSpan   `json:"evidence,omitempty"`
	Records    []ChangeRecord `json:"records"`
}

type CouplingEntry struct {
	FilePath        string  `json:"file_path"`
	CoChangeCount   int     `json:"co_change_count"`
	CoChangeScore   float64 `json:"co_change_score,omitempty"`
}

type CouplingPayload struct {
	Target   string          `json:"target"`
	Window   int             `json:"window_commits,omitempty"`
	Coupled  []CouplingEntry `json:"coupled"`
}
```

- [ ] **Step 2: Write the failing tests**

```go
func TestIntegration_GetChangeHistory_Structured(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleGetChangeHistory(ctx, makeRequest(map[string]interface{}{
		"name":  "pkg/datamodel/tables/supplier_funding.go",
		"limit": 5,
	}))
	if err != nil {
		t.Fatalf("handleGetChangeHistory error: %v", err)
	}
	if result.StructuredContent == nil {
		t.Fatal("expected StructuredContent")
	}
	var payload ChangeHistoryPayload
	raw, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Target == "" {
		t.Error("Target empty")
	}
}

func TestIntegration_GetCoupling_Structured(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleGetCoupling(ctx, makeRequest(map[string]interface{}{
		"file_path": "pkg/datamodel/tables/supplier_funding.go",
	}))
	if err != nil {
		t.Fatalf("handleGetCoupling error: %v", err)
	}
	if result.StructuredContent == nil {
		t.Fatal("expected StructuredContent")
	}
	var payload CouplingPayload
	raw, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Target == "" {
		t.Error("Target empty")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./internal/mcpserver/ -tags integration -run 'TestIntegration_GetChangeHistory_Structured|TestIntegration_GetCoupling_Structured' -v
```

Expected: FAIL — `StructuredContent` is nil.

- [ ] **Step 4: Rewrite both handlers**

In `handleGetChangeHistory`, build `ChangeHistoryPayload` from the same DB rows / git results currently used to build the prose `b`. Each commit row maps to one `ChangeRecord`. When the path resolves to a symbol, set `TargetKind = "symbol"` and populate `Evidence` from the matched `target` (`FilePath`, `LineStart`, `LineEnd`); when it resolves to a file, set `TargetKind = "file"` and leave `Evidence` zero.

In `handleGetCoupling`, build `CouplingPayload.Coupled` from the same edges iteration that feeds the prose.

Return `mcp.NewToolResultStructured(payload, b.String())` from both.

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/mcpserver/ -tags integration -run 'TestIntegration_GetChangeHistory|TestIntegration_GetCoupling' -v
```

Expected: PASS for the new tests and the pre-existing ones.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/handlers.go internal/mcpserver/handlers_integration_test.go internal/mcpserver/types.go
git commit -m "feat(mcpserver): structured payloads for get_change_history and get_coupling"
```

---

## Task 13: Structured payloads for save_knowledge and search_knowledge

**Files:**
- Modify: `internal/mcpserver/types.go`
- Modify: `internal/mcpserver/knowledge_handlers.go`
- Modify: `internal/mcpserver/handlers_integration_test.go`

- [ ] **Step 1: Add the payload types**

```go
type KnowledgeEntry struct {
	ID        int64        `json:"id"`
	Category  string       `json:"category"`
	Title     string       `json:"title"`
	Body      string       `json:"body"`
	Anchor    string       `json:"anchor,omitempty"`
	CreatedAt string       `json:"created_at,omitempty"`
	Evidence  EvidenceSpan `json:"evidence,omitempty"`
}

type SaveKnowledgePayload struct {
	Saved KnowledgeEntry `json:"saved"`
}

type SearchKnowledgePayload struct {
	Query   string           `json:"query,omitempty"`
	Total   int              `json:"total"`
	Entries []KnowledgeEntry `json:"entries"`
}
```

- [ ] **Step 2: Write the failing test**

```go
func TestIntegration_SearchKnowledge_Structured(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleSearchKnowledge(ctx, makeRequest(map[string]interface{}{
		"query": "test",
	}))
	if err != nil {
		t.Fatalf("handleSearchKnowledge error: %v", err)
	}
	if result.StructuredContent == nil {
		t.Fatal("expected StructuredContent")
	}
	var payload SearchKnowledgePayload
	raw, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Entries == nil {
		t.Error("Entries slice nil; expected at least empty slice")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/mcpserver/ -tags integration -run TestIntegration_SearchKnowledge_Structured -v
```

Expected: FAIL.

- [ ] **Step 4: Rewrite both knowledge handlers**

In `internal/mcpserver/knowledge_handlers.go`, mirror the prose builder into `SaveKnowledgePayload` / `SearchKnowledgePayload` and return `mcp.NewToolResultStructured(payload, b.String())` from each.

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/mcpserver/ -tags integration -run 'TestIntegration_SaveKnowledge|TestIntegration_SearchKnowledge' -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/knowledge_handlers.go internal/mcpserver/handlers_integration_test.go internal/mcpserver/types.go
git commit -m "feat(mcpserver): structured payloads for save_knowledge and search_knowledge"
```

---

## Task 14: Document the structured fields in the use-projectlens skill

**Files:**
- Modify: `claude/skills/use-projectlens/SKILL.md`
- Modify: `CLAUDE.md` (one-line reference under the MCP tools table)

- [ ] **Step 1: Add a "Structured fields" appendix to the skill**

In `claude/skills/use-projectlens/SKILL.md`, append a section listing the new fields agents should rely on. Concrete content:

```markdown
## Structured fields

Every tool returns both a human-readable text block and a typed
`structuredContent` payload (MCP `CallToolResult.structuredContent`).
Prefer the structured payload — text is for humans, fields are for you.

| Tool | Payload type | Notable fields |
|------|--------------|----------------|
| `find_symbol` | `FindSymbolPayload` | `hits[].evidence{file_path,line_start,line_end}` |
| `search_go_context` | `SearchGoContextPayload` | `degradation{degraded,reason,fallback}`, `hits[].evidence` |
| `get_symbol_context` | `SymbolContextPayload` | `target.evidence`, `callers[]`, `callees[]`, `implementors[]` |
| `get_package_summary` | `PackageSummaryPayload` | `generated_at`, `age_minutes`, `stale` |
| `get_table_context` | `TableContextPayload` | `columns[]`, `read_by[]`, `written_by[]` |
| `get_change_history` | `ChangeHistoryPayload` | `records[]`, `evidence` (when target is a symbol) |
| `get_coupling` | `CouplingPayload` | `coupled[].co_change_count` |
| `index_status` | `indexStatusPayload` | `providers[].state` ∈ `{reachable, configured, error}` |
| `save_knowledge` / `search_knowledge` | `SaveKnowledgePayload` / `SearchKnowledgePayload` | `entries[].evidence` |

**`degradation.degraded == true` rule:** the result is best-effort.
Either ask the user before acting on it, or re-issue the call after
the missing backend is back up.

**Provenance via `evidence`:** before quoting or editing based on a
hit, open the cited `file_path:line_start-line_end` to confirm — the
index can be stale relative to the working tree.
```

- [ ] **Step 2: Cross-reference from CLAUDE.md**

In `CLAUDE.md`, just under the MCP tools table, add one line:

```markdown
> **Structured responses.** Each tool returns a `structuredContent` payload in addition to text. See [`claude/skills/use-projectlens/SKILL.md`](claude/skills/use-projectlens/SKILL.md#structured-fields) for the field reference.
```

- [ ] **Step 3: Validate the docs**

```bash
grep -n "Structured fields" claude/skills/use-projectlens/SKILL.md
grep -n "Structured responses" CLAUDE.md
```

Expected: each grep returns one line.

- [ ] **Step 4: Commit**

```bash
git add claude/skills/use-projectlens/SKILL.md CLAUDE.md
git commit -m "docs: document structured MCP response fields for agents"
```

---

## Task 15: Final verification

- [ ] **Step 1: Run the full test suite**

```bash
make fmt vet test
```

Expected: zero diff from `gofmt`, vet clean, all tests PASS.

- [ ] **Step 2: Run the integration suite against a live DB**

```bash
go test ./internal/mcpserver/ -tags integration -v
```

Expected: every `TestIntegration_*_Structured*` PASSES.

- [ ] **Step 3: Smoke the MCP server end-to-end**

```bash
make build-mcp
./bin/projectlens-mcp &
MCP_PID=$!
sleep 1
curl -s -X POST http://localhost:8484/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"index_status","arguments":{}}}' | jq '.result.structuredContent.providers'
kill $MCP_PID
```

Expected: a JSON array containing at least one `ProviderHealth` entry with a `state` field set to one of `reachable`, `configured`, or `error`.

- [ ] **Step 4: No commit — verification only.**

---

## Self-review notes

- Coverage: each of the eight existing tools (`find_symbol`, `search_go_context`, `get_symbol_context`, `get_package_summary`, `get_table_context`, `get_change_history`, `get_coupling`, `index_status`) plus the two knowledge tools (`save_knowledge`, `search_knowledge`) gets one structured-output task. Provider health lands in Task 6 alongside `index_status`. Skill docs land in Task 14.
- Backwards-compatibility: every handler still returns the same text block via the `fallbackText` argument of `NewToolResultStructured`. Older clients that only read `result.Content[0].Text` keep working.
- Type consistency: `SymbolHit` is defined once in Task 7 and reused across Tasks 8, 9, 11, 12. Field names `evidence`, `package_name`, `signature` match across payloads.
- Out of scope (parked in `docs/plans/backlog.md`): the end-to-end smoke test, LightRAG integration, and any new index-stage work.
