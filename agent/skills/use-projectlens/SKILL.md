---
name: use-projectlens
description: MANDATORY — call the ProjectLens MCP tools BEFORE Read/Grep/Glob for any question about code structure, behavior, history, data flow, or impact. Applies to all agents (Claude, Cursor, Codex, etc.).
---

# Use ProjectLens First

This repo has a local intelligence service — **ProjectLens** — running at
`http://localhost:8484/mcp`. It has already parsed every Go symbol, every
database table, every git commit, and every saved lesson into a searchable
graph. **Querying it is faster, cheaper, and more accurate than re-discovering
the codebase with Read/Grep/Glob.**

## The rule

Before you open files, grep, or list directories to answer a question about
**what the code does, where it lives, who calls it, what it touches in the
database, how it has changed, or what depends on it** — call ProjectLens
first.

If a ProjectLens call answers the question fully, **stop**. Do not also grep
"to double-check". The index is the source of truth for structural facts.

## Decision flow

```
Question received
       │
       ▼
Is it about code structure / behavior / history / data flow / impact?
       │                                       │
      yes                                      no
       │                                       │
       ▼                                       ▼
Pick a ProjectLens tool                  Use Read/Edit/Bash normally
(table below)                           (file contents, edits, shell)
       │
       ▼
Got the answer?
       │           │
      yes          no (need a specific line, exact source)
       │           │
       ▼           ▼
   Done        Read only the 1–2 files ProjectLens pointed at
```

## Tool picker

| Question pattern | Tool |
|---|---|
| "Where is `<name>`?" / "Find symbol X" | `find_symbol` |
| "How does X work?" / "How is Y handled?" (natural language) | `search_go_context` |
| "What calls X? What does X call? Who implements interface I?" | `get_symbol_context` |
| "What does package P do? What does it export?" | `get_package_summary` |
| "Which Go code reads/writes table T? What columns does T have?" | `get_table_context` |
| "When was X last changed? Who changes it? What's the recent history?" | `get_change_history` |
| "What files change together with X?" / "If I edit X, what else should I touch?" | `get_coupling` |
| "Is the index fresh? When did it last run?" | `index_status` |
| "What do we know about <topic>?" / "Any lessons on Y?" | `search_knowledge` |
| "Remember that …" / "Save this lesson" | `save_knowledge` |

Knowledge anchored to a symbol or package auto-surfaces inside
`get_symbol_context`, `get_package_summary`, and `search_go_context` — no
extra call needed.

## Common workflows

These are the recurring shapes. Follow them — don't reinvent.

### Trace a behavior or symbol

> "Where is supplier funding approval implemented?"

1. `find_symbol` for the obvious name. If miss, `search_go_context` with the
   natural-language description.
2. `get_symbol_context` on the hit → callers, callees, implementors.
3. `get_package_summary` on its package → role in the system.
4. Open the top 1–2 files only to verify specifics.
5. Summarize: entry point → key steps → exit point.

### Investigate a failing or relevant test

> "Why is TestSupplierFundingApproval failing?"

1. `search_go_context` with the test name or behavior.
2. `get_symbol_context` on the production symbol under test → dependencies.
3. `get_package_summary` on the test's package.
4. Read the test file + the production file.
5. Explain: what the test expects vs what the production code does, and where
   they diverge.

### Estimate change impact

> "What breaks if I change `SupplierFunding.Approve()`?"

1. `find_symbol` to lock down the target.
2. `get_symbol_context` → all callers + interface implementors.
3. `get_package_summary` for each affected package (cap at 5).
4. `get_coupling` on the file → other files that historically change with it.
5. Summarize:
   - Direct callers that would break
   - Implementors that need updating
   - Packages that depend on this package
   - Confidence (high/medium/low) based on graph completeness

### Map data flow

> "Which code writes to `supplier_funding`?"

1. `get_table_context` with the table name → readers + writers + columns.
2. `get_symbol_context` on the most relevant writer to confirm the path.
3. `get_change_history` on the table's main writer if "what changed" matters.

## Anti-patterns

These are the failure modes to avoid:

- **Grep-first.** `grep -r SupplierFunding` before any MCP call — you've just
  wasted tokens re-doing what the index already answers.
- **Read-then-confirm.** Reading a 500-line file to find one function when
  `find_symbol` returns the exact location.
- **Broad exploration.** `ls internal/` and opening files at random instead
  of `get_package_summary`.
- **Re-deriving history.** `git log --oneline file.go` when
  `get_change_history` already has it indexed and ranked.
- **Ignoring stale index.** If results look wrong or empty, call
  `index_status`. If stale, surface that to the user — don't silently fall
  back to grep and pretend the index is fine.

## When NOT to use ProjectLens

- Reading **full file contents** — use `Read`.
- **Editing** code — use `Edit` / `Write`.
- **Running** commands, tests, builds — use `Bash`.
- Questions about **non-Go code, configs, scripts, prose docs** outside the
  indexed surface — use Read/Grep.
- Questions about **the user's own intent or this conversation** — answer
  from context.

ProjectLens is for **discovery and context**, not for code manipulation.

## Proactive freshness check

Before any **high-impact** task — change-impact estimate, multi-file
refactor, data-flow audit, migration planning, deep debug of unfamiliar
code — call `index_status` once at the start of the turn. One cheap MCP
call beats building a long answer on stale facts.

Skip the proactive check for:
- Quick lookups (`find_symbol`, single `get_symbol_context`).
- Questions that don't depend on the graph being fresh (definitions
  rarely move between hours).

Treat the index as stale and surface that to the user when any of:
- `stages.code.age_minutes` > 60, OR
- `git.dirty == true` AND the question is about current state, OR
- any `providers[].state` is `error` or `not_configured`.

