# Docs Stage (Confluence + Jira) Design

**Date:** 2026-05-01
**Status:** Superseded for chunking/retrieval (fetcher portion retained)

> **Superseded by [`2026-05-06-lightrag-integration-design.md`](./2026-05-06-lightrag-integration-design.md).**
> The Confluence/Jira fetchers, `documents` canonical table, `index-docs` CLI subcommand, and TUI `O` hotkey designed below are still in scope. The chunking-into-`chunks` path, `confluence://` / `jira://` `source_uri` schemes, and retrieval via `search_go_context` are replaced by LightRAG-backed `search_docs` / `search_concept` MCP tools. See the LightRAG design for the new ingest path and tool surface.

**Goal:** Implement the planned `docs` stage of the projectlens indexer so Confluence pages and Jira issues land in the existing `documents` table, get chunked + embedded into the same vector space as code, and become reachable via the existing retrieval router and MCP tools without retrieval-side changes.

## Goal

Code-only retrieval answers "what does this function do" but cannot answer "why does it exist", "which ticket drove this change", or "what's the spec for this flow". The Docs stage is the missing business-context layer:

- **Confluence pages** carry durable design docs, runbooks, package overviews, and team conventions.
- **Jira issues** carry the immediate "why" — the problem statement, acceptance criteria, comments, and ticket IDs that already appear in the codebase's commit messages.

Once docs are indexed:

- `search_go_context` returns code + doc results from the same `chunks` table (the router and reranker already discriminate on `source_type` — see `internal/retrieval/`).
- `get_symbol_context` and `get_package_summary` can append a `Related docs` block analogous to the existing `Related knowledge` block.
- The history stage's existing `FOR-\d+` ticket regex (planned link in `2026-04-17-doc-augmentation-design.md`) gains a real target — Jira documents the call graph never had access to.

This design takes the older `2026-04-17-doc-augmentation-design.md` sketch and turns it into an implementable stage matching the patterns set by `internal/datastore/` and `internal/history/`.

## Sources

### In scope (v1)

| Source     | What we fetch                                    | Auth                                  | Identity                                      |
|------------|--------------------------------------------------|---------------------------------------|-----------------------------------------------|
| Confluence | Page bodies (storage format → text), titles, space, ancestor IDs, version, last-updated. | `ATLASSIAN_EMAIL` + `ATLASSIAN_API_TOKEN` (PAT, Basic auth) | `documents.external_id = "<page_id>"` |
| Jira       | Issue summary, description, status, assignee, labels, components, fixVersions, comments (concatenated). | Same Atlassian credentials.                       | `documents.external_id = "<ISSUE-KEY>"` |

Confluence page tree is preserved as a soft signal only — the parent chain is recorded in `documents.metadata.ancestors` (array of page IDs) but is **not** turned into edges in v1. Cross-page hierarchy queries are deferred until we see a retrieval need.

### Deferred

- **Confluence attachments** (PDFs, images). Need OCR/text extraction; out of scope for v1. Skip silently, log count.
- **Jira issue links** (blocks, relates-to, duplicates). Worth modeling as edges, but only after we've validated the basic chunk shape.
- **Jira changelog history** (status transitions). Useful but high-volume; v1 only stores current state.
- **Confluence comments / inline comments**. Low-signal, often outdated; defer.
- **Cross-Atlassian-instance support.** v1 assumes a single instance configured by base URL.
- **Real-time webhooks.** v1 is poll-on-demand via the existing CLI/TUI cadence.
- **Edges from documents → code.** The commit-message ticket-extraction pass that creates `document mentions file` edges is owned by the history stage (see `2026-04-17-doc-augmentation-design.md`); the Docs stage stops at upserting documents and chunks. The history stage already has all it needs once `documents.external_id` matches the regex match group.

### Recommended default scope

