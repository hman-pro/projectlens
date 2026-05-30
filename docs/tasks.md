# ProjectLens Tasks

Date: 2026-05-30
Status: canonical project task list

This file owns the current task queue. Older dated priority notes and parked
backlogs should be folded here instead of creating another competing list.

## Priority Rules

- `P0`: correctness, data integrity, security, or agent-contract blocker.
- `P1`: active product priority.
- `P2`: important follow-up, not next.
- `P3`: parked idea or deliberate non-priority.

## Status Values

- `Active`: in design or implementation now.
- `Next`: ready to pick after active work.
- `Planned`: accepted direction, needs design/spec before coding.
- `Parked`: useful later, intentionally not near-term.
- `Done`: shipped and verified.

## Active Tasks

| Priority | Task | Status | Next Action | Done When |
|---|---|---|---|---|
| `P1` | Inspectable artifacts: close out `export graph` closure diagnostics | Active | Commit the in-flight `internal/export/graph.go` + `graph_test.go` work: `Export` returns `Diagnostics`, skip-reason classification, knowledge `table`-anchor fix, closure/endpoint-shape tests. Run `make test`, then close this task. | Closure diagnostics surface skipped edges on stderr + JSON envelope, all endpoint shapes covered by tests, changes committed. |
| `P1` | PR / review-context ingestion | Active | Write the focused design doc (observability prereq now shipped). Note the document-lane foundation already exists — `internal/storage/context_items.go` (`ExternalID`) and `context_sources.go` (`github:owner/repo`); design the GitHub client, incremental sync, redaction, idempotency on top of it. | Merged PRs, comments, reviews, and inline review metadata can be ingested incrementally with provenance, redaction, pagination tests, idempotency tests, and optional anchors to files/symbols. |

## Next Tasks

| Priority | Task | Status | Next Action | Done When |
|---|---|---|---|---|
| `P2` | End-to-end smoke test | Planned | Promote the brief below into an implementation plan. Existing `scripts/release-smoke.sh` only covers the embedding+DB contract — it is not the full loop. | `make smoke` proves the full indexer to storage to MCP loop against a small fixture repo in under 5 minutes. |

## Recently Done

| Priority | Task | Status | Evidence |
|---|---|---|---|
| `P1` | `projectlens report` (markdown/json, `--format`, `--out`, all sections) | Done | `cmd/projectlens/report.go`, `internal/report/` |
| `P1` | `projectlens export graph` (native-schema nodes+edges, stable IDs, streaming) | Done | `cmd/projectlens/export.go`, `internal/export/graph.go` — closure-diagnostics hardening remaining (see Active) |
| `P1` | Run observability for indexing stages | Done | migration 008 + `internal/storage/indexruns.go` + indexer wiring + report stage detail + TUI runs panel; provider identity + error redaction in place |

## 1. Inspectable Artifacts

Goal: make the indexed database legible without requiring a user or agent to
understand the schema directly.

This is the Graphify lesson: visible artifacts create trust. ProjectLens should
do this in a schema-native way rather than by copying Graphify's
graph-file-first architecture.

Deliverables:

- `projectlens report`
  - Markdown output by default.
  - JSON output with `--format json`.
  - Optional `--out <path>`.
- `projectlens export graph`
  - Streams native-schema graph JSON.
  - Includes nodes and edges with stable IDs.
  - Ensures every exported edge endpoint resolves to an exported node.

Initial report content:

- Repository and git state.
- Index stage freshness.
- Provider health.
- Writer activity.
- Counts for files, symbols, chunks, edges, tables, history rows, knowledge entries.
- Top packages by symbol/chunk count.
- Top datastore tables by read/write edge count.
- Top co-change relationships.
- Recent knowledge entries.
- Degraded or missing sections with suggested actions.

Acceptance criteria:

- A user can run one command and understand what ProjectLens currently knows.
- Report generation is read-only and safe during indexing.
- Graph export has a closure test: every edge endpoint exists in the node set.
- Output formats are deterministic enough for tests and future diffing.
- The implementation does not make `report` depend on `mcpserver` internals.

Risks:

- Graph node ID drift between nodes and edges.
- Report queries accidentally duplicating `index_status` logic.
- Read-only report output appearing authoritative while a writer is active.

Starting point:

- `docs/superpowers/plans/2026-05-21-report-and-graph-export.md`
- `docs/superpowers/plans/2026-05-21-report-and-graph-export-review.md`

## 2. Run Observability

Goal: make every indexing action explain what happened, not just whether a
stage is fresh.

This is the pnphive lesson: freshness is useful, but run evidence is what makes
operational behavior debuggable.

Deliverables:

- Stage/run records that capture status, timestamps, failure text, safe provider identity, and stage-specific counts.
- Report/TUI/status surfaces that can expose run evidence without requiring log scraping.
- Guardrails against storing secret-bearing config or provider endpoints.

