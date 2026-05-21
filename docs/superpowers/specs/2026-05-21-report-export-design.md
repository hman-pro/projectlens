# ProjectLens — Report and Graph Export (Phase 1)

Date: 2026-05-21
Status: design / spec — revised after review (`2026-05-21-report-export-design-review.md`)
Source: distilled from `docs/2026-05-21-graphify-comparison.md` Phase 1 recommendations.

Review fixes applied (see review doc for original findings):

- Provider health uses existing `reachable` / `configured` / `not_configured` / `error` vocabulary; no invented `ok` state.
- Storage queries match the real schema: packages group on `symbols.package_name`, datastore queries filter on `reads_table`/`writes_table`, `RecentKnowledgeEntries` orders by `created_at`, `TableStat` replaces `MigrationCount` with `SourceFileCount`.
- `--edges` filter uses the raw stored edge types (`calls`, `implements`, `imports`, `reads_table`, `writes_table`, `co_changes`, `knowledge_about`); JSON `type` mirrors them unchanged. No aliasing.
- `report.Builder` takes an `indexstate.Inspector` for provider probes and git state, moving those helpers out of `mcpserver` into a shared package.
- `IsWriterActive` joins `pg_stat_activity` so a stale lock row from a dead writer does not count as active.

Second-pass review fixes:

- Graph export package nodes are derived from `package_name` (no `packages` table exists); `package:<package_name>` is the node id.
- `SourceFileCount` projects each edge's source to a real `file_id` via `symbols.file_id`, so two SQL refs in one file count as one source file.
- Missing-stage `SuggestedAction` map uses real CLI subcommands; `code` maps to `reindex` because no `index-code` command exists.

Third-pass review fixes:

- Edge endpoints in the JSON example use the actual node-ID scheme (`sym:<id>`, `table:<engine>:<schema>.<name>`, `knowledge:<id>`).
- A single `nodeID(kind, id, ...)` function is the source of truth, called by both the node cursor and the edge serializer.
- `knowledge` nodes are added to the export so `knowledge_about` edges resolve. The node-ID table enumerates every `source_type` / `target_type` the indexer currently emits.
- An integration test asserts the graph-closure invariant: every edge endpoint exists in the exported node set.

Fourth-pass review fix:

- JSON example nodes now use the same ID scheme as the edges (`sym:4821`, `sym:9134`, `table:postgres:public.orders`, `knowledge:42`), so the worked example itself satisfies the graph-closure invariant.
Scope: Phase 1 only. Confidence/provenance (Phase 2), agent install CLI (Phase 3), freshness automation (Phase 4), docs integration (Phase 5) are out of scope for this spec and will be handled in separate sessions.

## Goal

Make ProjectLens's indexed state inspectable through two new CLI commands:

- `projectlens report` — human- and machine-readable summary artifact of what the index currently knows.
- `projectlens export graph` — portable, streamed JSON dump of nodes and edges from the polymorphic graph.

Both commands are read-only. They do not acquire the writer lock and are safe to run during indexing.

## Non-goals

- No MCP tool for report in v1 (CLI only — agents keep using `index_status`).
- No confidence vocabulary work (deferred to Phase 2).
- No HTML output, no interactive viewer.
- No node truncation (full graph dump is the default).
- No new ingestion or pipeline stages.
- No changes to existing writer lock, indexer, or MCP server behavior.

## Architecture

Two new internal packages plus thin CLI wiring.

```
internal/
  report/
    report.go       — Report struct, Builder, storage queries
    markdown.go     — MarkdownRenderer
    json.go         — JSONRenderer
    report_test.go  — fixture-based renderer tests
    builder_integration_test.go
  export/
    graph.go        — GraphExporter (streams nodes + edges as JSON)
    graph_test.go
    graph_integration_test.go
cmd/projectlens/
  report.go         — newReportCmd (cobra)
  export.go         — newExportCmd with export-graph subcommand
  report_test.go    — CLI smoke tests
```

### Boundaries

