# ProjectLens — Context Graph Data Model

Date: 2026-05-25
Status: design / spec draft
Source: pnphive comparison lessons, current ProjectLens graph schema, and the PR / docs / Slack / people modeling discussion.

## Goal

Define one general data model for human and business context that can cover:

- GitHub PRs, reviews, review comments, and issue comments.
- Atlassian Jira issues and comments.
- Confluence pages and comments.
- Slack conversations, threads, and messages.
- People and external identities that connect the same human across those systems.

This spec only designs the storage and graph contracts. It does not choose the migration path from pnphive, implement importers, or decide whether initial retrieval uses native pgvector, LightRAG, or both.

## Non-goals

- No implementation plan.
- No source-specific importer code.
- No pnphive database migration/import decision.
- No full Slack ingestion design.
- No Jira/Confluence auth design.
- No LightRAG sidecar implementation detail beyond stable IDs and handoff contracts.
- No replacement of ProjectLens's typed code, datastore, history, and knowledge tables.
- No automatic person merge by display name or fuzzy matching.

## Decision

Add a **context graph** layer centered on generic items, versions, chunks, participants, and identities.

The model treats every external artifact as a `context_item`:

- A PR is a conversation root.
- A PR review, PR review comment, Jira comment, Confluence comment, and Slack message are child items.
- A Jira issue and Confluence page are document-like root items.
- A Slack thread is a conversation root; Slack messages are child items.

Mutable text lives in append-only `context_item_versions`. Retrieval units live in `context_chunks`. People are canonical records connected to provider accounts through `person_identities`. Semantic links to ProjectLens's typed graph use the existing polymorphic `edges` table.

This gives ProjectLens one source-agnostic model for comments, conversations, documents, and people while preserving its typed code-intelligence core.

## Existing Schema Fit

ProjectLens already has several primitives this design should keep:

- `edges` is polymorphic with `(source_type, source_id, target_type, target_id, edge_type)`.
- `chunks` can already hold non-code content through `source_type` and `source_uri`.
- `embeddings` already attaches vectors to `chunks`.
- `index_runs` is being extended with run observability and flexible metrics.
- `knowledge_entries` remains the agent-captured knowledge table.

The existing `documents` table is too narrow for the planned source set. It can remain as legacy storage or be migrated later, but new broad-source ingestion should use the context graph model rather than stretching `documents` into PRs, comments, Slack messages, and people.

## Data Model

### `context_sources`

One row per configured external source or source scope.

Examples:

- `github` source for `example-org/ingest`.
- `jira` source for project `RP`.
- `confluence` source for space `FOR`.
- `slack` source for workspace plus channel allow-list.
- `repo_docs` source for local markdown under a target repo.

```sql
CREATE TABLE context_sources (
    id              BIGSERIAL PRIMARY KEY,
    source_type     TEXT NOT NULL,
    namespace       TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    base_url        TEXT,
    external_key    TEXT NOT NULL,
    config_hash     TEXT,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source_type, external_key)
);
```

`source_type` vocabulary for v1:

- `github`
- `jira`
- `confluence`
- `slack`
- `repo_docs`

`external_key` is the stable configured scope, not an individual item ID:

- `github:example-org/ingest`
- `jira:RP`
- `confluence:FOR`
- `slack:<workspace_id>:<channel_id>`
- `repo_docs:<repo_path_hash>`

### `context_source_state`

One row per source plus logical stream. This stores successful incremental state without overloading `context_sources`.

```sql
CREATE TABLE context_source_state (
    id                      BIGSERIAL PRIMARY KEY,
    source_id               BIGINT NOT NULL REFERENCES context_sources(id) ON DELETE CASCADE,
    stream                  TEXT NOT NULL,
    cursor_kind             TEXT NOT NULL,
    cursor_value            TEXT,
    last_successful_run_id  BIGINT REFERENCES index_runs(id) ON DELETE SET NULL,
    last_successful_at      TIMESTAMPTZ,
    metadata                JSONB NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (source_id, stream)
);
```

Examples:

- GitHub PR stream cursor: latest merged or updated timestamp.
- Jira stream cursor: latest issue updated timestamp.
- Confluence stream cursor: latest page version/update timestamp plus content hash checks.
- Slack stream cursor: channel timestamp cursor.

Watermarks advance only after successful runs. Failed or partial runs record evidence in `index_runs` but do not update `context_source_state`.

### `people`

Canonical person records. These are deliberately sparse.

