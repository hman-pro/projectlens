# Confidence and Provenance Design

Date: 2026-05-22
Status: design/spec for Phase 2 of `docs/2026-05-21-graphify-comparison.md` (Confidence + Provenance).
Predecessor: `docs/2026-05-21-graphify-comparison.md` (esp. sections "Graph Model and Confidence", "Phase 2").
Successor priority: `docs/2026-05-21-next-priorities.md` (Run Observability remains the synthesis #2 if this work deferred).

## Goal

Make every edge in the polymorphic `edges` table carry a trustable origin and a simple, agent-readable confidence vocabulary. Surface that in MCP responses, the report, and the graph export so downstream consumers can reason about trust without scraping prose.

## Vocabulary

Two orthogonal axes:

1. **Provenance (source of the assertion)** — text enum, stored on the edge:
   - `parser` — Go type-checker / `go/packages` (deterministic syntactic fact).
   - `callgraph` — CHA-derived call edge.
   - `sql_scanner` — Go SQL scanner / migration parser (datastore edges).
   - `history` — git log / co-change.
   - `knowledge` — user/agent-asserted knowledge anchor.
   - `docs` — future Confluence/Jira/PR docs lane.

2. **Confidence class (epistemic strength)** — text enum, graphify-compatible:
   - `extracted` — directly observed in source artifact. Highest trust.
   - `inferred` — derived by reasonable rule with known precision loss (e.g. CHA over-approximation, statistical co-change).
   - `ambiguous` — multiple resolutions possible, flagged for review.

Optional numeric `score` (existing `confidence REAL` column) remains the per-edge magnitude (e.g. co-change strength). It is **not** the class; class and score answer different questions.

## Schema

Two migrations shipped:

- `migrations/006_edge_provenance.up.sql` — adds the two columns, the `confidence_class` CHECK, and the partial indexes.
- `migrations/007_edge_provenance_check.up.sql` — adds the `provenance` CHECK constraint to enforce the documented vocabulary (review fix; provenance was free text without it).

Final shape:

```sql
ALTER TABLE edges
  ADD COLUMN provenance TEXT,
  ADD COLUMN confidence_class TEXT;

ALTER TABLE edges
  ADD CONSTRAINT edges_confidence_class_check
    CHECK (confidence_class IS NULL OR confidence_class IN ('extracted','inferred','ambiguous'));

ALTER TABLE edges
  ADD CONSTRAINT edges_provenance_check
    CHECK (provenance IS NULL OR provenance IN ('parser','callgraph','sql_scanner','history','knowledge','docs'));

CREATE INDEX idx_edges_provenance ON edges(provenance) WHERE provenance IS NOT NULL;
CREATE INDEX idx_edges_confidence_class ON edges(confidence_class) WHERE confidence_class IS NOT NULL;
```

Both CHECKs allow NULL so the columns can be added to a populated table without an immediate UPDATE. Backfill (below) fills them; new producers should ship a CHECK extension in the same migration that adds them.

Keep `confidence REAL` as the numeric score column. Existing coupling query (`COALESCE(e.confidence, 0)`) keeps working unchanged.

## Backfill Rules

One-shot SQL (idempotent — use `WHERE provenance IS NULL`):

| edge_type | provenance | confidence_class | score | rationale |
|---|---|---|---|---|
| `calls` | `callgraph` | `inferred` | unset | CHA over-approximates interface dispatch — upper bound, not exact |
| `implements` | `parser` | `extracted` | unset | Type-checker fact |
| `imports` | `parser` | `extracted` | unset | Type-checker fact |
| `co_changes` | `history` | `inferred` | existing or recomputed pair strength | Statistical from git |
| `knowledge_about` | `knowledge` | `extracted` | 1.0 | User/agent-asserted |
| `reads_table` (future) | `sql_scanner` | `extracted` for static SQL, `ambiguous` for string-concat | varies | Depends on scanner finding |
| `writes_table` (future) | `sql_scanner` | same as above | varies | |

Backfill ships as the top-level `projectlens index-backfill-provenance` CLI subcommand (single hyphenated name; not nested under a `index` group). It is partial-field repair, not blanket overwrite — a row that already has one of the two columns set keeps that value while the other is filled:

```sql
UPDATE edges
SET provenance = COALESCE(provenance, $2),
    confidence_class = COALESCE(confidence_class, $3)
WHERE edge_type = $1
  AND (provenance IS NULL OR confidence_class IS NULL);
```

This matters for two real cases: rows inserted before migration 006 (both columns NULL) and rows from older or broken writers that filled only one column. Re-runs on a fully-filled set are no-ops. Pipeline writes the new columns going forward (see "Writer changes" below).

## Writer Changes

Every edge writer attaches provenance + class at insert time. The actual call sites in this checkout (replacing the earlier draft list):

- `internal/indexer/indexer.go` — converts graph builder edges to `storage.EdgeRecord` and assigns provenance via the local `edgeProvenance(edgeType)` helper (calls=callgraph/inferred, implements=parser/extracted, imports=parser/extracted).
- `internal/history/indexer.go` — co_changes edges set provenance=history, confidence_class=inferred, and numeric `Confidence` = `CouplingPair.Strength`.
- `internal/datastore/indexer.go` — reads_table / writes_table edges set provenance=sql_scanner, confidence_class=extracted.
- `internal/storage/knowledge.go` — knowledge_about edges set provenance=knowledge, confidence_class=extracted, numeric Confidence=1.0.

`internal/graph/graph.go` itself does not write storage records — it returns `graph.Edge` values that the indexer transforms. Future writers must be added to this list and to the backfill table above in the same change.

A grep-style acceptance check covers this: every caller of `db.InsertEdges` must produce records with non-empty `Provenance` and `ConfidenceClass`.

`storage.EdgeRecord` and `storage.EdgeResult` both gain:

```go
Provenance      string `json:"provenance,omitempty"`
ConfidenceClass string `json:"confidence_class,omitempty"`
```

`storage.InsertEdges` extends the INSERT to include the two columns; ON CONFLICT updates them too. Empty strings round-trip as SQL NULL via the `nullableString` helper so the CHECK constraints stay satisfied.

## MCP Surface

`internal/mcpserver/types.go`:

- `SymbolHit`, `TableEdgeHit`, and `CouplingEntry` each gain optional `Provenance` and `ConfidenceClass` fields. Backwards compatible — omitted when unset. (`EvidenceSpan` is only file/line and remains unchanged — putting class on it would carry it on results that have no edge.)
- All three edge-bearing payloads — `SymbolContextPayload`, `TableContextPayload`, `CouplingPayload` — gain a top-level optional `Trust { worst_class }` field.
- `storage.GetCouplingEdges` is extended to return provenance + class alongside the strength so the coupling handler can populate the new fields.
- The shared `worstClassOf([]string)` helper (and the SymbolHit-specific `worstClass(...)` wrapper) compute the response-level worst class from the per-hit classes.
- Handlers that build edge-bearing responses must populate the fields. Three are pilots in this phase: `handleGetSymbolContext`, `handleGetTableContext`, and `handleGetCoupling`.

## Report Surface

`internal/report` adds a `EdgeTrust []storage.EdgeConfidenceStat` field on `Report` that powers a markdown "Edge Trust (provenance + confidence)" section and is emitted in the JSON renderer:

| Edge type | Provenance | Extracted | Inferred | Ambiguous | Unknown | Total |
|---|---|---|---|---|---|---|
| calls | callgraph | 0 | 283713 | 0 | 0 | 283713 |
| implements | parser | 12751 | 0 | 0 | 0 | 12751 |
| ... | | | | | | |

The "Unknown" column counts rows missing `confidence_class`; the partial-field backfill above is expected to keep this column at zero. A non-zero Unknown is a degradation signal worth surfacing alongside stage-staleness.

A `--show-ambiguous-only` filter is intentionally out of scope for this phase. If it lands later it should live as a `report.Options` flag with its own renderer toggle.

## Export Surface

`internal/export/graph.go` adds top-level `provenance` and `confidence_class` fields to each edge document (alongside the existing `source`, `target`, `type`, `confidence`, `source_attr`, and optional `properties`). They live at the edge level, not inside a nested `attrs` object — only nodes carry `attrs`. Schema version bumps to `projectlens-graph/v2`. Consumers reading v1 keep working — the new fields are additive.

## Tests

- `internal/storage/edges_integration_test.go::TestInsertEdgesProvenance` — round-trip insert/upsert with the new columns, NULL handling, CHECK violation on invalid class.
- `internal/storage/edges_integration_test.go::TestBackfillProvenance_PartialRepair` — three-row partial-repair fixture: NULL/NULL, prov-only, class-only. Asserts repair fills the gap, preserves set values, and is idempotent on rerun.
- `internal/mcpserver/handlers_integration_test.go::TestIntegration_GetSymbolContext_ProvenanceAndTrust` — graph-derived hits carry provenance + class; payload `Trust.WorstClass` set.
- `internal/mcpserver/handlers_integration_test.go::TestIntegration_GetTableContext_TrustAndProvenance` — same shape for table edges (skips when the live index has no reads/writes_table edges).
- `internal/mcpserver/handlers_integration_test.go::TestIntegration_GetCoupling_TrustAndProvenance` — coupling entries carry provenance=history, payload `Trust.WorstClass` set.
- `internal/report/markdown_test.go::TestMarkdownRenderer_SectionsPresent` asserts the `## Edge Trust (provenance + confidence)` header and at least two rendered rows (`calls`/`callgraph` and `implements`/`parser`).
- `internal/report/json_test.go::TestJSONRenderer_RoundTrip` seeds `EdgeTrust` into `fixtureReport` and asserts the rows survive a JSON round-trip.
- `internal/export/graph_integration_test.go::TestExportGraph_EdgeProvenance` decodes `provenance` + `confidence_class` on every exported edge, asserts the v2 schema version, and verifies known type→provenance mappings for whichever edge types are present in the current index. Tracks a `seen` set: fails fast if zero known types appear, and logs the unseen ones so a sparse fixture (e.g. no datastore stage producing `reads_table`/`writes_table`) is visible in the run output rather than silently bypassing the assertion. Skips when the live DB has no edges at all.

## Acceptance Criteria

1. Migrations 006 + 007 apply cleanly; partial indexes + both CHECK constraints present on `edges`.
2. After running `projectlens index-backfill-provenance`, the live invariant `SELECT COUNT(*) FROM edges WHERE provenance IS NULL OR confidence_class IS NULL` returns **0**. Re-running the command updates 0 rows.
3. Every caller of `db.InsertEdges` produces records with non-empty `Provenance` and `ConfidenceClass` per the writer table above. Grep on `EdgeRecord{` confirms.
4. `projectlens report` shows the per-type Edge Trust breakdown in both markdown and JSON renderers, with Unknown = 0.
5. `projectlens export graph` outputs `projectlens-graph/v2`; every edge document carries `provenance` + `confidence_class`; closure invariant (every edge endpoint resolves to a node) holds for all edge selectors.
6. All three pilot MCP tools — `get_symbol_context`, `get_table_context`, `get_coupling` — emit per-hit provenance/class and a top-level `Trust.worst_class` when their result set contains edges.
7. Unit + integration suites green.

## Risks

- **Backfill misclassification.** CHA edges marked `extracted` would over-promise. Rule table above guards against this; reviewers should sanity-check before merging the backfill.
- **JSON shape churn for MCP consumers.** Adding fields is additive but consumers asserting exact key sets will break. Document the additive contract in `agent/skills/use-projectlens/SKILL.md`.
- **Schema version bump.** Graph export consumers may pin to v1; v2 fields are additive so existing parsers keep reading the document. Don't drop any v1 field.
- **Performance.** Two new indexed columns on a 295k-row table are cheap. Backfill is a single `UPDATE` per edge_type — bounded by row count.

## Non-Goals

- Replacing the numeric `confidence` score with class-only data.
- Building per-MCP-tool confidence dashboards.
- Backfilling historical edges deleted by past `reindex --full` runs.
- Confidence on chunks/embeddings (separate concern).

## Sequence

Original implementation order (delivered 2026-05-22):

1. Migration 006 adds columns + class CHECK + indexes.
2. Extend `storage.EdgeRecord`, `storage.EdgeResult`, `InsertEdges`.
3. Update writer call sites (`internal/indexer/indexer.go`, `internal/history/indexer.go`, `internal/datastore/indexer.go`, `internal/storage/knowledge.go`).
4. Ship `projectlens index-backfill-provenance` subcommand.
5. Extend MCP types + pilot tool (`get_symbol_context`).
6. Extend report (`internal/report` + `internal/storage/inspect.go::EdgeConfidenceBreakdown`).
7. Extend graph export to schema v2.
8. Tests at each step; full integration sweep at the end.

Review follow-up (post-review changes shipped after the design review found gaps):

9. Migration 007 adds the provenance CHECK constraint.
10. Switch backfill to partial-field repair via COALESCE so a row missing only one column is repaired.
11. Extend `TableEdgeHit` + `CouplingEntry` with provenance/class; surface `Trust` on `TableContextPayload` and `CouplingPayload`.
12. Extend `storage.CouplingResult` + `GetCouplingEdges` to return provenance/class.
13. Update this doc to match shipped names (migration 006/007, top-level `index-backfill-provenance`), correct writer paths, and drop `--show-ambiguous-only` from this phase.

## Open Questions

1. Should `confidence_class` live on the chunk too (for retrieval ranking), or stay edge-only? Default: edge-only this pass.
2. Should `--show-ambiguous-only` land as a `report.Options` filter in a follow-up phase? Default: yes, once a producer actually emits ambiguous edges.
3. Should remaining read paths (`get_change_history`, `search_go_context`, `get_package_summary`) also surface trust? They don't return edges today, so this is a separate question about exposing chunk-level confidence.
