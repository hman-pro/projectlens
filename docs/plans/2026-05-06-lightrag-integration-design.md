# LightRAG Integration Design

Date: 2026-05-06
Status: Draft v4 (incorporates round-1, round-2, and round-3 review findings from `2026-05-06-lightrag-integration-design-review.md`)

## Summary

Integrate [LightRAG](https://github.com/HKUDS/LightRAG) into ProjectLens as a bundled Python sidecar to cover the areas where the current system is naive: prose documents, conceptual queries, and cross-source entity/relation graphs. ProjectLens's existing Go-native indexer (AST + CHA call graph + SCIP IDs) remains the source of truth for code structure. LightRAG owns prose retrieval (docs, package summaries, symbol docstrings, datastore-table summaries) and the LLM-extracted knowledge graph over them.

The two systems coexist behind separate MCP tools — no fusion ranker, no query router. ProjectLens additionally calls LightRAG internally to enrich a small set of existing tools with a `Related concepts` block, mirroring how `Related knowledge` is already attached today.

## Supersedes / Reuses

This design partially supersedes [`2026-05-01-docs-stage-design.md`](./2026-05-01-docs-stage-design.md):

- **Retained:** Go-side Confluence + Jira fetchers (`internal/docs/client_confluence.go`, `client_jira.go`, `render.go`), the `documents` canonical-metadata table (migration 002), the `index-docs` CLI subcommand, the TUI `O` hotkey, and credential handling via `ATLASSIAN_EMAIL` / `ATLASSIAN_API_TOKEN`.
- **Superseded:** chunking docs into the `chunks` table for embedding via `mxbai-embed-large`, retrieving docs via `search_go_context`, and the `confluence://` / `jira://` `source_uri` schemes for non-anchor chunks.
- **Net effect:** ProjectLens still fetches Atlassian content with full Go-side rate limiting, retry, and audit. Rendered text is handed to the LightRAG sidecar via HTTP instead of being embedded into `chunks`. Docs are reachable only through the new `search_docs` / `search_concept` MCP tools.

The May 1 file has been updated with a supersession header pointing here.

## Goals

- Unblock Phase 4 (Confluence + Jira ingestion + retrieval) without writing a doc retrieval pipeline from scratch.
- Add an LLM-extracted entity/relation graph over prose so concept queries (`"how does supplier funding work?"`) resolve across docs and code intent, not just symbol names.
- Keep ProjectLens's Go-only single-binary core untouched. New runtime dependencies are containerized sidecars.
- Stay on open-weight models for indexing to avoid per-token cost gates and keep a self-hosted story.
- Preserve a clean architectural boundary: each subsystem owns its storage and tools.
- Reuse ProjectLens's existing Atlassian fetcher path; LightRAG never talks to Confluence or Jira directly.

## Non-goals

- Replacing the existing call-graph or symbol retrieval. Code-shape queries (`find_symbol`, `get_symbol_context`, `get_coupling`) keep their current path.
- Running LightRAG over full Go function bodies. Bodies stay deterministic via AST.
- Cross-store fusion ranking or query routing classifiers between code and prose retrieval.
- Replacing ProjectLens's heuristic reranker in this phase (deferred).
- Adding a second query-side LLM provider (Anthropic Sonnet override) in v1; deferred.

## Architecture

### Deployment topology

Single docker-compose stack. New services in addition to the existing `postgres` and `projectlens-mcp`:

| Service | Image | Role |
|---|---|---|
| `lightrag-sidecar` | `projectlens/lightrag-sidecar:<tag>` (built from `docker/lightrag-sidecar/Dockerfile`) | Thin FastAPI wrapper around LightRAG core; exposes ProjectLens-owned `/insert`, `/delete`, `/query` endpoints with deterministic IDs and idempotent semantics. |
| `lightrag-memgraph` | `memgraph/memgraph` | Knowledge graph backend for LightRAG (`MemgraphStorage`). Cypher native. |
| `infinity` | `michaelf34/infinity` | Hosts `BAAI/bge-m3` (embeddings) + `BAAI/bge-reranker-v2-m3` (reranker) in one process. Cohere-/OpenAI-compatible API. |
| `ollama` | `ollama/ollama` (existing or new) | Hosts `qwen3:30b-a3b-q4` for entity/relation extraction and query-time keyword extraction; continues to host `mxbai-embed-large` for code embeddings. |

LightRAG storage layout:

| LightRAG store | Backend | Where |
|---|---|---|
| `LIGHTRAG_KV_STORAGE` | `PGKVStorage` | Database `lightrag` on shared Postgres |
| `LIGHTRAG_VECTOR_STORAGE` | `PGVectorStorage` | Database `lightrag` on shared Postgres (pgvector) |
| `LIGHTRAG_DOC_STATUS_STORAGE` | `PGDocStatusStorage` | Database `lightrag` on shared Postgres |
| `LIGHTRAG_GRAPH_STORAGE` | `MemgraphStorage` | `lightrag-memgraph` container |
| Workspace isolation | `POSTGRES_WORKSPACE=projectlens` | Tags rows in PG stores; names the Memgraph graph. |

The `lightrag` database is created at compose-init time alongside `projectlens`. Same Postgres container, two distinct databases — no plugin requirements (AGE not needed; graph lives in Memgraph), clean dump/restore boundary, zero impact on projectlens's existing migrations.

### `lightrag-sidecar` — own thin wrapper around LightRAG core

The official `lightrag-server` REST API does not expose `ids=[...]` on document insert; LightRAG core's `apipeline_enqueue_documents` filters already-seen IDs as duplicates rather than reprocessing them; and the structured query shape we need (`entities`, `relationships`, `chunks`, `references`) is exposed via `aquery_data`, not `aquery`. ProjectLens owns a small FastAPI wrapper that imports LightRAG core directly and exposes the contract we need.

**Layout:**

```
docker/lightrag-sidecar/
  Dockerfile           # python:3.11-slim + pip install lightrag-hku==<pinned> + our wrapper
  app/
    main.py            # FastAPI app, ~200 LOC
    config.py          # reads env, builds LightRAG instance
    schemas.py         # pydantic request/response models
  requirements.txt     # lightrag-hku==<pinned>, fastapi, uvicorn
  README.md            # how it differs from upstream lightrag-server
```

**Endpoints exposed:**

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/projectlens/insert` | Idempotent upsert. For each `(chunk_anchor_id, lightrag_doc_id, content)` tuple: call `rag.adelete_by_doc_id(lightrag_doc_id)` (no-op if absent), then `rag.ainsert(texts=[content], ids=[lightrag_doc_id])`. Caller doesn't need to know whether the doc existed. |
| `POST` | `/projectlens/delete` | Delete one or many `lightrag_doc_id`s; calls `rag.adelete_by_doc_id(...)`. |
| `POST` | `/projectlens/query` | Calls `rag.aquery_data(...)` and returns the structured shape unchanged. See [MCP query path](#mcp-query-path). |
| `GET` | `/projectlens/health` | Reports sidecar + Memgraph + Postgres + Infinity reachability. |

**Idempotent insert rationale (round-3 finding 1):** LightRAG core treats reused doc IDs as duplicates. Without an explicit delete, hash-mismatch updates would silently leave stale entities/relations in the KG. The sidecar always does delete-then-insert, so the caller's contract is "give me the new content under this ID." Delete is cheap when the ID is absent.

**Structured query rationale (round-3 finding 4):** `aquery_data` returns `entities`, `relationships`, `chunks`, and `references` as separate fields. Anchor parsing happens on `chunks[].content`. Synthesis would have required `aquery_llm` and is unnecessary because the calling agent does its own synthesis.

**Why not fork upstream:** we add no new logic to LightRAG itself. The wrapper is a thin transport adapter. `lightrag-hku` is a pinned PyPI dependency, upgraded by bumping the pin.

### Component topology

```
+---------------------------------+
| ProjectLens (Go)                 |
|  - indexer (code + docs fetch)  |
|  - 14 tables in db projectlens   |
|  - existing 10 MCP tools        |
|  - 2 new proxied MCP tools      |
+---------+--------------------+--+
          |                    |
          | reindex (HTTP)     | enrichment calls (HTTP)
          v                    v
+----------------------------------------+
| lightrag-sidecar (FastAPI + LightRAG)  |
|  - /projectlens/insert  /delete /query  |
|  - imports lightrag-hku core directly  |
|  - delete-then-insert (idempotent)     |
|  - aquery_data → structured response   |
+----+--------+-----------+--------------+
     |        |           |
     | PG     | Cypher    | OpenAI-compat / Cohere-compat
     v        v           v
+----------+ +-------------+   +------------+
| Postgres | | Memgraph    |   | Infinity   |
|  projectlens| graph       |   | bge-m3 +   |
|  lightrag  |             |   | bge-rerank |
+----------+ +-------------+   +------------+
                                ^
                                |
                                +— Ollama (qwen3:30b-a3b-q4 + mxbai)
```

### MCP surface

Two new MCP tools served by the existing `projectlens-mcp` Go server, which proxies to the sidecar via HTTP. Clients see ProjectLens as the only MCP endpoint.

| Tool | Sidecar mode | Purpose |
|---|---|---|
| `search_docs` | `hybrid` | Prose RAG across Confluence, Jira, READMEs, migration comments. Returns passages with source metadata. |
| `search_concept` | `mix` | Entity/relation graph traversal across docs + package summaries + symbol docstrings + table summaries. Returns entities, related concepts, and source passages. |

Existing tools (`find_symbol`, `get_symbol_context`, `search_go_context`, `get_table_context`, `get_coupling`, `get_change_history`, `get_package_summary`, `index_status`, `save_knowledge`, `search_knowledge`) keep their current implementation. Three of them gain a `Related concepts` block — see [Internal LightRAG uses](#internal-lightrag-uses).

The `claude/skills/use-projectlens/SKILL.md` tool-picker table is extended so the agent picks `search_docs` / `search_concept` for prose/conceptual questions and the existing tools for symbol-shaped questions.

## Data model

### Anchor identity (source vs chunk)

A long Confluence page or markdown doc is split into multiple chunks during pre-chunking. Source-level identity (the page) and chunk-level identity (the section) are tracked separately so that:

- Source-level deletes work (e.g. when a Confluence page is removed, delete every chunk where `source_anchor_id = X`).
- Chunk-level idempotent insert works (each section gets its own `lightrag_doc_id`).
- Single-chunk sources still use the same model uniformly (chunk ordinal `0` or section slug; never empty).

Format:

```
source_anchor_id  =  <type> . <id-shape>
chunk_anchor_id   =  <source_anchor_id> # <chunk_part>
lightrag_doc_id   =  sha1(chunk_anchor_id)
```

`<chunk_part>` is `section/<slug>` for heading-derived chunks, `ordinal/<n>` otherwise. Single-chunk sources always emit `ordinal/0`.

### Anchor header (in-content marker)

Every chunk fed to LightRAG carries a fixed-format header on its first line:

```
<<PROJECTLENS_ANCHOR type=doc_confluence source="confluence . FOR . 12345" chunk="confluence . FOR . 12345 # section/api-overview" path="https://relexsolutions.atlassian.net/wiki/spaces/FOR/pages/12345">>
```

Required guarantees:

1. **Pre-chunked on the ProjectLens side.** Chunks are sized below `chunk_token_size` (default 1200, with margin) so LightRAG's internal splitter never separates the header from its content.
2. **One header per chunk** on line 1. Body follows from line 2.
3. **ASCII-only token format.** No markdown specials. Tokenizer-stable.
4. **Stripped on retrieval.** The MCP proxy regexes the header out of `chunk_content` and returns it as structured metadata alongside the cleaned passage body.
5. **Extractor blacklist.** `PROJECTLENS_ANCHOR` is added to LightRAG's extraction prompt blacklist; a defensive post-extract filter drops any leaked anchor entity.

### Anchor types and ID formats

| `type` | `source_anchor_id` shape | Default `chunk_part` strategy | `path` |
|---|---|---|---|
| `symbol` | `go . <package_import_path> . <SymbolName>` | `ordinal/0` (always one chunk) | source file |
| `package_summary` | `pkg . <package_import_path>` | `ordinal/0` | package directory |
| `table` | `table . <engine> . <schema> . <name>` | `ordinal/0` | migration file or `n/a` |
| `doc_confluence` | `confluence . <space_key> . <page_id>` | `section/<slug>` per heading; fallback `ordinal/<n>` | URL |
| `doc_jira` | `jira . <project_key> . <issue_key>` | `ordinal/<n>` (description, then comments) | URL |
| `doc_readme` | `readme . <package_or_dir>` | `section/<slug>` per heading; fallback `ordinal/<n>` | source file |
| `doc_migration` | `migration . <filename>` | `ordinal/0` | `migrations/<file>` |

### State table — `lightrag_chunk_state` (in `projectlens` DB, schema `public`)

ProjectLens owns this table to gate re-extraction, scope reconciliation, and enable clean deletion:

```sql
CREATE TABLE lightrag_chunk_state (
    chunk_anchor_id   TEXT PRIMARY KEY,
    source_anchor_id  TEXT NOT NULL,
    source_type       TEXT NOT NULL,
    content_hash      TEXT NOT NULL,
    lightrag_doc_id   TEXT NOT NULL,
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX lightrag_chunk_state_source_idx       ON lightrag_chunk_state (source_anchor_id);
CREATE INDEX lightrag_chunk_state_type_seen_idx    ON lightrag_chunk_state (source_type, last_seen_at);
```

`lightrag_doc_id = sha1(chunk_anchor_id)`. ProjectLens passes it to the sidecar on every insert; the wrapper forwards to `rag.ainsert(..., ids=[...])` after a defensive `delete_by_doc_id`.

`source_type` is one of the keys from the anchor-types table (`symbol`, `package_summary`, `table`, `doc_confluence`, `doc_jira`, `doc_readme`, `doc_migration`). Used by reconciliation to scope deletion to the source set processed in a given run (see [docs stage logic](#trigger-and-pipeline-order)).

### LightRAG-side storage

Owned by LightRAG. Tables in the `lightrag` Postgres database are created on first boot of the sidecar; a Memgraph graph named `projectlens` is created on first KG write. ProjectLens does not hand-maintain LightRAG's schema.

## Ingest pipeline

### Sources fed to LightRAG

| Source | Pre-chunker | `source_type` |
|---|---|---|
| Confluence pages | section-based (heading boundaries), then size-capped | `doc_confluence` |
| Jira issues (description + comments) | per-issue, single chunk if small, else section split | `doc_jira` |
| Repo READMEs and `*.md` under `/docs` | heading-based | `doc_readme` |
| Migration files (`migrations/*.sql`, leading comments + statement summaries) | per-file | `doc_migration` |
| Package summaries (`summaries` table) | per-package | `package_summary` |
| Symbol docstrings (Go doc comments above exported symbols) | per-symbol | `symbol` |
| Datastore table summaries (`datastore_tables` table) | per-table (summary + column list) | `table` |

Function bodies are not ingested. Field/type comments are bundled with their parent type's symbol chunk.

### Atlassian fetch path (reused from May 1 plan)

LightRAG never talks to Confluence or Jira directly:

```
internal/docs/client_confluence.go ──fetch──▶ Confluence API
internal/docs/render.go            ──XHTML→plaintext──▶ rendered Page
internal/docs/indexer.go (modified) ──upsert metadata──▶ documents table
                                    ──pre-chunk + anchor──▶ sidecar /projectlens/insert
                                                              with ids=sha1(chunk_anchor_id)
```

Same flow for Jira via `client_jira.go`. Single source of truth for credentials, rate limiter, retries.

### Trigger and pipeline order

The new `docs` stage is triggered by exactly two CLI commands:

- `projectlens index-docs` — runs the docs stage standalone.
- `projectlens index-all` — runs the full pipeline including docs.

`reindex` and `bootstrap` remain code-only (they call `indexer.Run` for code + summaries + embed) — unchanged from today, no breaking change to operator workflows or to the existing `projectlens-indexer` compose service.

`index-all` order (final):

```
census → parse → chunk → graph → datastore → summarize → docs → history → embed
```

Rationale (rebuilt from data dependencies):

- `summarize` precedes `docs` because the docs stage ingests `summaries` rows into LightRAG.
- `docs` precedes `history` because the history stage's Jira-edge writer (see [Internal LightRAG uses](#internal-lightrag-uses)) queries LightRAG to confirm a Jira doc exists before writing a `document → file` mention edge.
- `embed` runs last to embed any code chunks added by intermediate stages without waiting on LightRAG.

The new `docs` stage logic:

1. Determine the **processed source set** from CLI flags and config:
   - `--source confluence` → `{doc_confluence}` only.
   - `--source jira` → `{doc_jira}` only.
   - No flag → all source types whose `lightrag.ingest.sources.<key>=true`.
2. For each source type in the processed set: resolve current `(chunk_anchor_id, source_anchor_id, content)` tuples from Postgres (`summaries`, `symbols`, `documents`, `datastore_tables`) and from the working tree (READMEs, migrations).
3. For each tuple, compute `content_hash`. Look up `lightrag_chunk_state` by `chunk_anchor_id`:
   - **Not present:** call sidecar `/projectlens/insert` with `ids=[sha1(chunk_anchor_id)]`. Add state row.
   - **Hash matches:** update `last_seen_at` only. No LLM call. (Hash gate.)
   - **Hash differs:** call sidecar `/projectlens/insert` with the same deterministic ID (sidecar does delete-then-insert). Update state row.
4. **Scoped reconciliation.** For each `source_type` in the processed source set, find state rows of that type whose `last_seen_at` predates this run's start. For each, call `/projectlens/delete` and remove the state row. State rows for source types **not** in the processed set are left alone.

The Postgres advisory writer lock (`LockID = 9876543210`) covers the `docs` stage.

`projectlens index-docs --full`:

1. Selects only the processed source set (still respects `--source` and config).
2. **Pre-pass delete.** Iterates state rows of those source types and calls `/projectlens/delete` for each `lightrag_doc_id`. Removes state rows after successful delete.
3. Runs the normal flow above with all hashes treated as new (since state is empty for the processed types).

The hash gate is the only steady-state cost control; no token budget, no manual confirmation flag.

### MCP query path

Read-only. The `projectlens-mcp` Go server delegates to the sidecar's `/projectlens/query` endpoint, which calls `rag.aquery_data(...)`.

Sidecar request shape:

```json
{
  "query": "<user query>",
  "mode": "hybrid",
  "include_references": true,
  "top_k": 20
}
```

`mode` is `hybrid` for `search_docs` and `mix` for `search_concept`. `top_k` is configurable from `configs/index.yaml`.

Sidecar response shape (passes through `aquery_data`'s structured result):

```json
{
  "entities": [
    { "name": "...", "description": "...", "source_id": "...", "score": 0.83 }
  ],
  "relationships": [
    { "src": "...", "tgt": "...", "description": "...", "score": 0.74 }
  ],
  "chunks": [
    { "content": "<<PROJECTLENS_ANCHOR ...>>\n<body>", "chunk_id": "...", "reference_id": "..." }
  ],
  "references": [
    { "id": "...", "source_id": "...", "title": "..." }
  ]
}
```

MCP proxy responsibility:

- For each `chunk`: regex first line `^<<PROJECTLENS_ANCHOR (.+)>>$`, parse `type`, `source`, `chunk`, `path`, strip the line. Re-emit as `{ anchor: {type, source_anchor_id, chunk_anchor_id, path}, passage: <body>, score, reference_id }`.
- Filter any entity whose name contains `PROJECTLENS_ANCHOR` (defensive against extractor leaks).
- Pass `entities` and `relationships` through to the caller; attach the original anchor-decorated chunks under a `passages` field.

Contract tests in `internal/mcpserver/lightrag_proxy_test.go`:

- Multi-chunk reference response with multiple anchors → all anchors parsed correctly.
- One-chunk + one-relation-only-hit response → both reach the caller.
- Missing anchor on a chunk → graceful skip + warn log; chunk still returned with `anchor: null`.
- Anchor leaked into entity list → filtered before return.
- Sidecar 5xx → MCP returns structured error, doesn't crash.

Sidecar contract tests in the wrapper repo:

- `insert(id=A, v1)` → `query` returns v1.
- `insert(id=A, v2)` → `query` returns v2 only (no v1 entities/relations remain).
- `delete(id=A)` → `query` returns no v1, no v2.
- Two-chunk insert: same `source_anchor_id`, different `chunk_anchor_id`s → both retrievable; deleting one leaves the other intact.

## Internal LightRAG uses

### (I) `Related concepts` block on existing context tools

`get_symbol_context`, `get_package_summary`, and `get_table_context` each gain a `Related concepts` section appended to their existing output, mirroring how `Related knowledge` is attached today.

Implementation per tool:

- After the existing query path, call sidecar `/projectlens/query` with `mode=local`, query string built from the tool target (symbol name + parent package, package import path, or schema-qualified table name), `top_k=5`.
- Filter results to those whose anchor type is one of: `package_summary`, `table`, `symbol`, or any `doc_*` whose entities overlap with the target.
- Return up to N concepts (configurable, default 5) with their anchor, a one-line snippet, and the link back to the appropriate ProjectLens tool.

If the sidecar is unhealthy, the existing tool output is returned without the `Related concepts` block — never a hard failure.

`get_table_context` works because the new `table` anchor type is fed into LightRAG from `datastore_tables` summaries.

### (II) History-stage Jira edge

The history stage parses each commit message processed during `file_history` ingestion and extracts `FOR-\d+` ticket references. New behavior:

- For each match, look up `documents.id WHERE source_type='jira' AND external_id='<ticket>'`.
- If a row exists **and** any `lightrag_chunk_state` row has `source_type='doc_jira'` with that `source_anchor_id` (i.e. the doc has been ingested into LightRAG), write a polymorphic edge:
  ```
  source_type = 'document'  source_id = <documents.id>
  target_type = 'file'      target_id = <files.id>
  edge_type   = 'mentions'  confidence = 1.0
  properties  = { "ticket": "FOR-1234", "commit": "<sha>" }
  ```
- Both IDs are valid `BIGINT`. The SHA is preserved as an edge property, not a column.
- If the Jira doc is not in LightRAG, the raw match is logged on the `file_history` row's `diff_snippet` only (today's behavior).

Ticket-extraction code lives in the history stage (`internal/history/`). Add unit tests covering: single match, multiple matches per commit, repeated mentions across commits (idempotent edge upsert via `(source_type, source_id, target_type, target_id, edge_type)` unique constraint).

### Deferred internal uses

- `search_go_context` ∪ `search_docs` fusion (boundary risk).
- LightRAG-enriched prompt for the package summarizer (slows indexer; revisit only if summary quality complaints surface).
- Cross-link between knowledge entries and LightRAG entities (needs schema work).
- Sonnet override for query-time keyword extraction (`QueryParam.model_func`); v1 uses Qwen for both indexing and query.

## Provider stack

Hardware target: Apple Silicon M2 Pro, 16-core GPU, 32GB unified memory. Production target: same models, swap to GPU host.

| Role | Model | Host | Notes |
|---|---|---|---|
| LightRAG indexing + query LLM | `Qwen3-30B-A3B` quantized to q4 | Ollama (Metal) | MoE, ~3B active params, ~18GB on disk. Used by both extraction (insert path) and query-time keyword extraction. v1 simplification — single LLM binding. |
| Embedding | `BAAI/bge-m3` (1024 dim) | Infinity | OpenAI-compatible endpoint. |
| Reranker | `BAAI/bge-reranker-v2-m3` | Infinity | Cohere/Jina-compatible endpoint; `RERANK_BINDING=jina` in LightRAG env points at Infinity directly. |
| Code chunk embedding (existing, unchanged) | `mxbai-embed-large` (1024 dim) | Ollama | Independent vector space; queried only by ProjectLens's existing tools. |

LightRAG's documented advice (`stronger model on query side than indexing`) targets answer-synthesis quality. We disable synthesis by calling `aquery_data`, so query-time work is reduced to keyword extraction — a small task Qwen handles fine. Adding a Sonnet override is deferred (open question 4).

Two embedding spaces are intentional — code chunks and prose chunks are queried through different tools and never compared directly.

### Memory budget on M2 Pro 32GB

| Component | Approximate memory |
|---|---|
| macOS + dev tools | 6–8 GB |
| Postgres (both DBs share one process) | 1–2 GB |
| Memgraph | 0.5–1 GB |
| Qwen3-30B-A3B q4 (Ollama) | ~18 GB |
| Infinity (bge-m3 + bge-reranker-v2-m3) | ~3 GB |
| Headroom | 1–3 GB |

Tight but workable for offline indexing. Mitigations:

- Stage indexing serially.
- Use Ollama's idle-unload to free the LLM between stages if needed.
- Fallback knob in `configs/index.yaml` to swap the indexing model to `Qwen3-14B` if real workload shows pressure. Code path is unchanged.

## Configuration

### `configs/index.yaml` — projectlens-only knobs

Slim block, only what projectlens itself needs to know:

```yaml
lightrag:
  enabled: true
  endpoint: http://lightrag-sidecar:9621
  ingest:
    chunk_token_size: 900
    sources:
      confluence: true
      jira: true
      readmes: true
      migrations: true
      package_summaries: true
      symbol_docstrings: true
      table_summaries: true
  query:
    top_k: 20
    related_concepts_top_k: 5
  internal_uses:
    related_concepts:
      enabled: true
    history_jira_edges:
      enabled: true
```

Provider configuration (LLM endpoint, embedding endpoint, reranker endpoint, storage backends) is **not** in this YAML. It lives in the docker-compose env block where LightRAG core actually reads it.

### docker-compose `lightrag-sidecar` env block

Source of truth for sidecar provider/storage config. The sidecar wrapper does not read `configs/index.yaml`; it reads its own env. Variables follow LightRAG core's naming.

| Env var | Example | Purpose |
|---|---|---|
| `LLM_BINDING` | `ollama` | LLM provider (used for both indexing extraction and query-time keyword extraction in v1) |
| `LLM_BINDING_HOST` | `http://ollama:11434` | Ollama URL |
| `LLM_MODEL` | `qwen3:30b-a3b-q4` | Pinned LLM |
| `EMBEDDING_BINDING` | `openai` | Embedding provider (OpenAI-compatible) |
| `EMBEDDING_BINDING_HOST` | `http://infinity:7997/v1` | Infinity URL |
| `EMBEDDING_MODEL` | `BAAI/bge-m3` | Embedding model |
| `EMBEDDING_DIM` | `1024` | Pinned at first ingest |
| `RERANK_BINDING` | `jina` | Reranker shape (Cohere-compatible works too) |
| `RERANK_BINDING_HOST` | `http://infinity:7997/v1/rerank` | Infinity rerank endpoint |
| `RERANK_MODEL` | `BAAI/bge-reranker-v2-m3` | Reranker model |
| `LIGHTRAG_KV_STORAGE` | `PGKVStorage` | KV backend |
| `LIGHTRAG_VECTOR_STORAGE` | `PGVectorStorage` | Vector backend |
| `LIGHTRAG_DOC_STATUS_STORAGE` | `PGDocStatusStorage` | Doc-status backend |
| `LIGHTRAG_GRAPH_STORAGE` | `MemgraphStorage` | Graph backend |
| `POSTGRES_HOST` | `postgres` | Shared Postgres host |
| `POSTGRES_PORT` | `5432` | |
| `POSTGRES_USER` | `projectlens` | Reuse projectlens role |
| `POSTGRES_PASSWORD` | `${PROJECTLENS_DB_PASSWORD}` | From projectlens `.env` |
| `POSTGRES_DATABASE` | `lightrag` | Separate database |
| `POSTGRES_WORKSPACE` | `projectlens` | Workspace tag for KV/Vector/DocStatus rows |
| `MEMGRAPH_URI` | `bolt://lightrag-memgraph:7687` | Memgraph endpoint |
| `MEMGRAPH_USERNAME` | empty | Memgraph default no-auth |
| `MEMGRAPH_PASSWORD` | empty | |
| `WORKSPACE` | `projectlens` | Memgraph graph name |
| `CHUNK_TOKEN_SIZE` | `1200` | Must be ≥ projectlens's `lightrag.ingest.chunk_token_size` |

The compose file references `${PROJECTLENS_*}` placeholders for user-overridable values (DB password, ports). Users edit `.env` once.

### Environment variables (ProjectLens side, additions)

| Variable | Purpose | Required |
|---|---|---|
| `PROJECTLENS_LIGHTRAG_ENDPOINT` | Override sidecar URL | No |
| `PROJECTLENS_LIGHTRAG_DB_NAME` | Override the LightRAG database name (default `lightrag`) | No |
| `PROJECTLENS_INFINITY_ENDPOINT` | Override Infinity URL | No |
| `ATLASSIAN_EMAIL` | Confluence + Jira basic-auth username | Yes (when docs stage runs) |
| `ATLASSIAN_API_TOKEN` | Confluence + Jira PAT | Yes (when docs stage runs) |

## CLI surface

`index-docs` (from May 1 plan) is **kept** and wired to call the sidecar:

```bash
projectlens index-docs                # incremental sync; respects config + flags for processed source set
projectlens index-docs --full         # pre-pass delete + re-extract for the processed source set
projectlens index-docs --dry-run      # fetch + render counts only, no writes
projectlens index-docs --source confluence
projectlens index-docs --source jira
```

`index-all` order (final):

```
census → parse → chunk → graph → datastore → summarize → docs → history → embed
```

`reindex` and `bootstrap` remain code-only — no behavioral change. Operators who want the full pipeline use `index-all`.

The `docs` stage acquires the writer lock as before. The TUI `O` hotkey from the May 1 plan is kept; pre-flight headline gains a sidecar-health info line.

## Failure modes and ops

- **Sidecar down.** `search_docs` / `search_concept` return a structured error. `Related concepts` blocks silently omitted from existing tools. `index-docs` fails fast. `index_status` reports sidecar health.
- **Memgraph down.** Sidecar ingest cannot write graph rows; `docs` stage fails. Existing projectlens tools unaffected.
- **Infinity down.** Sidecar ingest cannot embed or rerank; `docs` stage fails. Existing tools unaffected.
- **Ollama OOM.** v1 manual swap to `Qwen3-14B` in compose env; auto-fallback deferred.
- **LightRAG schema migration on upgrade.** Sidecar applies on boot. ProjectLens's `schema_migrations` table unaffected. Pin `lightrag-hku` version in `requirements.txt`; bump deliberately.
- **Memory pressure on M2.** Stage indexing serially; swap to 14B in env.

## Rollout plan (high level)

1. Add Memgraph, Infinity service definitions to `docker-compose.yml`. Pull `bge-m3`, `bge-reranker-v2-m3`, `qwen3:30b-a3b-q4`. Verify health probes.
2. Build `projectlens/lightrag-sidecar` image from `docker/lightrag-sidecar/Dockerfile` (FastAPI wrapper, delete-then-insert, `aquery_data`). Configure compose env per the table above. Boot, smoke-ingest a single README with anchor; verify update + delete contract tests pass.
3. Add `lightrag_chunk_state` migration to projectlens (with `source_anchor_id`, `source_type`).
4. Implement ProjectLens pre-chunker + anchor injection (with `source_anchor_id` + `chunk_anchor_id`) + state-table gate + scoped reconciliation. Wire into `internal/docs/indexer.go` so it hands rendered text to the sidecar instead of writing chunks. Add `table` anchor source pulling from `datastore_tables`.
5. Implement MCP tools `search_docs` and `search_concept` as proxies to the sidecar with anchor parsing + contract tests.
6. Implement (I) `Related concepts` block in `get_symbol_context`, `get_package_summary`, `get_table_context`.
7. Implement (II) history-stage `document → file` mention edges (with SHA in properties).
8. Update `index-all` order; verify `reindex`/`bootstrap` unchanged.
9. Update `claude/skills/use-projectlens/SKILL.md` tool-picker table.
10. Backfill: run `index-all --full` to populate the LightRAG KG.

## Open questions deferred to implementation

1. **Auto-fallback on indexing-LLM OOM.** v1 manual; watchdog deferred.
2. **Confluence section-slug stability.** When a heading is renamed, the slug changes → new chunk anchor → orphan detected as stale on the next reconciliation pass and deleted; new chunk inserted. Acceptable, but worth measuring orphan churn during backfill.
3. **Reranker for ProjectLens's existing semantic tools.** Infinity already hosts the model; opt-in flag in a follow-up phase.
4. **Sonnet override for query-time keyword extraction.** Open question for whether benchmark shows measurable quality gain over Qwen for this small task. If yes, add Anthropic provider to the sidecar in a follow-up phase via `QueryParam.model_func`.
5. **Retention policy for KG entities tied to deleted source documents.** Verify LightRAG's `delete_by_doc_id` prunes orphan entities/relations correctly under our access pattern.
6. **Memgraph backup story.** ProjectLens's existing `pg_dump` flow doesn't cover Memgraph; document the snapshot/replay command and where its data volume lives.
7. **Wrapper version pinning policy.** Cadence and process for bumping `lightrag-hku` in the sidecar's `requirements.txt`.

## Trade-offs accepted

- **Python in the deployment.** Sidecar adds a runtime; we explicitly chose not to reinvent LightRAG's KG extraction.
- **We own a thin wrapper around LightRAG core.** Required because `ids=`, idempotent updates, and `aquery_data` are core-only. Wrapper is small, transport-only.
- **Two embedding spaces.** Intentional, since the boundary tools never cross.
- **Open-weight model on the indexing path.** Slower than Sonnet per call, but no per-token cost and self-hostable. Hash gating prevents unnecessary re-runs.
- **Single LLM (Qwen) for indexing and query in v1.** Synthesis is disabled; query-time work is small. Sonnet override deferred to follow-up.
- **Hash-only cost control.** Trades worst-case full-reindex compute cost for predictability the rest of the time.
- **No fusion or routing between code and prose retrieval.** Boundary clarity over recall maximization.
- **Memgraph adds a service.** Avoids pinning shared Postgres to an AGE-bundled image; LightRAG graph queries get a real graph engine in return.
- **Separate `lightrag` database, shared instance.** Hard namespace boundary without spinning up a second Postgres.
- **`reindex` and `bootstrap` stay code-only.** No CLI breaking change. Operators who want the full pipeline use `index-all`.
- **Source vs chunk identity split.** Adds two columns to state but unblocks correct multi-chunk Confluence/markdown handling and source-level deletes.
- **Idempotent `/projectlens/insert`.** Always does delete-then-insert. Slightly more Memgraph/PG churn on first-time inserts (delete-on-absent is a no-op) in exchange for a contract that doesn't leak stale entities on update.
- **Reconciliation scoped by `source_type`.** Partial runs (`--source jira`, config-disabled sources) are safe — they don't sweep unrelated state rows.
