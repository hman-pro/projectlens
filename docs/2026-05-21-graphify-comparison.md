# ProjectLens vs Graphify Comparison

Date: 2026-05-21
Scope: high-signal, in-depth comparison of this project with `/Users/hamed.zohrehvand/source/github/graphify`.
Status: review artifact for future planning; no implementation decisions are final.

## Executive Summary

ProjectLens is on the correct path if the target product is a durable, local-first intelligence service for repeated agent work on a large Go/Postgres codebase.

Graphify is stronger at product packaging, first-run feedback, broad corpus coverage, and visible graph artifacts. ProjectLens is stronger at operational depth: typed MCP tools, Postgres-backed retrieval, code/datastore/history/knowledge layers, freshness checks, writer-lock discipline, and agent-facing structured evidence.

The main lesson is not to copy Graphify's architecture wholesale. The useful transfer is Graphify's user journey: one command, visible outputs, report-first orientation, confidence vocabulary, hook/update ergonomics, and cross-agent install polish. ProjectLens should keep its service-first and schema-backed design, but add an artifact/report layer and smoother adoption loop.

## Process Log

This pass compared current files in both checkouts rather than relying only on prior memory.

Steps performed:

1. Skimmed prior projectlens memory for relevant project direction.
2. Listed both repository structures.
3. Read ProjectLens user docs and design docs.
4. Read Graphify README, architecture, and how-it-works docs.
5. Inspected implemented ProjectLens surfaces:
   - CLI and command model.
   - Indexer pipeline.
   - Datastore and history indexing.
   - MCP tool registry and structured response types.
   - Knowledge storage.
6. Inspected implemented Graphify surfaces:
   - File detection and classification.
   - AST extraction and graph building.
   - Analysis/report generation.
   - CLI install/query/update/export commands.
   - MCP stdio server.
   - Cache, manifest, hook, and security helpers.
7. Retried `git status` with fsmonitor disabled; worktree was clean before writing this file.

Key local evidence:

| Area | ProjectLens | Graphify |
|---|---|---|
| Product statement | `README.md:3-5` | `/Users/hamed.zohrehvand/source/github/graphify/README.md:25-40` |
| Architecture summary | `CLAUDE.md:24-90` | `/Users/hamed.zohrehvand/source/github/graphify/ARCHITECTURE.md:5-31` |
| Primary pipeline | `internal/indexer/indexer.go` | `/Users/hamed.zohrehvand/source/github/graphify/docs/how-it-works.md:1-90` |
| Tool surface | `internal/mcpserver/tools.go` | `/Users/hamed.zohrehvand/source/github/graphify/graphify/__main__.py:1196-1340` |
| Structured/evidence model | `internal/mcpserver/types.go` | `/Users/hamed.zohrehvand/source/github/graphify/ARCHITECTURE.md:33-56` |
| Graph/report layer | `internal/storage/edges.go` | `/Users/hamed.zohrehvand/source/github/graphify/graphify/report.py` |
| Agent onboarding | `docs/AGENT_SETUP.md` and `agent/` | `/Users/hamed.zohrehvand/source/github/graphify/README.md:153-179` |
| Freshness/update | `index_status`, `index_runs`, writer lock | `/Users/hamed.zohrehvand/source/github/graphify/graphify/detect.py:868-1031`, `/Users/hamed.zohrehvand/source/github/graphify/graphify/hooks.py:262-294` |

## Product Intent

### ProjectLens

ProjectLens's stated product is "a memory and search layer for your codebase that any AI coding assistant can plug into." It indexes Go code, PostgreSQL schemas, git history, and planned docs into one MCP-backed query layer. The README frames the agent as the main consumer: the agent asks focused questions and receives precise answers from a local Postgres database.

This is a service-first product. The durable user value is not a one-off graph visualization; it is repeated, trusted retrieval during agent work.

### Graphify

Graphify's product is artifact-first. The README says a user types `/graphify .` and receives `graph.html`, `GRAPH_REPORT.md`, and `graph.json`. It is designed to work across many agents and many content types, including code, docs, PDFs, images, video, and Google Workspace files.

Graphify's first-run experience is very strong. It answers "what did the tool learn?" immediately with files a user can open, inspect, query, and commit.

### Assessment

The projects overlap at "persistent graph for agent context", but they optimize for different depths:

- Graphify optimizes for broad ingest, instant artifacts, and multi-agent distribution.
- ProjectLens optimizes for high-trust operational queries in one known technical ecosystem.

ProjectLens should not try to become Graphify. It should learn from Graphify's adoption surface while preserving its deeper contracts.

## Architecture Comparison

### Runtime Shape

ProjectLens:

- Go service and CLI.
- Postgres 16 + pgvector as the primary state store.
- Streamable HTTP MCP server.
- Configured providers for embeddings and summaries.
- TUI for operational visibility.
- Writer lock for mutation safety.

