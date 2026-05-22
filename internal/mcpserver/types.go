package mcpserver

import (
	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/retrieval"
)

// ProviderHealth reports the state of one configured provider.
type ProviderHealth = indexstate.ProviderHealth

// StageFreshness mirrors the per-stage shape used in index_status.
type StageFreshness = indexstate.StageFreshness

// SummarizerProber matches the summarizer probe contract used by indexstate.
type SummarizerProber = indexstate.SummarizerProber

// EvidenceSpan points at the bytes a structured result is derived from
// so an agent can cheaply re-read and verify before acting on them.
type EvidenceSpan struct {
	FilePath  string `json:"file_path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
}

// Degradation signals that a result is partial because a backend was
// unavailable. Agents should treat results as best-effort when
// Degraded == true; when false, Reason and Fallback are omitted and
// the result is fully trusted.
type Degradation struct {
	Degraded bool   `json:"degraded"`
	Reason   string `json:"reason,omitempty"`
	Fallback string `json:"fallback,omitempty"`
}

// SymbolHit is one structured result row used by find_symbol and any
// other tool that returns ranked symbols. The Evidence span points an
// agent at the bytes that produced this hit so it can re-read before
// acting.
type SymbolHit struct {
	Kind        string       `json:"kind"`
	Name        string       `json:"name"`
	Signature   string       `json:"signature,omitempty"`
	PackageName string       `json:"package_name,omitempty"`
	Score       float64      `json:"score"`
	DocComment  string       `json:"doc_comment,omitempty"`
	Evidence    EvidenceSpan `json:"evidence"`
	// Provenance + ConfidenceClass are set on graph-derived hits
	// (callers/callees/implementors) and identify the trust class of the
	// edge that produced the hit. Empty on the Target hit and on
	// lexical/semantic results.
	Provenance      string `json:"provenance,omitempty"`
	ConfidenceClass string `json:"confidence_class,omitempty"`
}

// FindSymbolPayload is the structured response for find_symbol.
type FindSymbolPayload struct {
	Query string      `json:"query"`
	Kind  string      `json:"kind,omitempty"`
	Hits  []SymbolHit `json:"hits"`
	Total int         `json:"total"`
}

// SearchGoContextPayload is the structured response for
// search_go_context. Degradation is non-zero when a backend was
// unavailable — agents should treat Hits as best-effort then.
type SearchGoContextPayload struct {
	Query       string      `json:"query"`
	QueryType   string      `json:"query_type"`
	Hits        []SymbolHit `json:"hits"`
	Total       int         `json:"total"`
	Degradation Degradation `json:"degradation"`
}

// toSymbolHit converts a retrieval.SearchResult into the structured
// SymbolHit shape. Defined here so multiple handlers can reuse it
// without importing retrieval in places that don't need it.
func toSymbolHit(r retrieval.SearchResult) SymbolHit {
	return SymbolHit{
		Kind:            r.Kind,
		Name:            r.SymbolName,
		Signature:       formatSignature(r),
		PackageName:     r.PackageName,
		Score:           r.Score,
		DocComment:      r.DocComment,
		Evidence:        EvidenceSpan{FilePath: r.FilePath, LineStart: r.LineStart, LineEnd: r.LineEnd},
		Provenance:      r.Provenance,
		ConfidenceClass: r.ConfidenceClass,
	}
}

// SymbolContextPayload is the structured response for get_symbol_context.
// Target carries the matched symbol (Evidence.FilePath:LineStart-LineEnd
// is where it lives). Callers, Callees, and Implementors are slices of
// SymbolHit so each carries its own evidence span. NotFound is set when
// the lookup matched no symbol — Target and the slices are then zero.
type SymbolContextPayload struct {
	Query        string      `json:"query,omitempty"`
	NotFound     bool        `json:"not_found,omitempty"`
	Target       SymbolHit   `json:"target"`
	ScipSymbol   string      `json:"scip_symbol,omitempty"`
	Callers      []SymbolHit `json:"callers,omitempty"`
	Callees      []SymbolHit `json:"callees,omitempty"`
	Implementors []SymbolHit `json:"implementors,omitempty"`
	Trust        *Trust      `json:"trust,omitempty"`
}

// Trust summarizes the worst confidence class encountered across the edges
// that fed a response. Agents can read this single field instead of scanning
// every hit. Ordering: ambiguous > inferred > extracted (worst first).
type Trust struct {
	WorstClass string `json:"worst_class"`
}

// worstClassRank maps the three confidence classes to their severity rank.
// Anything outside the enum is ignored (rank 0).
var worstClassRank = map[string]int{"extracted": 1, "inferred": 2, "ambiguous": 3}

// worstClassOf returns the worst (least trusted) class observed in classes.
// Empty strings and unknown values are ignored.
func worstClassOf(classes []string) string {
	worst := ""
	rank := 0
	for _, c := range classes {
		r, ok := worstClassRank[c]
		if !ok {
			continue
		}
		if r > rank {
			worst = c
			rank = r
		}
	}
	return worst
}

// worstClass returns the worst class across SymbolHit groups.
func worstClass(hits ...[]SymbolHit) string {
	var classes []string
	for _, group := range hits {
		for _, h := range group {
			classes = append(classes, h.ConfidenceClass)
		}
	}
	return worstClassOf(classes)
}

// PackageSummaryPayload is the structured response for
// get_package_summary. GeneratedAt + AgeMinutes + Stale are derived
// from the summaries row at response time. Stale is set when the
// summary is older than 7 days; agents can use it to decide whether
// to ask for a re-summarize before quoting.
type PackageSummaryPayload struct {
	PackageName     string   `json:"package_name"`
	Summary         string   `json:"summary,omitempty"`
	ModelVersion    string   `json:"model_version,omitempty"`
	GeneratedAt     string   `json:"generated_at,omitempty"`
	AgeMinutes      float64  `json:"age_minutes,omitempty"`
	Stale           bool     `json:"stale"`
	ExportedSymbols []string `json:"exported_symbols,omitempty"`
}

// TableColumn is one column from a datastore_tables.columns JSON blob.
type TableColumn struct {
	Name         string `json:"name"`
	Type         string `json:"type,omitempty"`
	IsNullable   bool   `json:"is_nullable"`
	IsPrimaryKey bool   `json:"is_primary_key"`
	Default      string `json:"default,omitempty"`
	ForeignKey   string `json:"foreign_key,omitempty"`
}

// TableEdgeHit is a code reference to a table — used in TableContextPayload
// to expose reads_table / writes_table edges with evidence spans.
// Provenance + ConfidenceClass identify the trust class of the edge.
type TableEdgeHit struct {
	Kind            string       `json:"kind"`
	Name            string       `json:"name"`
	Evidence        EvidenceSpan `json:"evidence"`
	Provenance      string       `json:"provenance,omitempty"`
	ConfidenceClass string       `json:"confidence_class,omitempty"`
}

// TableContextPayload is the structured response for get_table_context.
// NotFound is set when the table lookup didn't match anything; in that
// case Columns/ReadBy/WrittenBy are nil and TableName echoes the query.
// Trust summarizes the worst class across ReadBy + WrittenBy edges.
type TableContextPayload struct {
	TableName string         `json:"table_name"`
	NotFound  bool           `json:"not_found,omitempty"`
	Engine    string         `json:"engine,omitempty"`
	Columns   []TableColumn  `json:"columns,omitempty"`
	ReadBy    []TableEdgeHit `json:"read_by,omitempty"`
	WrittenBy []TableEdgeHit `json:"written_by,omitempty"`
	Trust     *Trust         `json:"trust,omitempty"`
}

// ChangeRecord is one structured change-history entry. Subject is the
// first line of the commit message when available; otherwise empty.
type ChangeRecord struct {
	Hash       string `json:"hash"`
	ShortHash  string `json:"short_hash"`
	Author     string `json:"author"`
	Date       string `json:"date"` // ISO date YYYY-MM-DD
	ChangeType string `json:"change_type,omitempty"`
	Subject    string `json:"subject,omitempty"`
}

// ChangeHistoryPayload is the structured response for get_change_history.
// TargetKind is "file" or "symbol" (empty when nothing matched). Evidence
// is populated when the target resolved to a symbol. NotFound is true
// when neither a file nor a symbol matched the query.
type ChangeHistoryPayload struct {
	Target     string         `json:"target"`
	TargetKind string         `json:"target_kind,omitempty"`
	NotFound   bool           `json:"not_found,omitempty"`
	Evidence   *EvidenceSpan  `json:"evidence,omitempty"`
	Records    []ChangeRecord `json:"records"`
}

// CouplingEntry is one structurally exposed co-change relationship.
// Provenance + ConfidenceClass identify the trust class of the underlying
// co_changes edge (typically history/inferred).
type CouplingEntry struct {
	FilePath        string  `json:"file_path"`
	Strength        float64 `json:"strength"`
	Provenance      string  `json:"provenance,omitempty"`
	ConfidenceClass string  `json:"confidence_class,omitempty"`
}

// CouplingPayload is the structured response for get_coupling. NotFound
// is set when the named file isn't indexed; Coupled is then nil. Trust
// summarizes the worst class across the Coupled entries.
type CouplingPayload struct {
	Target      string          `json:"target"`
	NotFound    bool            `json:"not_found,omitempty"`
	MinStrength float64         `json:"min_strength"`
	Coupled     []CouplingEntry `json:"coupled"`
	Trust       *Trust          `json:"trust,omitempty"`
}

// SaveKnowledgePayload is the structured response for save_knowledge.
//
// Deduped is true when the handler short-circuited on a recent duplicate
// (same source+title+body within the dedup window) — the returned ID points
// at the pre-existing entry. Anchors from the dup-hit call are still resolved
// and merged into the existing entry's edges (idempotent via the edges
// unique constraint), so a retry with corrected anchors can still attach.
//
// AnchorsUnresolved entries are formatted as "type:ref (reason)" — the
// reason is the resolver's diagnostic ("not found", "ambiguous: N matches
// — use SCIP id") so the agent can pick a canonical ref instead of retrying
// with the same short name.
type SaveKnowledgePayload struct {
	ID                int64    `json:"id"`
	Embedded          bool     `json:"embedded"`
	Deduped           bool     `json:"deduped,omitempty"`
	AnchorsResolved   int      `json:"anchors_resolved"`
	AnchorsUnresolved []string `json:"anchors_unresolved,omitempty"`
}

// KnowledgeHit is one structured row used by search_knowledge.
type KnowledgeHit struct {
	ID         int64    `json:"id"`
	Category   string   `json:"category"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Tags       []string `json:"tags,omitempty"`
	Score      float64  `json:"score,omitempty"`
	MatchedVia string   `json:"matched_via"` // "vector" | "anchor" | "both"
}

// SearchKnowledgePayload is the structured response for search_knowledge.
type SearchKnowledgePayload struct {
	Query   string         `json:"query,omitempty"`
	Total   int            `json:"total"`
	Entries []KnowledgeHit `json:"entries"`
}