A single Confluence space (the team's), a JQL filter scoped to the same project key with a 12-month rolling window. Tightly scoped by default — broad scope is easy to enable in YAML but costly to revert because chunks survive in the embedding index until cleaned up.

## Schema usage

The existing `documents` table (migration 002) already covers everything we need:

```sql
documents (
    id              BIGSERIAL PRIMARY KEY,
    source_type     TEXT NOT NULL,         -- 'confluence' | 'jira'
    external_id     TEXT NOT NULL,         -- page id | issue key
    title           TEXT NOT NULL,
    url             TEXT,
    body_text       TEXT,                  -- canonical plaintext, post-render
    last_synced_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata        JSONB,
    UNIQUE (source_type, external_id)
)
```

`storage.UpsertDocument` already exists and uses `(source_type, external_id)` as the conflict key, so re-running the stage is idempotent.

### Mapping

**Confluence page → documents row:**

| Column         | Value                                                                                           |
|----------------|-------------------------------------------------------------------------------------------------|
| `source_type`  | `"confluence"`                                                                                  |
| `external_id`  | numeric page id as string                                                                       |
| `title`        | page title                                                                                      |
| `url`          | `<base_url>/wiki/spaces/<SPACE>/pages/<id>` (rendered, not API URL)                              |
| `body_text`    | rendered plaintext from `body.storage` → strip XML/HTML, collapse whitespace                    |
| `metadata`     | `{ "space": "FOR", "ancestors": [..page ids..], "version": 12, "updated_at": "...", "author": "..." }` |

**Jira issue → documents row:**

| Column         | Value                                                                                           |
|----------------|-------------------------------------------------------------------------------------------------|
| `source_type`  | `"jira"`                                                                                        |
| `external_id`  | issue key, e.g. `"FOR-1234"`                                                                    |
| `title`        | issue summary                                                                                   |
| `url`          | `<base_url>/browse/<KEY>`                                                                       |
| `body_text`    | `description + "\n\n--- comments ---\n\n" + comments[] joined`                                  |
| `metadata`     | `{ "status": "...", "assignee": "...", "labels": [...], "components": [...], "fix_versions": [...], "issue_type": "Story", "updated_at": "..." }` |

### Chunking strategy

**Recommendation:** **one chunk per document** for both source types in v1. Reasons:

1. The projectlens mental model is one chunk per *unit of meaning*. For code that's a symbol; for docs the smallest stable unit is the whole page or issue.
2. Confluence section headings are inconsistent across teams — splitting by `<h2>` produces ragged chunks that hurt rerank quality more than they help recall.
3. The Ollama `mxbai-embed-large` context window (~512 tokens effective) is small, but page bodies that exceed `max_chunk_chars` are *truncated* (matching the existing 30000-char cap in the embed pipeline) rather than split. This gives one stable retrieval hit per page that can then drill into the source via `url`.
4. Section-level chunking can be added later as a `chunking_strategy: section` config switch without schema changes — the chunk → document relationship is already 1:N via `chunks.source_uri` linkage.

**Chunk record shape:**

| Field          | Confluence                                          | Jira                                  |
|----------------|-----------------------------------------------------|---------------------------------------|
| `source_type`  | `"confluence"`                                      | `"jira"`                              |
| `source_uri`   | `confluence://<space>/<page_id>`                    | `jira://<KEY>`                        |
| `content`      | `# <title>\n\n<plaintext>` (header gives lexical search a hit on the title) | `# <KEY>: <summary>\nStatus: <status>\n\n<description + comments>` |
| `token_count`  | `len(content) / 4` (matches `internal/datastore/`)  | same                                  |

The `source_uri` schemes (`confluence://`, `jira://`) are unique to the Docs stage — code chunks use repo-relative paths, datastore chunks use `schema.table`, knowledge chunks use the entry slug.

### What we do not store

- Raw `body.storage` XHTML — we keep `body_text` only. The plain text is what feeds embeddings *and* what an MCP consumer reads back; HTML offers no extra retrieval signal once stripped.
- Per-comment Jira rows — concatenation into the parent issue's `body_text` keeps the document model simple and matches how reviewers read the ticket.

## Configuration

Add a new top-level `docs` block to `configs/index.yaml`. It is **optional** — if absent, the stage no-ops with a friendly skip message, mirroring how the datastore stage already behaves when no engines are configured.

```yaml
docs:
  confluence:
    base_url: "https://relexsolutions.atlassian.net"
    spaces: ["FOR"]                 # space keys to enumerate fully
    page_ids: [5749964825]          # always-synced individual pages, even if outside `spaces`
    exclude_labels: ["archive", "deprecated"]
    max_pages_per_run: 2000          # safety bound; warn + truncate beyond this
  jira:
    base_url: "https://relexsolutions.atlassian.net"
    jql: "project = FOR AND updated >= -365d"
    include_comments: true
    max_issues_per_run: 5000
  http:
    timeout_seconds: 30
    max_retries: 3                   # exponential backoff: 1s, 2s, 4s
    rate_limit_per_second: 5         # both APIs share this budget
  chunking:
    strategy: "page"                 # "page" (default) | "section"  (deferred)
    max_chunk_chars: 30000           # matches embed truncation cap
```

### Go config types

Mirror the datastore/history pattern in `internal/config/config.go`:

```go
// DocsConfig controls Confluence + Jira indexing.
type DocsConfig struct {
    Confluence ConfluenceConfig `yaml:"confluence"`
    Jira       JiraConfig       `yaml:"jira"`
    HTTP       DocsHTTPConfig   `yaml:"http"`
    Chunking   DocsChunkConfig  `yaml:"chunking"`
}
```

`Config.Docs` slot is added to the top-level `Config` struct. `Load` applies defaults if `Docs.HTTP.TimeoutSeconds == 0`, etc.

### Environment variables

| Variable               | Purpose                                              | Required                          |
|------------------------|------------------------------------------------------|-----------------------------------|
| `ATLASSIAN_EMAIL`      | Basic-auth username for Confluence + Jira            | yes (when Docs stage runs)        |
| `ATLASSIAN_API_TOKEN`  | Personal Access Token (PAT) for Atlassian Cloud      | yes (when Docs stage runs)        |
| `ATLASSIAN_BASE_URL`   | Override `confluence.base_url` / `jira.base_url`     | no                                |

The stage refuses to start if either credential is missing — fail loud, no half-runs against anonymous endpoints.

## Implementation outline

Package layout under `internal/docs/`, mirroring the structure used by `internal/datastore/` and `internal/history/`:

```
internal/docs/
  client_confluence.go        // *Client wrapping Atlassian Cloud Confluence v2
  client_confluence_test.go
  client_jira.go              // *Client wrapping Jira v3 search + issue
  client_jira_test.go
  fetch.go                    // Iterators: ConfluencePages, JiraIssues
  render.go                   // body.storage → plaintext, comment concat
  render_test.go
  chunks.go                   // Document → ChunkRecord
  indexer.go                  // IndexDocs entry point (matches IndexDatastore signature)
  indexer_integration_test.go // build tag: integration
```

### Key types and shapes

```go
// Page is a normalized Confluence page after rendering.
type Page struct {
    ID         string
    Title      string
    URL        string
    Space      string
    Ancestors  []string
    Version    int
    UpdatedAt  time.Time
    Author     string
    PlainText  string             // rendered from body.storage
}

// Issue is a normalized Jira issue.
type Issue struct {
    Key          string
    Summary      string
    URL          string
    Description  string
    Comments     []Comment
    Status       string
    Assignee     string
    Labels       []string
    Components   []string
    FixVersions  []string
    IssueType    string
    UpdatedAt    time.Time
}

// AtlassianClient is the seam for testing; both clients implement the
// narrow surface we need (List + Get-by-id).
type ConfluenceClient interface {
    ListPages(ctx context.Context, space string, since time.Time) iter.Seq2[Page, error]
    GetPage(ctx context.Context, id string) (Page, error)
}

type JiraClient interface {
    SearchIssues(ctx context.Context, jql string, since time.Time) iter.Seq2[Issue, error]
}
```

The `iter.Seq2` (Go 1.23+ range-over-func) lets the indexer page through results lazily without buffering thousands of issues in memory — the same pattern as `internal/history/gitlog.go`'s commit iterator.

### Fetch → chunk → store flow

```
IndexDocs(ctx, db, cfg)
  1. Resolve credentials from env; abort with clear error if missing.
  2. Build ConfluenceClient + JiraClient (HTTP, shared rate limiter).
  3. Resolve incremental watermark per source:
       since_confluence = max(documents.last_synced_at WHERE source_type='confluence') - 5 min margin
       since_jira       = max(documents.last_synced_at WHERE source_type='jira')       - 5 min margin
     (5-min safety margin matches the history stage's incrementalSafetyMargin.)
     Full reindex flag forces since = epoch.
  4. For each space in cfg.Confluence.Spaces:
       for page, err := range client.ListPages(ctx, space, since_confluence) {
           rendered := Render(page.BodyStorage)
           db.UpsertDocument(...)             // returns id (refetch if needed)
           db.UpsertDocChunk(...)             // delete-then-insert by source_uri
       }
     Plus the explicit cfg.Confluence.PageIDs always synced.
  5. For Jira:
       for issue, err := range client.SearchIssues(ctx, cfg.Jira.JQL, since_jira) {
           // similar
       }
  6. Record run statistics in index_runs (stage='docs', counts: pages_synced, issues_synced, chunks_written, skipped, errors).
  7. Return (pagesSynced + issuesSynced, error).
```

### Incremental update strategy

- **Watermark per source_type.** The most recent `documents.last_synced_at` for that source is the high-water mark; subtract a 5-minute safety margin (mirrors `internal/history/indexer.go`).
- **Confluence:** API supports filtering by `updatedAt >=` via CQL. Only changed pages return.
- **Jira:** append `AND updated >= "<watermark>"` to the configured JQL.
- **Idempotent upsert:** `(source_type, external_id)` is unique. Re-running the same window touches at most a few rows due to `last_synced_at = NOW()` on conflict.
- **Chunk reconciliation:** for each upserted document, delete prior chunks where `source_uri = '<scheme>://<id>'` and re-insert. Embeddings cascade-delete via the existing `chunks` FK; the next `index-embed` pass picks up the new chunk(s).
- **Deletion handling:** v1 does *not* detect Confluence/Jira deletions. A row that disappears from upstream stays indexed until a `--full` reindex runs. Documenting deletes (via Atlassian audit log or REST `lastModifiedDate < since AND status=trashed` queries) is deferred — the typical cost of a stale chunk is low.

### HTTP client details (Go-native, no Python bridge)

Both Confluence v2 and Jira v3 are simple JSON REST APIs:

- Single shared `*http.Client` with `cfg.Docs.HTTP.TimeoutSeconds` timeout.
- Basic auth header (`email:api_token` base64).
- Rate-limited via `golang.org/x/time/rate.Limiter` (one limiter shared across both clients, keyed off the same Atlassian instance).
- Retry policy: 429 + 5xx with exponential backoff (1s, 2s, 4s); honor `Retry-After` if present.
- Pagination: Confluence uses cursor (`_links.next`); Jira uses `startAt`/`maxResults` (cap at 100). Both wrapped behind the iterator interfaces above.

The `relex-tools:confluence` / `relex-tools:jira` Python wrappers are read-only references — useful for confirming endpoint shapes and token handling, but not invoked at runtime.

## CLI surface

A new top-level subcommand `index-docs`, wired to `cmd/projectlens/main.go` next to `index-datastore` and `index-history`:

```bash
projectlens index-docs                    # incremental sync (default)
projectlens index-docs --full             # ignore watermark, refetch everything in scope
projectlens index-docs --dry-run          # fetch + render counts only, no DB writes
projectlens index-docs --source confluence  # restrict to one source for fast iteration
projectlens index-docs --source jira
```

`index-all` gains the `docs` stage immediately after `history` and before `summarize` + `embed` (so summaries can already see related docs in `documents`, and embed picks up the new chunks):

```
census → parse → chunk → graph → datastore → history → docs → summarize → embed
```

The Docs stage acquires the same writer lock as other mutating stages (`LockID = 9876543210`, see `migrations/005_writer_lock.up.sql`).

### Makefile

A new `make index-docs` target alongside the existing `make index-history` / `make index-datastore`. No changes to `make bootstrap` semantics — bootstrap will pick up Docs automatically once `index-all` includes it.

### TUI hotkey

Existing Pipeline-section hotkeys: `R`, `F`, `E`, `S`, `H`, `D`, `A`, `c`, `j`. The natural choice **`O`** (mnemonic: d**O**cs, since `D` is taken by Datastore). It's untaken in `internal/tui/sections/pipeline/model.go` and visually distinct from `D` in the controls block.

| Key | Action            | Confirmation       |
|-----|-------------------|--------------------|
| `O` | index-docs        | y/N preflight      |

**Preflight query:** `SELECT COUNT(*) FROM documents WHERE source_type IN ('confluence','jira') AND last_synced_at < NOW() - INTERVAL '24 hours'` — modal headline `"refresh 84 stale docs (last sync > 24h)? [y/N]"`. For an empty `documents` table the headline becomes `"first-time docs sync from <space>/<jql>? [y/N]"`.

The Pipeline card for "Docs" already exists and is marked `planned` — it flips to `idle` once the migration adds the index_runs stage and the first run completes. No card-rendering changes needed; the section reads stage state generically.

## Open questions

1. **Auth model.** Confirm Atlassian Cloud PAT (Basic auth with email + token) is the right model for the team's instance, vs a service account with OAuth 2.0 (3LO). PAT is dramatically simpler and matches the `relex-tools:*` wrappers; OAuth would force a token-refresh dance the indexer has no good place to hold. **Default assumption: PAT.**
2. **Rate-limit budget.** Atlassian Cloud's published throttle is "varies"; 5 req/s is a guess. We need a real number from a single full run before raising `max_pages_per_run` past the default. Plan to log observed 429s during the first reindex.
3. **JQL ownership.** Who owns `cfg.docs.jira.jql`? If too narrow, useful tickets fall out of search. If too broad (e.g. all of `FOR`), the index includes years of trivial bug reports and bloats the embedding store. Recommend starting with `project = FOR AND (issuetype in (Story, Epic) OR labels = "design") AND updated >= -365d` and iterating.
4. **Attachment handling cutoff.** When (if ever) do we add PDF/Word extraction for Confluence attachments? They often hold the meatiest specs but require a non-trivial extraction pipeline. Defer until a concrete retrieval miss is reported.
5. **Edit dedup vs version churn.** A Confluence page edited 12 times in a day produces 12 `last_synced_at` updates but the *content* may be unchanged at the visible-text level. Cheap mitigation: hash `body_text` and skip re-chunking when the hash matches the previous row's. Worth doing in v1 to spare the embed pass; flagged here for confirmation.

## Phased rollout

**Phase A — Confluence-only, single space, manual run (1-2 days):**
- `internal/docs/client_confluence.go` + `render.go` + `chunks.go` + `indexer.go` (Confluence path only).
- New `cmd/projectlens index-docs` with `--source confluence` working end-to-end against the team space.
- `index-all` does **not** yet include docs.
- Smoke-test against a known page: confirm the title appears in `documents.title`, the body in `documents.body_text`, and one chunk per page in `chunks` with `source_type='confluence'`.

**Phase B — Jira added (1 day):**
- `client_jira.go`, JQL config, comment concatenation.
- `--source jira` end-to-end.
- Wire `index-docs` (no `--source`) to run both.

**Phase C — Pipeline integration (0.5 day):**
- `index-all` includes `docs` between `history` and `summarize`.
- TUI `O` hotkey + Pipeline card flips from `planned` to live state.
- Makefile `make index-docs` target.

**Phase D — Polish (deferred / opportunistic):**
- Body-hash dedup to skip pointless re-embeds.
- Section-level chunking (`chunking.strategy: section`) once recall on long pages proves insufficient.
- Confluence attachment extraction.
- Jira issue-link edges (`blocks`, `relates_to`) into the polymorphic `edges` table.
- Deletion detection.

Phase A on its own already unblocks `search_go_context` returning Confluence results; Phase B unblocks the commit-message ticket-linking edge work owned by the history stage. Each phase is a coherent merge.
