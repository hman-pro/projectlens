# ProjectLens vs pnphive Comparison

Date: 2026-05-21
Scope: high-signal, in-depth comparison of this project with `/Users/hamed.zohrehvand/source/github/pnphive`.
Status: review artifact for future planning; no implementation decisions are final.

## Executive Summary

pnphive is the closest sibling project to ProjectLens in the local checkout set. Both projects are local-first knowledge systems for `example-org/ingest`, both use Postgres + pgvector, both expose an MCP surface for agent use, and both exist to keep agents from rediscovering context every session.

The strategic difference is that pnphive is a broad P&P knowledge-base RAG product, while ProjectLens is a typed codebase-intelligence service. pnphive optimizes for corpus breadth, one-command onboarding, snapshot restore, operational run history, and business-context retrieval across code, commits, PRs, docs, Confluence, and Jira. ProjectLens optimizes for structured code/data-flow answers: symbols, call graph edges, SQL table references, package summaries, change history, co-change coupling, index freshness, and anchorable local knowledge.

ProjectLens should not collapse into pnphive's flat chunk store. Its advantage is the typed graph and evidence-bearing MCP contract. The useful transfer is pnphive's product and operations layer: one-command install, snapshot/bootstrap flow, ingest history UX, append-only lineage for changing content, resource caps, broad business-source ingestion discipline, query-quality techniques, and explicit privacy posture.

## Process Log

This pass compared current files in both checkouts rather than relying only on prior memory.

Steps performed:

1. Reused the existing Graphify comparison artifact as the structural template.
2. Listed pnphive repository files.
3. Read pnphive README, pyproject, installer, config, CLI, MCP server, retrieval, storage, scrubber, migrations, tests, and local decision notes.
4. Re-read ProjectLens README, CLAUDE, indexer, MCP tool registry, structured response types, schema migrations, and report/export design.
5. Checked both worktrees with fsmonitor disabled after the default status command hit the known fsmonitor IPC error.

Key local evidence:

| Area | ProjectLens | pnphive |
|---|---|---|
| Product statement | `README.md` | `/Users/hamed.zohrehvand/source/github/pnphive/README.md` |
| Architecture summary | `CLAUDE.md` | `/Users/hamed.zohrehvand/source/github/pnphive/README.md`, `docs/notes/decisions.md` |
| Primary pipeline | `internal/indexer/indexer.go` | `/Users/hamed.zohrehvand/source/github/pnphive/src/pnphive/cli.py`, `src/pnphive/extract/*.py` |
| Storage model | `migrations/*.sql` | `/Users/hamed.zohrehvand/source/github/pnphive/sql/*.sql`, `src/pnphive/store/pgvector.py` |
| Retrieval model | `internal/retrieval/*.go` | `/Users/hamed.zohrehvand/source/github/pnphive/src/pnphive/retrieve.py`, `src/pnphive/serve.py` |
| MCP surface | `internal/mcpserver/tools.go`, `types.go` | `/Users/hamed.zohrehvand/source/github/pnphive/src/pnphive/mcp_server.py` |
| Onboarding | `docs/AGENT_SETUP.md`, `agent/` | `/Users/hamed.zohrehvand/source/github/pnphive/scripts/install.sh`, `scripts/pnphive-mcp.sh` |
| Operations | `index_status`, TUI, writer lock | `pnphive stats`, `pnphive history`, `ingest_runs`, lockfiles |
| Security/privacy | local providers, MCP structured evidence | `/Users/hamed.zohrehvand/source/github/pnphive/src/pnphive/scrub/text.py`, README egress notes |

## Product Intent

### ProjectLens

ProjectLens's stated product is "a memory and search layer for your codebase that any AI coding assistant can plug into." The project indexes Go code, PostgreSQL schemas, git history, and planned docs into one MCP-backed query layer. The agent is the primary consumer.

Its durable value is precise codebase intelligence:

- Find a symbol and where it lives.
- Ask how a Go implementation works.
- Inspect callers, callees, implementors, and package summaries.
- Ask which code reads or writes a database table.
- Inspect recent file/symbol history and co-change coupling.
- Save and search anchored knowledge learned during agent sessions.

ProjectLens is service-first and contract-first. Its best answers are typed, structured, and backed by evidence spans.

