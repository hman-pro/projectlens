package report

import (
	"context"
	"fmt"
	"time"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/storage"
	wl "github.com/hman-pro/projectlens/internal/storage/writelock"
)

// Report is the typed summary returned by Builder.Build. Renderers turn
// this into Markdown or JSON; the builder never formats output.
type Report struct {
	GeneratedAt  time.Time                    `json:"generated_at"`
	RepoPath     string                       `json:"repo_path,omitempty"`
	Git          indexstate.GitState          `json:"git"`
	Stages       map[string]StageDetail       `json:"stages"`
	Providers    []indexstate.ProviderHealth  `json:"providers"`
	TopPackages  []storage.PackageStat        `json:"top_packages"`
	TopTables    []storage.TableStat          `json:"top_tables"`
	HighCoupling []storage.CouplingPair       `json:"high_coupling"`
	EdgeTrust    []storage.EdgeConfidenceStat `json:"edge_trust"`
	Knowledge    KnowledgeInventory           `json:"knowledge"`
	Degraded     []StageDegradation           `json:"degraded"`
	Suggestions  []AgentQuestion              `json:"suggestions"`
	WriterActive bool                         `json:"writer_active"`
}

type KnowledgeInventory struct {
	TotalEntries     int                        `json:"total_entries"`
	CountsByCategory map[string]int             `json:"counts_by_category"`
	RecentEntries    []storage.KnowledgeSummary `json:"recent_entries"`
}

type StageDegradation struct {
	Stage           string `json:"stage"`
	Reason          string `json:"reason"`
	SuggestedAction string `json:"suggested_action"`
}

type AgentQuestion struct {
	Topic         string `json:"topic"`
	SuggestedTool string `json:"suggested_tool"`
	Example       string `json:"example"`
}

// Options controls per-section sizing. Zero values fall back to Defaults.
type Options struct {
	TopN int
}

func (o Options) topN() int {
	if o.TopN <= 0 {
		return 10
	}
	return o.TopN
}

// Builder assembles a Report from read-only queries.
type Builder struct {
	db        *storage.DB
	inspector indexstate.Inspector
	repoPath  string
	schema    string
	opts      Options
}

func NewBuilder(db *storage.DB, insp indexstate.Inspector, repoPath string, opts Options) *Builder {
	return &Builder{db: db, inspector: insp, repoPath: repoPath, schema: "public", opts: opts}
}

// WithSchema scopes the writer-active probe to a specific storage
// schema. Multi-project mode passes the project's storage_schema so
// the report does not mis-attribute another project's active writer.
func (b *Builder) WithSchema(schema string) *Builder {
	if schema != "" {
		b.schema = schema
	}
	return b
}

// Build runs queries sequentially and returns the populated Report.
// Per-section failures are logged into Report.Degraded; the Build call
// only returns an error for fatal conditions (DB connection, context
// cancellation).
func (b *Builder) Build(ctx context.Context) (*Report, error) {
	r := &Report{
		GeneratedAt: time.Now().UTC(),
		RepoPath:    b.repoPath,
		Stages:      map[string]StageDetail{},
		Knowledge:   KnowledgeInventory{CountsByCategory: map[string]int{}},
	}

	byStage, err := b.db.GetLatestRunsByStage(ctx)
	if err != nil {
		return nil, fmt.Errorf("report: latest runs: %w", err)
	}
	for stage, run := range byStage {
		freshness := indexstate.StageFreshness{
			Stage:          stage,
			Status:         run.Status,
			CommitSHA:      run.CommitSHA,
			StartedAt:      run.StartedAt.Format(time.RFC3339),
			FilesProcessed: run.FilesProcessed,
		}
		detail := StageDetail{
			StageFreshness: freshness,
			Providers: StageProviders{
				Embed:     run.ProviderEmbed,
				Summarize: run.ProviderSummarize,
			},
			Metrics: run.Metrics,
			Error:   run.ErrorText,
		}
		if run.CompletedAt != nil {
			detail.CompletedAt = run.CompletedAt.Format(time.RFC3339)
			detail.AgeMinutes = time.Since(*run.CompletedAt).Minutes()
			detail.DurationSeconds = run.CompletedAt.Sub(run.StartedAt).Seconds()
		}
		r.Stages[stage] = detail
	}

	r.Providers = b.inspector.ProbeProviders(ctx)
	r.Git = b.inspector.GitHeadAndDirty(ctx)

	if active, err := writerActive(ctx, b.db, b.schema); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "writer", Reason: err.Error(), SuggestedAction: ""})
	} else {
		r.WriterActive = active
	}

	limit := b.opts.topN()
	if pkgs, err := b.db.TopPackagesBySymbolCount(ctx, limit); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "top_packages", Reason: err.Error()})
	} else {
		r.TopPackages = pkgs
	}
	if tbls, err := b.db.TopDatastoreTablesByEdgeCount(ctx, limit); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "top_tables", Reason: err.Error()})
	} else {
		r.TopTables = tbls
	}
	if pairs, err := b.db.HighCouplingPairs(ctx, limit, 3); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "high_coupling", Reason: err.Error()})
	} else {
		r.HighCoupling = pairs
	}
	if stats, err := b.db.EdgeConfidenceBreakdown(ctx); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "edge_trust", Reason: err.Error()})
	} else {
		r.EdgeTrust = stats
	}
	if counts, err := b.db.KnowledgeStatsByCategory(ctx); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "knowledge_counts", Reason: err.Error()})
	} else {
		r.Knowledge.CountsByCategory = counts
		for _, n := range counts {
			r.Knowledge.TotalEntries += n
		}
	}
	if recent, err := b.db.RecentKnowledgeEntries(ctx, limit); err != nil {
		r.Degraded = append(r.Degraded, StageDegradation{Stage: "recent_knowledge", Reason: err.Error()})
	} else {
		r.Knowledge.RecentEntries = recent
	}

	r.Degraded = append(r.Degraded, deriveDegradation(r.Stages, r.Providers)...)
	r.Suggestions = deriveSuggestions(r)

	return r, nil
}

// writerActive is a thin indirection so unit tests can swap it.
var writerActive = func(ctx context.Context, db *storage.DB, schema string) (bool, error) {
	return wl.IsWriterActive(ctx, db, schema)
}
