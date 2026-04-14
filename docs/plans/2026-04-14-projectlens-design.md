# ProjectLens — Design Document

Version: 1.0
Date: 2026-04-14
Status: Validated via brainstorming session

---

## 1. Purpose

ProjectLens is a local-first, containerized repository intelligence layer for a large Go monorepo (~4,150 Go files, 34 services, 71 utility packages). It reduces repeated broad file exploration by Claude Code and replaces it with targeted retrieval over precomputed structure.

The system is optimized for Claude Code as the primary consumer, exposed via MCP (HTTP/SSE).

---

## 2. Core Pain Points Addressed

1. **Slow context gathering** — Claude Code spends too many tool calls opening files before answering
2. **Lost knowledge between sessions** — every conversation re-discovers the same architecture
3. **Missing dependency awareness** — Claude doesn't know what depends on what before making changes

---

## 3. Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Primary language | Go | Target repo is Go, ProjectLens is Go |
| Storage | Postgres + pgvector | Single store for metadata, vectors, and graph edges |
| Embedding model | OpenAI `text-embedding-3-large` | Enterprise plan, unlimited usage, high quality |
| Summarization | OpenAI `gpt-4o-mini` for packages, heuristic for files | Save Claude tokens for interactive work |
| Go parsing | `go/packages` + `go/callgraph` | Type-checked analysis with real call graphs |
| MCP transport | HTTP/SSE (persistent service) | Always-on, no cold start per session |
| Repo access | Read-only Docker volume mount | Simple, always current, no sync |
| Reindex trigger | On-demand via CLI, staleness exposed via MCP | Simple for MVP, agent-aware |
| Claude API usage | None for indexing/summarization | Reserved entirely for interactive coding |

---

## 4. Architecture

```
+---------------------------------------------------+
|  Claude Code                                       |
|    | MCP (HTTP/SSE)                                |
+---------------------------------------------------+
|  projectlens-mcp        (Go, long-lived service)    |
|    - query classification                          |
|    - lexical + semantic + graph retrieval           |
|    - ranking & reranking                           |
|    | SQL                                           |
+---------------------------------------------------+
|  projectlens-db          (Postgres + pgvector)      |
|    - symbols, chunks, summaries, edges             |
|    - embeddings (3072-dim)                         |
|    - index state & git refs                        |
+---------------------------------------------------+
|  projectlens-indexer     (Go, on-demand process)    |
|    - go/packages + callgraph analysis              |
|    - symbol extraction & chunking                  |
|    - heuristic file summaries                      |
|    - LLM package summaries (OpenAI)                |
|    - OpenAI text-embedding-3-large                 |
|    - graph edge construction                       |
|    | reads from mounted repo (read-only)           |
+---------------------------------------------------+
```

Three runtime components:
- **projectlens-mcp** — persistent MCP server, handles all Claude Code queries
- **projectlens-indexer** — on-demand process, runs the indexing pipeline
- **projectlens-db** — Postgres + pgvector, single persistent store

Plus a CLI (`projectlens`) for developer interaction.

---

## 5. Data Model

### Tables

**`files`**
- path, package_name, checksum, language, is_generated, is_test, line_count
- heuristic_summary (extracted doc comments + exported signatures)
- indexed_at, commit_sha

**`symbols`**
- file_id, name, kind, package_name, receiver (for methods)
- signature, doc_comment, line_start, line_end
- checksum, indexed_at

**`chunks`**
- symbol_id, content (signature + doc + body + package context)
- token_count

**`embeddings`**
- chunk_id, model_version, vector (3072 dims)

**`summaries`**
- package_name, summary_text, model_version, generated_at

**`edges`**
- source_symbol_id, target_symbol_id, edge_type
- Edge types: `calls`, `imports`, `implements`, `depends_on`

**`index_runs`**
- run_id, started_at, completed_at, commit_sha, files_processed, status

**`git_refs`**
- branch, commit_sha, indexed_at

### Design principles
- Symbols are the primary unit, not files
- Chunks are 1:1 with symbols (no arbitrary token window splitting)
- Edges connect symbols to symbols
- Index runs enable staleness detection

---

## 6. Indexer Pipeline

### Step 1 — Census
Walk the mounted repo, discover all `.go` files, classify (handwritten vs generated, production vs test), compare checksums against last index run, produce a work list.

