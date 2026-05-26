package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/hman-pro/projectlens/internal/history"
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

	payload := FindSymbolPayload{Query: name, Kind: kind, Total: len(results)}
	payload.Hits = make([]SymbolHit, 0, len(results))
	for _, r := range results {
		payload.Hits = append(payload.Hits, toSymbolHit(r))
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

	payload := SearchGoContextPayload{
		Query:     query,
		QueryType: string(qr.QueryType),
		Total:     len(results),
	}
	payload.Hits = make([]SymbolHit, 0, len(results))
	for _, r := range results {
		payload.Hits = append(payload.Hits, toSymbolHit(r))
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

// handleGetSymbolContext handles the get_symbol_context tool call.
func (s *Server) handleGetSymbolContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("get_symbol_context: missing required argument 'name'"), nil
	}
	filePath := req.GetString("file_path", "")

	results, err := retrieval.LexicalSearch(ctx, s.db, name, 10)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_symbol_context: symbol lookup failed", err), nil
	}
	if len(results) == 0 {
		return mcp.NewToolResultStructured(
			SymbolContextPayload{Query: name, NotFound: true},
			fmt.Sprintf("No symbol found matching %q.", name),
		), nil
	}

	target := results[0]
	if filePath != "" {
		for _, r := range results {
			if r.FilePath == filePath {
				target = r
				break
			}
		}
	}

	payload := SymbolContextPayload{Query: name, Target: toSymbolHit(target)}

	var b strings.Builder
	fmt.Fprintf(&b, "Symbol: %s %s\n", target.Kind, formatSignature(target))
	fmt.Fprintf(&b, "Package: %s\n", target.PackageName)
	fmt.Fprintf(&b, "File: %s:%d-%d\n", target.FilePath, target.LineStart, target.LineEnd)
	if target.DocComment != "" {
		fmt.Fprintf(&b, "Doc: %s\n", truncateDoc(target.DocComment))
	}

	symRecords, _ := s.db.GetSymbolByName(ctx, target.SymbolName)
	for _, sr := range symRecords {
		if sr.ID == target.SymbolID {
			if sr.ScipSymbol != nil {
				payload.ScipSymbol = *sr.ScipSymbol
				fmt.Fprintf(&b, "SCIP: %s\n", *sr.ScipSymbol)
			}
			break
		}
	}

	if callers, err := retrieval.GetCallers(ctx, s.db, target.SymbolID, 2); err == nil && len(callers) > 0 {
		b.WriteString("\nCallers:\n")
		for _, c := range callers {
			payload.Callers = append(payload.Callers, toSymbolHit(c))
			fmt.Fprintf(&b, "  - %s %s (%s:%d)\n", c.Kind, c.SymbolName, c.FilePath, c.LineStart)
		}
	}

	if callees, err := retrieval.GetCallees(ctx, s.db, target.SymbolID, 2); err == nil && len(callees) > 0 {
		b.WriteString("\nCallees:\n")
		for _, c := range callees {
			payload.Callees = append(payload.Callees, toSymbolHit(c))
			fmt.Fprintf(&b, "  - %s %s (%s:%d)\n", c.Kind, c.SymbolName, c.FilePath, c.LineStart)
		}
	}

	if target.Kind == "interface" {
		if implementors, err := retrieval.GetImplementors(ctx, s.db, target.SymbolID); err == nil && len(implementors) > 0 {
			b.WriteString("\nImplementors:\n")
			for _, impl := range implementors {
				payload.Implementors = append(payload.Implementors, toSymbolHit(impl))
				fmt.Fprintf(&b, "  - %s %s (%s:%d)\n", impl.Kind, impl.SymbolName, impl.FilePath, impl.LineStart)
			}
		}
	}

	if target.SymbolID > 0 {
		if extra := s.surfaceKnowledgeForSymbol(ctx, target.SymbolID); extra != "" {
			b.WriteString(extra)
		}
	}

	if wc := worstClass(payload.Callers, payload.Callees, payload.Implementors); wc != "" {
		payload.Trust = &Trust{WorstClass: wc}
		fmt.Fprintf(&b, "\nTrust: worst edge class is %q.\n", wc)
	}

	return mcp.NewToolResultStructured(payload, b.String()), nil
}

