//go:build integration

// Integration tests for MCP handlers against a live database.
// Run with: go test ./internal/mcpserver/ -tags integration -v
//
// Prerequisites:
//   - Postgres running on localhost:5433 with projectlens database
//   - Ingest monorepo indexed (files, symbols, embeddings populated)
//   - OPENAI_API_KEY set (for semantic search query embedding)
//
// These tests verify that the MCP tools return meaningful results
// when called against real indexed data.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/providers/openai"
	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/mark3labs/mcp-go/mcp"
)

const testDB = "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"

// setupIntegrationServer creates a Server connected to the live database.
func setupIntegrationServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()

	db, err := storage.Connect(ctx, testDB)
	if err != nil {
		t.Skipf("cannot connect to test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Check if data exists.
	files, err := db.ListFiles(ctx)
	if err != nil || len(files) == 0 {
		t.Skip("no indexed data in database — run index first")
	}

	var embedder retrieval.QueryEmbedder
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey != "" {
		embedder = openai.NewClientWithDims(apiKey, 1024)
	}

	router := retrieval.NewRouter(db, embedder)
	return New(db, router, 0, "")
}

func makeRequest(args map[string]interface{}) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	}
}

// --- find_symbol ---

func TestIntegration_FindSymbol_ExactMatch(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleFindSymbol(ctx, makeRequest(map[string]interface{}{
		"name": "SupplierFunding",
	}))
	if err != nil {
		t.Fatalf("handleFindSymbol error: %v", err)
	}

	text := extractText(t, result)
	if !strings.Contains(text, "symbol") && !strings.Contains(text, "Symbol") &&
		!strings.Contains(text, "SupplierFunding") && !strings.Contains(text, "No symbols") {
		t.Errorf("unexpected result: %s", text)
	}
	t.Logf("find_symbol result:\n%s", truncateForLog(text))
}

func TestIntegration_FindSymbol_NotFound(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleFindSymbol(ctx, makeRequest(map[string]interface{}{
		"name": "ZzzNonExistentSymbol999",
	}))
	if err != nil {
		t.Fatalf("handleFindSymbol error: %v", err)
	}

	text := extractText(t, result)
	if !strings.Contains(text, "No symbols found") {
		t.Errorf("expected 'No symbols found', got: %s", text)
	}
}

func TestIntegration_FindSymbol_MissingArg(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleFindSymbol(ctx, makeRequest(map[string]interface{}{}))
	if err != nil {
		t.Fatalf("handleFindSymbol error: %v", err)
	}

	text := extractText(t, result)
	if !strings.Contains(text, "missing") {
		t.Errorf("expected error about missing arg, got: %s", text)
	}
}

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
	if payload.Hits == nil {
		t.Fatal("expected non-nil Hits slice")
	}
	if len(payload.Hits) == 0 {
		t.Skip("no SupplierFunding symbol in test corpus; nothing to assert")
	}
	h := payload.Hits[0]
	if h.Evidence.FilePath == "" || h.Evidence.LineStart == 0 {
		t.Errorf("Evidence missing: %+v", h.Evidence)
	}
}

// --- search_go_context ---

func TestIntegration_SearchGoContext_Lexical(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleSearchGoContext(ctx, makeRequest(map[string]interface{}{
		"query": "SupplierFunding",
	}))
	if err != nil {
		t.Fatalf("handleSearchGoContext error: %v", err)
	}

	text := extractText(t, result)
	if strings.Contains(text, "No results") {
		t.Error("expected results for SupplierFunding query")
	}
	t.Logf("search result:\n%s", truncateForLog(text))
}

func TestIntegration_SearchGoContext_Semantic(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set — skipping semantic search test")
	}

	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleSearchGoContext(ctx, makeRequest(map[string]interface{}{
		"query": "how does approval workflow work",
	}))
	if err != nil {
		t.Fatalf("handleSearchGoContext error: %v", err)
	}

	text := extractText(t, result)
	if strings.Contains(text, "No results") {
		t.Error("expected semantic results for approval workflow query")
	}
	t.Logf("semantic search result:\n%s", truncateForLog(text))
}

func TestIntegration_SearchGoContext_StructuredDegraded(t *testing.T) {
	srv := setupIntegrationServer(t)
	// Force semantic to be skipped by clearing the embedder.
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
		t.Error("expected Degradation.Fallback to be non-empty")
	}
	if !strings.Contains(payload.Degradation.Reason, "no embedder configured") {
		t.Errorf("Degradation.Reason missing expected text, got: %q", payload.Degradation.Reason)
	}
}

// --- get_symbol_context ---

