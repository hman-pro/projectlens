# Knowledge layer — design

**Date:** 2026-04-23
**Status:** Draft (pending implementation plan)
**Author:** hamed.zohrehvand

## 1. Motivation

Working in this codebase produces a steady stream of small but durable insights — root causes, gotchas, conventions, domain-term clarifications, recipes, and design decisions — that today live only in transient places: chat transcripts, commit messages, Slack, the author's head. Repo-intel already indexes code, datastore, history, and (planned) docs. It does not yet capture the *wisdom* generated while working on the code.

The knowledge layer adds a sixth source type to the existing graph and uses Claude itself to detect and persist these insights as they happen. Once captured, knowledge becomes searchable in the same vector space as code chunks and is auto-surfaced wherever the anchored symbol/file/package is queried.

Goals:
- Capture is **lightweight** — one MCP call mid-conversation, no context switch.
- Detection is **enforced** — a Stop hook ensures Claude evaluates every turn against a defined trigger list, not just when it remembers.
- Retrieval is **passive when possible, explicit when needed** — knowledge surfaces automatically alongside the symbols it relates to, with a dedicated tool for direct queries.
- Storage is **consistent with existing architecture** — reuses `chunks`, `embeddings`, and the polymorphic `edges` table; one new metadata table.

Non-goals (v1):
- No draft/review/promotion workflow.
- No version chains or supersedes-by relationships.
- No cross-entry links (entry → entry).
- No automatic mining of past PRs/commits/Confluence (could come later as a separate `import` source).
- No CLI capture command (MCP-only).

## 2. Categories

Six categories, each backed by a `CHECK` constraint on `knowledge_entries.category`:

| Category | Definition | Example |
|---|---|---|
| `lesson` | Postmortem-flavored — "I ran into X, here's what I learned/fixed" | "halfvec(1024) ANN index needs `lists` ≥ √rows or recall collapses" |
| `best_practice` | Prescriptive, forward-looking — "when doing X, prefer Y" | "Use `pgx.Batch` for >5 inserts in the same call site" |
| `convention` | Style/taste rule for this repo | "Provider clients live under `internal/providers/<name>`" |
| `domain_knowledge` | Terminology, conceptual model | "A *reservation* in our domain is a soft hold; an *allocation* is committed inventory" |
| `how_to` | Step-by-step recipe | "How to add a new MCP tool: register in cmd/, define handler, write storage func, add test" |
| `decision` | ADR-style — "we picked X over Y because Z" | "Chose Ollama over OpenAI for embeddings: local, free, sufficient quality at 1024-dim" |

## 3. Trigger signals (what Claude watches for)

Encoded in the `capture-knowledge` skill. Each maps to a likely category:

1. **User correction** — "no, don't do that" / "we don't do X here" → `convention` or `best_practice`
2. **User reveals a rule** — "from now on…" / "always use X" / "never commit to main" → `convention`
3. **Domain term clarified** — "X is not the same as Y" → `domain_knowledge`
4. **Non-obvious root cause found** — symptom-to-cause chain not derivable from code → `lesson`
5. **Stuck → unstuck moment** — workaround, flag, or tool combo that broke a blocker → `how_to` or `lesson`
6. **Repeated task** — Claude or user redoes something done before → `how_to`
7. **Surprise / gotcha** — "looks right but breaks because…" → `lesson`
8. **Design decision with rationale** — "we picked X over Y because Z" → `decision`
9. **Pattern observed across files** — "every service does X the same way" → `convention`

## 4. Architecture

```
                                    ┌──────────────────────────┐
   Claude (in any session)          │  Claude Code             │
   detects a trigger signal         │  • capture-knowledge skill│
        │                           │  • Stop hook (forcing fn)│
        ▼                           └──────────┬───────────────┘
   ┌─────────────────┐                         │ MCP call
   │ save_knowledge  │◄────────────────────────┘
   │   (MCP tool)    │
   └────────┬────────┘
            │ writes
            ▼
   ┌─────────────────────────────────────────────────────┐
   │ Postgres                                             │
   │  ├─ knowledge_entries  (metadata)                    │
   │  ├─ chunks (source_type='knowledge') ─► embeddings   │
   │  └─ edges (edge_type='knowledge_about' → symbols/    │
   │              files/packages/tables)                   │
   └────────────────────┬─────────────────────────────────┘
                        │ reads
                        ▼
   ┌─────────────────────────────────────────────────────┐
   │ MCP retrieval                                         │
   │  ├─ search_knowledge       (explicit query)           │
   │  └─ search_go_context  ┐                              │
   │     get_symbol_context ├─► auto-surfaces anchored     │
   │     get_package_summary┘    knowledge                 │
   └─────────────────────────────────────────────────────┘
```

Three loops, each independently useful:
- **Capture loop** — Claude detects signal → calls `save_knowledge` → entry persisted, embedded, anchored.
- **Explicit retrieval** — `search_knowledge` for direct queries.
- **Passive retrieval** — every existing context tool auto-surfaces anchored knowledge for the symbols/packages it returns.