- `report.Builder` takes `storage.DB` + an `indexstate.Inspector` (provides provider probes + git head/dirty helpers) + repo path. Returns a typed `*Report`. Does no I/O on output.
- `indexstate.Inspector` is the shared, MCP-server-free home for `probeProviders` and `gitHeadAndDirty`. It is constructed from the same config + provider router as the MCP server uses, so both surfaces see identical state.
- Renderers (`MarkdownRenderer`, `JSONRenderer`) take a `*Report` and an `io.Writer`. Pure formatting.
- `export.GraphExporter` streams via SQL cursors directly to an `io.Writer`. Never loads the full graph in memory.
- CLI files do flag parsing + wiring only — they construct DB, build the `Inspector` from existing config-loading helpers, hand both to the builder.

Reuses `storage.DB`, follows the existing `internal/indexer` / `internal/retrieval` layout convention.

### Read-only

Neither command acquires the advisory writer lock (id `9876543210`). Both can run concurrently with `bootstrap` / `reindex` / other writers. Snapshot inconsistency during active writes is acceptable; the report header surfaces whether a writer is currently active.

## Data model

```go
// internal/report/report.go
type Report struct {
    GeneratedAt   time.Time
    RepoPath      string
    Git           GitState
    Stages        map[string]StageFreshness
    Providers     []ProviderHealth
    TopPackages   []PackageStat
    TopTables     []TableStat
    HighCoupling  []CouplingPair
    Knowledge     KnowledgeInventory
    Degraded      []StageDegradation
    Suggestions   []AgentQuestion
    WriterActive  bool   // true iff index_locks shows an active holder at query time
}

type GitState struct {
    Head  string
    Dirty bool
}

type PackageStat struct {
    ImportPath  string
    SymbolCount int
    FileCount   int
}

type TableStat struct {
    Schema           string
    Name             string
    Engine           string
    ReadRefs         int   // edges of type "reads_table"
    WriteRefs        int   // edges of type "writes_table"
    SourceFileCount  int   // distinct files containing read/write refs,
                           // resolved by joining edges.source_id → symbols.file_id
                           // for source_type='symbol' rows (the only producer today)
}

type CouplingPair struct {
    FileA          string
    FileB          string
    CoChangeCount  int
}

type KnowledgeInventory struct {
    TotalEntries     int
    CountsByCategory map[string]int
    RecentEntries    []KnowledgeSummary // id, title, category, source, saved_at — last N
}

type StageDegradation struct {
    Stage            string
    Reason           string
    SuggestedAction  string
}

type AgentQuestion struct {
    Topic        string
    SuggestedTool string
    Example       string
}
```

`StageFreshness` and `ProviderHealth` are reused. They currently live in `internal/mcpserver/types.go`. Move both (plus `GitState`) to a neutral location — proposed: `internal/storage/types.go` or new `internal/types/`. `mcpserver` and `report` both import the shared location. This avoids `report` depending on `mcpserver`.

### Derived fields

- `Degraded` is derived from `Stages` + `Providers`:
  - any of `{code, summarize, embed, history, datastore}` missing from `Stages` → degraded entry with a stage-specific `SuggestedAction`, mapped against the real CLI surface:
    - `code` → `run projectlens reindex` (no `index-code` subcommand exists; the code stage is owned by `bootstrap` / `reindex`)
    - `summarize` → `run projectlens index-summarize`
    - `embed` → `run projectlens index-embed`
    - `history` → `run projectlens index-history`
    - `datastore` → `run projectlens index-datastore`
  - provider `State` of `error` or `not_configured` → degraded entry quoting `ProviderHealth.Error`. `State` values `reachable` and `configured` count as healthy.
  - `Stages[x].AgeMinutes > 24*60` and `Status == "completed"` → degraded entry with `SuggestedAction = "run projectlens reindex"`.

  The stage→action map lives in a single `var` in `internal/report` so adding a new stage is one change.

  The provider `State` vocabulary is fixed by the existing shared type (`reachable`, `configured`, `not_configured`, `error`). The report does not invent an `ok` state.