### pnphive

pnphive's stated product is a "Local retrieval-augmented assistant for Pricing & Promotions knowledge." It indexes the `example-org/ingest` monorepo, archived `example-org/frontend` history, merged PRs, design docs, Confluence pages, and Jira tickets into a local pgvector store. Claude Code consumes it through one MCP tool: `search_knowledge_base`.

Its durable value is broad P&P institutional memory:

- Why a behavior exists.
- What a PR discussion or review said.
- What a Jira ticket or Confluence page decided.
- What older frontend or commit history said before the monorepo merge.
- What cross-source context is relevant to a business or product question.

pnphive is corpus-first and retrieval-first. Its best answers are top-K chunks with source headers, optionally summarized by Claude.

### Assessment

These projects overlap more than ProjectLens and Graphify did. They are both local RAG systems over the same business/code ecosystem, but they differ in the shape of trust:

- pnphive trusts a broad chunk corpus plus hybrid retrieval/reranking to surface the right context.
- ProjectLens trusts typed extraction, schema-aware storage, graph edges, and structured MCP responses.

ProjectLens should keep its typed code-intelligence identity. pnphive shows what ProjectLens is missing around installability, corpus breadth, run observability, and user-facing operational polish.

## Architecture Comparison

### Runtime Shape

ProjectLens:

- Go binaries: CLI, MCP HTTP server, TUI.
- Postgres 16 + pgvector as primary state.
- Streamable HTTP MCP server usable by any compatible agent.
- Provider abstraction for embeddings and summaries.
- Writer lock around mutating index work.
- Agent integration assets under `agent/`.

pnphive:

- Python package and Typer CLI.
- Docker Compose stack with Postgres, bootstrap restore, and warm pnphive container.
- Postgres + pgvector with a single `chunks` table as the main corpus.
- Stdio MCP server through FastMCP, currently oriented around Claude Code.
- Local sentence-transformer embedder and reranker kept warm in the service process.
- Wrapper scripts for Docker-backed CLI and MCP registration.

### Pipeline Shape

ProjectLens's main code indexer runs a typed pipeline:

1. Census.
2. Work-list diff.
3. Git state.
4. `go/packages` parse.
5. Store files and symbols.
6. Symbol chunking.
7. Call graph edge creation.
8. Package summaries.
9. Embeddings.
10. Index run completion.

Additional stages index datastore references, git history/coupling, embeddings, summaries, and knowledge.

pnphive's pipeline is source-extractor oriented:

1. Extract one source: code, docs, commits, PRs, Confluence, Jira.
2. Scrub content.
3. Chunk into source/repo/source_id/chunk_idx rows.
4. Compute content hash.
5. Skip unchanged current rows.
6. Embed changed rows.
7. Supersede older current rows and insert new rows.
8. Record a full-fidelity `ingest_runs` row with status, timings, config, environment, HTTP stats, scrub stats, and sub-source counts.

The difference matters. ProjectLens has a richer model of code behavior, while pnphive has a richer model of ingestion operations and business-source coverage.

## Storage Model

### ProjectLens

ProjectLens stores typed entities:

- `files`
- `symbols`
- `chunks`
- `embeddings`
- `summaries`
- polymorphic `edges`
- `datastore_tables`
- `documents`
- `symbol_history`
- `file_history`
- `knowledge_entries`
- `index_runs`
- `git_refs`
- `index_locks`

This schema lets ProjectLens answer questions that depend on structure, not just text similarity. For example, `get_table_context` can return table columns and read/write references; `get_symbol_context` can return callers/callees/implementors; `get_coupling` can return co-change relationships.

### pnphive

pnphive stores a deliberately generic corpus:

- `chunks(source, repo, source_id, chunk_idx, content, metadata, embedding, content_tsv)`
- `ingest_runs(...)`
- append-only lineage fields on chunks: `is_current`, `inserted_at`, `superseded_at`, `content_hash`, `run_id`

The schema is flatter, but it has two valuable properties:

- It can absorb many source types quickly without adding a new domain table per source.
- It can preserve old chunk versions while default retrieval searches only `is_current = TRUE`.

### Assessment

ProjectLens's schema is better for high-confidence code and database questions. pnphive's schema is better for broad ingestion and operational simplicity.

