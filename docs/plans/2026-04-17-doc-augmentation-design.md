# Doc Augmentation (Phase 4) Design

**Date:** 2026-04-17
**Status:** Draft
**Goal:** Integrate Confluence pages and Jira tickets into the intelligence graph, linking business context to code via commit messages and semantic similarity.

## Approach

**Hybrid linking:** Hard links from git commit message ticket IDs (precise), soft links from semantic similarity in shared vector space (broad coverage).

## Pipeline

```
1. Fetch Confluence pages from configured spaces/page IDs
2. Fetch Jira tickets matching configured JQL filter
3. Upsert into documents table
4. Chunk document body text → chunks (source_type='confluence'/'jira')
5. Extract Jira ticket IDs from git commit messages (regex: FOR-\d+)
6. Create edges: document → mentions → file (via commit linkage)
7. Embed doc chunks via index embed (same vector space as code)
```

## Confluence Integration

**API:** Confluence REST API v2 (cloud)
**Auth:** `ATLASSIAN_USERNAME` + `ATLASSIAN_API_TOKEN` in .env
**Fetch strategy:**
- Configured spaces (e.g., `FOR`) — fetch all pages
- Specific page IDs (e.g., team pages) — always synced
- Incremental: `last_synced_at` comparison, only fetch modified pages

**Chunking:** Split page body into ~1000 token chunks. Each chunk gets `source_type='confluence'`, `source_uri=page_url`.

**Note:** The user already has the `relex-tools:confluence` CLI wrapper which handles auth and fetching. We can shell out to it or reimplement the HTTP calls in Go.

## Jira Integration

**API:** Jira REST API v3 (cloud)
**Auth:** Same Atlassian credentials
**Fetch strategy:**
- JQL filter (e.g., `project = FOR AND updated >= -30d`)
- Store: key, title, description, status, assignee, labels
- Incremental: only tickets updated since last sync

**Chunking:** Ticket description + comments as one chunk per ticket. `source_type='jira'`, `source_uri=ticket_key`.

## Commit-Message Ticket Linking

**The most precise link between code and docs.**

```
1. git log --format="%H|%s" -- <indexed files>
2. Regex: FOR-\d+ (extract Jira ticket IDs)
3. For each (ticket_id, commit):
   - Find which files were changed in that commit
   - Create edge: document(ticket) → mentions → file
```

This creates hard links: "Jira ticket FOR-1234 is related to files X, Y, Z because commit abc123 referenced it."

## Storage

**`documents` table (existing, from migration 002):**
- `source_type` ('confluence' / 'jira')
- `external_id` (page ID or ticket key)
- `title`, `url`, `body_text`, `metadata` (JSONB)
- `last_synced_at` for incremental updates

**`chunks` table (existing, extended in migration 002):**
- `source_type='confluence'` or `source_type='jira'`
- `source_uri=page_url` or `source_uri=ticket_key`
- Embedded into same vector space as code

**`edges` table:**
- `edge_type='mentions'`: document → file (from commit messages)
- `edge_type='documents'`: document → symbol/package (from semantic similarity, future)

## Configuration

```yaml
docs:
  confluence:
    base_url: "https://relexsolutions.atlassian.net"
    spaces: ["FOR"]
    page_ids: [5749964825]  # Team Grace page
  jira:
    base_url: "https://relexsolutions.atlassian.net"
    projects: ["FOR"]
    jql_filter: "project = FOR AND updated >= -30d"
  ticket_pattern: "FOR-\\d+"  # regex for extracting ticket IDs from commits
```

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `ATLASSIAN_USERNAME` | Confluence/Jira auth |
| `ATLASSIAN_API_TOKEN` | Confluence/Jira auth |

## CLI

```bash
projectlens index docs --repo /path --db "..."
```

## Unified Search

Once doc chunks are embedded, `search_go_context` automatically returns them alongside code results, tagged with `source_type`. No changes needed to the search pipeline — it already handles multi-source results.

## Implementation Decision: Go HTTP Client vs Shell Out

Two options for Confluence/Jira API calls:
- **Go HTTP client:** More control, proper error handling, testable
- **Shell out to relex-tools CLI:** Already handles auth, but adds Python dependency

**Recommendation:** Go HTTP client. Simple REST calls, no external dependency.