func TestIntegration_GetSymbolContext(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	// First find a known symbol
	result, err := srv.handleGetSymbolContext(ctx, makeRequest(map[string]interface{}{
		"name": "Store",
	}))
	if err != nil {
		t.Fatalf("handleGetSymbolContext error: %v", err)
	}

	text := extractText(t, result)
	if !strings.Contains(text, "Symbol:") && !strings.Contains(text, "No symbol") {
		t.Errorf("unexpected result format: %s", truncateForLog(text))
	}
	t.Logf("symbol context:\n%s", truncateForLog(text))
}

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
		t.Fatal("expected StructuredContent on get_symbol_context (NotFound payload should still ship)")
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

// --- get_package_summary ---

func TestIntegration_GetPackageSummary(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleGetPackageSummary(ctx, makeRequest(map[string]interface{}{
		"package_name": "supplierfunding",
	}))
	if err != nil {
		t.Fatalf("handleGetPackageSummary error: %v", err)
	}

	text := extractText(t, result)
	if !strings.Contains(text, "Package:") {
		t.Errorf("expected 'Package:' in result, got: %s", truncateForLog(text))
	}
	t.Logf("package summary:\n%s", truncateForLog(text))
}

func TestIntegration_GetPackageSummary_NotFound(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleGetPackageSummary(ctx, makeRequest(map[string]interface{}{
		"package_name": "zzznonexistent",
	}))
	if err != nil {
		t.Fatalf("handleGetPackageSummary error: %v", err)
	}

	text := extractText(t, result)
	if !strings.Contains(text, "No") {
		t.Logf("result for nonexistent package: %s", truncateForLog(text))
	}
}

// --- index_status ---

func TestIntegration_IndexStatus(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleIndexStatus(ctx, makeRequest(map[string]interface{}{}))
	if err != nil {
		t.Fatalf("handleIndexStatus error: %v", err)
	}

	text := extractText(t, result)
	if !strings.Contains(text, "Status") {
		t.Errorf("expected status info, got: %s", truncateForLog(text))
	}
	if !strings.Contains(text, "Files") && !strings.Contains(text, "files") {
		t.Errorf("expected file count in status, got: %s", truncateForLog(text))
	}
	t.Logf("index status:\n%s", truncateForLog(text))
}

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
		case "reachable", "configured", "not_configured", "error":
		default:
			t.Fatalf("ProviderHealth.State=%q not in {reachable,configured,not_configured,error}", p.State)
		}
	}
}

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

// --- get_table_context ---

func TestIntegration_GetTableContext(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	// This may return "no table found" if index-datastore hasn't been run.
	result, err := srv.handleGetTableContext(ctx, makeRequest(map[string]interface{}{
		"table_name": "sets",
	}))
	if err != nil {
		t.Fatalf("handleGetTableContext error: %v", err)
	}

	text := extractText(t, result)
	t.Logf("table context:\n%s", truncateForLog(text))
}

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
		t.Fatal("expected StructuredContent on get_table_context (NotFound payload should still ship)")
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

// --- get_change_history ---

func TestIntegration_GetChangeHistory_ByFile(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleGetChangeHistory(ctx, makeRequest(map[string]interface{}{
		"name":  "pkg/datamodel/tables/supplier_funding.go",
		"limit": 5,
	}))
	if err != nil {
		t.Fatalf("handleGetChangeHistory error: %v", err)
	}

	text := extractText(t, result)
	t.Logf("change history:\n%s", truncateForLog(text))
}

func TestIntegration_GetChangeHistory_BySymbol(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleGetChangeHistory(ctx, makeRequest(map[string]interface{}{
		"name":  "SupplierFunding",
		"limit": 3,
	}))
	if err != nil {
		t.Fatalf("handleGetChangeHistory error: %v", err)
	}

	text := extractText(t, result)
	t.Logf("symbol change history:\n%s", truncateForLog(text))
}

// TestIntegration_GetChangeHistory_BySymbol_NoRepoPath verifies the fallback
// message when the server is configured without a repoPath and the
// symbol_history table has no rows for the target symbol. The message should
// direct the user to configure repoPath (since symbol-level git history needs
// the on-disk repo) and must NOT suggest running 'projectlens index-history'
// (which only populates file_history, not symbol_history).
func TestIntegration_GetChangeHistory_BySymbol_NoRepoPath(t *testing.T) {
	// setupIntegrationServer already constructs a Server with repoPath="".
	// symbol_history is never populated by any indexer stage, so this
	// exercises the fallback branch of handleGetChangeHistory.
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleGetChangeHistory(ctx, makeRequest(map[string]interface{}{
		"name":  "SupplierFunding",
		"limit": 3,
	}))
	if err != nil {
		t.Fatalf("handleGetChangeHistory error: %v", err)
	}

	text := extractText(t, result)
	if strings.Contains(text, "No file or symbol found") {
		t.Fatalf("symbol lookup drift: seeded symbol not found by LexicalSearch, so the fallback branch was never exercised; got: %s", text)
	}
	if !strings.Contains(text, "repoPath") {
		t.Errorf("expected fallback message to mention 'repoPath', got: %s", text)
	}
	if strings.Contains(text, "index-history") {
		t.Errorf("fallback message should not mention 'index-history' (misleading for symbol history), got: %s", text)
	}
	t.Logf("no-repoPath symbol fallback:\n%s", truncateForLog(text))
}