- `Suggestions` are deterministic rules over what's present:
  - datastore stage healthy → suggest `get_table_context` with one table from `TopTables`.
  - history stage healthy → suggest `get_coupling` with one file from `HighCoupling`.
  - code stage healthy and `TopPackages` non-empty → suggest `get_package_summary` with the top package.
  - knowledge entries exist → suggest `search_knowledge`.
  - No LLM calls. Pure rule evaluation.

- `WriterActive` is true iff a row exists in `index_locks` whose `backend_pid` is also present in `pg_stat_activity.pid`. This matches the liveness condition used by `writelock.Acquire` (which reaps stale rows on the same join) and avoids reporting a crashed-writer ghost row as an active writer.

### New storage queries

Added to `internal/storage/`. All matched against the current schema:

- `TopPackagesBySymbolCount(ctx, limit int) ([]PackageStat, error)` — `GROUP BY package_name` over `symbols`, `COUNT(*) AS symbol_count`, `COUNT(DISTINCT file_id) AS file_count`. Order by `symbol_count DESC`.
- `TopDatastoreTablesByEdgeCount(ctx, limit int) ([]TableStat, error)` — joins `edges` (filtered to `edge_type IN ('reads_table','writes_table')` and `target_type='datastore_table'`) with `datastore_tables` on `target_id`. To produce a real per-file count, the query also projects each edge's source to a `file_id` via a `CASE` on `source_type`:
  - `source_type='symbol'` → join `symbols` on `edges.source_id = symbols.id`, take `symbols.file_id` (this is the only producer today, per `internal/datastore/indexer.go`)
  - `source_type='file'` → use `edges.source_id` directly (reserved for future file-sourced datastore edges)
  - other source types → excluded from `SourceFileCount`

  Aggregates per table: `SUM(CASE edge_type WHEN 'reads_table' THEN 1 ELSE 0 END) AS read_refs`, same shape for `writes_table`, plus `COUNT(DISTINCT projected_file_id) AS source_file_count`. Order by `read_refs + write_refs DESC`.
- `HighCouplingPairs(ctx, limit int, minCount int) ([]CouplingPair, error)` — derives symmetric pairs from `file_history` co-change. `minCount` defaults to 3.
- `KnowledgeStatsByCategory(ctx) (map[string]int, error)` — `SELECT category, COUNT(*) FROM knowledge_entries GROUP BY category`.
- `RecentKnowledgeEntries(ctx, limit int) ([]KnowledgeSummary, error)` — order by `created_at DESC` (the existing column; `updated_at` would surface edits as "new", which is not what we want).
- `IsWriterActive(ctx) (bool, error)` — `SELECT EXISTS(SELECT 1 FROM index_locks l WHERE l.lock_id = $1 AND l.backend_pid IN (SELECT pid FROM pg_stat_activity WHERE pid IS NOT NULL))`. `$1` is the writer `LockID` constant.

Each query lives in the storage file matching its table (`symbols.go`, `datastore.go`, `history.go`, `knowledge.go`, `writelock/`). Integration tests gated behind `//go:build integration`, following the existing pattern. The `IsWriterActive` test must include a "ghost row with dead backend_pid → returns false" case to lock in the liveness semantics.

`TopDatastoreTablesByEdgeCount` returns no `MigrationCount` because the current `datastore_tables` schema only carries a single `source_file_id` per row. `SourceFileCount` replaces it and is derivable from the edges join.

## CLI surface

```bash
# Report
projectlens report                                # markdown to stdout
projectlens report --format json                  # JSON to stdout
projectlens report --out report.md                # markdown to file (format inferred from extension)
projectlens report --format json --out report.json
projectlens report --top 10                       # top-N for packages / tables / coupling (default 10)

# Graph export
projectlens export graph                          # JSON to stdout, full dump
projectlens export graph --out graph.json
projectlens export graph --edges calls,reads_table # filter edge types (raw names)
projectlens export graph --include-evidence       # include EvidenceSpan blobs (default off, large)
```

### Flag rules