The best blend is not one schema replacing the other. ProjectLens should keep typed core tables, but borrow pnphive's lineage and run-audit ideas for source types that naturally behave like documents: Confluence pages, Jira issues/comments, PR discussions, and design docs.

## Retrieval Model

### ProjectLens

ProjectLens exposes multiple retrieval paths:

- Exact and lexical symbol search.
- Semantic search over embedded code chunks.
- Query classification and routing.
- Graph traversal for callers/callees/implementors/package dependencies.
- SQL table context through datastore edges.
- History and coupling through git-derived tables.
- Knowledge search by vector and anchor.

The MCP responses are typed payloads. The most important design choice is that results carry evidence spans: file path plus line range. Agents can re-read exact bytes before changing code.

### pnphive

pnphive retrieval is classic high-quality RAG:

- Embed query locally.
- Vector top-N by cosine distance.
- Text top-N by `tsvector` / `ts_rank`.
- Reciprocal rank fusion.
- Cross-encoder rerank.
- Optional query rewriting in the CLI path.
- Default current-only retrieval, with `--include-history` for superseded chunks.

The MCP tool returns formatted chunks with source/repo/source_id/chunk headers and useful metadata such as title, author, created date, and URL when present.

### Assessment

pnphive is stronger at "find the best evidence from a broad corpus." ProjectLens is stronger at "return the exact code object and its relationships."

ProjectLens should borrow pnphive's retrieval quality stack where it fits:

- Hybrid vector + lexical retrieval with reciprocal rank fusion for document-like sources.
- Cross-encoder reranking for broad natural-language context searches.
- Optional query rewriting for user-facing CLI/report workflows.

ProjectLens should not route precise symbol/table/context tools through a generic top-K chunk interface. That would throw away its main advantage.

## MCP Surface

### ProjectLens Strength

ProjectLens exposes 10 tools:

- `find_symbol`
- `search_go_context`
- `get_symbol_context`
- `get_package_summary`
- `get_table_context`
- `index_status`
- `get_change_history`
- `get_coupling`
- `save_knowledge`
- `search_knowledge`

This is a strong agent contract. The tool names map directly to agent intentions, and the payloads distinguish not-found, degraded, stale, anchored, and evidence-backed cases.

### pnphive Strength

pnphive exposes one broad tool:

- `search_knowledge_base`

The tool description is excellent. It tells Claude exactly when to use it: P&P domain knowledge, code patterns, design rationale, why-decisions, bug history, customer-driven features, PR review discussions, Jira history, and Confluence docs.

The one-tool surface is easy to register and easy to understand. It also lets Claude issue multiple phrasings if needed.

### Assessment

ProjectLens's tool surface is better for precision work. pnphive's tool description is better at teaching the agent when to reach for the capability.

Transferable lesson: ProjectLens should keep multiple typed tools, but improve descriptions and skill prompts so agents know which tool to use for broad business-context questions versus symbol/table/change-impact questions.

## Onboarding and Distribution

### pnphive Strength

pnphive is much stronger here:

- One command: `bash scripts/install.sh`.
- Installs or verifies `gh`.
- Seeds `.env`.
- Downloads a snapshot release.
- Builds the Docker image.
- Starts Postgres/bootstrap/pnphive.
- Restores snapshot idempotently.
- Registers the Claude Code MCP server if `claude` is available.
- Provides a simple wrapper: `./scripts/pnphive stats | history | query | ingest`.

The snapshot restore is especially important. A new user does not have to perform a multi-hour full ingest before the tool becomes useful.

### ProjectLens Current State

ProjectLens has good docs and agent assets:

- `docs/AGENT_SETUP.md`
- `agent/skills`
- Claude and Codex snippets
- Make targets for bootstrap, reindex, index-all, MCP, TUI

But the first-run path is still heavier. Users need to start Postgres, configure providers, index the target repo, build/start the MCP server, then wire the agent. There is no snapshot bootstrap loop yet.

### Assessment

ProjectLens should copy this product pattern directly, adjusted for its stack:

- `scripts/install.sh` or `projectlens install` for first-time setup.
- Snapshot download/restore for internal target repos where allowed.
- Idempotent MCP registration helpers for Claude, Codex, Cursor, and generic MCP.
- A wrapper that makes `status`, `report`, `query`, and `reindex` discoverable.

