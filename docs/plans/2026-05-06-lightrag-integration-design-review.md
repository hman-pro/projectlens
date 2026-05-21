# Review: LightRAG Integration Design v3

Reviewed against `docs/plans/2026-05-06-lightrag-integration-design.md` (Draft v3), the current projectlens codebase, the updated May 1 Docs Stage design, and current upstream LightRAG API/core documentation as of 2026-05-06.

## Findings

### High: reused deterministic LightRAG IDs do not re-extract changed documents

The design relies on stable IDs for both incremental updates and full reindex:

- `lightrag_doc_id = sha1(anchor_id)` is passed to `rag.ainsert(..., ids=[...])`.
- A hash change calls `/projectlens/insert` with the same deterministic ID and expects LightRAG to upsert/re-extract.
- `reindex --full` truncates `lightrag_chunk_state` and treats every chunk as new.

That does not match current LightRAG core behavior. `ainsert` enqueues documents by ID, then `apipeline_enqueue_documents` filters out IDs already present in `doc_status`; duplicates are recorded as duplicate/failed attempts and are not reprocessed. The core docs confirm custom IDs are supported, but the current implementation treats already-seen IDs as duplicates rather than update-in-place records.

Effect: changed content under the same anchor will update `lightrag_chunk_state` locally but leave the old LightRAG entities, relations, chunks, and embeddings in place. `reindex --full` has the same problem because it only truncates ProjectLens state; the LightRAG workspace still contains the old doc IDs, so re-inserting the same IDs is skipped.

Recommended fix: make update semantics explicit. On hash mismatch, call LightRAG deletion for the existing `lightrag_doc_id`, wait for successful cleanup, then insert the new content with the same ID. For `--full`, either clear the whole LightRAG workspace/database/graph before re-inserting, or iterate all known `lightrag_doc_id` values and delete them before truncating state. Add a sidecar contract test that proves `insert(id=A, content=v1)`, then update to `content=v2`, returns v2 in `/projectlens/query` and no stale v1 anchors/entities remain.

References:

- Design: `docs/plans/2026-05-06-lightrag-integration-design.md:179`, `:231-240`, `:471-475`
- LightRAG core docs: `rag.insert("TEXT1", ids=["ID_FOR_TEXT1"])`
- LightRAG current source: `apipeline_enqueue_documents` filters already present doc IDs and creates duplicate records instead of reprocessing them.

### High: multi-chunk documents still collide on `anchor_id` and LightRAG doc ID

The design says Confluence pages are section-based and README/docs markdown is heading-based, then also says the chunk state table is keyed by `anchor_id` and the deterministic LightRAG doc ID is `sha1(anchor_id)`. The anchor ID shapes for `doc_confluence`, `doc_jira`, `doc_readme`, and `doc_migration` identify the source object, not the chunk.

That means a long Confluence page with three sections produces three chunks with the same logical anchor ID:

```text
confluence . FOR . 12345
confluence . FOR . 12345
confluence . FOR . 12345
```

Those chunks cannot all fit in `lightrag_chunk_state(anchor_id PRIMARY KEY)`, and they cannot all be inserted into LightRAG with `ids=[sha1(anchor_id)]`. One wins, the others collide or are treated as duplicates. The open question about section granularity acknowledges this, but it is not safe to defer because the v3 ingest source table already chooses section/heading-based chunking.

Effect: first full ingest of a realistic long page can silently lose sections or fail at the state table/sidecar boundary. Retrieval then misses content even though the document was "processed."

Recommended fix: distinguish source identity from chunk identity. For example:

```text
source_anchor_id = confluence . FOR . 12345
chunk_anchor_id  = confluence . FOR . 12345 # section-slug-or-ordinal
lightrag_doc_id  = sha1(chunk_anchor_id)
```

Store `source_anchor_id`, `chunk_anchor_id`, `content_hash`, and `lightrag_doc_id` in state, with `chunk_anchor_id` as the primary key. Use `source_anchor_id` only for source-level deletes and UI linking. Add a test for one page splitting into multiple chunks and verify all anchors survive insert, state reconciliation, and query parsing.

