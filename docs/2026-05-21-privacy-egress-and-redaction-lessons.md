# Privacy, Egress, and Redaction Lessons From pnphive

Date: 2026-05-21
Status: lessons and future-spec guidance
Source: `docs/2026-05-21-pnphive-comparison.md`

## Purpose

Capture pnphive's privacy and egress lessons before ProjectLens expands into broader document and business-context ingestion.

This document defines future guardrails. It is not an implementation plan.

## Decision

ProjectLens should make privacy, egress, and redaction explicit before adding broad business-source ingestion.

pnphive's README is clear about the boundary: the corpus and embeddings stay local; only top-K chunks leave the machine when asking Claude to answer. ProjectLens needs a similarly explicit posture before adding PR comments, Jira, Confluence, or other human-authored sources.

## Why This Matters

Typed code indexing has a narrower privacy surface than broad business-source ingestion.

PR comments, Jira issues, Confluence pages, and comments can contain:

- customer-specific details,
- incident details,
- credentials or token fragments,
- private URLs,
- commercial or rollout details,
- personal data,
- internal decision context that should not be exported casually.

If ProjectLens ingests these sources, it must define what is stored, embedded, summarized, reported, exported, and sent to external providers.

## pnphive Lessons To Reuse

### 1. State The Egress Boundary Clearly

pnphive explicitly says storage and retrieval are local, while top-K chunks can leave the laptop when Claude answer mode is used.

ProjectLens should document each data path:

- local parsing,
- local storage,
- local embeddings,
- external summarization if configured,
- external answering if added later,
- generated reports,
- graph exports,
- MCP responses.

Users should not have to infer which content can leave the machine.

### 2. Redact Before Embedding

pnphive scrubs chunks before embedding. That ordering is important.

ProjectLens should require redaction before:

- embedding,
- summarization,
- report excerpt generation,
- graph/document export,
- any external model call.

Once a secret is embedded or sent to a provider, redacting the stored text later is too late.

### 3. Track Redaction Counters

pnphive records scrub statistics in run metadata.

ProjectLens should store redaction counters in run observability:

- private-key chunks dropped,
- JWT-like tokens redacted,
- high-entropy environment values redacted,
- source-specific redactions,
- chunks skipped due to sensitivity.

Reports should show counts without exposing redacted content.

### 4. Scope By Allow-List

pnphive scopes Jira projects and Confluence spaces deliberately.

ProjectLens should require explicit allow-lists for broad sources:

- GitHub repositories,
- Jira projects,
- Confluence spaces,
- optional page/project exclusions,
- optional date windows.

Do not ingest everything an auth token can access.

### 5. Reports and Exports Are Egress Surfaces

ProjectLens's planned report/export work creates artifacts that can be copied, attached, or committed.

That means report/export must avoid accidental leakage:

- default reports should prefer counts, metadata, and links over long raw excerpts,
- graph export should make evidence blobs opt-in,
- sensitive source types should have redaction status,
- output files should identify when they include raw document content.

### 6. Author Metadata Needs A Policy

pnphive retains author metadata where useful.

ProjectLens should decide per source whether to retain:

- author names,
- emails,
- account IDs,
- avatars or profile URLs,
- timestamps.

This should be a documented policy, not incidental behavior.

## Proposed Data Handling Rules

### Local-Only By Default

Default behavior should keep source content, chunks, embeddings, and retrieval local.

External model calls should be explicit by provider configuration and visible in status/report output.

### Scrub Before Derived Storage

For document-like sources:

1. Fetch raw content.
2. Normalize content.
3. Redact/drop sensitive content.
4. Store scrubbed content.
5. Embed scrubbed content.
6. Summarize only scrubbed content if summarization is enabled.

Raw fetched content should not be persisted unless there is a clear, protected cache design.

### Minimize Report Excerpts

Default report output should not include large raw chunks from Jira/Confluence/PR comments.

Good default:

- counts,
- freshness,
- source coverage,
- top linked entities,
- recent titles/IDs,
- URLs,
- redaction counters.

Raw excerpts should require an explicit flag or separate query surface.

### Make Egress Visible

Status/report output should identify provider configuration:

- embedding provider,
- summarization provider,
- whether provider is local or remote,
- model name,
- configured/reachable/error state.

Do not hide remote provider use behind a generic "healthy" flag.

## Minimum Future Acceptance Criteria

Before broad business-source ingestion ships:

- Redaction runs before embedding.
- Redaction behavior has unit tests.
- Every broad source has explicit allow-list config.
- Run records include redaction counters.
- Report output includes redaction/source coverage summaries.
- External provider use is visible in status/report.
- Graph/document exports do not include raw evidence blobs by default.
- Error messages and config snapshots are scrubbed before storage.

## Risks

- Redaction false negatives can leak sensitive content.
- Redaction false positives can remove useful technical evidence.
- External provider config can be misunderstood by users.
- Reports and exports can become uncontrolled copies of sensitive content.
- API errors can include sensitive URLs or request details.

## Non-Goals

- Perfect secret detection.
- Full DLP classification.
- Legal/compliance policy replacement.
- Ingesting customer-specific systems by default.
- Automatically sending broad Jira/Confluence context to external LLMs.

## Recommendation

Adopt pnphive's clear privacy posture before broad source expansion:

1. Keep local retrieval local by default.
2. Redact before embedding or summarization.
3. Track redaction counters in run observability.
4. Require allow-lists for broad sources.
5. Treat reports and exports as potential egress surfaces.
6. Make remote provider use visible in status and reports.