This matters more than it looks. A code-intelligence service that takes hours before first useful output loses users before they see the typed graph value.

## Freshness and Operations

### ProjectLens

ProjectLens has a strong freshness model:

- `index_status` reports stage freshness.
- `index_runs` tracks per-stage status.
- TUI surfaces index health, provider state, runs, logs, and actions.
- Writer lock serializes mutating operations.
- Provider health is part of the index state contract.

### pnphive

pnphive has a strong run-history model:

- `pnphive stats` shows current chunk counts, source/repo breakdown, run statuses, recent runs, and failures.
- `pnphive history` shows detailed per-run durations, version, inserted/skipped/superseded counts, HTTP request counts, embedding time, and errors.
- `ingest_runs` stores environment, config snapshot, CLI args, timings, HTTP stats, scrub stats, sub-source counts, and error text.
- Watermarks advance only on successful runs.
- Ingest lockfile prevents concurrent ingestion.
- Test wrapper and ingest wrapper both enforce resource caps and concurrency discipline.

### Assessment

ProjectLens's freshness contract is more agent-facing. pnphive's run history is more operator-facing.

ProjectLens should merge the strengths:

- Keep `index_status` and TUI.
- Add pnphive-like run history detail for every indexing stage.
- Store config snapshot and environment fingerprint for each run.
- Track inserted/updated/skipped/superseded counts per stage.
- Make `projectlens report` include these details once the report/export work lands.

## Corpus Coverage

### pnphive Strength

pnphive indexes high-value business context that ProjectLens currently only plans or partially models:

- current code
- commits
- merged PRs
- PR reviews
- PR review comments
- repo docs
- Confluence pages
- Confluence comments
- Jira issue bodies
- Jira comments
- archived frontend history

That corpus is what answers "why" questions. Typed code structure alone often cannot explain product rationale, customer pressure, rollout decisions, or review context.

### ProjectLens Strength

ProjectLens has higher-confidence code and datastore extraction:

- Go symbols from `go/packages`.
- Call graph edges.
- Interface implementation context.
- SQL table schemas and read/write edges.
- Co-change coupling.
- Anchored knowledge captured by agents during real work.

### Assessment

ProjectLens should not try to ingest all pnphive sources as undifferentiated text in the core code indexer. It should add a document/business-context lane with source-specific metadata and freshness tracking, then connect it to typed anchors where possible.

Good examples:

- PR discussions can anchor to files, symbols, or migrations when path/line metadata is available.
- Jira tickets can anchor to symbols/tables/packages mentioned in the chunk.
- Confluence pages can become documents with stable URLs and section IDs.
- Retrieved document chunks can supplement `search_go_context`, not replace `get_symbol_context`.

## Security and Privacy

### pnphive Strength

pnphive is explicit about privacy:

- Bulk corpus and embeddings stay local.
- Only top-K matching chunks leave the laptop when using Claude answer mode.
- MCP retrieval itself is local and no-LLM.
- Content scrubber drops private-key chunks and redacts JWTs and high-entropy env values.
- The README clearly documents Anthropic, GitHub, and Atlassian credential boundaries.

The broad source scope also creates a bigger privacy surface. Jira, Confluence, PR comments, and code can contain secrets, customer data, or sensitive incident context. pnphive addresses this with scrubber logic and explicit docs, but the risk is still inherently larger than a code-only index.

### ProjectLens

ProjectLens's current privacy posture is simpler:

- Source code stays local.
- Embeddings can be local by default.
- MCP serves from local Postgres.
- Structured evidence lets agents inspect small exact spans instead of shipping broad chunks by default.

But as ProjectLens adds docs/Jira/Confluence, it inherits pnphive's risk profile. It needs the same explicit redaction and egress story before broad-source ingestion becomes default.

### Assessment

Before expanding business-source ingestion, ProjectLens should define:

- Which content is embedded locally.
- Which content can leave the machine through summarization or answer generation.
- Which sources are scrubbed, dropped, or metadata-only.
- How secrets and high-entropy values are redacted.
- Whether author names/emails are retained.
- How generated reports/export files avoid leaking sensitive chunks by accident.

