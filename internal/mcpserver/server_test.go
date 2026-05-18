package mcpserver

import (
	"sort"
	"testing"
)

// registryTools returns the canonical tool list via the live registry —
// the same list Start() and MCPServer() iterate.
func registryTools() []toolRegistration {
	return New(nil, nil, 8484, "").toolRegistry()
}

func TestRegistryCount(t *testing.T) {
	regs := registryTools()
	if got := len(regs); got != 10 {
		t.Fatalf("expected 10 tool registrations, got %d", got)
	}
}

func TestRegistryNames(t *testing.T) {
	expected := []string{
		"find_symbol",
		"get_change_history",
		"get_coupling",
		"get_package_summary",
		"get_symbol_context",
		"get_table_context",
		"index_status",
		"save_knowledge",
		"search_go_context",
		"search_knowledge",
	}

	regs := registryTools()
	names := make([]string, len(regs))
	for i, r := range regs {
		names[i] = r.tool.Name
	}
	sort.Strings(names)

	for i, want := range expected {
		if names[i] != want {
			t.Errorf("tool[%d]: expected name %q, got %q", i, want, names[i])
		}
	}
}

func TestRegistryDescriptions(t *testing.T) {
	for _, r := range registryTools() {
		if r.tool.Description == "" {
			t.Errorf("tool %q has empty description", r.tool.Name)
		}
		if r.handler == nil {
			t.Errorf("tool %q has nil handler", r.tool.Name)
		}
	}
}

func TestFindSymbolSchema(t *testing.T) {
	tool := findSymbolTool()

	if tool.Name != "find_symbol" {
		t.Fatalf("expected name find_symbol, got %s", tool.Name)
	}

	// Check required fields.
	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "name" {
		t.Errorf("expected required=[name], got %v", tool.InputSchema.Required)
	}

	// Check properties exist.
	if _, ok := tool.InputSchema.Properties["name"]; !ok {
		t.Error("missing 'name' property")
	}
	if _, ok := tool.InputSchema.Properties["kind"]; !ok {
		t.Error("missing 'kind' property")
	}
}

func TestSearchGoContextSchema(t *testing.T) {
	tool := searchGoContextTool()

	if tool.Name != "search_go_context" {
		t.Fatalf("expected name search_go_context, got %s", tool.Name)
	}

	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "query" {
		t.Errorf("expected required=[query], got %v", tool.InputSchema.Required)
	}

	for _, prop := range []string{"query", "package_filter", "top_k"} {
		if _, ok := tool.InputSchema.Properties[prop]; !ok {
			t.Errorf("missing %q property", prop)
		}
	}
}

func TestGetSymbolContextSchema(t *testing.T) {
	tool := getSymbolContextTool()

	if tool.Name != "get_symbol_context" {
		t.Fatalf("expected name get_symbol_context, got %s", tool.Name)
	}

	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "name" {
		t.Errorf("expected required=[name], got %v", tool.InputSchema.Required)
	}

	for _, prop := range []string{"name", "file_path"} {
		if _, ok := tool.InputSchema.Properties[prop]; !ok {
			t.Errorf("missing %q property", prop)
		}
	}
}

func TestGetPackageSummarySchema(t *testing.T) {
	tool := getPackageSummaryTool()

	if tool.Name != "get_package_summary" {
		t.Fatalf("expected name get_package_summary, got %s", tool.Name)
	}

	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "package_name" {
		t.Errorf("expected required=[package_name], got %v", tool.InputSchema.Required)
	}

	if _, ok := tool.InputSchema.Properties["package_name"]; !ok {
		t.Error("missing 'package_name' property")
	}
}

func TestGetTableContextSchema(t *testing.T) {
	tool := getTableContextTool()

	if tool.Name != "get_table_context" {
		t.Fatalf("expected name get_table_context, got %s", tool.Name)
	}

	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "table_name" {
		t.Errorf("expected required=[table_name], got %v", tool.InputSchema.Required)
	}

	if _, ok := tool.InputSchema.Properties["table_name"]; !ok {
		t.Error("missing 'table_name' property")
	}
}

func TestIndexStatusSchema(t *testing.T) {
	tool := indexStatusTool()

	if tool.Name != "index_status" {
		t.Fatalf("expected name index_status, got %s", tool.Name)
	}

	// index_status has no required fields and no properties.
	if len(tool.InputSchema.Required) != 0 {
		t.Errorf("expected no required fields, got %v", tool.InputSchema.Required)
	}
}

