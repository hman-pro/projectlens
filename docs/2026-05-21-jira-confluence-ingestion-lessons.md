# Jira and Confluence Ingestion Lessons From pnphive

Date: 2026-05-21
Status: lessons and future-spec guidance
Source: `docs/2026-05-21-pnphive-comparison.md`

## Purpose

Capture what pnphive already learned from ingesting Jira and Confluence, so ProjectLens can reuse the design lessons later without making Jira or Confluence immediate priorities.

This document is not an implementation plan. It defines the product value, constraints, guardrails, and likely shape of a future ProjectLens document-ingestion lane.

## Decision

Jira and Confluence are worth adding eventually because they contain high-value business and product context that code structure cannot recover:

- Why a behavior exists.
- Which customer or product problem motivated it.
- What decisions were made outside code review.
- What rollout or support nuance is attached to a feature.
- Which historical discussions explain surprising code.

They should not be implemented before the current top priorities:

1. Inspectable artifacts.
2. Run observability.
3. PR / review-context ingestion.

PR/review ingestion should come first because it has lower privacy/auth complexity and stronger file/path/line adjacency. Jira and Confluence should build on the same document lane once provenance, redaction, run tracking, and retrieval behavior are proven.

## pnphive Lessons To Reuse

### 1. Treat Business Sources As First-Class Context

pnphive proved that Jira and Confluence are not secondary docs. They often hold the reason behind code:

- Jira descriptions and comments capture bug reports, feature pressure, acceptance details, and support/customer nuance.
- Confluence pages capture design rationale, meeting notes, product explanations, and onboarding material.

ProjectLens should eventually make these searchable from the same agent workflow as code, tables, history, and knowledge.

### 2. Preserve Source Identity

pnphive stores chunks with explicit `source`, `repo`, `source_id`, and metadata.

ProjectLens should preserve at least:

- Source type: `jira`, `jira_comment`, `confluence`, `confluence_comment`.
- Stable external ID: issue key, page ID, comment ID.
- Stable URL.
- Title / summary.
- Author where allowed.
- Created and updated timestamps.
- Project key or Confluence space key.
- Section heading for Confluence chunks.
- Issue status/type/priority/labels/components/fix versions for Jira.

Without source identity, business-context answers become hard to verify.

### 3. Incremental Behavior Is Mandatory

pnphive's Jira ingestion uses update timestamps and watermarks. Confluence uses page versions and content hashes.

ProjectLens should not ship Jira/Confluence ingestion as full-refresh-only. Future ingestion should support:

- Jira updated-since filtering.
- Confluence page-version or updated timestamp checks.
- Idempotent re-runs.
- Content-hash skip for unchanged chunks.
- Watermark advancement only after successful runs.
- Partial/failed run reporting without advancing freshness.

### 4. Lineage Matters

pnphive's append-only chunk model keeps old versions while default retrieval searches current chunks.

ProjectLens should consider append-only lineage for imported business documents:

- `is_current`
- `inserted_at`
- `superseded_at`
- `content_hash`
- `run_id`

This is useful for "what changed in the docs/ticket?" questions and for avoiding silent overwrites of context that previously influenced code.

### 5. Redaction Must Be Built In, Not Bolted On

pnphive scrubs chunks before embedding:

- Drop private-key chunks.
- Redact JWT-like tokens.
- Redact high-entropy environment values.
- Track scrub counts in run statistics.

ProjectLens should require redaction before any Jira/Confluence chunk is embedded or summarized. The scrubber should produce counters that flow into run observability and reports.

### 6. Comments Are High Value

pnphive indexes Jira comments and Confluence comments separately from main bodies.

ProjectLens should not ingest only issue/page bodies. Comments often contain:

- Final clarifications.
- Customer-specific constraints.
- Rollout corrections.
- Engineering caveats.
- Links to related work.

Each comment should be its own document/chunk source with stable provenance.

### 7. Query Quality Needs Hybrid Retrieval

Business-source language rarely matches code identifiers exactly.