References: `docs/plans/2026-05-06-lightrag-integration-design.md:147-163`, `:169-179`, `:191-197`, `:231-236`, `:462-463`

### High: stale-row deletion is unscoped and conflicts with partial docs runs

The docs stage deletes every state row whose `last_seen_at` predates the run start after processing the current candidate set. The CLI also keeps partial runs:

```bash
projectlens index-docs --source confluence
projectlens index-docs --source jira
```

Those two contracts conflict. A Confluence-only run will refresh only Confluence candidates; Jira, README, migration, package-summary, symbol-docstring, and table-summary rows will still have old `last_seen_at` values and will be eligible for deletion even though their sources were intentionally not processed. The same risk exists when config disables a source under `lightrag.ingest.sources`.

Effect: a scoped refresh can delete unrelated LightRAG docs and remove valid state rows. The next query loses content for sources that were not part of the run.

Recommended fix: make reconciliation scoped. Add `source_type` and, if needed, `source_scope` to `lightrag_chunk_state`, and delete stale rows only for the source set processed in that run. Reserve "delete all stale rows" for a run that has explicitly enumerated every enabled source. Contract tests should cover `--source confluence` leaving existing Jira/readme/table rows untouched.

References: `docs/plans/2026-05-06-lightrag-integration-design.md:231-236`, `:350-369`, `:420-427`

### Medium: the custom query wrapper is underspecified for structured chunks and anchors

The sidecar endpoint table says `/projectlens/query` forwards to `rag.aquery(...)`, while the MCP proxy contract expects structured chunk content, entities, scores, and anchors parsed out of `chunk_content`. Current upstream LightRAG separates these concerns:

- `/query` and `/query/stream` use `aquery_llm` and can attach references/chunk content in API responses.
- `/query/data` uses `aquery_data` and returns structured `entities`, `relationships`, `chunks`, and `references`.
- Core `QueryParam` has `include_references`, but `include_chunk_content` is an API-level request field, not a core `QueryParam` field.

Because ProjectLens is not using the official server, the custom wrapper must either call `aquery_data` or reproduce the official server's reference/chunk enrichment logic. A simple `rag.aquery(... only_need_context=true ...)` wrapper is unlikely to produce the structured fields the Go MCP proxy needs.

Effect: `search_docs` / `search_concept` may have no reliable place to parse anchors from, especially for graph-local results where entities and relations are returned separately from chunks.

Recommended fix: define the sidecar response schema directly around LightRAG's structured data result:

```json
{
  "entities": [...],
  "relationships": [...],
  "chunks": [{"content": "<<PROJECTLENS_ANCHOR ...>>\n...", "chunk_id": "...", "reference_id": "..."}],
  "references": [...]
}
```

Then implement `/projectlens/query` by calling `rag.aquery_data(...)` or by copying the upstream server's `aquery_llm` enrichment path intentionally. Add a sidecar unit test with a fake LightRAG result containing two anchored chunks and one relation-only hit.

References:

- Design: `docs/plans/2026-05-06-lightrag-integration-design.md:84-87`, `:242-272`
- LightRAG API source: `QueryRequest.include_chunk_content` is excluded before constructing `QueryParam`; `/query/data` returns structured chunks/entities/relationships.

### Medium: query-side model choice is documented but not wired

The provider stack says the indexing LLM is Qwen through Ollama and the query-side LLM is `claude-sonnet-4-6` through Anthropic. The compose env table, however, configures only one LightRAG LLM binding:

```text
LLM_BINDING=ollama
LLM_BINDING_HOST=http://ollama:11434
LLM_MODEL=qwen3:30b-a3b-q4
```

LightRAG uses the configured LLM for query-time work such as keyword extraction unless the caller supplies an override model function. The custom wrapper could do that, but the design does not specify an Anthropic key, a second provider client, or when `QueryParam.model_func` should be used.

Effect: implementation will likely use Qwen for both indexing extraction and query-side LightRAG operations, contrary to the design's quality/performance assumption. If an implementer adds Anthropic ad hoc later, the sidecar config surface changes after rollout.