### Step 2 — Parse & Extract
Load work list through `go/packages` with full type checking. Extract symbols (functions, methods, structs, interfaces, constants, variables). Build call graph via `golang.org/x/tools/go/callgraph`. Extract interface implementation relationships.

### Step 3 — Chunk
One chunk per symbol: signature + doc comment + body + package context. No arbitrary token window splitting.

### Step 4 — Edges
From call graph: `calls` edges. From type checker: `implements` edges. From imports: `imports` edges. From package analysis: `depends_on` edges.

### Step 5 — Summarize
Files: heuristic (package doc + exported signatures + doc comments). Packages: OpenAI `gpt-4o-mini` batch call with exported symbols and doc comments as input.

### Step 6 — Embed
Batch embed all chunks via OpenAI `text-embedding-3-large`. Store vectors with model version tag.

### Step 7 — Commit Index
Write `index_runs` record. Update `git_refs`.

Incremental reindex: checksum comparison in step 1 means only changed files flow through steps 2-6.

---

## 7. Retrieval Pipeline

### Stage 1 — Query Classification
Classify into: `exact_symbol`, `implementation_search`, `package_overview`, `dependency_trace`.

### Stage 2 — Parallel Retrieval

| Path | When | How |
|------|------|-----|
| Lexical | Always | Exact match on symbol name, package name, file path |
| Semantic | Implementation/overview queries | Embed query, cosine similarity via pgvector |
| Graph | Dependency queries | BFS/DFS traversal of edges from seed symbol |

### Stage 3 — Ranking & Assembly

Scoring factors:
- Exact symbol name match: strong boost
- Same package as query context: boost
- Handwritten production code: baseline
- Test file: penalty (unless test-related query)
- Generated code: strong penalty
- Semantic similarity: weighted in
- Graph distance: closer = higher

Returns: symbol name, file path, line range, summary snippet, relationship context.

---

## 8. MCP Tool Surface

### MVP Tools (5)

**`find_symbol`**
- Input: symbol name (exact or fuzzy), optional kind filter
- Retrieval: lexical first, semantic fallback
- Returns: matched symbols with file path, line range, signature, doc comment, package

**`search_go_context`**
- Input: natural language query, optional package filter
- Retrieval: semantic + lexical, merged and ranked
- Returns: top-k relevant symbols/chunks with summaries and file locations

**`get_symbol_context`**
- Input: symbol name + file path (or symbol ID)
- Retrieval: graph traversal
- Returns: the symbol + callers, callees, interface implementations, package summary

**`get_package_summary`**
- Input: package name
- Returns: LLM-generated summary, exported symbols with signatures, dependency relationships

**`index_status`**
- Input: none
- Returns: last index timestamp, commit SHA, staleness warning, file count

### Design rule
Push retrieval intelligence into ProjectLens. The agent calls high-value tools, not dozens of low-level search steps.

---

## 9. Docker Compose

```yaml
services:
  postgres:
    image: pgvector/pgvector:pg16
    volumes:
      - projectlens-data:/var/lib/postgresql/data
    ports:
      - "5433:5432"
    environment:
      POSTGRES_DB: projectlens
      POSTGRES_USER: projectlens
      POSTGRES_PASSWORD: ${PROJECTLENS_DB_PASSWORD}

  projectlens-mcp:
    build: ./docker
    command: serve-mcp
    ports:
      - "8484:8484"
    volumes:
      - ${PROJECTLENS_REPO_PATH}:/repo:ro
    environment:
      DATABASE_URL: postgres://projectlens:${PROJECTLENS_DB_PASSWORD}@postgres:5432/projectlens
      OPENAI_API_KEY: ${OPENAI_API_KEY}
    depends_on:
      - postgres

  projectlens-indexer:
    build: ./docker
    command: reindex
    profiles: ["index"]
    volumes:
      - ${PROJECTLENS_REPO_PATH}:/repo:ro
    environment:
      DATABASE_URL: postgres://projectlens:${PROJECTLENS_DB_PASSWORD}@postgres:5432/projectlens
      OPENAI_API_KEY: ${OPENAI_API_KEY}
    depends_on:
      - postgres

volumes:
  projectlens-data:
```