// --- get_coupling ---

func TestIntegration_GetCoupling(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleGetCoupling(ctx, makeRequest(map[string]interface{}{
		"name":         "pkg/datamodel/tables/supplier_funding.go",
		"min_strength": 0.2,
	}))
	if err != nil {
		t.Fatalf("handleGetCoupling error: %v", err)
	}

	text := extractText(t, result)
	t.Logf("coupling:\n%s", truncateForLog(text))
}

// --- save_knowledge (source field roundtrip) ---

// TestIntegration_SaveKnowledge_SourceExplicit asserts that an explicit
// `source: "codex"` argument is persisted verbatim — verifies the MCP tool
// is vendor-neutral after the agent-portability refactor.
func TestIntegration_SaveKnowledge_SourceExplicit(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	title := fmt.Sprintf("save-knowledge-source-codex-%d", time.Now().UnixNano())
	result, err := srv.handleSaveKnowledge(ctx, makeRequest(map[string]interface{}{
		"category": "lesson",
		"title":    title,
		"body":     "Vendor-neutral source param test.",
		"source":   "codex",
	}))
	if err != nil {
		t.Fatalf("handleSaveKnowledge error: %v", err)
	}
	text := extractText(t, result)

	var resp SaveKnowledgePayload
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", text, err)
	}
	t.Cleanup(func() {
		_, _ = srv.db.Pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE id = $1`, resp.ID)
		_, _ = srv.db.Pool.Exec(ctx,
			`DELETE FROM chunks WHERE source_uri = $1`,
			fmt.Sprintf("knowledge:%d", resp.ID))
	})

	var got string
	if err := srv.db.Pool.QueryRow(ctx,
		`SELECT source FROM knowledge_entries WHERE id = $1`, resp.ID).Scan(&got); err != nil {
		t.Fatalf("read back source: %v", err)
	}
	if got != "codex" {
		t.Errorf("source: got %q, want %q", got, "codex")
	}
}

// TestIntegration_SaveKnowledge_SourceDefault asserts that omitting `source`
// stores the vendor-neutral default "agent" (not the historical "claude").
func TestIntegration_SaveKnowledge_SourceDefault(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	title := fmt.Sprintf("save-knowledge-source-default-%d", time.Now().UnixNano())
	result, err := srv.handleSaveKnowledge(ctx, makeRequest(map[string]interface{}{
		"category": "lesson",
		"title":    title,
		"body":     "Default source test.",
	}))
	if err != nil {
		t.Fatalf("handleSaveKnowledge error: %v", err)
	}
	text := extractText(t, result)

	var resp SaveKnowledgePayload
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", text, err)
	}
	t.Cleanup(func() {
		_, _ = srv.db.Pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE id = $1`, resp.ID)
		_, _ = srv.db.Pool.Exec(ctx,
			`DELETE FROM chunks WHERE source_uri = $1`,
			fmt.Sprintf("knowledge:%d", resp.ID))
	})

	var got string
	if err := srv.db.Pool.QueryRow(ctx,
		`SELECT source FROM knowledge_entries WHERE id = $1`, resp.ID).Scan(&got); err != nil {
		t.Fatalf("read back source: %v", err)
	}
	if got != "agent" {
		t.Errorf("source default: got %q, want %q", got, "agent")
	}
}

// --- save_knowledge synchronous-embed ---

// stubEmbedder returns a deterministic 1024-dim vector for any input, so the
// test does not require Ollama or OPENAI_API_KEY. The first slot is 1.0, the
// rest 0; the query embedding for the same body therefore matches exactly
// under cosine similarity.
type stubEmbedder struct{}

func (stubEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, 1024)
		v[0] = 1.0
		out[i] = v
	}
	return out, nil
}