Recommended fix: either remove the separate query-side Sonnet claim for v1, or define sidecar env/config for a second query LLM and state exactly which endpoint paths use it. If using LightRAG core overrides, make `/projectlens/query` set `QueryParam.model_func` from that provider while `/projectlens/insert` keeps the Ollama indexing model.

References: `docs/plans/2026-05-06-lightrag-integration-design.md:317-323`, `:378-404`; LightRAG `QueryParam` includes an optional `model_func` override.

### Medium: trigger semantics are broader than the rollout steps and current CLI

The design says `projectlens reindex`, `bootstrap`, and `index-all` all use the new full order:

```text
census -> parse -> chunk -> graph -> datastore -> summarize -> docs -> history -> embed
```

But the CLI section and rollout plan only call out `index-all` reordering. In the current repo, `bootstrap` and `reindex` call `indexer.Run`, which performs the code pipeline plus package summaries and embeddings; they do not run datastore or history. `index-all` is the separate orchestration command that runs code, datastore, history, summarize, and embed.

Effect: implementers can reasonably make different choices. One may only add docs to `index-all`; another may expand `reindex`/`bootstrap` to run every side stage, changing existing operator expectations and the docker `projectlens-indexer` service, which currently runs `reindex`.

Recommended fix: choose the command contract explicitly:

- If `reindex` remains code-only, change the trigger section to name only `index-all` and `index-docs`, and update the compose/indexer story separately.
- If `reindex` and `bootstrap` become all-stage commands, add that as a deliberate CLI breaking change and include the exact implementation step for both commands and the TUI job registry.

References: `docs/plans/2026-05-06-lightrag-integration-design.md:215-227`, `:430-456`; current `cmd/projectlens/main.go` has separate `reindex`/`bootstrap` and `index-all` paths.

## Resolved From Prior Reviews

- The deterministic-ID requirement is now correctly recognized as requiring a ProjectLens-owned wrapper rather than the official LightRAG REST insert endpoint.
- The graph backend issue is resolved by using Memgraph instead of PostgreSQL AGE/`PGGraphStorage`.
- The storage isolation story is materially better: LightRAG has its own `lightrag` Postgres database plus a Memgraph service, instead of sharing ProjectLens migrations.
- The May 1 Docs Stage design is now marked as superseded for chunking/retrieval, so the repository no longer presents two active docs-retrieval designs.
- Table anchors and table summary ingestion are now included, so `get_table_context` enrichment has a plausible LightRAG source.
- The Jira edge design now uses `document -> file` IDs that fit the existing `edges` schema, with the commit SHA stored in `properties`.
- The pipeline order now puts `summarize` before `docs` and `docs` before `history`, which satisfies the package-summary and Jira-edge dependencies for `index-all`.

## Open Questions

- Should LightRAG update be implemented as delete-then-insert, or should `lightrag_doc_id` include the content hash and keep old IDs only long enough for cleanup?
- Should all pre-chunked sources use both `source_anchor_id` and `chunk_anchor_id`, even when they usually produce one chunk? That would make the state model uniform.
- Is `reindex` intended to stay code-centric, or should it become a full orchestration command like `index-all`?
- Should the sidecar expose a structured `/projectlens/query-data` endpoint separately from `/projectlens/query`, or should the single query endpoint always return the structured shape the MCP proxy needs?

## External References

- LightRAG API Server docs: https://github.com/HKUDS/LightRAG/blob/main/docs/LightRAG-API-Server.md
- LightRAG Core docs: https://github.com/HKUDS/LightRAG/blob/main/docs/ProgramingWithCore.md
- LightRAG `QueryParam` / query API source: https://raw.githubusercontent.com/HKUDS/LightRAG/main/lightrag/base.py and https://raw.githubusercontent.com/HKUDS/LightRAG/main/lightrag/api/routers/query_routes.py
- LightRAG insert pipeline source: https://raw.githubusercontent.com/HKUDS/LightRAG/main/lightrag/lightrag.py
