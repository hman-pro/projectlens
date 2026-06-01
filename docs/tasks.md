# ProjectLens Tasks

Date: 2026-06-01
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
| `P1` | Agent setup UX | Active | Write the focused design spec for `lens agent install/status/doctor`, scoped to Claude and Codex setup using the canonical assets under `agent/`. | A user can install ProjectLens wiring for Claude or Codex, check whether it is current, and diagnose the agent-to-MCP loop without manually copying snippets or guessing what is stale. |

## Next Tasks

| Priority | Task | Status | Next Action | Done When |
|---|---|---|---|---|
| `P2` | End-to-end smoke test | Planned | Promote the brief below into an implementation plan. Existing `scripts/release-smoke.sh` only covers the embedding+DB contract — it is not the full loop. | `make smoke` proves the full indexer to storage to MCP loop against a small fixture repo in under 5 minutes. |

## Recently Done

| Priority | Task | Status | Evidence |
|---|---|---|---|
| `P1` | `lens report` (markdown/json, `--format`, `--out`, all sections) | Done | `cmd/lens/report.go`, `internal/report/` |
| `P1` | `lens export graph` (native-schema nodes+edges, stable IDs, streaming) | Done | `cmd/lens/export.go`, `internal/export/graph.go` |
| `P1` | Inspectable artifacts: `export graph` closure diagnostics | Done | `Export` returns `Diagnostics`; `edgeSkipReason` classifier + `diagnostics.skipped_edges` envelope field + CLI stderr `WARN`; knowledge `table`-anchor JOIN/`edgeEndpoint` fix; endpoint-shape + classifier unit tests in `graph_test.go`; self-contained `TestExportGraph_TableAnchorEdge` integration test |
| `P1` | Run observability for indexing stages | Done | migration 008 + `internal/storage/indexruns.go` + indexer wiring + report stage detail + TUI runs panel; provider identity + error redaction in place |
| `P1` | CLI command and binary rename from `projectlens` to `lens` | Done | `cmd/lens/`, `cmd/lens-mcp/`, `cmd/lens-tui/`; `Makefile` builds `bin/lens*`; user-facing docs, agent assets, TUI hints, Docker entrypoints, and current source-of-truth docs use `lens` while retaining ProjectLens product/env/MCP identities |

## 1. Inspectable Artifacts

Goal: make the indexed database legible without requiring a user or agent to
understand the schema directly.

This is the Graphify lesson: visible artifacts create trust. ProjectLens should
do this in a schema-native way rather than by copying Graphify's
graph-file-first architecture.

Deliverables:

- `lens report`
  - Markdown output by default.
  - JSON output with `--format json`.
  - Optional `--out <path>`.
- `lens export graph`
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

## 3. Agent Setup UX

Goal: make ProjectLens easy to connect to MCP-capable coding agents without
manual snippet copying or stale local agent assets.

This is the Graphify install-flow lesson, narrowed to ProjectLens's actual
public-alpha need: CLI-assisted Claude and Codex setup. It should improve the
agent onboarding loop without taking on binary/package distribution.

Scope:

- `lens agent install claude`
- `lens agent install codex`
- `lens agent status`
- `lens agent doctor`
- Dry-run output for write commands.
- Idempotent install/update behavior.
- Clear distinction between generated ProjectLens-managed files and
  user-owned agent config.
- Validation that the MCP URL responds and exposes the expected ProjectLens
  tool list.
- Validation that installed skills, hooks, and snippets match the canonical
  files under `agent/`.
- Actionable repair guidance for missing binary, dead MCP server, wrong MCP
  URL, stale skills, stale hooks, missing hooks, unsupported agent, and agent
  config that points at the wrong endpoint.

Out of scope:

- Homebrew, release artifacts, package-manager distribution, or public binary
  packaging.
- Snapshot distribution.
- Supporting every MCP-capable agent in the first pass.
- Auto-starting background services.
- Indexing behavior changes.

Acceptance criteria:

- A user can run one install command for Claude or Codex and get the local
  agent assets into the expected place.
- Re-running install is safe and reports what changed, what was already current,
  and what requires manual user approval.
- `status` reports installed/current/missing/stale state for supported agents
  without mutating files.
- `doctor` verifies the configured MCP endpoint, expected tool names, canonical
  skill/hook/snippet versions, and likely PATH/binary problems.
- Error output includes concrete next actions rather than only failure text.
- Tests cover idempotency, dry-run behavior, stale asset detection, unsupported
  agents, bad endpoints, and user-owned config preservation.

Risks:

- Mutating user agent config too aggressively.
- Hiding important MCP/server failures behind a generic "not installed" status.
- Letting copied agent assets drift from the canonical `agent/` directory.
- Coupling the setup flow to package distribution decisions that should remain
  separate.

Starting points:

- `agent/skills/use-projectlens/SKILL.md`
- `agent/skills/capture-knowledge/SKILL.md`
- `agent/claude/`
- `agent/codex/`
- `docs/AGENT_SETUP.md`
- `docs/2026-05-21-graphify-comparison.md`

## 4. End-to-End Smoke Test

Goal: add one command that proves the full ProjectLens loop is healthy:
Postgres up, migrations applied, indexer can ingest a tiny fixture repo, and
every MCP tool returns a sensible structured payload.

Why it matters: no CI gate currently exercises the combined indexer to storage
to MCP-handler path. Unit/integration tests cover slices, but the end-to-end
contract is checked mostly by hand.

Sketch of scope:

- Fixture Go module under `testdata/smoke-repo/` with 3 to 5 files: one exported function, one interface plus implementor, one SQL migration, and one struct with a doc comment.
- Test driver under `internal/smoketest/` or `cmd/lens/smoke_test.go` with build tag `smoke`.
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

- One-command local binary install.
- Snapshot distribution.
- Jira ingestion. See `docs/2026-05-21-jira-confluence-ingestion-lessons.md` for future-spec guidance.
- Confluence ingestion. See `docs/2026-05-21-jira-confluence-ingestion-lessons.md` for future-spec guidance.
- Slack ingestion.
- PR / review-context ingestion.
- Public packaging.
- Replacing ProjectLens's typed schema with a flat RAG chunk store.

Additional future-spec inputs:

- `docs/2026-05-21-resource-safety-lessons-from-pnphive.md`
- `docs/2026-05-21-document-retrieval-quality-lessons.md`
- `docs/2026-05-21-privacy-egress-and-redaction-lessons.md`

## Suggested Sequence

1. Design agent setup UX for Claude and Codex around the canonical `agent/`
   assets.
2. Implement `lens agent install/status/doctor`.
3. Promote the end-to-end smoke test brief into an implementation plan.
4. Revisit Jira/Confluence only if external document/context ingestion becomes
   a product priority again.

## Planning Boundary

This file is a priority tracker, not a full implementation plan. Each `P1`
task should get its own design/spec or implementation plan before coding starts
when it touches schema, auth, API pagination, privacy, storage, retrieval, or
agent-facing contracts.
