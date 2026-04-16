# ProjectLens Intelligence Platform Design

**Date:** 2026-04-16
**Status:** Draft
**Goal:** Evolve ProjectLens from a code-only indexer into a codebase intelligence platform that indexes code, data schemas, change history, and business documentation into a unified searchable graph.

## Vision

A single query returns code symbols, database tables, git history, Confluence pages, and Jira tickets — all ranked together by relevance. The platform serves the entire ingest monorepo (~2,913 handwritten Go files, 34 services, 71 utility packages) via MCP.

## Four Layers

| Layer | What | Source |
|-------|------|--------|
| A. Code | Symbols, packages, call graph | Go source (already working) |
| B. Data | Tables, columns, SQL queries, code-to-table edges | Migration files + raw SQL in Go |
| C. History | Last 10 commits per symbol/file, co-change coupling | Git log + diff |
| D. Docs | Confluence pages, Jira tickets, linked to code | Atlassian APIs + commit message ticket IDs |

## Schema Design

### Refactored Existing Tables

**`symbols`** — add SCIP-style ID and role bitset:
- `scip_symbol TEXT` — hierarchical ID: `go . example-org/ingest/core/supplierfunding . CalculateFunding()`
- `roles INTEGER DEFAULT 0` — bitset: Definition(1), ReadAccess(2), WriteAccess(4), Import(8), Test(16), Generated(32)

**`chunks`** — make universal across all content types:
- `source_type TEXT NOT NULL DEFAULT 'code'` — discriminator: `code`, `confluence`, `jira`, `migration`
- `source_uri TEXT` — external reference: Confluence page URL, Jira ticket key, migration file path
- `symbol_id` becomes nullable (null for doc/migration chunks, set for code chunks)

**`edges`** — make polymorphic for the full graph:
- Replace `source_symbol_id, target_symbol_id` with `source_type, source_id, target_type, target_id`
- Source/target types: `symbol`, `file`, `datastore_table`, `document`
- Add `properties JSONB` — flexible metadata per edge
- Add `confidence REAL` — coupling strength, semantic similarity score
- Edge types: `calls`, `implements`, `imports` (existing) + `reads_table`, `writes_table`, `co_changes`, `documents`, `mentions`, `migrates`

All other existing tables (`files`, `embeddings`, `summaries`, `index_runs`, `git_refs`) remain unchanged. Embeddings continue to link via `chunk_id` — since all content types flow through `chunks`, they share the same vector space automatically.

### New Tables

**`datastore_tables`** — database schema catalog:
- `id BIGSERIAL PRIMARY KEY`
- `name TEXT NOT NULL` — table name (e.g., `supplier_fundings`)
- `engine TEXT NOT NULL` — `postgres` or `clickhouse`
- `schema_name TEXT`
- `columns JSONB` — array of {name, type, nullable, constraints}
- `source_file_id BIGINT FK → files` — which migration file defines it
- `indexed_at TIMESTAMPTZ`

**`documents`** — external doc registry:
- `id BIGSERIAL PRIMARY KEY`
- `source_type TEXT NOT NULL` — `confluence` or `jira`
- `external_id TEXT NOT NULL` — page ID or ticket key (e.g., `FOR-1234`)
- `title TEXT NOT NULL`
- `url TEXT`
- `body_text TEXT` — raw content for chunking
- `last_synced_at TIMESTAMPTZ`
- `metadata JSONB` — Jira: status/assignee/labels; Confluence: space/parent page
- `UNIQUE(source_type, external_id)`

**`symbol_history`** — git-level change tracking per symbol:
- `id BIGSERIAL PRIMARY KEY`
- `symbol_id BIGINT FK → symbols`
- `commit_hash TEXT NOT NULL`
- `author TEXT NOT NULL`
- `committed_at TIMESTAMPTZ NOT NULL`
- `change_type TEXT NOT NULL` — `added`, `modified`, `deleted`
- `diff_snippet TEXT`
- Capped at 10 per symbol (oldest evicted on reindex)

**`file_history`** — git-level change tracking per file:
- `id BIGSERIAL PRIMARY KEY`
- `file_id BIGINT FK → files`
- `commit_hash TEXT NOT NULL`
- `author TEXT NOT NULL`
- `committed_at TIMESTAMPTZ NOT NULL`
- `change_type TEXT NOT NULL` — `added`, `modified`, `deleted`
- `diff_snippet TEXT`
- Capped at 10 per file (oldest evicted on reindex)

**Total: 12 tables** — `files`, `symbols`, `chunks`, `embeddings`, `edges`, `summaries`, `index_runs`, `git_refs`, `datastore_tables`, `documents`, `symbol_history`, `file_history`

## Independent Pipeline Stages

Each stage is a standalone CLI command. Reads inputs from DB, writes outputs to DB. No in-memory dependencies between stages.

```
projectlens index code      — census + parse + chunk + call graph
projectlens index datastore — scan migrations + SQL in code → datastore_tables + edges
projectlens index history   — git log → symbol_history + file_history + co-change edges
projectlens index docs      — fetch Confluence/Jira → documents + chunks
projectlens index embed     — embed all chunks missing embeddings (any source_type)
projectlens index summarize — generate summaries for anything missing one
projectlens index all       — run all of the above in order
```

### Stage Details