ProjectLens should not rely on vector-only lookup for Jira/Confluence. The future document lane should use:

- lexical search,
- vector search,
- reciprocal rank fusion,
- optional reranking,
- source-aware ranking or caps so one long ticket/page does not dominate top-K.

### 8. Scope Control Is Product Quality

pnphive intentionally scoped Jira to specific projects and Confluence to specific spaces.

ProjectLens should not default to "all accessible Jira/Confluence." Future config should require explicit allow-lists:

- Jira projects.
- Confluence spaces.
- Optional page/project exclusions.
- Optional date windows.
- Optional comment inclusion settings.

Broad access increases privacy risk and lowers retrieval quality.

## Proposed Future Shape For ProjectLens

### Storage Direction

Use the existing typed model where possible, but add a document lane for business sources.

Likely shape:

- `documents`
  - source type, external ID, title, URL, body text, metadata, last synced timestamp.
- `chunks`
  - document-backed chunks with source type and source URI.
- `edges`
  - optional links from document chunks to symbols, files, packages, tables, or knowledge entries.
- `index_runs`
  - detailed source-specific run records.

Jira/Confluence should not replace typed code/table/history entities. They should add human context around them.

### Anchoring Direction

When possible, imported chunks should link to typed ProjectLens entities:

- Jira issue mentions a table name -> `document_about datastore_table`.
- Jira issue mentions a package/path -> `document_about file/package`.
- Confluence section mentions a symbol -> `document_about symbol`.
- PR/Jira/Confluence links reference each other -> `related_document`.

Anchoring should be best-effort and explain unresolved references rather than silently inventing links.

### Retrieval Direction

Future retrieval should support questions like:

- "What Jira context exists for this behavior?"
- "What Confluence docs explain this package?"
- "Why does this flow branch this way?"
- "Which tickets or docs mention this table?"
- "What business context should I read before changing this symbol?"

The likely MCP surface should be explicit rather than hidden:

- Either extend `search_go_context` to include document context when useful.
- Or add a separate `search_business_context` / `search_documents` tool.

Do not force exact symbol/table tools to return broad business-context chunks unless the caller asks for them.

## Minimum Future Acceptance Criteria

Before Jira/Confluence ingestion is considered shippable:

- Source allow-list is explicit in config.
- Auth failures are reported clearly and do not corrupt freshness.
- Incremental runs are idempotent.
- Watermarks advance only on successful runs.
- Every chunk has stable provenance and a URL.
- Comments are modeled separately from parent bodies.
- Redaction runs before embedding.
- Scrub counters are stored in run metadata.
- Report output shows source coverage, freshness, failures, and redaction counts.
- Tests cover pagination, incremental re-run, unchanged-content skip, failed-run watermark behavior, and redaction.

## Risks

- Jira/Confluence content can contain customer-sensitive or credential-like data.
- Search quality can degrade if business chunks overwhelm code chunks.
- Auth and API shape can vary by tenant, permissions, and product version.
- Confluence storage-format HTML requires careful normalization.
- Jira comments and updated timestamps can make incremental semantics subtle.
- Anchors to current code can go stale as code moves.

## Non-Goals For The First Future Pass

- Indexing every accessible Jira project or Confluence space.
- Slack ingestion.
- Customer-specific project ingestion by default.
- Full historical time-travel UI.
- Replacing code/table/history MCP tools with generic document search.
- Automatic external summarization of full Jira/Confluence pages without explicit egress policy.

## Recommendation

When ProjectLens is ready for business-source ingestion, implement Jira and Confluence after PR/review ingestion proves the document lane.

The correct path is:

1. Build inspectable artifacts and richer run observability.
2. Add PR/review ingestion with provenance, redaction, and anchoring.
3. Generalize the document lane.
4. Add Jira with strict project allow-lists and update-time incremental sync.
5. Add Confluence with strict space allow-lists and page-version/content-hash incremental sync.

This preserves ProjectLens's core advantage: typed code intelligence enriched by human context, not replaced by a flat business-document RAG.
