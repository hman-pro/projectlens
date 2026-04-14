package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/mark3labs/mcp-go/mcp"
)

// handleFindSymbol handles the find_symbol tool call.
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

	// Filter by kind if specified.
	if kind != "" {
		filtered := results[:0]
		for _, r := range results {
			if r.Kind == kind {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	if len(results) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No symbols found matching %q.", name)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d symbol(s) matching %q:\n", len(results), name)
	for i, r := range results {
		b.WriteString("\n")
		fmt.Fprintf(&b, "%d. %s %s\n", i+1, r.Kind, formatSignature(r))
		fmt.Fprintf(&b, "   Package: %s\n", r.PackageName)
		fmt.Fprintf(&b, "   File: %s:%d-%d\n", r.FilePath, r.LineStart, r.LineEnd)
		if r.DocComment != "" {
			fmt.Fprintf(&b, "   Doc: %s\n", truncateDoc(r.DocComment))
		}
	}

	return mcp.NewToolResultText(b.String()), nil
}

// handleSearchGoContext handles the search_go_context tool call.
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

	if len(results) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No results found for query %q.", query)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d result(s) for %q (query type: %s):\n", len(results), query, qr.QueryType)
	for i, r := range results {
		b.WriteString("\n")
		fmt.Fprintf(&b, "%d. %s %s (score: %.2f, source: %s)\n", i+1, r.Kind, formatSignature(r), r.Score, r.Source)
		fmt.Fprintf(&b, "   Package: %s\n", r.PackageName)
		fmt.Fprintf(&b, "   File: %s:%d-%d\n", r.FilePath, r.LineStart, r.LineEnd)
		if r.DocComment != "" {
			fmt.Fprintf(&b, "   Doc: %s\n", truncateDoc(r.DocComment))
		}
	}

	return mcp.NewToolResultText(b.String()), nil
}

// handleGetSymbolContext handles the get_symbol_context tool call.
func (s *Server) handleGetSymbolContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("get_symbol_context: missing required argument 'name'"), nil
	}
	filePath := req.GetString("file_path", "")

	// Find symbol via lexical search.
	results, err := retrieval.LexicalSearch(ctx, s.db, name, 10)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_symbol_context: symbol lookup failed", err), nil
	}
	if len(results) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No symbol found matching %q.", name)), nil
	}

	// If file_path provided, filter to that file.
	target := results[0]
	if filePath != "" {
		for _, r := range results {
			if r.FilePath == filePath {
				target = r
				break
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Symbol: %s %s\n", target.Kind, formatSignature(target))
	fmt.Fprintf(&b, "Package: %s\n", target.PackageName)
	fmt.Fprintf(&b, "File: %s:%d-%d\n", target.FilePath, target.LineStart, target.LineEnd)
	if target.DocComment != "" {
		fmt.Fprintf(&b, "Doc: %s\n", truncateDoc(target.DocComment))
	}

	// Get callers.
	callers, err := retrieval.GetCallers(ctx, s.db, target.SymbolID, 2)
	if err == nil && len(callers) > 0 {
		b.WriteString("\nCallers:\n")
		for _, c := range callers {
			fmt.Fprintf(&b, "  - %s %s (%s:%d)\n", c.Kind, c.SymbolName, c.FilePath, c.LineStart)
		}
	}

	// Get callees.
	callees, err := retrieval.GetCallees(ctx, s.db, target.SymbolID, 2)
	if err == nil && len(callees) > 0 {
		b.WriteString("\nCallees:\n")
		for _, c := range callees {
			fmt.Fprintf(&b, "  - %s %s (%s:%d)\n", c.Kind, c.SymbolName, c.FilePath, c.LineStart)
		}
	}

	// Get implementors (only makes sense for interfaces).
	if target.Kind == "interface" {
		implementors, err := retrieval.GetImplementors(ctx, s.db, target.SymbolID)
		if err == nil && len(implementors) > 0 {
			b.WriteString("\nImplementors:\n")
			for _, impl := range implementors {
				fmt.Fprintf(&b, "  - %s %s (%s:%d)\n", impl.Kind, impl.SymbolName, impl.FilePath, impl.LineStart)
			}
		}
	}

	return mcp.NewToolResultText(b.String()), nil
}

// handleGetPackageSummary handles the get_package_summary tool call.
func (s *Server) handleGetPackageSummary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pkgName, err := req.RequireString("package_name")
	if err != nil {
		return mcp.NewToolResultError("get_package_summary: missing required argument 'package_name'"), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Package: %s\n", pkgName)

	// Get LLM summary.
	summary, err := s.db.GetSummaryByPackage(ctx, pkgName)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_package_summary: failed to get summary", err), nil
	}
	if summary != nil {
		fmt.Fprintf(&b, "\nSummary:\n%s\n", summary.SummaryText)
	} else {
		b.WriteString("\nNo LLM summary available for this package.\n")
	}

	// Get symbols in the package.
	symbols, err := s.db.GetSymbolsByPackage(ctx, pkgName)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_package_summary: failed to get symbols", err), nil
	}

	if len(symbols) > 0 {
		b.WriteString("\nExported symbols:\n")
		for _, sym := range symbols {
			if !isExported(sym.Name) {
				continue
			}
			sig := sym.Signature
			if sig == "" {
				sig = sym.Name
			}
			fmt.Fprintf(&b, "  - %s %s\n", sym.Kind, sig)
		}
	} else {
		b.WriteString("\nNo symbols found in this package.\n")
	}

	return mcp.NewToolResultText(b.String()), nil
}

