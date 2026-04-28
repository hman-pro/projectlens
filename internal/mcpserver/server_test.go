package mcpserver

import (
	"sort"
	"testing"
)

func TestToolDefsCount(t *testing.T) {
	defs := toolDefs()
	if got := len(defs); got != 8 {
		t.Fatalf("expected 8 tool definitions, got %d", got)
	}
}

func TestToolDefsNames(t *testing.T) {
	expected := []string{
		"find_symbol",
		"get_change_history",
		"get_coupling",
		"get_package_summary",
		"get_symbol_context",
		"get_table_context",
		"index_status",
		"search_go_context",
	}

	defs := toolDefs()
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	sort.Strings(names)

	for i, want := range expected {
		if names[i] != want {
			t.Errorf("tool[%d]: expected name %q, got %q", i, want, names[i])
		}
	}
}

func TestToolDefsDescriptions(t *testing.T) {
	defs := toolDefs()
	for _, d := range defs {
		if d.Description == "" {
			t.Errorf("tool %q has empty description", d.Name)
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

func TestAllToolsReadOnly(t *testing.T) {
	defs := toolDefs()
	for _, d := range defs {
		if d.Annotations.ReadOnlyHint == nil || !*d.Annotations.ReadOnlyHint {
			t.Errorf("tool %q should be marked read-only", d.Name)
		}
		if d.Annotations.DestructiveHint == nil || *d.Annotations.DestructiveHint {
			t.Errorf("tool %q should not be marked destructive", d.Name)
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