func TestServerRegistersAllTools(t *testing.T) {
	srv := New(nil, nil, 8484, "")
	mcpSrv := srv.MCPServer()

	tools := mcpSrv.ListTools()
	if len(tools) != 10 {
		t.Fatalf("expected 10 registered tools, got %d", len(tools))
	}

	expected := map[string]bool{
		"find_symbol":         true,
		"search_go_context":   true,
		"get_symbol_context":  true,
		"get_package_summary": true,
		"get_table_context":   true,
		"index_status":        true,
		"get_change_history":  true,
		"get_coupling":        true,
		"save_knowledge":      true,
		"search_knowledge":    true,
	}

	for name := range tools {
		if !expected[name] {
			t.Errorf("unexpected tool registered: %q", name)
		}
	}

	for name := range expected {
		if _, ok := tools[name]; !ok {
			t.Errorf("expected tool %q not registered", name)
		}
	}
}

// writingTools enumerates tools that mutate state — they must not carry
// ReadOnlyHint=true. Everything else in the registry is read-only.
var writingTools = map[string]bool{
	"save_knowledge": true,
}

func TestToolReadOnlyHints(t *testing.T) {
	for _, r := range registryTools() {
		ro := r.tool.Annotations.ReadOnlyHint
		if writingTools[r.tool.Name] {
			if ro != nil && *ro {
				t.Errorf("tool %q writes state but is marked read-only", r.tool.Name)
			}
			continue
		}
		if ro == nil || !*ro {
			t.Errorf("tool %q should be marked read-only", r.tool.Name)
		}
		if r.tool.Annotations.DestructiveHint == nil || *r.tool.Annotations.DestructiveHint {
			t.Errorf("tool %q should not be marked destructive", r.tool.Name)
		}
	}
}

func TestGetChangeHistorySchema(t *testing.T) {
	tool := getChangeHistoryTool()

	if tool.Name != "get_change_history" {
		t.Fatalf("expected name get_change_history, got %s", tool.Name)
	}

	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "name" {
		t.Errorf("expected required=[name], got %v", tool.InputSchema.Required)
	}

	for _, prop := range []string{"name", "limit"} {
		if _, ok := tool.InputSchema.Properties[prop]; !ok {
			t.Errorf("missing %q property", prop)
		}
	}
}

func TestSaveKnowledgeSchema(t *testing.T) {
	tool := saveKnowledgeTool()

	if tool.Name != "save_knowledge" {
		t.Fatalf("expected name save_knowledge, got %s", tool.Name)
	}

	// category, title, body are required; source is optional and vendor-neutral.
	requiredSet := map[string]bool{}
	for _, r := range tool.InputSchema.Required {
		requiredSet[r] = true
	}
	for _, want := range []string{"category", "title", "body"} {
		if !requiredSet[want] {
			t.Errorf("expected %q to be required, got required=%v", want, tool.InputSchema.Required)
		}
	}
	if requiredSet["source"] {
		t.Errorf("source must be optional, found in required=%v", tool.InputSchema.Required)
	}
	if _, ok := tool.InputSchema.Properties["source"]; !ok {
		t.Error("missing 'source' property — agents need a way to declare their identity")
	}
}

func TestGetCouplingSchema(t *testing.T) {
	tool := getCouplingTool()

	if tool.Name != "get_coupling" {
		t.Fatalf("expected name get_coupling, got %s", tool.Name)
	}

	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "name" {
		t.Errorf("expected required=[name], got %v", tool.InputSchema.Required)
	}

	for _, prop := range []string{"name", "min_strength"} {
		if _, ok := tool.InputSchema.Properties[prop]; !ok {
			t.Errorf("missing %q property", prop)
		}
	}
}

func TestTruncateDiff(t *testing.T) {
	t.Run("short diff unchanged", func(t *testing.T) {
		got := truncateDiff("short diff", 100)
		if got != "short diff" {
			t.Errorf("unexpected result: %q", got)
		}
	})

	t.Run("long diff truncated at newline", func(t *testing.T) {
		diff := "line1\nline2\nline3\nline4"
		got := truncateDiff(diff, 12)
		if got != "line1\nline2\n   ..." {
			t.Errorf("unexpected result: %q", got)
		}
	})
}

func TestFormatHelpers(t *testing.T) {
	t.Run("truncateDoc short", func(t *testing.T) {
		got := truncateDoc("Hello world.")
		if got != "Hello world." {
			t.Errorf("unexpected result: %q", got)
		}
	})

	t.Run("truncateDoc multiline", func(t *testing.T) {
		got := truncateDoc("First line.\nSecond line.")
		if got != "First line." {
			t.Errorf("expected only first line, got: %q", got)
		}
	})

	t.Run("isExported", func(t *testing.T) {
		if !isExported("Hello") {
			t.Error("expected Hello to be exported")
		}
		if isExported("hello") {
			t.Error("expected hello to not be exported")
		}
		if isExported("") {
			t.Error("expected empty string to not be exported")
		}
	})
}
