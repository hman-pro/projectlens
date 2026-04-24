package mcpserver

import "github.com/mark3labs/mcp-go/mcp"

func saveKnowledgeTool() mcp.Tool {
	return mcp.NewTool("save_knowledge",
		mcp.WithDescription(
			"Persist a piece of durable knowledge captured during a Claude session. "+
				"Use only when one of the 9 capture-knowledge signals fires "+
				"(see capture-knowledge skill). Anchors are optional but greatly improve "+
				"retrieval — prefer symbol > package > file > table > none."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("category",
			mcp.Required(),
			mcp.Enum("lesson", "best_practice", "convention",
				"domain_knowledge", "how_to", "decision"),
			mcp.Description("Knowledge category"),
		),
		mcp.WithString("title", mcp.Required(),
			mcp.Description("Short, searchable headline (≤120 chars)")),
		mcp.WithString("body", mcp.Required(),
			mcp.Description("Markdown body. Include the *why*, not just the *what*.")),
		mcp.WithArray("tags",
			mcp.Description("Free-form tags for filtering (lowercase, hyphenated)"),
			mcp.Items(map[string]any{"type": "string"}),
		),
		mcp.WithArray("anchors",
			mcp.Description("Optional anchors. Each: {type: symbol|file|package|table, ref: string}"),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type": map[string]any{"type": "string", "enum": []string{"symbol", "file", "package", "table"}},
					"ref":  map[string]any{"type": "string"},
				},
				"required": []string{"type", "ref"},
			}),
		),
		mcp.WithString("session_id",
			mcp.Description("Optional session identifier (caller-supplied)")),
	)
}

func searchKnowledgeTool() mcp.Tool {
	return mcp.NewTool("search_knowledge",
		mcp.WithDescription(
			"Search captured knowledge entries by natural-language query, "+
				"optional category, and optional anchor."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("query",
			mcp.Description("Natural-language query (optional if anchor given)")),
		mcp.WithString("category",
			mcp.Description("Optional category filter"),
			mcp.Enum("lesson", "best_practice", "convention",
				"domain_knowledge", "how_to", "decision"),
		),
		mcp.WithString("anchor_type",
			mcp.Description("Anchor type: symbol|file|package|table"),
			mcp.Enum("symbol", "file", "package", "table"),
		),
		mcp.WithString("anchor_ref",
			mcp.Description("Anchor reference (scip_symbol|path|package_name|table_name)")),
		mcp.WithNumber("limit",
			mcp.Description("Max results (default 10)")),
	)
}
