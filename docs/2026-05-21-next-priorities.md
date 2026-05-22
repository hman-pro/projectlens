# ProjectLens Next Priorities

Date: 2026-05-21
Status: priority task list for planning
Inputs:

- `docs/2026-05-21-graphify-comparison.md`
- `docs/2026-05-21-pnphive-comparison.md`
- `docs/2026-05-21-jira-confluence-ingestion-lessons.md`
- `docs/2026-05-21-resource-safety-lessons-from-pnphive.md`
- `docs/2026-05-21-document-retrieval-quality-lessons.md`
- `docs/2026-05-21-privacy-egress-and-redaction-lessons.md`

## Decision

The next work should focus on making ProjectLens more inspectable, more operationally explainable, and richer in code-adjacent human context.

Agent install / local-instance distribution is intentionally not a near-term priority. It can wait until the core product surface is easier to inspect and the indexed data is easier to audit.

## Top 3 Priorities

1. Ship inspectable artifacts: `projectlens report` and `projectlens export graph`.
2. Upgrade run observability for all indexing stages.
3. Add a PR / review-context ingestion lane.

The ordering matters. First expose what the current typed index knows. Then make index runs explainable. Then add human context from PRs and reviews on top of a more observable platform.

## 1. Inspectable Artifacts

### Goal

Make the indexed database legible without requiring a user or agent to understand the schema directly.

This is the Graphify lesson: visible artifacts create trust. ProjectLens should do this in a schema-native way rather than by copying Graphify's graph-file-first architecture.

### Deliverables

- `projectlens report`
  - Markdown output by default.
  - JSON output with `--format json`.
  - Optional `--out <path>`.
- `projectlens export graph`
  - Streams native-schema graph JSON.
  - Includes nodes and edges with stable IDs.
  - Ensures every exported edge endpoint resolves to an exported node.

### Initial Report Content

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

### Acceptance Criteria

- A user can run one command and understand what ProjectLens currently knows.
- Report generation is read-only and safe during indexing.
- Graph export has a closure test: every edge endpoint exists in the node set.
- Output formats are deterministic enough for tests and future diffing.
- The implementation does not make `report` depend on `mcpserver` internals.

### Risks

- Graph node ID drift between nodes and edges.
- Report queries accidentally duplicating `index_status` logic.
- Read-only report output appearing authoritative while a writer is active.

### Notes

There is already a detailed design/spec and implementation plan under `docs/superpowers/` for this feature. That work remains the best starting point.

## 2. Run Observability

### Goal

Make every indexing action explain what happened, not just whether a stage is fresh.

This is the pnphive lesson: freshness is useful, but run evidence is what makes operational behavior debuggable.

### Deliverables

Extend stage/run tracking so each mutating index command records:

- Stage name.
- Status: running, completed, failed, cancelled, partial if needed.
- Started/completed timestamps.
- Target repo path, git head, branch, dirty state.
- Config snapshot relevant to the stage.
- Provider identity and health snapshot.
- Counts: scanned, inserted, updated, skipped, deleted, embedded, summarized, edges created.
- Timings by major phase.
- Error text and failure phase.
- Process identity where useful: host, pid, command, binary version/commit.

### Product Surfaces

The run data should feed:

- `index_status`
- TUI pipeline/jobs views
- `projectlens report`
- CLI status/history commands if added later

### Acceptance Criteria

- A failed run leaves enough detail to identify which phase failed and why.
- A no-op incremental run is recorded as a meaningful completed check, not invisible.
- `index_status` can continue to answer freshness questions without hiding run-level details from report/TUI.
- The schema can support future stages without another redesign.

### Risks

- Overloading `index_runs` with stage-specific fields that do not generalize.
- Breaking existing status/TUI assumptions.
- Recording secrets in config snapshots or errors.

### Notes

Keep snapshots selective. The goal is reproducibility and debugging, not dumping complete environment or credential material into Postgres.

## 3. PR / Review-Context Ingestion

### Goal

Add the first document-like business/human-context lane, starting with merged PRs and review discussions.

This is the highest-value corpus expansion before Jira/Confluence because PR context is close to code and often carries file/path/line metadata.

### Scope

Start with the target repository's GitHub data:

- Merged PR title/body/metadata.
- Top-level issue comments on PRs.
- Review summaries.
- Inline review comments.
- Relevant path/line/commit metadata where available.

Out of scope for this first pass:

- Jira.
- Confluence.
- Slack.
- Generic web/docs ingestion.
- Agent install/distribution.

### Storage Direction

Use a document-like lane rather than flattening the typed code model.

Likely concepts:

- `documents` for PR/review/comment source records.
- `chunks` with `source_type='pr'`, `source_type='pr_review'`, or similar.
- Stable external IDs such as `github:owner/repo#123`.
- Metadata for author, merged_at, url, state, path, line, review state.
- Optional edges from PR chunks to files/symbols when anchors resolve.

### Retrieval Direction

PR context should support questions like:

- Why was this code changed?
- What did reviewers decide?
- Was this behavior debated before?
- Which PR introduced this path?
- What human context exists around this symbol/table/package?

It should complement typed tools, not replace them:

- `get_symbol_context` remains structural.
- `get_change_history` remains commit/file oriented.
- Broad context search can include PR chunks as supporting evidence.
- Future report output can summarize PR ingestion coverage.

### Acceptance Criteria

- Incremental ingestion is possible by merge/update timestamp.
- Re-running ingestion is idempotent.
- Every stored chunk has stable provenance and a URL.
- Inline comments preserve path/line metadata when GitHub provides it.
- Content is scrubbed before embedding.
- The feature has tests for pagination, idempotency, and anchor resolution behavior.

### Risks

- GitHub API rate limits and auth variability.
- Review comment positions becoming stale relative to current HEAD.
- Storing sensitive discussion content without enough redaction.
- Poor retrieval ranking if PR chunks are mixed naively with code chunks.

### Notes

The first implementation should prefer correctness and provenance over broad coverage. A small but well-anchored PR corpus is more useful than a large unstructured dump.

## Explicit Non-Priorities

These are valuable, but not immediate:

- One-command local install / agent install flow.
- Snapshot distribution.
- Jira ingestion. See `docs/2026-05-21-jira-confluence-ingestion-lessons.md` for future-spec guidance.
- Confluence ingestion. See `docs/2026-05-21-jira-confluence-ingestion-lessons.md` for future-spec guidance.
- Slack ingestion.
- Public packaging.
- Replacing ProjectLens's typed schema with a flat RAG chunk store.

Additional pnphive lessons are captured separately for future specs:

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

This document is not an implementation plan. Each priority should get its own design/spec or implementation plan before coding starts, especially the PR/review ingestion lane because it touches auth, API pagination, privacy, storage, retrieval, and anchoring.