## 5. Schema

New migration: `migrations/004_knowledge_layer.up.sql`.

### 5.1 `knowledge_entries`

```sql
CREATE TABLE knowledge_entries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category     TEXT NOT NULL CHECK (category IN (
                     'lesson', 'best_practice', 'convention',
                     'domain_knowledge', 'how_to', 'decision')),
    title        TEXT NOT NULL,
    body         TEXT NOT NULL,                  -- markdown
    tags         TEXT[] NOT NULL DEFAULT '{}',
    source       TEXT NOT NULL DEFAULT 'claude', -- 'claude' | 'cli' | 'import'
    session_id   TEXT,                           -- optional Claude session
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX knowledge_entries_category_idx ON knowledge_entries(category);
CREATE INDEX knowledge_entries_tags_idx     ON knowledge_entries USING GIN(tags);
```

### 5.2 `chunks` (existing — extended)

No schema change. The `source_type` discriminator gains the value `'knowledge'`:
- `source_type = 'knowledge'`
- `source_id = knowledge_entries.id`
- `content = title + "\n\n" + body`

The chunk flows through the existing embedding pipeline (Ollama mxbai-embed-large → halfvec(1024) in `embeddings`).

### 5.3 `edges` (existing — extended)

No schema change. New `edge_type = 'knowledge_about'`:
- `source_type = 'knowledge'`, `source_id = knowledge_entries.id`
- `target_type ∈ {'symbol','file','package','table'}`
- `target_id` references the existing `symbols.id` / `files.id` / `package` row / `datastore_tables.id`
- `confidence = 1.0` for user/Claude-anchored; `<1.0` reserved for future auto-anchoring

### 5.4 Explicitly **out of scope** for v1

- No `status` / lifecycle column. Delete or update in place is enough.
- No `superseded_by` / version chain.
- No draft vs published state.
- No cross-entry links.

## 6. Capture path

Three pieces, each with one job. Each works alone; combined, they make skipping capture more effort than performing it.

### 6.1 MCP tool: `save_knowledge`

Lives in `cmd/projectlens-mcp/` next to the existing 5 tools.

```
save_knowledge(
    category:    "lesson"|"best_practice"|"convention"|
                 "domain_knowledge"|"how_to"|"decision",
    title:       string,
    body:        string,        // markdown
    tags?:       string[],
    anchors?:    [ { type: "symbol"|"file"|"package"|"table",
                     ref:  string } ],
    session_id?: string
)
-> { id:                 uuid,
     embedded:           bool,
     anchors_resolved:   int,
     anchors_unresolved: string[] }
```

Server-side, single transaction:
1. Insert `knowledge_entries` row.
2. Insert `chunks` row (`source_type='knowledge'`); enqueue for embedding via existing pipeline.
3. For each anchor, resolve `ref` against existing IDs (symbol scip, file path, package path, table name) and insert one `edges` row. Unresolved refs are returned in the response — never failed — so capture never blocks on a stale anchor.

### 6.2 Skill: `capture-knowledge`

Ships in the repo at `claude/skills/capture-knowledge/SKILL.md` (sibling to `trace-go-flow`, `debug-go-test`, `explain-go-impact`). Modeled on Claude's auto-memory skill structure. Contents:

- **Categories table** — the 6 types with one-line definitions and one example each.
- **Trigger checklist** — the 9 signals from §3, each tagged with a likely category.
- **When NOT to save** — anti-spam rules:
  - No current-session ephemera ("the bug we just fixed in this PR").
  - No restating CLAUDE.md content.
  - No rephrasing of code that's self-explanatory.
  - No captures during exploratory wandering — only after a clear signal.
  - No duplicate captures within the same session.
- **Calling pattern** — exact `save_knowledge` invocation with anchoring guidance:
  - `symbol` anchor when about a specific function or type.
  - `package` anchor when about a layer or subsystem.
  - `file` anchor when about a config or single non-code file.
  - `table` anchor when about a datastore concern.
  - No anchors for broad/cross-cutting wisdom.
- **Examples** — 4–6 worked examples (one per common signal) showing input → resulting tool call.

### 6.3 Stop hook (the forcing function)

Project-scoped, in `.claude/settings.json` of any repo using projectlens. Repo-intel ships `claude/settings-snippet.json` users merge into their target repo (same pattern as `claude/mcp-config.json`).

```json
{
  "hooks": {
    "Stop": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "echo '<system-reminder>Before stopping: scan this turn against the capture-knowledge skill. If any of the 9 signals fired, call save_knowledge now. If nothing qualifies, stop. Do not narrate this check.</system-reminder>'"
      }]
    }]
  }
}
```

Two safeguards against loops and noise:
- **One-shot per stop** — the hook respects `stop_hook_active`; the reminder fires once per stop attempt, not on the follow-up stop after Claude saves.
- **Silent-skip clause** — the skill explicitly tells Claude not to narrate "I checked and nothing qualified", so no leak into the user's view when there is nothing to capture.