- Port 5433 to avoid local Postgres conflicts
- Indexer uses Docker Compose profiles — runs only on demand
- Repo path via env var, not hardcoded

---

## 10. CLI Commands

| Command | Purpose |
|---------|---------|
| `projectlens census` | Walk repo, classify files, report what would be indexed |
| `projectlens bootstrap` | Run migrations + full index from scratch |
| `projectlens reindex` | Incremental reindex (changed files only). `--full` for complete. `--dry-run` to preview. |
| `projectlens status` | Show last index time, commit SHA, counts, staleness |
| `projectlens inspect-symbol <name>` | Look up a symbol: file, line range, callers, callees, implements |
| `projectlens inspect-package <name>` | Show package summary, exported symbols, dependencies |
| `projectlens query <text>` | Run full retrieval pipeline from terminal. `--mode` for specific path. |

All commands share `--config` flag for classification rules and settings.

---

## 11. Claude Code Integration

### MCP Configuration
```json
{
  "mcpServers": {
    "projectlens": {
      "type": "sse",
      "url": "http://localhost:8484/mcp"
    }
  }
}
```

### CLAUDE.md Guidance
Add to the target repo's CLAUDE.md:
- Use ProjectLens before opening files for implementation questions
- Preferred tool order: find_symbol → get_symbol_context → get_package_summary
- Check index_status if results seem stale
- Only open files to verify, not to explore

### Skills (3)

**`trace-go-flow`** — locate implementation path for a behavior
1. find_symbol or search_go_context
2. get_symbol_context for dependencies
3. Open top 1-2 files to verify
4. Summarize implementation flow

**`debug-go-test`** — investigate test behavior
1. search_go_context (test-aware query)
2. get_symbol_context for production code under test
3. Explain expected vs actual behavior

**`explain-go-impact`** — estimate what breaks if you change something
1. find_symbol for the target
2. get_symbol_context (callers + implementors)
3. get_package_summary for affected packages
4. Summarize impact and uncertainty

---

## 12. Repository Structure

```
projectlens/
  cmd/
    projectlens/          # CLI entrypoint
    projectlens-mcp/      # MCP server entrypoint
  internal/
    census/              # file discovery and classification
    classifier/          # handwritten vs generated vs test
    parser/              # go/packages wrapper
    symbols/             # symbol extraction
    chunks/              # chunking logic
    summaries/           # heuristic + LLM summaries
    graph/               # edge construction and traversal
    retrieval/           # lexical + semantic search
    rerank/              # ranking and scoring
    mcpserver/           # MCP HTTP/SSE handler
    storage/             # Postgres queries and migrations
    openai/              # OpenAI client (embeddings + completions)
  configs/
    index.yaml           # classification rules, excluded paths
  docker/
    Dockerfile
    docker-compose.yml
  migrations/            # SQL migrations
  docs/
    plans/               # design documents
  scripts/               # helper scripts
```

---

## 13. MVP Scope

**In:**
- Handwritten Go file indexing
- Symbol extraction with type-checked parsing
- Call graph and interface implementation edges
- Package-level import/dependency edges
- Heuristic file summaries
- LLM package summaries (OpenAI)
- Semantic embeddings (OpenAI text-embedding-3-large)
- Lexical + semantic + graph retrieval
- Ranked results
- 5 MCP tools
- 7 CLI commands
- Docker Compose deployment
- Claude Code integration (MCP config + CLAUDE.md + 3 skills)

**Out (deferred):**
- Test indexing (separate namespace)
- Change history / co-change analysis
- Markdown / doc indexing
- SQL / proto / GraphQL / Terraform indexing
- Figma / Miro metadata
- Team packaging
- Hooks for automatic reindex
- Branch-aware indexing

---

## 14. Open Questions

1. **Call graph algorithm** — CHA (faster, less precise) vs RTA (slower, more precise) for `go/callgraph`. Recommendation: start with CHA, upgrade to RTA if precision is insufficient.
2. **Embedding batch size** — how many chunks per OpenAI API call. Tune based on rate limits and latency.
3. **Package summary prompt** — exact prompt template for OpenAI package summarization. Iterate during implementation.
4. **Ranking weights** — initial scoring weights are heuristic. Tune based on eval results.
5. **pgvector index type** — IVFFlat vs HNSW. At ~3K vectors, HNSW is fine and simpler.