Graphify:

- Python library plus skill/CLI.
- File artifact output under `graphify-out/`.
- NetworkX graph as the in-memory and serialized graph model.
- Optional MCP stdio server over `graph.json`.
- Hooks and per-agent install helpers.

### Pipeline Shape

ProjectLens's core indexer runs a durable pipeline:

1. Census.
2. Work-list diff.
3. Git state.
4. `go/packages` parse.
5. Store files and symbols.
6. Symbol chunks.
7. Call graph edges.
8. Package summaries.
9. Embeddings.
10. Index run completion.

Graphify's documented pipeline is simpler:

1. `detect()`.
2. `extract()`.
3. `build_graph()`.
4. `cluster()`.
5. `analyze()`.
6. `report()`.
7. `export()`.

The difference matters. ProjectLens has more operational correctness burden: partial runs, freshness, DB migrations, concurrent writes, provider health, and query degradation. Graphify can move faster because the main state is an output directory and a graph file.

### Storage Model

ProjectLens's Postgres schema is appropriate for its mission:

- Symbols, files, chunks, embeddings, summaries.
- Polymorphic edges.
- Datastore tables.
- File and symbol history.
- Knowledge entries.
- Index runs and locks.

Graphify's graph file is appropriate for portability:

- Node-link graph.
- Node metadata: id, label, file type, source file.
- Edge metadata: source, target, relation, confidence, confidence score, source file.
- Optional hyperedges.

ProjectLens should keep Postgres as the source of truth. A Graphify-style `graph.json` can be an export, not the canonical store.

## Retrieval and Agent Surface

### ProjectLens Strength

ProjectLens exposes a task-shaped MCP API:

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

This is better than a generic graph API for day-to-day coding agents because it maps to actual coding questions: symbol lookup, table impact, call context, co-change risk, freshness, and durable lessons.

The structured payload types are also a strong direction. `EvidenceSpan`, `Degradation`, `ProviderHealth`, stale package summaries, table references, change records, and knowledge hits give agents machine-readable trust signals. This is a higher-quality contract than forcing agents to scrape prose.

### Graphify Strength

Graphify exposes lower-level graph operations and user-facing CLI commands:

- `graphify query`
- `graphify path`
- `graphify explain`
- `graphify export callflow-html`
- Optional MCP tools such as `query_graph`, `get_node`, `get_neighbors`, and `shortest_path`.

This is less domain-specific, but much easier to understand and demo. The user can query the artifact even without wiring a long-running service.

### Assessment

ProjectLens's MCP surface is the right primary surface. The gap is not tool quality; it is visibility and experimentation. A user should be able to run a command and get an inspectable report of what ProjectLens currently knows.

## Graph Model and Confidence

Graphify has a simple and useful confidence model:

- `EXTRACTED`: directly found in source.
- `INFERRED`: reasonable deduction, usually with a confidence score.
- `AMBIGUOUS`: uncertain, flagged for review.

ProjectLens already has stronger evidence spans and degradation fields, but its graph edges do not yet present a user-facing confidence vocabulary as clearly. `storage.EdgeRecord` has a `Confidence` field, and the edges table can support this, but the product docs and MCP result model should make confidence more visible.

Recommended direction:

- Keep ProjectLens's typed evidence spans.
- Add or standardize edge confidence semantics.
- Prefer both source and method in confidence metadata:
  - `source`: parser, callgraph, sql_scanner, history, agent, lightrag, docs.
  - `confidence`: extracted, inferred, ambiguous.
  - `score`: optional numeric confidence.

Graphify's terminology is useful because it is easy for an agent and a human to reason about. ProjectLens can adopt the vocabulary without adopting the graph-file architecture.

## Freshness and Update Model

ProjectLens's freshness model is stronger for service use:

- `index_runs` and per-stage runs.
- `index_status` tool.
- Git HEAD and dirty-state intent.
- Provider health.
- Writer lock.
- Incremental history safeguards.

Graphify's freshness model is stronger for casual use:

- Manifest-based change detection.
- Content-hash cache.
- `graphify update`.
- Git hooks after commit/checkout.
- Clear stale graph guidance in generated report.

ProjectLens should keep `index_status` as the trust contract, but add a friendlier loop:

- `projectlens report` should include index freshness and last indexed commit.
- A post-commit or watch-style helper could call a cheap changed-file scan or remind the user to reindex.
- Agent setup should include an easy "is this fresh?" command that prints actionable status, not just raw stage data.

## Artifacts and Human Feedback

This is the biggest gap.

Graphify produces:

- `graph.html`
- `GRAPH_REPORT.md`
- `graph.json`
- Optional call-flow HTML.
- Optional wiki/vault-style exports.

ProjectLens currently has:

- A database.
- CLI status and inspect commands.
- MCP tools.
- TUI.
- Design docs.

The database and tools are more powerful, but they are less visible. For planning, debugging, and onboarding, ProjectLens needs artifact outputs that summarize what the index contains.

Recommended artifact layer:

1. `projectlens report --out docs/projectlens-report.md`
   - Index freshness.
   - Stage health.
   - Provider health.
   - Top packages by symbol count.
   - Top datastore tables by read/write edge count.
   - High-coupling files.
   - Knowledge entries by category.
   - Suggested questions.
   - Known degradation or missing stages.

2. `projectlens export graph --format json`
   - Export polymorphic edges and nodes from Postgres.
   - Include source type, evidence spans, confidence, and freshness metadata.
   - Treat this as portable output, not the source of truth.

3. `projectlens explain-index`
   - Human-readable inventory: "what was indexed and what was skipped".
   - Similar to Graphify's corpus check, but for Go/datastore/history/docs stages.

## Agent Onboarding and Distribution

ProjectLens already has a strong agent direction:

- Vendor-neutral `agent/skills`.
- Claude and Codex wiring.
- `docs/AGENT_SETUP.md`.
- `save_knowledge` with source attribution.
- Hooks for session start, pre-edit impact checks, and stop-time capture.

Graphify is stronger in breadth and polish:

- Many agent-specific install commands.
- Clear command tables.
- Skill files packaged with the Python distribution.
- One obvious command: `graphify install`.

ProjectLens likely does not need to support as many agents immediately. It does need a cleaner install/update story:

- `projectlens agent install codex`
- `projectlens agent install claude`
- `projectlens agent status`
- `projectlens agent doctor`

The current docs and snippets are good for maintainers, but Graphify's CLI install flow is better for users.

## Scope and Corpus Coverage

Graphify supports many languages and media types. That is impressive, but much of it is necessarily heuristic. It also mixes deterministic AST extraction with semantic extraction from model/subagent output.

ProjectLens should stay narrower:

- Go code.
- PostgreSQL schemas and SQL usage.
- Git history and co-change.
- Docs and knowledge that relate to the repo.
- Agent memory.

The right expansion path is depth over breadth:

1. Improve Go symbol identity and edge precision.
2. Improve datastore edge precision.
3. Finish docs/Jira/Confluence integration with clear source IDs.
4. Add report/export artifact surfaces.
5. Only then consider multi-language extraction if the target monorepo demands it.

## Security and Privacy

ProjectLens has a naturally strong local-first position because the source of truth is local Postgres and local repo access. Provider calls are explicit via configured summarizer/embedder.

Graphify has a more complex privacy surface because it supports URLs, docs, PDFs, images, office files, videos, and multiple LLM backends. It compensates with security helpers:

- URL scheme validation.
- SSRF protections.
- Redirect validation.
- Size caps.
- Graph path validation.
- Label sanitization.

ProjectLens should learn from this before adding richer docs ingestion:

- Any URL/Confluence/Jira fetcher needs explicit scheme/host allowlists.
- Any exported HTML needs label/content sanitization.
- Any file ingestion beyond repo source should have clear skip rules for secrets.
- Agent-facing docs should state exactly what leaves the machine.

## Testing Posture

Both projects have meaningful test coverage.

Local counts from this pass:

- ProjectLens: 57 Go test files under `internal/` and `cmd/`.
- Graphify: 46 Python test files under `tests/`.

The test styles differ:

- ProjectLens tests focus on storage integration, MCP handlers, retrieval, writer locks, parser behavior, history, and datastore indexing.
- Graphify tests cover many product edges: language extraction, install flows, cache, validate, export, query/path/explain CLI, hooks, watch, security, and incremental behavior.

ProjectLens could use more Graphify-like product-flow tests around:

- Agent install snippets.
- Report/export command output.
- Freshness/status text.
- Hook behavior if hooks are added.
- CLI "first successful run" experience.

## Correct Path Assessment

ProjectLens is on the correct path for the stated mission.

Strong signals:

- The MCP tool surface is specific to coding-agent workflows.
- Structured payloads with evidence spans are the right agent contract.
- Postgres + pgvector is appropriate for durable, queryable, multi-stage state.
- Datastore and history are first-class; Graphify treats similar concepts more generically.
- Knowledge capture is aligned with repeated agent work and memory across sessions.
- Writer locking and freshness checks show the project is treating operational reality seriously.

Risks:

- It may become powerful but invisible: users cannot easily inspect what the system knows.
- Setup is heavier than Graphify, so onboarding needs more support.
- Docs integration and LightRAG-style plans need careful source identity and update semantics.
- Confidence and provenance exist in pieces but need a consistent public contract.
- The current service-first model lacks a simple artifact users can pass around or review.

## Transferable Lessons From Graphify