Acceptance criteria:

- A failed run leaves enough detail to identify which phase failed and why.
- A no-op incremental run is recorded as a meaningful completed check, not invisible.
- `index_status` can continue to answer freshness questions without hiding run-level details from report/TUI.
- The schema can support future stages without another redesign.

Risks:

- Overloading `index_runs` with stage-specific fields that do not generalize.
- Breaking existing status/TUI assumptions.
- Recording secrets in config snapshots or errors.

Starting point:

- `docs/2026-05-23-run-observability-design.md`

## 3. PR / Review-Context Ingestion

Goal: add the first document-like business/human-context lane, starting with
merged PRs and review discussions.

This is the highest-value corpus expansion before Jira/Confluence because PR
context is close to code and often carries file/path/line metadata.

Scope:

- Merged PR title/body/metadata.
- Top-level issue comments on PRs.
- Review summaries.
- Inline review comments.
- Relevant path/line/commit metadata where available.

Out of scope for the first pass:

- Jira.
- Confluence.
- Slack.
- Generic web/docs ingestion.
- Agent install/distribution.

Storage direction:

- Use a document-like lane rather than flattening the typed code model.
- Store stable external IDs such as `github:owner/repo#123`.
- Preserve metadata for author, merged time, URL, state, path, line, and review state.
- Add optional edges from PR chunks to files/symbols when anchors resolve.

Retrieval direction:

- Answer why code changed, what reviewers decided, whether behavior was debated, which PR introduced a path, and what human context exists around a symbol/table/package.
- Complement typed tools instead of replacing them.
- Let broad context search include PR chunks as supporting evidence.

Acceptance criteria:

- Incremental ingestion is possible by merge/update timestamp.
- Re-running ingestion is idempotent.
- Every stored chunk has stable provenance and a URL.
- Inline comments preserve path/line metadata when GitHub provides it.
- Content is scrubbed before embedding.
- Tests cover pagination, idempotency, and anchor resolution behavior.

Risks:

- GitHub API rate limits and auth variability.
- Review comment positions becoming stale relative to current HEAD.
- Storing sensitive discussion content without enough redaction.
- Poor retrieval ranking if PR chunks are mixed naively with code chunks.

## 4. End-to-End Smoke Test

Goal: add one command that proves the full ProjectLens loop is healthy:
Postgres up, migrations applied, indexer can ingest a tiny fixture repo, and
every MCP tool returns a sensible structured payload.

Why it matters: no CI gate currently exercises the combined indexer to storage
to MCP-handler path. Unit/integration tests cover slices, but the end-to-end
contract is checked mostly by hand.

Sketch of scope:

- Fixture Go module under `testdata/smoke-repo/` with 3 to 5 files: one exported function, one interface plus implementor, one SQL migration, and one struct with a doc comment.
- Test driver under `internal/smoketest/` or `cmd/projectlens/smoke_test.go` with build tag `smoke`.
- The driver should spin up Postgres via `testcontainers-go` or reuse local `5433` when an environment variable says so, run migrations, invoke indexing, start the MCP server in process, and call every tool in `toolRegistry`.
- Assertions should target `StructuredContent` shape rather than prose text.
- CI should run the smoke test on every push.

Pass criteria:

- `make smoke` exits `0` within 5 minutes.

Dependencies / readiness:

- Easier after agent-native MCP responses and observability surfaces settle.
- Should pin deterministic in-memory embedder/summarizer stubs to avoid CI flakiness against live providers.

Out of scope:

- Performance benchmarks.
- Real provider live tests.
- TUI smoke.

## Parked / Non-Priorities

These are valuable, but not immediate:

- One-command local install / agent install flow.
- Snapshot distribution.
- Jira ingestion. See `docs/2026-05-21-jira-confluence-ingestion-lessons.md` for future-spec guidance.
- Confluence ingestion. See `docs/2026-05-21-jira-confluence-ingestion-lessons.md` for future-spec guidance.
- Slack ingestion.
- Public packaging.
- Replacing ProjectLens's typed schema with a flat RAG chunk store.

Additional future-spec inputs:

- `docs/2026-05-21-resource-safety-lessons-from-pnphive.md`
- `docs/2026-05-21-document-retrieval-quality-lessons.md`
- `docs/2026-05-21-privacy-egress-and-redaction-lessons.md`

## Suggested Sequence

1. Finish inspectable artifacts using the existing report/export spec.
2. Extend run observability and wire it into report/TUI/status.
3. Design PR/review ingestion against the improved run model.
4. Implement PR/review ingestion with provenance, redaction, and tests.
5. Revisit Jira/Confluence after the document lane and privacy model are proven.

## Planning Boundary

This file is a priority tracker, not a full implementation plan. Each `P1`
task should get its own design/spec or implementation plan before coding starts
when it touches schema, auth, API pagination, privacy, storage, retrieval, or
agent-facing contracts.