```sql
CREATE TABLE people (
    id              BIGSERIAL PRIMARY KEY,
    display_name    TEXT,
    primary_email_hash TEXT,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Rules:

- `people` is not a directory sync.
- It should not store raw email by default.
- `primary_email_hash` is optional and used only when a source exposes verified email.
- Person merge must be explicit or based on strong external evidence, not display-name similarity.

### `person_identities`

Provider-specific accounts that may map to a canonical person.

```sql
CREATE TABLE person_identities (
    id                  BIGSERIAL PRIMARY KEY,
    person_id           BIGINT REFERENCES people(id) ON DELETE SET NULL,
    provider            TEXT NOT NULL,
    external_account_id TEXT NOT NULL,
    username            TEXT,
    display_name        TEXT,
    email_hash          TEXT,
    profile_url         TEXT,
    confidence_class    TEXT NOT NULL DEFAULT 'extracted',
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, external_account_id)
);
```

`provider` vocabulary for v1:

- `github`
- `atlassian`
- `slack`
- `git`

Identity resolution rules:

- Same provider + same external account ID is the same identity.
- Verified email hash may connect identities to the same person.
- Git commit author identity can connect to GitHub only through verified email hash or explicit mapping.
- Slack display names and GitHub usernames alone are not enough to auto-merge people.
- Ambiguous identities remain unmerged and can still participate independently.

### `context_items`

One stable logical item from an external source.

```sql
CREATE TABLE context_items (
    id              BIGSERIAL PRIMARY KEY,
    source_id       BIGINT NOT NULL REFERENCES context_sources(id) ON DELETE CASCADE,
    item_type       TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    parent_item_id  BIGINT REFERENCES context_items(id) ON DELETE SET NULL,
    root_item_id    BIGINT REFERENCES context_items(id) ON DELETE SET NULL,
    url             TEXT,
    title           TEXT,
    state           TEXT,
    created_at      TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source_id, item_type, external_id)
);
```

`item_type` vocabulary for the planned sources:

- `github_pr`
- `github_pr_review`
- `github_pr_issue_comment`
- `github_pr_review_comment`
- `jira_issue`
- `jira_comment`
- `confluence_page`
- `confluence_comment`
- `slack_thread`
- `slack_message`
- `repo_markdown_doc`

Parent/root rules:

- Root items have `parent_item_id IS NULL` and `root_item_id = id` after insert.
- PR reviews, PR comments, and PR review comments have the PR as `root_item_id`.
- Jira comments have the issue as `root_item_id`.
- Confluence comments have the page as `root_item_id`.
- Slack messages have the thread as `root_item_id`; a single-message thread can use the message item as both root and content item.

### `context_item_versions`

Append-only content snapshots for every item type.

```sql
CREATE TABLE context_item_versions (
    id                BIGSERIAL PRIMARY KEY,
    item_id           BIGINT NOT NULL REFERENCES context_items(id) ON DELETE CASCADE,
    external_version  TEXT,
    content_hash      TEXT NOT NULL,
    body_text         TEXT NOT NULL,
    redaction         JSONB NOT NULL DEFAULT '{}'::jsonb,
    metadata          JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_current        BOOLEAN NOT NULL DEFAULT TRUE,
    inserted_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at     TIMESTAMPTZ,
    run_id            BIGINT REFERENCES index_runs(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX context_item_versions_current_idx
    ON context_item_versions(item_id)
    WHERE is_current = TRUE;

CREATE INDEX context_item_versions_lineage_idx
    ON context_item_versions(item_id, inserted_at DESC);
```

Rules:

- Redaction happens before inserting `body_text`.
- If a fetched item has the same `content_hash` as the current version, update item metadata and `last_seen_at`, but do not insert a new version.
- If content changes, mark the current version non-current and insert a new current version.
- Deleted remote items set `context_items.deleted_at`; previous versions remain unless retention policy later removes them.
- Default retrieval uses only current versions.
- Historical retrieval must be explicit.

### `context_chunks`

Chunk metadata for document/conversation retrieval. This table links context versions to either native ProjectLens `chunks` rows, LightRAG document IDs, or both.

```sql
CREATE TABLE context_chunks (
    id                  BIGSERIAL PRIMARY KEY,
    item_version_id     BIGINT NOT NULL REFERENCES context_item_versions(id) ON DELETE CASCADE,
    chunk_key           TEXT NOT NULL,
    chunk_anchor_id     TEXT NOT NULL,
    source_anchor_id    TEXT NOT NULL,
    chunk_index         INTEGER NOT NULL,
    heading             TEXT,
    content_hash        TEXT NOT NULL,
    token_count         INTEGER NOT NULL DEFAULT 0,
    chunk_id            BIGINT REFERENCES chunks(id) ON DELETE SET NULL,
    lightrag_doc_id     TEXT,
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (item_version_id, chunk_key),
    UNIQUE (chunk_anchor_id)
);
```

Identity rules:

- `source_anchor_id` identifies the logical source item:
  - `github.pr.example-org/ingest.12345`
  - `jira.RP.RP-12345`
  - `confluence.FOR.5749964825`
  - `slack.T123.C456.1716500000.000100`
- `chunk_anchor_id` identifies the retrieval chunk:
  - `<source_anchor_id>#ordinal/0`
  - `<source_anchor_id>#comment/<external_comment_id>`
  - `<source_anchor_id>#section/<slug>`
- `lightrag_doc_id = sha1(chunk_anchor_id)` when the LightRAG sidecar is used.

This preserves the source-vs-chunk identity split required for multi-section documents, comment threads, and scoped deletion.

Native embedding rule:

- If the native pgvector path is used, each context chunk also creates or updates one row in the existing `chunks` table with `source_type='context'` and `source_uri='context:<chunk_anchor_id>'`.
- `context_chunks.chunk_id` points at that row.
- The existing `embeddings` table can then index the chunk without a separate context embedding table.

LightRAG rule:

- If the LightRAG path is used, each context chunk is inserted with deterministic `lightrag_doc_id`.
- The sidecar insert contract must be idempotent update, not duplicate-skip. Delete-then-insert is acceptable.
- Scoped reconciliation deletes only chunks whose source type was processed in the current run.

### `context_participants`

People and identities attached to context items through roles.

```sql
CREATE TABLE context_participants (
    id              BIGSERIAL PRIMARY KEY,
    item_id         BIGINT NOT NULL REFERENCES context_items(id) ON DELETE CASCADE,
    person_id       BIGINT REFERENCES people(id) ON DELETE SET NULL,
    identity_id     BIGINT REFERENCES person_identities(id) ON DELETE SET NULL,
    role            TEXT NOT NULL,
    source_role     TEXT,
    occurred_at     TIMESTAMPTZ,
    is_current      BOOLEAN NOT NULL DEFAULT TRUE,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (item_id, identity_id, role, source_role)
);
```

Role vocabulary:

- `author`
- `commenter`
- `reviewer`
- `approver`
- `requester`
- `assignee`
- `reporter`
- `mentioned`
- `participant`

Rules:

- Every item with an author should have an `author` participant if the source exposes identity.
- Slack mentions and Jira mentions can create `mentioned` participants.
- Mutable roles such as Jira assignee should update `is_current`; the old observation can stay as non-current if the importer has enough source evidence.
- Participant links can be identity-only when person resolution is ambiguous.

## Graph Edges

Use the existing `edges` table for semantic links between context and ProjectLens entities.

New source/target type values:

- `context_item`
- `context_chunk`
- `person`
- `person_identity`

Important edge types:

| Edge type | Source | Target | Meaning |
|---|---|---|---|
| `mentions` | context item/chunk | file, symbol, package, datastore table, context item, person | Extracted textual mention. |
| `about` | context item/chunk | file, symbol, package, datastore table | Stronger resolved topical relation. |
| `references` | context item | context item | Source-provided URL/reference/link. |
| `introduced_by` | file/symbol | context item | PR or issue introduced the current code/history evidence. |
| `discusses` | context item/chunk | symbol/table/package | Human discussion about a typed entity. |
| `same_person_as` | person identity | person identity | Explicit or high-confidence identity merge evidence. |

The existing `provenance` CHECK currently covers `parser`, `callgraph`, `sql_scanner`, `history`, `knowledge`, and `docs`. Context producers should extend that vocabulary before writing edges. Recommended additions:

- `github`
- `atlassian`
- `slack`
- `repo_docs`
- `identity_resolver`

Confidence rules:

- Source-provided links and IDs are `extracted`.
- Regex/entity mentions are `inferred` unless exact typed resolution is unambiguous.
- Identity joins from verified email hash are `inferred`.
- Manual identity joins are `extracted` with provenance `identity_resolver`.
- Ambiguous mention candidates are either not written or written as `ambiguous` only when the consumer needs to see the ambiguity.

## Source Mappings

### GitHub PRs

Root item:

- `item_type='github_pr'`
- `external_id='<owner>/<repo>#<number>'`
- `title`: PR title
- `state`: merged/closed/open as observed
- `metadata`: base/head refs, merged_at, merge_commit_sha, labels, changed file paths, additions/deletions if fetched

Child items:

- `github_pr_issue_comment`
- `github_pr_review`
- `github_pr_review_comment`

Review-comment metadata:

- path
- line
- original_line
- side
- commit_id
- original_commit_id
- diff_hunk
- review_id

Anchoring:

- Changed file paths create `context_item -> file` `mentions` or `references` edges when the current file exists.
- Inline review comments create `context_chunk -> file` edges using `path`.
- Symbol anchors are best-effort from `path:line` against current indexed symbol ranges and should be marked `inferred` because review comments can point at historical lines.

### Jira

Root item:

- `item_type='jira_issue'`
- `external_id='<PROJECT>-<number>'`
- `title`: issue summary
- `state`: issue status
- `metadata`: issue type, priority, labels, components, fix versions, resolution, updated timestamp

Child items:

- `jira_comment`

People roles:

- reporter
- assignee
- commenter
- mentioned

Anchoring:

- Issue keys referenced in commits or PRs create `references` edges between context items.
- Mentions of packages, files, symbols, and tables create best-effort `mentions` / `about` edges.

### Confluence

Root item:

- `item_type='confluence_page'`
- `external_id='<space>:<page_id>'`
- `title`: page title
- `metadata`: space, ancestors, version, labels, updated timestamp

Child items:

- `confluence_comment`

Chunking:

- Heading-aware chunks where headings are stable enough.
- Fallback ordinal chunks for pages without reliable headings.
- `source_anchor_id` remains page-level; `chunk_anchor_id` carries section or ordinal identity.

### Slack

Root item:

- `item_type='slack_thread'`
- `external_id='<workspace>:<channel>:<thread_ts>'`
- `title`: generated from channel + thread timestamp or first message excerpt
- `metadata`: workspace ID, channel ID/name, thread timestamp

Child items:

- `slack_message`

People roles:

- author
- participant
- mentioned

Privacy rules:

- Slack ingestion must be channel allow-list only.
- Direct messages are out of scope unless explicitly enabled in a later design.
- Raw user emails are not stored by default.
- Deleted messages mark `deleted_at`; previous versions follow retention policy.

### Repo Markdown Docs

Root item:

- `item_type='repo_markdown_doc'`
- `external_id='<repo-relative-path>'`
- `title`: first H1 or file path
- `metadata`: path, commit SHA, file checksum

Chunking:

- H1/H2-aware chunks.
- File path creates `references` edge to `files` when indexed as a repo file.

## Retrieval Contract

Exact code tools remain exact:

- `find_symbol`
- `get_symbol_context`
- `get_table_context`
- `get_change_history`
- `get_coupling`

Context search uses the context graph:

- future `search_context` or `search_documents` should retrieve across current `context_chunks`.
- optional source filters: `github`, `jira`, `confluence`, `slack`, `repo_docs`.
- optional item filters: PR, review comment, Jira issue, Slack thread, etc.
- historical retrieval requires `include_history=true`.

Ranking should follow the document-retrieval lessons:

1. Lexical candidates.
2. Vector candidates.
3. Reciprocal rank fusion.
4. Source/document diversity caps.
5. Optional reranking.

Result payloads must include:

- item type
- source display name
- title
- URL
- external ID
- author/participant identity where allowed
- created/updated timestamp
- chunk heading/key
- current vs historical marker
- resolved anchors and confidence where present

## Privacy and Egress

The model assumes broad context may contain sensitive data.

Required rules before any importer ships:

- Source allow-lists are mandatory.
- Redaction runs before `context_item_versions.body_text`, `chunks.content`, embeddings, LightRAG insert, report excerpts, or export evidence blobs.
- Redaction counters live in `context_item_versions.redaction` and roll up into `index_runs.metrics`.
- Config snapshots and error text must not persist secrets.
- Reports and graph exports must not include raw broad-source content by default.
- Provider status/report output must make remote model use visible.
- Raw email storage is disabled by default; email hashes are enough for identity matching unless a later design explicitly needs raw email.

## Run Observability

Every context ingestion command should create an `index_runs` row.

Stage name:

- `context`

Recommended `metrics` keys:

- `sources`
- `items_seen`
- `items_inserted`
- `items_updated`
- `items_deleted`
- `versions_inserted`
- `chunks_inserted`
- `chunks_skipped_unchanged`
- `edges_inserted`
- `identities_seen`
- `people_linked`
- `redacted_values`
- `dropped_chunks`
- `http_requests`
- `http_errors`

Per-source watermarks live in `context_source_state`, not only in `index_runs`.

## Migration Direction

This spec does not choose the migration/import path, but the model supports three later options:

1. **Fresh native importers.** Implement source-specific Go importers that populate the context graph directly.
2. **pnphive bridge import.** Read pnphive rows and map `source`, `repo`, `source_id`, `chunk_idx`, `metadata`, and lineage into context items/versions/chunks.
3. **Side-by-side MCP.** Keep pnphive as an external MCP source while ProjectLens implements native context ingestion gradually.

The recommended later sequence is:

1. Create schema and storage contracts.
2. Add repo markdown and GitHub PR importers first.
3. Decide whether pnphive bridge import is worth it after native GitHub importer shape is proven.
4. Add Jira/Confluence.
5. Add Slack only after privacy and identity handling are proven.

## Tests

Storage tests:

- Insert source, item, current version, chunk, participant.
- Re-ingest unchanged content and verify no new version.
- Re-ingest changed content and verify old version is non-current and new version is current.
- Delete remote item and verify `deleted_at` without losing lineage.
- Unique constraints reject duplicate source/item/chunk anchors.

Identity tests:

- Same provider/account ID reuses identity.
- Verified email hash can link two identities to one person.
- Display-name match alone does not link identities.
- Ambiguous identities can participate without a person row.

Edge tests:

- Context item can link to file/symbol/table/package through polymorphic `edges`.
- Context chunk can link to file/symbol/table/package.
- Invalid context provenance is rejected after the provenance CHECK is extended.
- Symbol anchor from path/line is `inferred`, not `extracted`.

Privacy tests:

- Redaction happens before version/chunk insert.
- Redaction counters persist on the version and run metrics.
- Raw email is absent by default when provider exposes email.
- Report/export fixtures omit raw context content unless explicitly requested.

Importer contract tests for later implementation:

- GitHub pagination and idempotency.
- Jira failed-run watermark behavior.
- Confluence page-version/content-hash skip.
- Slack channel allow-list enforcement.

## Acceptance Criteria

1. The schema can represent PRs, PR reviews, PR comments, Jira issues/comments, Confluence pages/comments, Slack threads/messages, and repo markdown docs without source-specific tables.
2. Every item has stable source identity and URL when the source provides one.
3. Every mutable text body has append-only lineage with current-only default retrieval.
4. Every retrieval chunk has a stable `source_anchor_id` and `chunk_anchor_id`.
5. People connect across systems through explicit `person_identities`, not fuzzy display-name merging.
6. Context can link to files, symbols, packages, datastore tables, knowledge entries, and other context items through the existing graph.
7. Redaction, allow-lists, and run observability are part of the data model, not follow-up patches.
8. The existing typed ProjectLens tools keep their current deterministic contracts.

## Risks

- **Over-generalization.** A generic model can hide source-specific behavior. Mitigation: keep source metadata JSONB, but require source-specific importer contract tests.
- **Identity over-merge.** Incorrectly merging people is worse than leaving identities separate. Mitigation: conservative merge rules and confidence-bearing identity edges.
- **Lineage growth.** Append-only versions can grow quickly for Slack and comments. Mitigation: retention policy is deferred but must be explicit before Slack ships.
- **Retrieval dilution.** Broad context can bury exact code evidence. Mitigation: separate context search from typed code tools and use source/document caps.
- **Privacy leakage.** Slack/Jira/Confluence can contain sensitive content. Mitigation: allow-lists, redaction-before-storage, no raw report/export excerpts by default.
- **Historical anchors drift.** PR review lines and Slack links may refer to old code. Mitigation: mark path/line symbol resolution as `inferred` and preserve original metadata.

## Open Questions

1. Should `documents` be migrated into `context_items` immediately when the context schema lands, or left as a legacy table until Jira/Confluence importers are implemented?
2. Should native pgvector context retrieval ship before LightRAG integration, or should `context_chunks` only feed LightRAG in the first implementation?
3. What retention policy should Slack use for deleted or edited messages?
4. Should manually confirmed identity merges be edited through a CLI command, a config file, or direct SQL for the first pass?
5. Should `search_context` be a new MCP tool name, or should the older `search_docs` plan be broadened to include Slack/PR conversations?

## Suggested Implementation Phases

1. Schema-only context graph migration plus storage APIs and tests.
2. Repo markdown importer as a low-risk source.
3. GitHub PR/review/comment importer with people and file/path anchors.
4. Context search over current chunks using native lexical/vector/RRF.
5. Jira and Confluence importers.
6. Slack importer after privacy and identity behavior are proven.
7. Optional pnphive bridge import if native importers do not cover important historical corpus quickly enough.
