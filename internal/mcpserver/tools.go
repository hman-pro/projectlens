package mcpserver

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// toolRegistration pairs an MCP tool definition with its handler. The
// registry below is the single source of truth — both Start() and
// MCPServer() iterate it, so a new tool only needs to be added in one
// place.
type toolRegistration struct {
	tool    mcp.Tool
	handler server.ToolHandlerFunc
}

// toolRegistry returns every MCP tool the server exposes, paired with
// its handler. Order is preserved for deterministic registration.
func (s *Server) toolRegistry() []toolRegistration {
	return []toolRegistration{
		{findSymbolTool(), s.handleFindSymbol},
		{searchGoContextTool(), s.handleSearchGoContext},
		{getSymbolContextTool(), s.handleGetSymbolContext},
		{getPackageSummaryTool(), s.handleGetPackageSummary},
		{getTableContextTool(), s.handleGetTableContext},
		{indexStatusTool(), s.handleIndexStatus},
		{getChangeHistoryTool(), s.handleGetChangeHistory},
		{getCouplingTool(), s.handleGetCoupling},
		{saveKnowledgeTool(), s.handleSaveKnowledge},
		{searchKnowledgeTool(), s.handleSearchKnowledge},
	}
}

// findSymbolTool defines the find_symbol tool.
func findSymbolTool() mcp.Tool {
	return mcp.NewTool("find_symbol",
		mcp.WithDescription("Find a Go symbol by name. Returns matching symbols with file path, line range, signature, and package."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Symbol name to search for"),
		),
		mcp.WithString("kind",
			mcp.Description("Optional symbol kind filter"),
			mcp.Enum("func", "method", "struct", "interface", "const", "var"),
		),
	)
}

// searchGoContextTool defines the search_go_context tool.
func searchGoContextTool() mcp.Tool {
	return mcp.NewTool("search_go_context",
		mcp.WithDescription("Search for Go code by natural language query. Returns relevant symbols ranked by relevance."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Natural language search query"),
		),
		mcp.WithString("package_filter",
			mcp.Description("Optional package name to restrict results"),
		),
		mcp.WithNumber("top_k",
			mcp.Description("Maximum number of results to return (default 10)"),
		),
	)
}

// getSymbolContextTool defines the get_symbol_context tool.
func getSymbolContextTool() mcp.Tool {
	return mcp.NewTool("get_symbol_context",
		mcp.WithDescription("Get full context for a symbol including callers, callees, and interface implementations."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Symbol name to look up"),
		),
		mcp.WithString("file_path",
			mcp.Description("Optional file path to disambiguate symbols with the same name"),
		),
	)
}

// getPackageSummaryTool defines the get_package_summary tool.
func getPackageSummaryTool() mcp.Tool {
	return mcp.NewTool("get_package_summary",
		mcp.WithDescription("Get a summary of a Go package including its purpose, exported symbols, and dependencies."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("package_name",
			mcp.Required(),
			mcp.Description("Fully qualified package name"),
		),
	)
}

// getTableContextTool defines the get_table_context tool.
func getTableContextTool() mcp.Tool {
	return mcp.NewTool("get_table_context",
		mcp.WithDescription("Get database table schema, columns, and which Go code reads/writes it. Use with table names like 'rounding.sets' or just 'sets'."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("table_name",
			mcp.Required(),
			mcp.Description("Table name, optionally schema-qualified (e.g., 'rounding.sets' or 'sets')"),
		),
	)
}

// indexStatusTool defines the index_status tool.
func indexStatusTool() mcp.Tool {
	return mcp.NewTool("index_status",
		mcp.WithDescription("Check per-stage freshness, current git HEAD/dirty state, and embedder health for the ProjectLens index. Returns a human-readable summary plus a fenced ```json``` block with fields stages.<stage>.{status, age_minutes, completed_at, files_processed}, git.{head, dirty}, embedder_healthy."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

// getChangeHistoryTool defines the get_change_history tool.
func getChangeHistoryTool() mcp.Tool {
	return mcp.NewTool("get_change_history",
		mcp.WithDescription("Show recent git commits that changed a file or symbol. For symbols, shows only commits that modified the symbol's code."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("File path (e.g., 'core/funding/store.go') or symbol name (e.g., 'CalculateFunding')"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of commits to return (default: 10)"),
		),
	)
}

// getCouplingTool defines the get_coupling tool.
func getCouplingTool() mcp.Tool {
	return mcp.NewTool("get_coupling",
		mcp.WithDescription("Show files that frequently change together with the given file (co-change coupling analysis). Higher strength means stronger coupling."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("File path (e.g., 'core/funding/store.go')"),
		),
		mcp.WithNumber("min_strength",
			mcp.Description("Minimum coupling strength 0.0-1.0 (default: 0.3)"),
		),
	)
}