## Testing

### ProjectLens

ProjectLens has broader Go test coverage by file count: 59 `*_test.go` files in the current checkout. It covers storage, retrieval, MCP behavior, report/export design tests, TUI behavior, history indexing, and integration paths.

### pnphive

pnphive has 12 pytest files across unit, integration, and e2e suites. The test harness is unusually pragmatic:

- DB tests isolate with `_test_*` repo names.
- Stub embedder avoids loading real models for many tests.
- Resource caps are enforced in `tests/conftest.py`.
- A lockfile prevents concurrent pytest invocations after real laptop OOM incidents.
- E2E tests mock network and subprocess boundaries for extractors.

### Assessment

ProjectLens has more test surface. pnphive has better operational test discipline around local resource safety. ProjectLens's test runner and TUI/indexer workflows could adopt similar guardrails for heavyweight provider/model/DB tests.

## Where ProjectLens Is Stronger

- Typed symbol model instead of opaque code chunks.
- Go parser and call graph extraction.
- Datastore schema and read/write edge model.
- Structured MCP payloads with not-found/degraded/stale semantics.
- Evidence spans for code changes.
- Multiple intent-specific MCP tools.
- Agent-captured knowledge anchored to code, files, tables, and packages.
- Writer lock integrated with DB-backed liveness semantics.
- TUI for live operational control.
- Vendor-neutral Streamable HTTP MCP server.
- Report/export design keeps graph artifacts native to the schema.

These are meaningful advantages. pnphive's flat chunk model would struggle to answer many of ProjectLens's best questions without additional code inspection by the agent.

## Where pnphive Is Stronger

- One-command install.
- Snapshot bootstrap.
- Dockerized always-warm service.
- Direct Claude Code MCP registration.
- Broad business corpus: PRs, reviews, commits, Jira, Confluence.
- Append-only chunk lineage with current-only retrieval default.
- Detailed run history and source/repo statistics.
- Query rewriting, RRF, and reranking for broad RAG quality.
- Clear resource caps for laptop operation.
- Explicit scrubber and privacy language.
- Practical wrappers for day-to-day use.

These are product and operations strengths. They make pnphive easier to adopt and easier to trust as a running local tool.

## Transferable Lessons From pnphive

### 1. Add a Snapshot and Bootstrap Path

ProjectLens's first useful result currently requires a full index. pnphive avoids that with snapshot download and restore.

Recommendation:

- Add snapshot export/restore for known internal target repos.
- Include schema version and provider metadata in the snapshot.
- Make restore idempotent.
- Let users run `projectlens status` immediately after restore.

### 2. Build a One-Command Installer

pnphive's `scripts/install.sh` handles prerequisites, `.env`, snapshot, Docker, service startup, and MCP registration.

Recommendation:

- Add `scripts/install.sh` or `projectlens install`.
- Keep it idempotent.
- Register agent integrations when CLIs are present.
- Print a short verification prompt at the end.

### 3. Expand Run History Beyond Freshness

ProjectLens already knows stage freshness. pnphive records the operational evidence behind a run.

Recommendation:

- Store environment fingerprint, config snapshot, provider versions, CLI args, stage timings, inserted/updated/skipped counts, and error text.
- Show these in TUI and `projectlens report`.
- Preserve "latest successful watermark" semantics for incremental stages.

### 4. Use Append-Only Lineage for Document-Like Sources

pnphive's `is_current` / `superseded_at` model is useful for Jira, Confluence, docs, PR discussions, and agent knowledge.

Recommendation:

- Keep code symbols/files as typed current-state entities.
- Use append-only lineage for source documents whose history is useful.
- Add an explicit `include_history` or time-travel option for document retrieval.

### 5. Borrow Hybrid RAG Quality Techniques

ProjectLens's typed tools should stay typed, but broad document search would benefit from pnphive's retrieval stack.

Recommendation:

- Use vector + lexical retrieval with RRF for document/business-context search.
- Add reranking where source breadth makes top-K quality unstable.
- Consider query rewriting only for broad queries, not exact symbol/table tools.

### 6. Add Business Context Without Flattening the Core

pnphive proves that Jira, Confluence, PR reviews, and comments are valuable for P&P work.

Recommendation:

