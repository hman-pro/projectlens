---
name: capture-knowledge
description: Detect and persist durable lessons, best practices, conventions, domain knowledge, how-tos, and decisions encountered during a session via the save_knowledge MCP tool
---

## When to use

Whenever any of the 9 trigger signals fires during a session. The Stop hook
will remind you to scan; this skill tells you *what* to scan for and *how* to
record it.

## Categories

| Category | Use when |
|---|---|
| `lesson` | Postmortem-flavored: "I ran into X, here's what I learned/fixed" |
| `best_practice` | Forward-looking rule: "when doing X, prefer Y" |
| `convention` | Repo-specific style/taste rule |
| `domain_knowledge` | Terminology, conceptual model, business semantics |
| `how_to` | Step-by-step recipe for a recurring task |
| `decision` | "We picked X over Y because Z" (ADR-style) |

## Trigger signals (the 9)

| Signal | Likely category |
|---|---|
| User correction ("don't do that", "we don't do X here") | convention / best_practice |
| User reveals a rule ("from now on…", "always X", "never Y") | convention |
| Domain term clarified ("X is not the same as Y") | domain_knowledge |
| Non-obvious root cause found (symptom → cause not derivable from code) | lesson |
| Stuck → unstuck moment (workaround, flag, tool combo broke a blocker) | how_to / lesson |
| Repeated task (you or user did it before) | how_to |
| Surprise / gotcha ("looks right but breaks because…") | lesson |
| Design decision made with rationale | decision |
| Pattern observed across files ("every service does X the same way") | convention |

## When NOT to save

- Current-session ephemera ("the bug we just fixed in this PR") — that goes in the commit message.
- Restating CLAUDE.md content — it's already there.
- Rephrasing self-explanatory code — well-named identifiers already document it.
- Exploratory wandering — only after a clear signal, not "just in case".
- Duplicates within the same session — one capture per insight.

## Dedup first — search before saving

Before calling `save_knowledge`, call `search_knowledge` with a tight query
on the title or the rule's keywords (and the same `anchor` if you have one).

- If a matching entry already exists, **leave it alone**. Do not call
  `save_knowledge` again — content-level dedup is now done server-side
  (identical `source`+`title`+`body` within 60s short-circuits with
  `deduped: true`), but a re-save with even slightly different prose
  still creates a new row. There is no upsert path today.
- If the search returns near-misses on a different facet (same symbol, but
  a different rule), save the new one — they coexist.
- If the existing entry is genuinely wrong or outdated, surface that to
  the user. Mutating an existing entry needs a DB-level fix (or a future
  `update_knowledge` tool) — it is not something the agent can do via
  `save_knowledge`.

Skipping this step leads to 5 sessions = 5 near-duplicate entries on the
same convention. Don't pollute the knowledge layer.

## Response flags are informational — do not retry

`save_knowledge` returns three diagnostic fields that look like failures
but are not. **Never retry a save based on these — they describe the row
that was already written.** Retrying produces a near-duplicate when your
second body differs even slightly from the first.

| Field | Meaning | Action |
|---|---|---|
| `embedded: false` | Sync embed step skipped (no embedder wired, or transient embedder failure). Entry + chunk are persisted; the next `index-embed` pass picks it up. | None. Lexical and anchor search work immediately; semantic search lags by one indexer pass. |
| `anchors_unresolved: ["symbol:Foo (not found)"]` | The ref didn't match any symbol in the index. | Re-check the symbol name from `find_symbol` / `get_symbol_context`; if it's truly absent, drop the anchor. Don't blind-retry with the same ref. |
| `anchors_unresolved: ["symbol:Foo (ambiguous: 7 matches — use SCIP id)"]` | The short name is shared by multiple symbols and the resolver refuses to guess. | Look up the SCIP id (e.g. `go . core/funding . Match`) via `get_symbol_context` and re-anchor with that exact ref. |
| `deduped: true` | The server detected a recent identical save and returned the original id. Any new resolvable anchors in the second call have been merged into the original entry's edges. | None. The entry exists with id from the response. |

If you got a `deduped: true` response, the user-facing summary should
still cite the returned id — there is no "failure" to recover from.

## How to call `save_knowledge`

Required: `category`, `title`, `body`. Optional but strongly preferred: `anchors`, `tags`, `source`.

**Anchor selection** (most specific that applies):
- `symbol` — about a specific function/type. `ref` = full SCIP symbol (e.g., `go . internal/storage . DB.UpsertChunk()`).
- `package` — about a layer or subsystem. `ref` = package name (e.g., `internal/embeddings`).
- `file` — about a single non-code file (config, migration). `ref` = repo-relative path.
- `table` — about a datastore concern. `ref` = table name.
- *no anchor* — broad/cross-cutting wisdom only.

**Body content**: lead with the rule or finding. Then a "Why:" line (the reason — incident, constraint, preference). Then a "How to apply:" line (when this kicks in). The Why is what makes the entry useful in 6 months when the surrounding context has changed.

**`source` field**: pass the originating agent — `"claude"`, `"codex"`,
`"cursor"`, etc. Defaults to `"agent"` if omitted, which destroys the audit
trail. Always set it.

## How entries propagate

`save_knowledge` writes a `knowledge_entries` row **plus** a paired
`chunks` row with `source_type='knowledge'`. The next `index-embed`
pass picks it up and writes an embedding — until then the entry is
findable by anchor and lexical search but not by semantic search.

To force immediate semantic availability, the user can run
`make index-embed`. Otherwise the next scheduled reindex covers it.

Anchored entries auto-surface inside `get_symbol_context`,
`get_package_summary`, and `search_go_context` — no extra
`search_knowledge` call required from the consuming agent.

## Examples

**Lesson, anchored to a symbol:**
```
save_knowledge(
  category="lesson",
  title="halfvec(1024) ANN index needs lists ≥ √rows",
  body="When the lists parameter is too small relative to row count, recall collapses below 50%.\n\n**Why:** Hit this when we scaled past 50k chunks — top-k results stopped including obvious matches.\n**How to apply:** When tuning vector indexes, set lists ≈ √(expected rows), reindex with CONCURRENTLY.",
  tags=["pgvector", "performance"],
  anchors=[{"type":"package","ref":"internal/storage"}],
  source="claude"
)
```

**Convention, anchored to a package:**
```
save_knowledge(
  category="convention",
  title="Provider clients live under internal/providers/<name>",
  body="Each external API gets its own subdirectory under internal/providers/. Constructor takes config + http.Client.\n\n**Why:** Keeps boundary tests isolated and makes provider swaps trivial.\n**How to apply:** Adding a new external API → create internal/providers/<name>/, expose Client struct, add config block in configs/index.yaml.",
  tags=["architecture"],
  anchors=[{"type":"package","ref":"internal/providers"}],
  source="claude"
)
```

**Decision, no anchor (broad):**
```
save_knowledge(
  category="decision",
  title="Ollama for embeddings, Anthropic for summaries",
  body="Local Ollama mxbai-embed-large for embeddings; Claude Sonnet via Anthropic API for summaries.\n\n**Why:** Embeddings are high-volume + privacy-sensitive (run locally, free). Summaries are low-volume + benefit from quality (worth API cost).\n**How to apply:** Don't add new providers without a clear reason; prefer extending one of these two.",
  tags=["providers"],
  source="claude"
)
```

## Silent skip

If you scan a turn and nothing qualifies, **stop silently** — do not narrate
"I checked and nothing qualified". The user will not see the check; that's the
point.