// handleGetPackageSummary handles the get_package_summary tool call.
func (s *Server) handleGetPackageSummary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pkgName, err := req.RequireString("package_name")
	if err != nil {
		return mcp.NewToolResultError("get_package_summary: missing required argument 'package_name'"), nil
	}

	// Resolve aliases like "core/supplierfunding" → "supplierfunding".
	// Treat resolver errors as real failures, not "not found".
	resolved, err := s.db.ResolvePackageName(ctx, pkgName)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_package_summary: failed to resolve package", err), nil
	}
	if resolved != "" && resolved != pkgName {
		pkgName = resolved
	}

	payload := PackageSummaryPayload{PackageName: pkgName}

	var b strings.Builder
	fmt.Fprintf(&b, "Package: %s\n", pkgName)

	// Get LLM summary.
	summary, err := s.db.GetSummaryByPackage(ctx, pkgName)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_package_summary: failed to get summary", err), nil
	}
	if summary != nil {
		payload.Summary = summary.SummaryText
		payload.ModelVersion = summary.ModelVersion
		payload.GeneratedAt = summary.GeneratedAt.Format(time.RFC3339)
		age := time.Since(summary.GeneratedAt)
		payload.AgeMinutes = age.Minutes()
		if age > 7*24*time.Hour {
			payload.Stale = true
		}
		fmt.Fprintf(&b, "\nSummary:\n%s\n", summary.SummaryText)
	} else {
		b.WriteString("\nNo LLM summary available for this package.\n")
	}

	// Get exported symbols. Filter and cap in SQL so the cap cannot hide
	// exports behind unexported names.
	const exportedLimit = 500
	symbols, err := s.db.GetExportedSymbolsByPackageLimited(ctx, pkgName, exportedLimit)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_package_summary: failed to get symbols", err), nil
	}

	if len(symbols) > 0 {
		totalExported, err := s.db.CountSymbolsByPackage(ctx, pkgName)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("get_package_summary: failed to count symbols", err), nil
		}
		truncated := totalExported > len(symbols)
		if truncated {
			fmt.Fprintf(&b, "\nExported symbols (showing %d of %d, truncated):\n", len(symbols), totalExported)
		} else {
			b.WriteString("\nExported symbols:\n")
		}
		for _, sym := range symbols {
			sig := sym.Signature
			if sig == "" {
				sig = sym.Name
			}
			payload.ExportedSymbols = append(payload.ExportedSymbols, sig)
			fmt.Fprintf(&b, "  - %s %s\n", sym.Kind, sig)
		}
	} else {
		b.WriteString("\nNo exported symbols found in this package.\n")
	}

	if pkgName != "" {
		if extra := s.surfaceKnowledgeForPackage(ctx, pkgName); extra != "" {
			b.WriteString(extra)
		}
	}

	return mcp.NewToolResultStructured(payload, b.String()), nil
}