## 7. Retrieval path

### 7.1 Explicit tool: `search_knowledge`

```
search_knowledge(
    query:    string,
    category?: "lesson"|"best_practice"|...   // filter
    anchor?:   { type, ref }                  // "what's known about X"
    limit?:    int (default 10)
)
-> [ { id, category, title, body, tags,
       anchors: [{type, ref, name}],
       score: float, matched_via: "vector"|"anchor"|"both" } ]
```

Two retrieval paths, OR-combined and reranked:
- **Vector path** — embed `query`, search `embeddings` filtered to `chunks.source_type='knowledge'`. Same code path as `search_go_context`, only the source-type filter differs.
- **Anchor path** — when `anchor` is provided, traverse `edges` (`edge_type='knowledge_about'`, `target_id=<resolved>`). Optionally walk one hop up (symbol → its package) to surface package-level wisdom for symbol-level queries.

The existing rerank module gives a small bonus for `matched_via='both'`.

### 7.2 Passive surfacing in existing tools

Add a `knowledge` (or `related_knowledge`) field to the responses of three existing tools — top 3 anchored entries each, no extra MCP round-trip needed:

- **`get_symbol_context`** — query `edges` for `knowledge_about` pointing at this symbol *and* its package. Returns title + category + 1-line summary; full body fetched on demand.
- **`get_package_summary`** — same, scoped to the package.
- **`search_go_context`** — for the top-N returned code chunks, look up anchored knowledge via their `package_path` / `symbol_id`. Append as a separate `related_knowledge` block so it does not pollute the code ranking.

Each surface caps at 3 entries to keep payloads tight.

### 7.3 CLI parity

Mirror MCP for shell users:

```
projectlens knowledge search "<query>" [--category X] [--anchor symbol:Foo]
projectlens knowledge list   [--category X] [--tag Y]
projectlens knowledge show   <id>
projectlens knowledge delete <id>
```

No `add` CLI in v1 — capture is MCP-only per the lightweight call.

## 8. Testing

- **Storage** (`internal/storage/knowledge_test.go`) — CRUD on `knowledge_entries`, anchor edge insertion, polymorphic queries (knowledge → symbol/file/package/table). Real Postgres via the existing test harness.
- **MCP tool tests** — `save_knowledge` end-to-end (insert → chunk → enqueue embed → resolve anchors); `search_knowledge` against seeded fixtures (vector path, anchor path, combined).
- **Integration roundtrip** (`//go:build integration`) — capture an entry, run embedding, query both via `search_knowledge` and via `get_symbol_context` (anchor surfacing). Confirms the full pipeline.
- **Skill smoke test** — manual checklist: feed Claude 5 transcripts known to contain each signal type, confirm `save_knowledge` is called with sensible category and anchor.

## 9. Error handling

- **Unresolved anchors** — returned in `anchors_unresolved`, save still succeeds. Capture must never block on stale code references.
- **Embedding failure** — chunk persists without embedding (existing fallback in the embedder); retried by the next embed pass. Knowledge remains anchor-retrievable in the meantime.
- **Invalid category** — enum CHECK rejects the insert; MCP returns a structured error so Claude can correct and retry.
- **Duplicate captures** — not deduplicated in v1. Cheap to delete later; semantic dedup is a rabbit hole.
- **Hook fires without skill installed** — the injected reminder references a skill name; if absent, Claude no-ops. No crash, just degraded enforcement.
- **MCP server down** — Claude sees the tool error and reports it inline; nothing else breaks.

## 10. Files to add/modify

**New:**
- `migrations/004_knowledge_layer.up.sql`
- `internal/storage/knowledge.go` + `internal/storage/knowledge_test.go`
- `internal/mcpserver/save_knowledge.go`
- `internal/mcpserver/search_knowledge.go`
- `cmd/projectlens/knowledge.go` (CLI subcommand)
- `claude/skills/capture-knowledge/SKILL.md`
- `claude/settings-snippet.json` (Stop hook for users to merge)

**Modify:**
- `cmd/projectlens-mcp/main.go` — register two new tools.
- `internal/mcpserver/get_symbol_context.go` — add anchored-knowledge surfacing.
- `internal/mcpserver/get_package_summary.go` — same.
- `internal/mcpserver/search_go_context.go` — add `related_knowledge` block.
- `internal/storage/edges.go` — accept `edge_type='knowledge_about'` and `source_type='knowledge'`.
- `internal/embeddings/...` — confirm `source_type='knowledge'` flows through (likely already generic).
- `claude/CLAUDE.md.snippet` — document the new tools and hook setup.
- `CLAUDE.md` (this repo) — same.

## 11. Open questions for implementation plan

- Exact resolution rules for `anchor.ref` strings (e.g., `symbol:` prefix vs typed objects).
- Whether `package` anchors point to a synthetic package row or just a string column.
- Reranker tuning for mixed code+knowledge results in `search_go_context`.
- Whether to emit a structured event/log when a capture happens, for telemetry.
