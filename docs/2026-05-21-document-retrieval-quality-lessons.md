# Document Retrieval Quality Lessons From pnphive

Date: 2026-05-21
Status: lessons and future-spec guidance
Source: `docs/2026-05-21-pnphive-comparison.md`

## Purpose

Capture pnphive's retrieval-quality lessons for future ProjectLens document and business-context search.

This document applies to broad, document-like sources: PRs, reviews, docs, Jira, Confluence, and agent knowledge. It does not propose replacing ProjectLens's typed symbol/table/graph tools.

## Decision

ProjectLens should use a stronger retrieval stack for document-like context than plain vector search.

pnphive's practical recipe is:

- vector search,
- lexical search,
- reciprocal rank fusion,
- cross-encoder reranking,
- optional query rewriting,
- source-aware context formatting.

ProjectLens should borrow this where it fits, while preserving exact typed tools for code and datastore questions.

## Boundary

Use document retrieval quality techniques for:

- PR and review comments,
- Jira issues and comments,
- Confluence pages and comments,
- repo docs,
- captured knowledge,
- broad "why" and business-context questions.

Do not use generic document retrieval as a replacement for:

- `find_symbol`,
- `get_symbol_context`,
- `get_table_context`,
- `get_change_history`,
- `get_coupling`,
- exact package/symbol/table lookup.

Typed tools should remain deterministic and evidence-span oriented.

## pnphive Lessons To Reuse

### 1. Vector-Only Search Is Not Enough

Business context uses synonyms, product language, ticket language, and human shorthand. Code identifiers often do not appear in the exact form the user asks.

Vector search helps, but it can miss exact IDs, issue keys, table names, flags, enum values, and error strings. Lexical search is still necessary.

ProjectLens's document lane should combine semantic and lexical retrieval.

### 2. Reciprocal Rank Fusion Is a Good Default

pnphive combines vector and `tsvector` results with reciprocal rank fusion.

This is a good default because it is:

- simple,
- deterministic,
- explainable,
- independent of raw score calibration across different retrieval methods.

ProjectLens can use this for document-like sources before adding more complex ranking.

### 3. Reranking Helps Broad Corpora

pnphive uses a cross-encoder reranker after building a candidate pool.

ProjectLens should consider reranking when sources become broad enough that top-K quality matters:

- PRs and review comments,
- Jira,
- Confluence,
- large docs sets.

Reranking should be optional and provider-backed. Exact symbol/table tools should not depend on it.

### 4. Query Rewriting Is Useful But Should Be Scoped

pnphive's CLI can ask Claude for alternate query phrasings, then retrieve across all variants and rerank against the original query.

ProjectLens should treat query rewriting as a broad-context feature, not as a default for exact tools.

Good use cases:

- "why does this behavior exist?"
- "what customer context explains this?"
- "what did reviewers decide about this flow?"

Poor use cases:

- exact symbol lookup,
- exact table lookup,
- deterministic impact analysis,
- low-latency MCP tool calls where extra LLM calls would surprise the user.

### 5. Source-Aware Caps Prevent One Source From Dominating

Long PR threads, Confluence pages, or Jira tickets can produce many chunks that crowd out other evidence.

Future retrieval should consider:

- per-source caps,
- per-document caps,
- diversity by source type,
- preference for direct anchors when the query names a symbol/table/path,
- explicit `include_history` or source filters.

### 6. Provenance Formatting Is Part Of Retrieval Quality

pnphive returns chunks with source headers and metadata. This makes retrieved context usable.

ProjectLens should make document results easy to verify:

- source type,
- external ID,
- URL,
- title,
- author if allowed,
- created/updated timestamp,
- section heading or comment identity,
- resolved anchors if any.

Without provenance, high-quality ranking still produces low-trust answers.

## Proposed Future Shape

### Retrieval Pipeline For Documents

For document-like sources:

1. Build lexical candidates.
2. Build vector candidates.
3. Fuse candidates with RRF.
4. Apply source/document diversity caps.
5. Optionally rerank.
6. Return structured hits with provenance.

### MCP Surface Options

Two reasonable designs:

- Add `search_documents` or `search_business_context`.
- Or extend `search_go_context` to optionally include document context for broad queries.

The first option is cleaner for agent tool selection. The second option may feel smoother to users. Either way, exact tools should stay exact.

### Ranking Inputs

Document ranking should be able to use:

- query text,
- source type,
- recency,
- anchor match strength,
- exact ID/path/table/symbol mentions,
- current vs historical lineage,
- source confidence or extraction quality.

Do not overfit this early. Start with simple fusion, then add weighting only after observing failures.

## Minimum Future Acceptance Criteria

Before broad document retrieval is considered shippable:

- Lexical and vector retrieval both contribute candidates.
- Fusion behavior is deterministic and tested.
- Reranking can be disabled.
- Results include stable provenance.
- Source/document caps prevent a single long document from monopolizing top-K.
- Exact symbol/table tools remain unaffected.
- Tests cover lexical-only hit, vector-only hit, duplicate fusion, source caps, and empty/degraded retrieval.

## Risks

- Rerankers can add latency and local resource pressure.
- Query rewriting can create hidden LLM egress and cost.
- Ranking broad business documents beside code chunks can bury precise code evidence.
- Overly complex ranking too early can become hard to debug.

## Non-Goals

- Replacing typed graph retrieval.
- Adding a full search DSL immediately.
- Making every MCP query call an LLM for rewriting.
- Ranking all source types with one global score without source awareness.

## Recommendation

Use pnphive's retrieval quality stack for the future document lane:

1. Start with lexical + vector + RRF.
2. Add source/document caps.
3. Add optional reranking when broad corpora justify it.
4. Keep query rewriting as an explicit broad-context enhancement.
5. Preserve typed tools as the high-confidence path for code, tables, and graph relationships.

