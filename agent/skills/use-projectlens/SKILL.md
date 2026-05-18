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

## Stale index handling

If a query returns nothing for an obviously-present symbol, or returns a
deleted file:

1. Call `index_status`. It returns a human-readable summary plus a fenced
   ```json``` block with this shape:

   ```json
   {
     "stages": {
       "code":      {"stage":"code","status":"completed","age_minutes":12.3, ...},
       "summarize": {"stage":"summarize","status":"completed","age_minutes":5.0, ...},
       "embed":     {"stage":"embed","status":"completed","age_minutes":4.8, ...}
     },
     "git": {"head":"<sha>","dirty":false},
     "embedder_healthy": true
   }
   ```

2. If `stages.code.age_minutes` is large (e.g. > 60), or `git.dirty` is
   `true`, or `embedder_healthy` is `false`, tell the user the index looks
   stale/degraded and suggest `make reindex` (or `index-all` for a full
   rebuild). Do not silently fall back to grep and pretend the answer is
   authoritative.