- `--format`: `markdown` or `json`. Default `markdown` for `report`. If `--out` is given and `--format` is not, format is inferred from the extension: `.md` / `.markdown` → markdown, `.json` → json. Explicit `--format` wins on conflict. Unknown extension with no `--format` → error.
- `--out`: writes atomically via temp file in the same directory + rename. Stdout if absent.
- `--top N`: integer, bounds `1..200`. Default `10`. Applies only to `TopPackages`, `TopTables`, `HighCoupling`, `RecentKnowledgeEntries`.
- `--edges` (graph only): comma-separated subset of the **raw edge_type vocabulary** currently stored in `edges`: `{calls, implements, imports, reads_table, writes_table, co_changes, knowledge_about, all}`. Default `all`. Invalid name → error. Raw names are exposed (no aliases) so the export and the filter use the same identifiers; matches what users see in the JSON `type` field. The allowed set is sourced from a single constant shared between the CLI validator and the export query.
- `--include-evidence` (graph only): boolean. When true, edge rows include their `properties.evidence` blob unchanged. Default false to keep export sizes manageable.
- Exit codes: `0` success, `1` runtime/internal error, `2` bad flags. Degraded index state does not change exit code (degradation is data, surfaced in the payload).

### Graph JSON schema (native)

```json
{
  "schema_version": "projectlens-graph/v1",
  "generated_at": "2026-05-21T12:34:56Z",
  "git_head": "abc123",
  "git_dirty": false,
  "nodes": [
    {
      "id": "sym:4821",
      "type": "symbol",
      "label": "Indexer.Run",
      "attrs": {
        "package": "internal/indexer",
        "file_id": 712,
        "kind": "method"
      }
    },
    {
      "id": "sym:9134",
      "type": "symbol",
      "label": "DB.Insert",
      "attrs": {
        "package": "internal/storage",
        "file_id": 401,
        "kind": "method"
      }
    },
    {
      "id": "table:postgres:public.orders",
      "type": "datastore_table",
      "label": "public.orders",
      "attrs": { "engine": "postgres", "schema": "public" }
    },
    {
      "id": "knowledge:42",
      "type": "knowledge",
      "label": "Orders table writes go through ReserveInventory",
      "attrs": { "category": "domain_knowledge" }
    }
  ],
  "edges": [
    {
      "source": "sym:4821",
      "target": "sym:9134",
      "type": "calls",
      "confidence": 1.0,
      "source_attr": "callgraph",
      "properties": {}
    },
    {
      "source": "knowledge:42",
      "target": "table:postgres:public.orders",
      "type": "knowledge_about",
      "confidence": 1.0,
      "source_attr": "agent",
      "properties": {}
    }
  ]
}
```

Edge endpoints (`source`, `target`) use the **same node-ID function** as the nodes cursor. There is one place that builds a node ID from `(type, id, …)` and both cursors call it. Endpoints that cannot be resolved to a node in the export are an export bug; an integration test asserts that the node-ID set is a superset of the union of edge endpoints.

`schema_version` is a literal string. Bump on breaking changes.

Node `type` values: `symbol`, `file`, `package`, `datastore_table`, `knowledge`. Edge `type` is the raw `edges.edge_type` value — currently one of `calls`, `implements`, `imports`, `reads_table`, `writes_table`, `co_changes`, `knowledge_about`. The exporter never renames or normalizes these; new types added to the indexer flow through automatically (and must be added to the `--edges` allow-list constant).

### Node ID function

A single function `nodeID(kind, id, attrs) string` resolves every node and every edge endpoint. The mapping covers every `source_type` / `target_type` value the indexer currently emits (verified from `internal/indexer/indexer.go`, `internal/datastore/indexer.go`, `internal/history/indexer.go`, `internal/storage/knowledge.go`):

| Kind             | ID format                              | Backing data                                                                 |
|------------------|----------------------------------------|------------------------------------------------------------------------------|
| `symbol`         | `sym:<symbols.id>`                     | `symbols` row                                                                |
| `file`           | `file:<files.id>`                      | `files` row                                                                  |
| `datastore_table`| `table:<engine>:<schema>.<name>`       | `datastore_tables` row; null schema rendered as empty string `table:<engine>:.<name>` |
| `package`        | `package:<package_name>`               | Derived from `package_name` columns                                          |
| `knowledge`      | `knowledge:<knowledge_entries.id>`     | `knowledge_entries` row                                                      |

