# Independent Pipeline Stages + New MCP Tools (Phase 5) Design

**Date:** 2026-04-17
**Status:** Draft
**Goal:** Refactor the monolithic indexer into independent, composable pipeline stages and add MCP tools for history and coupling queries.

## Independent Stages

Currently `bootstrap` and `reindex` run everything monolithically. Refactor into:

```
projectlens index code       — census + parse + chunk + call graph
projectlens index datastore  — scan migrations + SQL in code (Phase 2, done)
projectlens index history    — git log + coupling detection (Phase 3)
projectlens index docs       — Confluence + Jira fetch (Phase 4)
projectlens index embed      — embed ALL chunks missing embeddings (any source_type)
projectlens index summarize  — generate summaries for anything missing one
projectlens index all        — run all stages in order
```

### `index embed` (standalone)

```sql
SELECT c.id, c.content FROM chunks c
LEFT JOIN embeddings e ON e.chunk_id = c.id
WHERE e.id IS NULL
```

Embed all unembedded chunks regardless of `source_type`. One pass for code + docs + migration chunks. Uses configured embedding provider (Ollama/OpenAI).

### `index summarize` (standalone)

Generate summaries for:
- Packages without a summary (existing)
- Datastore tables without a summary (new — use Claude to describe table purpose)
- Documents without a summary (future)

### `index all`

```go
func IndexAll(ctx, db, repoPath, cfg) error {
    IndexCode(ctx, db, repoPath, cfg)      // existing reindex --full
    IndexDatastore(ctx, db, repoPath, cfg) // Phase 2
    IndexHistory(ctx, db, repoPath, cfg)   // Phase 3
    IndexDocs(ctx, db, repoPath, cfg)      // Phase 4
    IndexSummarize(ctx, db, cfg)           // standalone
    IndexEmbed(ctx, db, cfg)               // standalone
}
```

### Incremental via git

The future `index all --incremental`:
```
git log --since=<last_index> --name-only
  → changed Go files → reindex code (only those files)
  → changed migrations → reindex datastore
  → new commits → update history + coupling
  → ticket IDs in messages → link docs
  → new chunks → embed
```

## New MCP Tools

### `get_change_history`

**Purpose:** Show recent changes for a file or symbol.

**Input:** `name` (file path or symbol name), `limit` (default 10)

**Output:**
```
Change history for core/supplierfunding/pgstore/store.go:

1. abc1234 (2026-04-15) by alice — FOR-567: fix funding calculation rounding
2. def5678 (2026-04-10) by bob — FOR-432: add multi-currency support
3. ghi9012 (2026-04-01) by alice — refactor: extract funding validation
...
```

For symbols: run `git log -p -- <file>`, map hunks to symbol line range, show only relevant commits.

### `get_coupling`

**Purpose:** Show what changes together with a given file or symbol.

**Input:** `name` (file path or symbol name), `min_strength` (default 0.3)

**Output:**
```
Co-change coupling for core/supplierfunding/pgstore/store.go:

Strong coupling (>= 0.5):
  - core/supplierfunding/pgstore/store_test.go (0.82, 15 co-changes)
  - models/supplierfunding/types.go (0.61, 9 co-changes)

Notable coupling (>= 0.3):
  - service/graphql/promoservice/funding_resolver.go (0.38, 6 co-changes)
  - pkg/datamodel/tables/supplier_funding.go (0.33, 5 co-changes)
```

## Updated MCP Tool Count

After Phase 5, total MCP tools: **8**

| Tool | Phase | Purpose |
|------|-------|---------|
| `find_symbol` | 1 | Find Go symbol by name |
| `search_go_context` | 1 | Unified semantic search (code + docs + tables) |
| `get_symbol_context` | 1 | Symbol + callers, callees, implementors |
| `get_package_summary` | 1 | Package summary + exports |
| `index_status` | 1 | Index freshness per stage |
| `get_table_context` | 2 | Table schema + readers/writers |
| `get_change_history` | 5 | Recent commits for file/symbol |
| `get_coupling` | 5 | Co-change partners ranked by strength |