**`index code`** (existing, modified):
- Census: classify files (handwritten/test/generated/excluded)
- Parse: `go/packages` with full type checking, extract symbols, generate `scip_symbol` IDs and `roles` bitset
- Chunk: one chunk per symbol with `source_type='code'`
- Graph: CHA call graph, interface implementations, import edges
- Incremental: checksum comparison, only changed files reprocessed

**`index datastore`** (new):
- Parse migration SQL files → extract CREATE TABLE/ALTER TABLE → populate `datastore_tables`
- Scan Go source files for raw SQL strings (look for `db.Query`, `db.Exec`, `db.QueryRow` and similar patterns)
- Parse SQL to extract table references and operation type (SELECT/INSERT/UPDATE/DELETE)
- Create edges: symbol → `reads_table`/`writes_table` → datastore_table
- Incremental: re-scan only changed migration files + changed Go files

**`index history`** (new):
- `git log` per file, map diff hunks to symbol line ranges
- Populate `file_history` and `symbol_history` (capped at 10 per entity)
- Compute co-change pairs: files/symbols appearing in the same commit
- Store coupling as edges: `edge_type='co_changes'`, `confidence=co_change_count/total_changes`
- Incremental: `git log --since=<last_index_timestamp>`

**`index docs`** (new):
- Confluence: fetch pages from configured spaces/page IDs via Atlassian REST API
- Jira: fetch tickets matching configured JQL filter
- Store in `documents` table, chunk body text into `chunks(source_type='confluence'/'jira')`
- Extract Jira ticket IDs from git commit messages (regex: `FOR-\d+`)
- Create edges: document → `mentions`/`documents` → symbol/file
- Incremental: `last_synced_at` comparison, only fetch modified pages/tickets

**`index embed`** (new, replaces embedded step in code pipeline):
- Query: `SELECT * FROM chunks WHERE id NOT IN (SELECT chunk_id FROM embeddings)`
- Embed all unembedded chunks regardless of `source_type`
- Batched OpenAI calls (100 per batch)
- One pass for code + docs + migration chunks

**`index summarize`** (existing, extended):
- Generate summaries for packages without one (existing)
- Generate summaries for datastore tables without one (new)
- Generate summaries for documents without one (new)

## MCP Tools (8)

### Modified Existing Tools

| Tool | Changes |
|------|---------|
| `find_symbol` | Add `scip_symbol` and `roles` to response |
| `search_go_context` | Returns code + doc + datastore results from unified vector space, tagged by `source_type` |
| `get_symbol_context` | Add: tables it reads/writes, recent history (last 10 commits), related Jira tickets |
| `get_package_summary` | Add: datastore tables used by package, related Confluence pages |
| `index_status` | Show per-stage status (code/datastore/history/docs/embed/summarize) |

### New Tools

| Tool | Purpose |
|------|---------|
| `get_table_context` | Given a DB table name → columns, which symbols read/write it, migration history |
| `get_change_history` | Given a symbol or file → last 10 commits, authors, diffs, what changed with it |
| `get_coupling` | Given a symbol or file → co-change partners ranked by coupling strength |

## Configuration

```yaml
repo_path: "/path/to/ingest"
database_url: "postgres://..."

# Code indexing (existing)
index:
  include_patterns: ["**/*.go"]
  exclude_patterns: ["**/vendor/**", "**/third_party/**", "**/testdata/**", "**/*_test.go", "**/node_modules/**"]
  generated_markers: ["Code generated", "DO NOT EDIT", "_generated.go", "_gen.go", ".pb.go", "_grpc.go", "_string.go", "zz_generated"]

# Datastore indexing
datastore:
  engines:
    - name: postgres
      migration_paths: ["db/migrations/**/*.sql"]
    - name: clickhouse
      migration_paths: ["cmd/clickhouse/**/*.sql"]
  sql_scan_paths: ["core/**", "service/**", "pkg/**"]

# History
history:
  max_commits_per_symbol: 10
  max_commits_per_file: 10

# Docs
docs:
  confluence:
    base_url: "https://relexsolutions.atlassian.net"
    spaces: ["FOR"]
    page_ids: [5749964825]
  jira:
    base_url: "https://relexsolutions.atlassian.net"
    projects: ["FOR"]
    jql_filter: "project = FOR AND updated >= -30d"

# Embeddings
embeddings:
  provider: openai
  model: text-embedding-3-large
  dimensions: 3072
```

## Design Decisions

- **Postgres property graph over Neo4j** — at ~10K nodes and ~50K edges, recursive CTEs work fine. No operational overhead of a second database.
- **Unified chunks + embeddings** — all content types share the same vector space. One semantic search query returns everything.
- **Polymorphic edges** — one edge table for call graph, data flow, coupling, and doc links. Simplifies traversal and queries.
- **SCIP-style symbol IDs** — hierarchical text IDs are more debuggable than integer IDs for cross-referencing.
- **Independent pipeline stages** — each stage reads from DB, writes to DB. No in-memory coupling. Run any stage independently.
- **Co-change coupling as edges** — CodeScene-style coupling detection stored in the same graph as code relationships.
- **Commit message ticket extraction** — `FOR-\d+` regex on git commits creates hard links between code and Jira. Semantic similarity fills remaining gaps.
- **10-commit cap on history** — keeps storage bounded, focuses on recent evolution.