Knowledge nodes are part of the export so `knowledge_about` edges resolve. The nodes cursor adds a fifth branch: `SELECT id, title FROM knowledge_entries`. No source/target type besides those five is currently produced; if a new one is added later, `nodeID` must be extended in the same change.

`source_attr` documents which indexer stage produced the edge: `parser`, `callgraph`, `sql_scanner`, `history`, `agent`, `migration`. ProjectLens's existing edge rows do not always carry this — where missing, emit `"unknown"`. Confidence-vocabulary work (Phase 2) tightens this later.

`properties` is the raw `edges.properties` JSONB unchanged. With `--include-evidence`, this is preserved; without, `properties.evidence` is stripped before write.

## Data flow

### Report

1. CLI parses flags, loads config, constructs `storage.DB` and `indexstate.Inspector` (which wraps the same provider router and git helpers `mcpserver` uses).
2. `report.NewBuilder(db, inspector, repoPath).Build(ctx)` is called.
3. Builder runs queries sequentially (no transaction needed across read-only tables):
   1. `GetLatestRunsByStage`
   2. `inspector.ProbeProviders(ctx)` → `[]ProviderHealth`
   3. `inspector.GitHeadAndDirty(ctx)` → `GitState`
   4. `IsWriterActive`
   5. `TopPackagesBySymbolCount`
   6. `TopDatastoreTablesByEdgeCount`
   7. `HighCouplingPairs`
   8. `KnowledgeStatsByCategory` + `RecentKnowledgeEntries`
   9. derive `Degraded`
   10. derive `Suggestions`
4. Returns `*Report`.
5. Selected renderer writes to `io.Writer` (stdout or temp file).
6. If `--out`, rename temp file to final.

### Graph export

1. CLI parses flags → opens `storage.DB`.
2. `export.NewGraphExporter(db).Export(ctx, w, opts)` is called.
3. Two `pgx.Rows` cursors:
   - Nodes cursor: streamed in five passes (no `UNION ALL` across heterogeneous columns):
     1. `SELECT id, package_name, name, kind, file_id FROM symbols` → emits `sym:<id>` nodes.
     2. `SELECT id, path, package_name FROM files` → emits `file:<id>` nodes.
     3. `SELECT id, engine, schema_name, name FROM datastore_tables` → emits `table:<engine>:<schema>.<name>` nodes.
     4. `SELECT DISTINCT package_name FROM symbols UNION SELECT DISTINCT package_name FROM files UNION SELECT DISTINCT package_name FROM summaries` → emits `package:<package_name>` nodes. There is no `packages` table; package identity is the column on those three tables.
     5. `SELECT id, title, category FROM knowledge_entries` → emits `knowledge:<id>` nodes so `knowledge_about` edges resolve.
   - Edges cursor: `SELECT source_type, source_id, target_type, target_id, edge_type, confidence, properties FROM edges WHERE edge_type = ANY($1)` honoring `--edges`. For each row, the exporter calls the same `nodeID(source_type, source_id, ...)` and `nodeID(target_type, target_id, ...)` used by the nodes cursor. For `datastore_table` endpoints the exporter joins `datastore_tables` to resolve `(engine, schema_name, name)` (one `LEFT JOIN` in the edge query, not a per-row lookup). For `package` endpoints (none today, reserved for future), the package name is read from a property field.
4. Writes JSON incrementally using `encoding/json` Encoder for each row, framed manually:
   - Write `{"schema_version":...,"generated_at":...,"git_head":...,"git_dirty":...,`
   - Write `"nodes":[`, iterate node cursor with comma separation, write `]`,
   - Write `"edges":[`, iterate edge cursor with comma separation, write `]}`.
5. Bounded memory: one row at a time.

## Error handling

