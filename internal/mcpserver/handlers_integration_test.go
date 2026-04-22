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
	"os"
	"strings"
	"testing"

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