// handleIndexStatus handles the index_status tool call.
func (s *Server) handleIndexStatus(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	run, err := s.db.GetLatestRun(ctx)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("index_status: failed to get latest run", err), nil
	}

	if run == nil {
		return mcp.NewToolResultText("No index runs found. Run 'projectlens bootstrap' to create the initial index."), nil
	}

	var b strings.Builder
	b.WriteString("ProjectLens Index Status\n")
	b.WriteString("=======================\n")
	fmt.Fprintf(&b, "Status:            %s\n", run.Status)
	fmt.Fprintf(&b, "Commit SHA:        %s\n", run.CommitSHA)
	fmt.Fprintf(&b, "Started at:        %s\n", run.StartedAt.Format(time.RFC3339))
	if run.CompletedAt != nil {
		fmt.Fprintf(&b, "Completed at:      %s\n", run.CompletedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "Files processed:   %d\n", run.FilesProcessed)
	fmt.Fprintf(&b, "Symbols extracted: %d\n", run.SymbolsExtracted)
	fmt.Fprintf(&b, "Edges created:     %d\n", run.EdgesCreated)

	// Staleness warning.
	if run.CompletedAt != nil {
		age := time.Since(*run.CompletedAt)
		if age > 24*time.Hour {
			fmt.Fprintf(&b, "\nWARNING: Index is %.0f hours old. Consider running 'projectlens reindex'.\n", age.Hours())
		}
	} else if run.Status == "running" {
		b.WriteString("\nIndex is currently being built.\n")
	}

	return mcp.NewToolResultText(b.String()), nil
}

// formatSignature formats a SearchResult's display name. If a signature exists
// it is used; otherwise the symbol name is returned.
func formatSignature(r retrieval.SearchResult) string {
	if r.Signature != "" {
		return r.Signature
	}
	return r.SymbolName
}

// truncateDoc returns the first sentence or up to 120 characters of a doc
// comment, whichever is shorter.
func truncateDoc(doc string) string {
	doc = strings.TrimSpace(doc)
	if idx := strings.Index(doc, "\n"); idx > 0 {
		doc = doc[:idx]
	}
	if len(doc) > 120 {
		doc = doc[:117] + "..."
	}
	return doc
}

// isExported returns true if the name starts with an uppercase letter.
func isExported(name string) bool {
	if name == "" {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}