// setupKnowledgeServer wires an mcpserver.Server with a stub embedder so
// handleSaveKnowledge can embed synchronously without external services.
func setupKnowledgeServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Connect(ctx, testDB)
	if err != nil {
		t.Skipf("cannot connect to test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	router := retrieval.NewRouter(db, stubEmbedder{})
	return New(db, router, 0, "")
}

// TestIntegration_SaveKnowledge_EmbedsSynchronously asserts that
// handleSaveKnowledge writes an embeddings row when an embedder is configured
// (Embedded:true), and that handleSearchKnowledge then finds the entry via
// the vector path on the same body.
func TestIntegration_SaveKnowledge_EmbedsSynchronously(t *testing.T) {
	srv := setupKnowledgeServer(t)
	ctx := context.Background()

	marker := fmt.Sprintf("save-knowledge-test-%d", time.Now().UnixNano())
	body := "Synchronous embed verification body for " + marker

	t.Cleanup(func() {
		_, _ = srv.db.Pool.Exec(ctx,
			`DELETE FROM knowledge_entries WHERE title LIKE $1`, marker+"%")
	})

	// 1. save_knowledge with the stub embedder configured.
	saveRes, err := srv.handleSaveKnowledge(ctx, makeRequest(map[string]interface{}{
		"category": "lesson",
		"title":    marker + " title",
		"body":     body,
	}))
	if err != nil {
		t.Fatalf("handleSaveKnowledge: %v", err)
	}
	text := extractText(t, saveRes)

	var resp SaveKnowledgePayload
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal save response %q: %v", text, err)
	}
	if !resp.Embedded {
		t.Fatalf("expected Embedded:true, got response: %s", text)
	}
	if resp.ID == 0 {
		t.Fatalf("expected non-zero entry id, got response: %s", text)
	}

	// 2. embeddings row exists for the entry's paired chunk.
	var embCount int
	if err := srv.db.Pool.QueryRow(ctx, `
        SELECT count(*) FROM embeddings e
        JOIN chunks c ON c.id = e.chunk_id
        WHERE c.source_uri = $1`, fmt.Sprintf("knowledge:%d", resp.ID),
	).Scan(&embCount); err != nil {
		t.Fatalf("count embeddings: %v", err)
	}
	if embCount != 1 {
		t.Fatalf("expected 1 embedding row for entry %d, got %d", resp.ID, embCount)
	}

	// 3. search_knowledge on the same body hits the entry via the vector path.
	searchRes, err := srv.handleSearchKnowledge(ctx, makeRequest(map[string]interface{}{
		"query": body,
		"limit": 5,
	}))
	if err != nil {
		t.Fatalf("handleSearchKnowledge: %v", err)
	}
	got := extractText(t, searchRes)
	if !strings.Contains(got, fmt.Sprintf("#%d", resp.ID)) {
		t.Fatalf("expected search hit on entry #%d in:\n%s", resp.ID, got)
	}
	if !strings.Contains(got, "[vector]") && !strings.Contains(got, "[both]") {
		t.Fatalf("expected match_via=vector|both for entry #%d in:\n%s", resp.ID, got)
	}
}

// TestIntegration_SaveKnowledge_NoEmbedderReturnsFalse asserts that without an
// embedder configured, save_knowledge still persists the entry but reports
// Embedded:false rather than failing.
func TestIntegration_SaveKnowledge_NoEmbedderReturnsFalse(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Connect(ctx, testDB)
	if err != nil {
		t.Skipf("cannot connect to test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Router with nil embedder — EmbedQuery returns an error.
	srv := New(db, retrieval.NewRouter(db, nil), 0, "")

	marker := fmt.Sprintf("save-knowledge-noembed-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx,
			`DELETE FROM knowledge_entries WHERE title LIKE $1`, marker+"%")
	})

	saveRes, err := srv.handleSaveKnowledge(ctx, makeRequest(map[string]interface{}{
		"category": "lesson",
		"title":    marker + " title",
		"body":     "Body for no-embedder path",
	}))
	if err != nil {
		t.Fatalf("handleSaveKnowledge: %v", err)
	}
	var resp SaveKnowledgePayload
	if err := json.Unmarshal([]byte(extractText(t, saveRes)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID == 0 {
		t.Fatalf("expected entry to be persisted (id != 0), got: %+v", resp)
	}
	if resp.Embedded {
		t.Fatalf("expected Embedded:false when no embedder configured, got: %+v", resp)
	}
}

// --- structured shape: get_change_history + get_coupling ---

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
		t.Fatal("expected StructuredContent on get_change_history (NotFound payload should still ship)")
	}
	var payload ChangeHistoryPayload
	raw, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Target == "" {
		t.Error("Target empty")
	}
	if payload.TargetKind != "file" && payload.TargetKind != "symbol" {
		t.Errorf("unexpected TargetKind=%q", payload.TargetKind)
	}
}