### 1. Add visible outputs

Graphify wins the first-run feedback loop. ProjectLens should add a report/export layer that makes the database legible.

Suggested near-term work:

- `projectlens report`
- `projectlens export graph`
- `projectlens explain-index`

### 2. Make confidence simple

Graphify's `EXTRACTED / INFERRED / AMBIGUOUS` tags are easy to understand. ProjectLens can layer this onto typed evidence and edge provenance.

Suggested near-term work:

- Define confidence vocabulary in `internal/mcpserver/types.go` and docs.
- Attach confidence to graph-derived MCP responses.
- Include confidence in graph/report exports.

### 3. Improve install ergonomics

Graphify has a memorable install path. ProjectLens has good snippets but should provide commands.

Suggested near-term work:

- `projectlens agent install claude`
- `projectlens agent install codex`
- `projectlens agent status`
- `projectlens agent doctor`

### 4. Add artifact-based debugging

Graphify's `GRAPH_REPORT.md` doubles as a user-facing audit trail. ProjectLens should produce a similar artifact for a repo index.

Suggested report sections:

- Corpus/index summary.
- Freshness.
- Provider health.
- Query readiness.
- Top packages.
- Top tables.
- High-coupling files.
- Knowledge inventory.
- Missing/degraded stages.
- Suggested agent questions.

### 5. Keep update flow visible

ProjectLens's `index_status` is correct but should be surfaced more often and more plainly.

Suggested near-term work:

- Add freshness summary to report.
- Add `projectlens status --agent` or structured JSON output.
- Add optional hook/watch guidance after the report exists.

### 6. Treat docs/media ingestion carefully

Graphify supports broad content types, but that comes with privacy and security complexity. ProjectLens should not expand ingestion without the same level of explicit security posture.

Suggested near-term work:

- Host allowlist for remote docs.
- Secret skip rules for local docs.
- Source IDs for every document/chunk.
- Clear "what leaves the machine" docs.

## What Not To Copy

Do not copy these Graphify choices directly:

- Making `graph.json` the canonical store. ProjectLens needs DB state.
- Broad multi-language/media support before the Go/Postgres path is excellent.
- Generic graph-only MCP as the primary agent API. ProjectLens's task-shaped tools are better.
- LLM-derived semantic edges as a substitute for deterministic parser/datastore/history edges.
- Huge agent/platform matrix before core install commands are solid for Claude and Codex.

## Proposed Roadmap

### Phase 1: Report and Export

Goal: make ProjectLens inspectable.

Tasks:

- Add `projectlens report`.
- Add `projectlens export graph`.
- Include stage freshness, provider health, top entities, and degradation.
- Add tests for generated report shape.

### Phase 2: Confidence and Provenance

Goal: make every relationship trustable.

Tasks:

- Define confidence terms and edge provenance.
- Backfill or default existing edge types:
  - call graph: extracted/inferred depending on source.
  - SQL scanner: extracted with caveats.
  - co-change: extracted from git history with score.
  - agent knowledge: extracted from agent/user session.
- Surface confidence in MCP structured payloads and report/export.

### Phase 3: Agent Install CLI

Goal: reduce setup friction.

Tasks:

- Add `projectlens agent install/status/doctor`.
- Validate MCP URL and tool list.
- Validate skills/snippets are current.
- Document update/uninstall path.

### Phase 4: Freshness Automation

Goal: make stale indexes hard to miss.

Tasks:

- Add optional hook/watch guidance.
- Add cheap changed-file detection.
- Extend report with "actions to refresh".
- Keep `index_status` as the machine-readable source of truth.

### Phase 5: Docs Integration

Goal: expand context without corrupting identity.

Tasks:

- Align with existing docs-stage and LightRAG design work.
- Preserve stable source document IDs.
- Avoid duplicate source/chunk identity.
- Add security boundaries before remote fetch.

## Questions For The Next Deep Dive

1. Should ProjectLens export a Graphify-compatible `graph.json`, or define its own richer schema?
2. Which report sections can be built entirely from current DB tables?
3. Should confidence live at the edge row level only, or also at MCP result level?
4. How should a report represent degraded semantic search when embeddings are missing?
5. What is the minimal `agent install` command that works for both Claude and Codex?
6. Can current `index_status` structured output become the canonical JSON for `projectlens status --json`?
7. Should generated reports live under the target repo, the projectlens checkout, or both?

## Bottom Line

ProjectLens should stay service-first and schema-backed. That is the correct strategic difference from Graphify.

The immediate opportunity is to add Graphify-style visibility:

- report artifact,
- portable graph export,
- simple confidence vocabulary,
- CLI-driven agent install,
- clearer freshness/update loop.

That would preserve ProjectLens's stronger correctness model while making it easier for humans and agents to see, trust, and reuse what the system already knows.