Suggest `make reindex` (incremental) or `make index-all` (full) and
stop building on the stale answer.

## Writer-lock awareness

Mutating indexer commands (`bootstrap`, `reindex`, `index-datastore`,
`index-history`, `index-embed`, `index-summarize`, `index-all`) take a
single Postgres advisory lock. Read-only commands and the MCP server
bypass it.

If the user asks you to run a mutating command and it exits with code
**75** plus a line like:

```
another writer holds the lock: pid=<n> host=<h> cmd="<c>" started=<RFC3339>
```

1. Tell the user *who* holds the lock — quote the line verbatim (pid,
   host, cmd, started_at). The TUI Jobs section also surfaces a
   running indexer job.
2. **Default: wait** for the holder to finish, then retry. Auto-recovery
   reaps the row if the holder crashes.
3. `projectlens unlock --force` is a **last resort**, only when
   auto-recovery has clearly failed (holder PID is dead, row pinned
   for hours). It kills the holder's DB session and rolls back any
   in-flight transactions.

Never call `unlock --force` yourself without explicit user
confirmation.

## Stale index handling

If a query returns nothing for an obviously-present symbol, or returns a
deleted file:

1. Call `index_status`. It returns a human-readable summary plus a typed
   `structuredContent` payload with this shape:

   ```json
   {
     "stages": {
       "code":      {"stage":"code","status":"completed","age_minutes":12.3, ...},
       "summarize": {"stage":"summarize","status":"completed","age_minutes":5.0, ...},
       "embed":     {"stage":"embed","status":"completed","age_minutes":4.8, ...}
     },
     "git": {"head":"<sha>","dirty":false},
     "providers": [
       {"role":"embedder","provider":"ollama","state":"reachable"},
       {"role":"summarizer","provider":"anthropic","state":"configured"}
     ]
   }
   ```

2. If `stages.code.age_minutes` is large (e.g. > 60), or `git.dirty` is
   `true`, or any `providers[].state` is `error` / `not_configured`, tell
   the user the index looks stale/degraded and suggest `make reindex` (or
   `index-all` for a full rebuild). Do not silently fall back to grep and
   pretend the answer is authoritative.

## Structured fields

Every tool returns both human-readable text and a typed `structuredContent`
payload (MCP `CallToolResult.structuredContent`). Prefer the structured
payload — text is for humans, fields are for you.

| Tool | Payload type | Notable fields |
|------|--------------|----------------|
| `find_symbol` | `FindSymbolPayload` | `hits[].evidence{file_path,line_start,line_end}` |
| `search_go_context` | `SearchGoContextPayload` | `degradation{degraded,reason,fallback}`, `hits[].evidence` |
| `get_symbol_context` | `SymbolContextPayload` | `target.evidence`, `scip_symbol`, `callers[]`, `callees[]`, `implementors[]` |
| `get_package_summary` | `PackageSummaryPayload` | `generated_at`, `age_minutes`, `stale`, `exported_symbols[]` |
| `get_table_context` | `TableContextPayload` | `columns[]`, `read_by[]`, `written_by[]` (each carries an evidence span) |
| `get_change_history` | `ChangeHistoryPayload` | `target_kind` ∈ `file\|symbol`, `evidence` (set when symbol), `records[]` |
| `get_coupling` | `CouplingPayload` | `coupled[].strength`, `min_strength` |
| `index_status` | `indexStatusPayload` | `stages` map, `git`, `providers[].state` ∈ `reachable\|configured\|not_configured\|error` |
| `save_knowledge` | `SaveKnowledgePayload` | `id`, `embedded`, `deduped`, `anchors_resolved`, `anchors_unresolved[]` |
| `search_knowledge` | `SearchKnowledgePayload` | `entries[].matched_via` ∈ `vector\|anchor\|both` |

**`degradation.degraded == true`** means the result is best-effort — a backend
was unavailable (e.g. the embedder). Either ask the user before acting on it
or re-issue the call once the missing backend is up.

**`evidence` spans** (`file_path:line_start-line_end`) point at the bytes a
hit was derived from. Before quoting or editing based on a hit, open the
cited span to confirm — the index can be stale relative to the working tree.

**`providers[].state`** in `index_status` is one of four values:
- `reachable` — probe ran and the provider responded.
- `configured` — credentials/endpoint set but no probe was run (the
  probe is too expensive to run on every status call, e.g. Anthropic).
- `not_configured` — no provider wired OR credentials missing. The
  `error` field carries a short reason; `provider` may be empty (no
  provider) or carry the intended name (e.g. `openai` when the key
  is missing).
- `error` — probe ran and failed; the `error` field carries the message.

**`save_knowledge` response flags** are informational, not errors — never
retry a save based on them (you would create a near-duplicate when your
second body differs even slightly).

- `embedded: false` — sync embed step skipped (no embedder wired, or
  transient failure). Entry + chunk are persisted; the next `index-embed`
  pass picks it up. Lexical and anchor search work immediately; semantic
  search lags by one indexer pass.
- `deduped: true` — server detected a recent identical save
  (same `source`+`title`+`body`+`category` within 60s) and returned the
  original entry's id. The reported `embedded` field reflects the
  original entry's true state, not the retry call. New resolvable anchors
  in the second call are merged into the original entry's edges.
- `anchors_unresolved: ["symbol:Foo (reason)"]` — each entry is formatted
  as `"type:ref (reason)"`. Reasons:
  - `not found` — ref doesn't match anything in the index. Re-check the
    name via `find_symbol` / `get_symbol_context`; if truly absent, drop
    the anchor and don't retry with the same ref.
  - `ambiguous: N matches — use SCIP id` — the short name is shared by
    multiple symbols. Look up the SCIP id (e.g.
    `go . core/funding . Match`) via `get_symbol_context` and re-anchor.