func TestIntegration_GetCoupling_Structured(t *testing.T) {
	srv := setupIntegrationServer(t)
	ctx := context.Background()

	result, err := srv.handleGetCoupling(ctx, makeRequest(map[string]interface{}{
		"name": "pkg/datamodel/tables/supplier_funding.go",
	}))
	if err != nil {
		t.Fatalf("handleGetCoupling error: %v", err)
	}
	if result.StructuredContent == nil {
		t.Fatal("expected StructuredContent on get_coupling (NotFound payload should still ship)")
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

// --- save_knowledge dedup + anchor-reason behavior ---

// TestIntegration_SaveKnowledge_DedupShortCircuits asserts that a second
// save_knowledge with identical (source, title, body) inside the dedup
// window returns the existing entry id with Deduped:true and writes no new
// row.
func TestIntegration_SaveKnowledge_DedupShortCircuits(t *testing.T) {
	srv := setupKnowledgeServer(t)
	ctx := context.Background()

	marker := fmt.Sprintf("dedup-handler-%d", time.Now().UnixNano())
	args := map[string]interface{}{
		"category": "lesson",
		"title":    marker + " title",
		"body":     "Identical body for dedup verification.",
		"source":   "test-dedup-handler",
	}

	t.Cleanup(func() {
		_, _ = srv.db.Pool.Exec(ctx,
			`DELETE FROM knowledge_entries WHERE title = $1`, args["title"])
	})

	first, err := srv.handleSaveKnowledge(ctx, makeRequest(args))
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	var firstPayload SaveKnowledgePayload
	if err := json.Unmarshal([]byte(extractText(t, first)), &firstPayload); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if firstPayload.ID == 0 || firstPayload.Deduped {
		t.Fatalf("first call should insert fresh, got %+v", firstPayload)
	}

	second, err := srv.handleSaveKnowledge(ctx, makeRequest(args))
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	var secondPayload SaveKnowledgePayload
	if err := json.Unmarshal([]byte(extractText(t, second)), &secondPayload); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if !secondPayload.Deduped {
		t.Fatalf("expected Deduped:true on retry, got %+v", secondPayload)
	}
	if secondPayload.ID != firstPayload.ID {
		t.Fatalf("expected dedup to reuse id %d, got %d", firstPayload.ID, secondPayload.ID)
	}

	var rowCount int
	if err := srv.db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_entries WHERE title = $1`, args["title"]).Scan(&rowCount); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected dedup to keep row count at 1, got %d", rowCount)
	}
}

// TestIntegration_SaveKnowledge_AnchorReasonsSurfaced asserts that an
// unresolved short-name anchor surfaces as "type:ref (reason)" in
// AnchorsUnresolved so the agent sees why it failed instead of retrying
// blindly.
func TestIntegration_SaveKnowledge_AnchorReasonsSurfaced(t *testing.T) {
	srv := setupKnowledgeServer(t)
	ctx := context.Background()

	marker := fmt.Sprintf("anchor-reason-%d", time.Now().UnixNano())
	missingRef := marker + "NoSuchSymbol"
	args := map[string]interface{}{
		"category": "lesson",
		"title":    marker + " title",
		"body":     "Anchor reason surfacing test.",
		"source":   "test-anchor-reason",
		"anchors": []interface{}{
			map[string]interface{}{"type": "symbol", "ref": missingRef},
		},
	}

	t.Cleanup(func() {
		_, _ = srv.db.Pool.Exec(ctx,
			`DELETE FROM knowledge_entries WHERE title = $1`, args["title"])
	})

	res, err := srv.handleSaveKnowledge(ctx, makeRequest(args))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	var payload SaveKnowledgePayload
	if err := json.Unmarshal([]byte(extractText(t, res)), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.AnchorsUnresolved) != 1 {
		t.Fatalf("expected exactly 1 unresolved anchor, got %+v", payload.AnchorsUnresolved)
	}
	got := payload.AnchorsUnresolved[0]
	if !strings.Contains(got, "symbol:"+missingRef) {
		t.Errorf("expected ref in unresolved entry, got %q", got)
	}
	if !strings.Contains(got, "not found") {
		t.Errorf("expected reason 'not found' in unresolved entry, got %q", got)
	}
}

// --- helpers ---

func extractText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil {
		t.Fatal("nil result")
	}
	if len(result.Content) == 0 {
		t.Fatal("empty content")
	}
	textContent, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return textContent.Text
}

func truncateForLog(s string) string {
	if len(s) > 500 {
		return s[:500] + "\n   ... (truncated)"
	}
	return s
}