// handleGetTableContext handles the get_table_context tool call.
func (s *Server) handleGetTableContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tableName, err := req.RequireString("table_name")
	if err != nil {
		return mcp.NewToolResultError("get_table_context: missing required argument 'table_name'"), nil
	}

	table, err := s.db.GetDatastoreTableByName(ctx, tableName, "postgres")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_table_context: lookup failed", err), nil
	}
	if table == nil {
		tables, _ := s.db.ListDatastoreTables(ctx)
		for _, t := range tables {
			if strings.HasSuffix(t.Name, "."+tableName) || t.Name == tableName {
				table = &t
				break
			}
		}
	}
	if table == nil {
		return mcp.NewToolResultStructured(
			TableContextPayload{TableName: tableName, NotFound: true},
			fmt.Sprintf("No table found matching %q. Run 'projectlens index-datastore' to index database schemas.", tableName),
		), nil
	}

	payload := TableContextPayload{TableName: table.Name, Engine: table.Engine}

	var b strings.Builder
	fmt.Fprintf(&b, "Table: %s\n", table.Name)
	fmt.Fprintf(&b, "Engine: %s\n", table.Engine)

	if table.Columns != nil {
		b.WriteString("\nColumns:\n")
		var columns []TableColumn
		if err := json.Unmarshal(table.Columns, &columns); err == nil {
			payload.Columns = columns
			for _, col := range columns {
				attrs := col.Type
				if col.IsPrimaryKey {
					attrs += " PRIMARY KEY"
				}
				if !col.IsNullable {
					attrs += " NOT NULL"
				}
				if col.Default != "" {
					attrs += " DEFAULT " + col.Default
				}
				if col.ForeignKey != "" {
					attrs += " → " + col.ForeignKey
				}
				fmt.Fprintf(&b, "  - %s %s\n", col.Name, attrs)
			}
		}
	}

	readEdges, _ := s.db.GetEdgesTargetingDatastoreTable(ctx, table.ID, "reads_table")
	writeEdges, _ := s.db.GetEdgesTargetingDatastoreTable(ctx, table.ID, "writes_table")

	var edgeClasses []string
	if len(readEdges) > 0 {
		b.WriteString("\nRead by:\n")
		for _, e := range readEdges {
			payload.ReadBy = append(payload.ReadBy, TableEdgeHit{
				Kind:            e.SymbolKind,
				Name:            e.SymbolName,
				Evidence:        EvidenceSpan{FilePath: e.FilePath, LineStart: e.LineStart, LineEnd: e.LineEnd},
				Provenance:      e.Provenance,
				ConfidenceClass: e.ConfidenceClass,
			})
			edgeClasses = append(edgeClasses, e.ConfidenceClass)
			fmt.Fprintf(&b, "  - %s %s (%s:%d)\n", e.SymbolKind, e.SymbolName, e.FilePath, e.LineStart)
		}
	}
	if len(writeEdges) > 0 {
		b.WriteString("\nWritten by:\n")
		for _, e := range writeEdges {
			payload.WrittenBy = append(payload.WrittenBy, TableEdgeHit{
				Kind:            e.SymbolKind,
				Name:            e.SymbolName,
				Evidence:        EvidenceSpan{FilePath: e.FilePath, LineStart: e.LineStart, LineEnd: e.LineEnd},
				Provenance:      e.Provenance,
				ConfidenceClass: e.ConfidenceClass,
			})
			edgeClasses = append(edgeClasses, e.ConfidenceClass)
			fmt.Fprintf(&b, "  - %s %s (%s:%d)\n", e.SymbolKind, e.SymbolName, e.FilePath, e.LineStart)
		}
	}
	if len(readEdges) == 0 && len(writeEdges) == 0 {
		b.WriteString("\nNo code references discovered. Run 'projectlens index-datastore' to scan for SQL usage.\n")
	}

	if wc := worstClassOf(edgeClasses); wc != "" {
		payload.Trust = &Trust{WorstClass: wc}
		fmt.Fprintf(&b, "\nTrust: worst edge class is %q.\n", wc)
	}

	return mcp.NewToolResultStructured(payload, b.String()), nil
}

// probeProviders queries the configured embedder and summarizer and
// returns a slice of ProviderHealth ready for the index_status
// payload. Order is stable: embedder first, then summarizer.
func (s *Server) probeProviders(ctx context.Context) []ProviderHealth {
	return s.inspector.ProbeProviders(ctx)
}

// indexStatusPayload is the machine-parseable block agents can inspect
// without text-scraping. Fields here are stable; skill SKILL.md
// references them by name.
type indexStatusPayload struct {
	ProjectSlug   string                    `json:"project_slug,omitempty"`
	StorageSchema string                    `json:"storage_schema,omitempty"`
	Stages        map[string]StageFreshness `json:"stages"`
	Git           struct {
		Head  string `json:"head,omitempty"`
		Dirty bool   `json:"dirty"`
	} `json:"git"`
	Providers []ProviderHealth `json:"providers"`
}