- Ingest these as a document layer.
- Preserve source-specific metadata and stable URLs.
- Link to symbols/files/tables/packages when possible.
- Let `search_go_context` include business chunks as supporting context while `get_symbol_context` remains structural.

### 7. Copy the Resource-Safety Practices

pnphive's local resource caps came from real operational pain.

Recommendation:

- Add test/integration wrappers that cap heavyweight local provider work.
- Prevent accidental concurrent expensive test/indexer invocations where they can saturate a laptop.
- Make resource defaults explicit in docs and `.env`.

### 8. Make Privacy and Egress Explicit

Broad business-source ingestion needs a clear boundary.

Recommendation:

- Document what stays local and what can leave during summarization/answering.
- Add scrubber tests before adding Jira/Confluence as default stages.
- Include redaction counters in run history and reports.

## Things Not To Copy Directly

Do not copy these pnphive choices directly into ProjectLens:

- A single generic `chunks` table as the canonical model for code.
- One broad MCP search tool as the only agent interface.
- Stdio-only MCP as the primary transport.
- Claude-Code-only registration as the main integration story.
- Manual migration application as the normal operational path.
- Flat chunk headers as a substitute for file/line evidence spans.
- Broad source ingestion without first-class privacy and retention decisions.

These choices are reasonable for pnphive's product, but they would weaken ProjectLens's typed-code intelligence promise.

## Strategic Roadmap Implications

### Phase A: Productize First-Run

Highest leverage:

1. Snapshot export/restore.
2. One-command install.
3. Agent registration helpers.
4. `projectlens report` as the first visible audit artifact.

This is the pnphive lesson with the fastest adoption payoff.

### Phase B: Improve Run Observability

Add pnphive-style run detail to ProjectLens:

1. Config snapshot.
2. Environment/provider fingerprint.
3. Per-stage timings.
4. Insert/update/skip counts.
5. Redaction/provider/error counters where relevant.
6. Better history in TUI and report output.

### Phase C: Add Business-Context Sources

Bring in pnphive's corpus breadth cautiously:

1. PRs and review comments first, because they can often anchor to files/lines.
2. Repo docs next, because they are local and easier to scrub.
3. Confluence/Jira after privacy, auth, redaction, and incremental semantics are settled.

### Phase D: Retrieval Quality for Broad Sources

For the new document lane:

1. Hybrid vector + lexical retrieval.
2. RRF fusion.
3. Optional reranker.
4. Optional query rewriting.
5. Current-only default with explicit history inclusion.

### Phase E: Connect Documents to Typed Graph

The differentiator is not merely importing pnphive's corpus. It is linking that corpus to ProjectLens's typed entities:

- PR review comment -> file/symbol if path/line resolves.
- Jira ticket -> package/table/symbol if names resolve.
- Confluence section -> package/table/domain concept.
- Agent knowledge -> exact anchors through `save_knowledge`.

## Open Questions

1. Should ProjectLens support a snapshot format for any target repo, or only blessed internal targets?
2. Should broad business-context ingestion live in ProjectLens itself, or be bridged from pnphive as an external MCP source?
3. Should ProjectLens add a general `search_business_context` tool, or fold business chunks into `search_go_context`?
4. How strict should redaction be for Jira/Confluence? Drop suspicious chunks, redact inline, or metadata-only for sensitive sources?
5. Should `projectlens report` include sample chunk excerpts, or only counts/metadata to avoid accidental leakage?
6. Should append-only history apply to `knowledge_entries` as well as imported documents?
7. Should ProjectLens adopt pnphive's reranker dependency, or keep provider-neutral reranking behind an interface?

## Final Recommendation

ProjectLens should treat pnphive as a product/operations reference, not as an architecture replacement.

The concrete direction:

- Keep ProjectLens's typed graph, structured MCP tools, evidence spans, writer lock, and provider abstraction.
- Borrow pnphive's installer, snapshot bootstrap, run-history depth, resource-safety practices, lineage model for documents, and broad-source retrieval quality techniques.
- Add business context as a linked document layer, not as a flat replacement for symbols/tables/edges.

If Graphify taught ProjectLens to add visible artifacts, pnphive teaches it to become easier to install, easier to operate, and richer in business context without giving up typed code intelligence.