- DB connection failure → exit 1, stderr `report: connect db: <err>`.
- Per-section query failure during report build → log warning to stderr, append a `StageDegradation` entry with the error, continue. Report still ships with partial data so the user can see what worked.
- Provider probe timeout → already handled by existing `probeProviders`. Treated as degraded provider.
- Git head unreachable → empty `Git.Head`, `Dirty = false`. Not fatal. No degraded entry (matches `index_status` behavior).
- `--out` write failure → exit 1. No partial file because of atomic rename.
- Graph export mid-stream failure → flush what was already written, exit 1, stderr error. JSON may be truncated. Documented behavior; users should treat non-zero exit as "output may be incomplete".
- Invalid flags → exit 2, cobra prints usage. Includes `--top` out of range, unknown `--edges` name, unknown extension with no `--format`.

## Concurrency with the writer

Read-only queries can race with active `bootstrap`/`reindex` writes. This is acceptable. Different sections of the report may reflect slightly different points in time during an active write. The report header surfaces `writer_active: true` based on `index_locks`, signalling to readers that numbers may shift.

The graph export is point-in-time per cursor. Nodes and edges streams open separately, so a write between them can produce edges that reference a node version captured a moment earlier. Documented; not corrected. Phase 2 freshness work can revisit if needed.

## Markdown report layout

```
# ProjectLens Report

**Generated:** 2026-05-21T12:34:56Z
**Repo:** /path/to/target
**Git HEAD:** abc123 (dirty)
**Writer active:** no

## Index Freshness

| Stage      | Status     | Completed              | Age      | Files |
|------------|------------|------------------------|----------|-------|
| code       | completed  | 2026-05-21T11:00:00Z   | 1h 34m   | 2913  |
| summarize  | completed  | ...                    | ...      | ...   |
| ...        |            |                        |          |       |

## Providers

| Role        | Provider   | State      |
|-------------|------------|------------|
| embedder    | ollama     | reachable  |
| summarizer  | anthropic  | configured |

## Top Packages (by symbol count)

| Package                 | Symbols | Files |
|-------------------------|---------|-------|
| internal/indexer        | 142     | 18    |
| ...                     |         |       |

## Top Datastore Tables (by edge count)

| Table              | Engine | Reads | Writes | Source Files |
|--------------------|--------|-------|--------|--------------|
| public.orders      | sql    | 47    | 12     | 9            |
| ...                |        |       |        |              |

## High-Coupling File Pairs (co-change)

| File A                | File B                 | Co-changes |
|-----------------------|------------------------|------------|
| internal/indexer/...  | internal/storage/...   | 23         |

## Knowledge Inventory

- Total entries: 42
- By category: lesson 12, best_practice 9, convention 8, domain_knowledge 7, how_to 4, decision 2
- Recent (10 most recent listed in JSON output)

## Degraded / Missing

- `embed` stage age 73h — suggested: `projectlens reindex`
- Provider `summarizer` (anthropic): rate limited — last error 18m ago

## Suggested Agent Questions

- "What does `internal/indexer` do?" → `get_package_summary internal/indexer`
- "Who reads `public.orders`?" → `get_table_context public.orders`
- "Which files change with `internal/storage/edges.go`?" → `get_coupling internal/storage/edges.go`
- "Have we captured anything about indexer?" → `search_knowledge indexer`
```

JSON output mirrors the `Report` struct one-to-one.

## Testing

### Unit (no DB)

- `internal/report/markdown_test.go` — table-driven against fixture `Report` structs: empty, fully populated, fully degraded, partial degradation. Asserts presence of section headers, top-N counts, degraded-section text, suggestion lines.
- `internal/report/json_test.go` — fixture → marshal → unmarshal → field equality. Catches schema drift.
- `internal/export/graph_test.go` — fake `pgx.Rows` implementations or in-memory iterators. Assert streamed JSON parses, schema_version matches, nodes/edges arrive in declared order, comma framing is correct, `--include-evidence` flag toggles the `properties.evidence` field. Includes a **graph-closure invariant test**: every `edge.source` and `edge.target` value appears in the set of emitted node IDs. Test fixtures cover every source_type / target_type combination currently produced by the indexer (symbol↔symbol, file↔file, symbol→datastore_table, knowledge→{symbol,file,datastore_table,package}).