// handleIndexStatus handles the index_status tool call. Returns a
// human-readable summary followed by a fenced JSON block listing each
// stage's freshness, current git HEAD/dirty state, and embedder health.
func (s *Server) handleIndexStatus(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	byStage, err := s.db.GetLatestRunsByStage(ctx)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("index_status: failed to get latest runs by stage", err), nil
	}

	payload := indexStatusPayload{
		ProjectSlug:   s.slug,
		StorageSchema: s.storageSchema,
		Stages:        map[string]StageFreshness{},
		Providers:     s.probeProviders(ctx),
	}
	for stage, run := range byStage {
		st := StageFreshness{
			Stage:          stage,
			Status:         run.Status,
			CommitSHA:      run.CommitSHA,
			StartedAt:      run.StartedAt.Format(time.RFC3339),
			FilesProcessed: run.FilesProcessed,
		}
		if run.CompletedAt != nil {
			st.CompletedAt = run.CompletedAt.Format(time.RFC3339)
			st.AgeMinutes = time.Since(*run.CompletedAt).Minutes()
		}
		payload.Stages[stage] = st
	}

	payload.Git.Head, payload.Git.Dirty = s.gitHeadAndDirty(ctx)

	var b strings.Builder
	if s.slug != "" {
		fmt.Fprintf(&b, "project: %s (storage_schema=%s)\n\n", s.slug, s.storageSchema)
	}
	b.WriteString("ProjectLens Index Status\n")
	b.WriteString("=======================\n")
	if len(payload.Stages) == 0 {
		b.WriteString("No index runs found. Run 'projectlens bootstrap' to create the initial index.\n")
	} else {
		for _, stage := range []string{"code", "summarize", "embed", "history", "datastore"} {
			st, ok := payload.Stages[stage]
			if !ok {
				continue
			}
			fmt.Fprintf(&b, "[%s] status=%s", st.Stage, st.Status)
			if st.CompletedAt != "" {
				fmt.Fprintf(&b, " completed=%s age=%.0fm", st.CompletedAt, st.AgeMinutes)
			} else if st.StartedAt != "" {
				fmt.Fprintf(&b, " started=%s", st.StartedAt)
			}
			if st.FilesProcessed > 0 {
				fmt.Fprintf(&b, " files=%d", st.FilesProcessed)
			}
			b.WriteString("\n")
			if st.AgeMinutes > 24*60 && st.Status == "completed" {
				fmt.Fprintf(&b, "  WARNING: %s stage is %.0fh old — consider reindex.\n", stage, st.AgeMinutes/60)
			}
		}
	}

	if payload.Git.Head != "" {
		fmt.Fprintf(&b, "Git HEAD: %s (dirty=%v)\n", payload.Git.Head, payload.Git.Dirty)
	}

	if len(payload.Providers) > 0 {
		b.WriteString("\nProviders:\n")
		for _, p := range payload.Providers {
			if p.Provider == "" {
				fmt.Fprintf(&b, "  %s: %s", p.Role, p.State)
			} else {
				fmt.Fprintf(&b, "  %s (%s): %s", p.Role, p.Provider, p.State)
			}
			if p.Error != "" {
				fmt.Fprintf(&b, " (%s)", p.Error)
			}
			b.WriteString("\n")
		}
	}

	return mcp.NewToolResultStructured(payload, b.String()), nil
}

// gitHeadAndDirty returns the current HEAD SHA and whether the working
// tree has uncommitted changes. Empty SHA when no repoPath is configured
// or git isn't reachable — agents treat that as "unknown".
func (s *Server) gitHeadAndDirty(ctx context.Context) (string, bool) {
	gs := s.inspector.GitHeadAndDirty(ctx)
	return gs.Head, gs.Dirty
}

