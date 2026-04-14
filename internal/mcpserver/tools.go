package mcpserver

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// toolDefs returns the 5 MCP tool definitions for ProjectLens.
func toolDefs() []mcp.Tool {
	return []mcp.Tool{
		findSymbolTool(),
		searchGoContextTool(),
		getSymbolContextTool(),
		getPackageSummaryTool(),
		indexStatusTool(),
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

// indexStatusTool defines the index_status tool.
func indexStatusTool() mcp.Tool {
	return mcp.NewTool("index_status",
		mcp.WithDescription("Check the freshness and status of the ProjectLens index."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}