### Integration (`//go:build integration`)

- `internal/report/builder_integration_test.go` — seeds Postgres via existing test fixtures, runs `Builder.Build()` against a stub `Inspector`, asserts:
  - `TopPackages` populated and ordered.
  - `TopTables` populated; `ReadRefs` / `WriteRefs` reflect seeded `reads_table` / `writes_table` edges; `SourceFileCount` reflects distinct source files.
  - `Stages` reflects seeded `index_runs` rows.
  - `Degraded` empty when all stages present, recent, and providers in `reachable` / `configured` state.
  - `Degraded` populated when a stub `Inspector` returns a provider with `State="error"` or `"not_configured"`.
  - `WriterActive` flips true when a holder row with a live `pg_backend_pid()` is inserted, and stays false when a row references a backend pid that does not exist in `pg_stat_activity`.
- `internal/export/graph_integration_test.go` — seeds files + symbols + edges, exports to `bytes.Buffer`, validates JSON parses, node and edge counts match seed, `--edges` filtering works.

### CLI smoke tests

- `cmd/projectlens/report_test.go` — invokes `newReportCmd().Execute()` with `--out tmpfile`:
  - file written, format inferred from extension, exit code 0.
  - `--top 0` and `--top 300` → exit 2.
  - `--format html` → exit 2.
  - `--out foo.txt` with no `--format` → exit 2 (unknown extension).
- `cmd/projectlens/export_test.go` — `--edges call,bogus` → exit 2.

### Coverage

Aim for 80%+ on `internal/report` and `internal/export`. CLI files are thin; smoke tests cover them.

## Shared `internal/indexstate` package

New package `internal/indexstate/`. Contains:

- Types lifted from `internal/mcpserver/types.go`:
  - `StageFreshness`
  - `ProviderHealth` (keeps existing state vocabulary: `reachable`, `configured`, `not_configured`, `error`)
  - `GitState`
- `Inspector` interface and default implementation:
  - `ProbeProviders(ctx) []ProviderHealth` — wraps the existing router + summarizer probes (moved from `internal/mcpserver/handlers.go`).
  - `GitHeadAndDirty(ctx) GitState` — moved from `internal/mcpserver/handlers.go`.

`mcpserver` keeps its MCP payload types but imports the shared structs and delegates probing to the same `Inspector` so the report and `index_status` cannot drift. `index_status` behavior is unchanged.

This refactor is part of this spec because `report` consumes both the types and the probing behavior, and because the boundary review flagged that a `storage.DB`-only builder cannot exercise provider health.

## Sequencing for implementation

1. Create `internal/indexstate` and move `StageFreshness`, `ProviderHealth`, `GitState`, `probeProviders` (becomes `Inspector.ProbeProviders`), and `gitHeadAndDirty` (becomes `Inspector.GitHeadAndDirty`). Update `internal/mcpserver/handlers.go` to delegate. Keep `index_status` MCP tests green.
2. Add the new storage queries with integration tests.
3. Implement `internal/report` (Builder + Markdown + JSON renderers + unit tests).
4. Wire `cmd/projectlens/report.go` + CLI smoke tests.
5. Implement `internal/export` (GraphExporter streaming + unit tests + integration test).
6. Wire `cmd/projectlens/export.go` + CLI smoke tests.
7. Update `README.md` (mention `report` + `export graph` commands), `CLAUDE.md` (CLI commands section, repository structure), `docs/AGENT_SETUP.md` if relevant.

## Open questions left for the next pass

These are deferred to other sessions and are not blocking this spec:

- Phase 2: edge confidence vocabulary, `source_attr` backfill, MCP exposure of confidence.
- Whether to add a `generate_report` MCP tool later (current decision: CLI-only v1).
- Where generated reports should live by convention (target repo `docs/`, `.projectlens/`, or projectlens checkout) — current decision: user picks via `--out`, no default location.
- Whether report should learn to render Graphify-compatible `graph.json` as an extra format (current decision: native only).