// handleGetChangeHistory handles the get_change_history tool call.
func (s *Server) handleGetChangeHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("get_change_history: missing required argument 'name'"), nil
	}
	limit := req.GetInt("limit", 10)
	if limit <= 0 {
		limit = 10
	}

	// Try to find as a file path first.
	file, err := s.db.GetFileByPath(ctx, name)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_change_history: file lookup failed", err), nil
	}

	if file != nil {
		// Found as file — get file history from DB.
		records, err := s.db.GetFileHistory(ctx, file.ID, limit)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("get_change_history: failed to get file history", err), nil
		}

		payload := ChangeHistoryPayload{Target: name, TargetKind: "file"}
		payload.Records = make([]ChangeRecord, 0, len(records))
		var b strings.Builder
		if len(records) == 0 {
			fmt.Fprintf(&b, "No change history found for %s. Run 'projectlens index-history' to index git history.", name)
			return mcp.NewToolResultStructured(payload, b.String()), nil
		}

		fmt.Fprintf(&b, "Change history for %s:\n\n", name)
		for i, r := range records {
			date := r.CommittedAt.Format("2006-01-02")
			shortHash := r.CommitHash
			if len(shortHash) > 7 {
				shortHash = shortHash[:7]
			}
			payload.Records = append(payload.Records, ChangeRecord{
				Hash:       r.CommitHash,
				ShortHash:  shortHash,
				Author:     r.Author,
				Date:       date,
				ChangeType: r.ChangeType,
			})
			fmt.Fprintf(&b, "%d. %s (%s) by %s — %s\n", i+1, shortHash, date, r.Author, r.ChangeType)
		}
		return mcp.NewToolResultStructured(payload, b.String()), nil
	}

	// Not found as file — try as symbol.
	results, err := retrieval.LexicalSearch(ctx, s.db, name, 1)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_change_history: symbol lookup failed", err), nil
	}
	if len(results) == 0 {
		return mcp.NewToolResultStructured(
			ChangeHistoryPayload{Target: name, NotFound: true, Records: []ChangeRecord{}},
			fmt.Sprintf("No file or symbol found matching %q.", name),
		), nil
	}

	target := results[0]

	// If repoPath is configured, use git-based symbol evolution.
	if s.repoPath != "" {
		changes, err := history.GetSymbolEvolution(s.repoPath, target.FilePath, target.SymbolName, target.LineStart, target.LineEnd, limit)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("get_change_history: git history failed", err), nil
		}

		evidence := &EvidenceSpan{FilePath: target.FilePath, LineStart: target.LineStart, LineEnd: target.LineEnd}
		payload := ChangeHistoryPayload{Target: target.SymbolName, TargetKind: "symbol", Evidence: evidence}
		payload.Records = make([]ChangeRecord, 0, len(changes))

		var b strings.Builder
		if len(changes) == 0 {
			fmt.Fprintf(&b, "No change history found for symbol %q in %s.", target.SymbolName, target.FilePath)
			return mcp.NewToolResultStructured(payload, b.String()), nil
		}

		fmt.Fprintf(&b, "Change history for symbol %s (%s:%d-%d):\n\n", target.SymbolName, target.FilePath, target.LineStart, target.LineEnd)
		for i, c := range changes {
			shortHash := c.Hash
			if len(shortHash) > 7 {
				shortHash = shortHash[:7]
			}
			date := time.Unix(c.Timestamp, 0).Format("2006-01-02")
			subject := firstLine(c.Message)
			payload.Records = append(payload.Records, ChangeRecord{
				Hash:      c.Hash,
				ShortHash: shortHash,
				Author:    c.Author,
				Date:      date,
				Subject:   subject,
			})
			fmt.Fprintf(&b, "%d. %s (%s) by %s — %s\n", i+1, shortHash, date, c.Author, c.Message)
			if c.DiffSnippet != "" {
				snippet := truncateDiff(c.DiffSnippet, 500)
				for _, line := range strings.Split(snippet, "\n") {
					fmt.Fprintf(&b, "   %s\n", line)
				}
				b.WriteString("\n")
			}
		}
		return mcp.NewToolResultStructured(payload, b.String()), nil
	}

	// Fallback: use DB-based symbol history.
	records, err := s.db.GetSymbolHistory(ctx, target.SymbolID, limit)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_change_history: failed to get symbol history", err), nil
	}

	evidence := &EvidenceSpan{FilePath: target.FilePath, LineStart: target.LineStart, LineEnd: target.LineEnd}
	payload := ChangeHistoryPayload{Target: target.SymbolName, TargetKind: "symbol", Evidence: evidence}
	payload.Records = make([]ChangeRecord, 0, len(records))

	if len(records) == 0 {
		if s.repoPath == "" {
			return mcp.NewToolResultStructured(payload, "Symbol-level change history requires repoPath configured on the MCP server. Set REPO_PATH env or repo_path in configs/index.yaml, then restart. (File-level history via get_change_history on a file path works without it.)"), nil
		}
		// Defensive: unreachable today (symbol_history is not populated); retained for when a future indexer stage writes to it.
		return mcp.NewToolResultStructured(payload, fmt.Sprintf("No change history found for symbol %q in %s.", target.SymbolName, target.FilePath)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Change history for symbol %s (%s:%d-%d):\n\n", target.SymbolName, target.FilePath, target.LineStart, target.LineEnd)
	for i, r := range records {
		date := r.CommittedAt.Format("2006-01-02")
		shortHash := r.CommitHash
		if len(shortHash) > 7 {
			shortHash = shortHash[:7]
		}
		payload.Records = append(payload.Records, ChangeRecord{
			Hash:       r.CommitHash,
			ShortHash:  shortHash,
			Author:     r.Author,
			Date:       date,
			ChangeType: r.ChangeType,
		})
		fmt.Fprintf(&b, "%d. %s (%s) by %s — %s\n", i+1, shortHash, date, r.Author, r.ChangeType)
		if r.DiffSnippet != nil && *r.DiffSnippet != "" {
			snippet := truncateDiff(*r.DiffSnippet, 500)
			for _, line := range strings.Split(snippet, "\n") {
				fmt.Fprintf(&b, "   %s\n", line)
			}
			b.WriteString("\n")
		}
	}
	return mcp.NewToolResultStructured(payload, b.String()), nil
}

// handleGetCoupling handles the get_coupling tool call.
func (s *Server) handleGetCoupling(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("get_coupling: missing required argument 'name'"), nil
	}
	minStrength := float32(req.GetFloat("min_strength", 0.3))
	if minStrength < 0 {
		minStrength = 0
	}
	if minStrength > 1 {
		minStrength = 1
	}

	file, err := s.db.GetFileByPath(ctx, name)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_coupling: file lookup failed", err), nil
	}
	if file == nil {
		return mcp.NewToolResultStructured(
			CouplingPayload{Target: name, NotFound: true, MinStrength: float64(minStrength), Coupled: []CouplingEntry{}},
			fmt.Sprintf("No file found matching %q. Provide the exact indexed file path.", name),
		), nil
	}

	couplings, err := s.db.GetCouplingEdges(ctx, file.ID, minStrength)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_coupling: failed to get coupling edges", err), nil
	}

	payload := CouplingPayload{Target: name, MinStrength: float64(minStrength)}
	payload.Coupled = make([]CouplingEntry, 0, len(couplings))
	var edgeClasses []string
	for _, c := range couplings {
		payload.Coupled = append(payload.Coupled, CouplingEntry{
			FilePath:        c.FilePath,
			Strength:        float64(c.Strength),
			Provenance:      c.Provenance,
			ConfidenceClass: c.ConfidenceClass,
		})
		edgeClasses = append(edgeClasses, c.ConfidenceClass)
	}
	if wc := worstClassOf(edgeClasses); wc != "" {
		payload.Trust = &Trust{WorstClass: wc}
	}

	var b strings.Builder
	if len(couplings) == 0 {
		fmt.Fprintf(&b, "No co-change coupling found for %s (min strength: %.1f). Run 'projectlens index-history' to build coupling data.", name, minStrength)
		return mcp.NewToolResultStructured(payload, b.String()), nil
	}

	fmt.Fprintf(&b, "Co-change coupling for %s:\n", name)
	var strong, notable []string
	for _, c := range couplings {
		line := fmt.Sprintf("  - %s (strength: %.2f)", c.FilePath, c.Strength)
		if c.Strength >= 0.5 {
			strong = append(strong, line)
		} else {
			notable = append(notable, line)
		}
	}
	if len(strong) > 0 {
		b.WriteString("\nStrong coupling (>= 0.5):\n")
		for _, line := range strong {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(notable) > 0 {
		fmt.Fprintf(&b, "\nNotable coupling (>= %.1f):\n", minStrength)
		for _, line := range notable {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	return mcp.NewToolResultStructured(payload, b.String()), nil
}

// firstLine returns the first line of s, trimmed. Returns "" if s is
// empty. Used to extract a commit subject from a multi-line message.
func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}

// truncateDiff truncates a diff snippet to maxLen bytes, cutting at the last
// newline before the limit.
func truncateDiff(diff string, maxLen int) string {
	if len(diff) <= maxLen {
		return diff
	}
	truncated := diff[:maxLen]
	if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
		truncated = truncated[:idx]
	}
	return truncated + "\n   ..."
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
