# Use ProjectLens First (Codex)

> Paste this block into your repo's `AGENTS.md` (or your Codex system prompt
> override) so Codex reaches for ProjectLens before grepping or reading files.
> Codex does not load `.claude/skills/*.md` the way Claude Code does — this
> file is the Codex-flavored equivalent of the `use-projectlens` and
> `capture-knowledge` skills bundled under `claude/skills/`.

---

This repo has a local intelligence service called **ProjectLens** running at
`http://localhost:8484/mcp`. It exposes 10 MCP tools that index every Go
symbol, database table, git change, and saved lesson in this codebase. When
the MCP server is connected, the tools appear in your tool list as
`find_symbol`, `search_go_context`, `get_symbol_context`,
`get_package_summary`, `get_table_context`, `get_change_history`,
`get_coupling`, `index_status`, `save_knowledge`, and `search_knowledge`.

## The rule

Before you open files, grep, or list directories to answer a question about
**what the code does, where it lives, who calls it, what it touches in the
database, how it has changed, or what depends on it** — call ProjectLens
first. If a ProjectLens call answers the question fully, stop. Do not also
grep "to double-check"; the index is the source of truth for structural
facts.

## Tool picker

| Question pattern | Tool |
|---|---|
| "Where is `<name>`?" / "Find symbol X" | `find_symbol` |
| "How does X work?" / "How is Y handled?" (natural language) | `search_go_context` |
| "What calls X? What does X call? Who implements interface I?" | `get_symbol_context` |
| "What does package P do? What does it export?" | `get_package_summary` |
| "Which Go code reads/writes table T? What columns does T have?" | `get_table_context` |
| "When was X last changed? Who changes it? What's the recent history?" | `get_change_history` |
| "What files change together with X?" | `get_coupling` |
| "Is the index fresh?" | `index_status` |
| "What do we know about `<topic>`?" | `search_knowledge` |
| "Remember that …" / "Save this lesson" | `save_knowledge` |

Knowledge anchored to a symbol or package auto-surfaces inside
`get_symbol_context`, `get_package_summary`, and `search_go_context` — no
extra call needed.

## Common workflows

**Trace a behavior.** `find_symbol` (or `search_go_context` if the name is
fuzzy) → `get_symbol_context` for callers/callees → `get_package_summary`
for the enclosing package → open at most the top 1–2 files to verify.

**Investigate a failing test.** `search_go_context` with the test name →
`get_symbol_context` on the production symbol under test → read the test +
production file → explain the divergence.

**Estimate change impact.** `find_symbol` → `get_symbol_context` (callers,
implementors) → `get_package_summary` per affected package → `get_coupling`
on the file → summarise direct callers, implementors, dependent packages,
and confidence.

**Map data flow.** `get_table_context` → `get_symbol_context` on the most
relevant writer → `get_change_history` if "what changed" matters.

## Saving durable knowledge

When you encounter a non-obvious lesson, convention, decision, or
domain-term clarification during a session, call `save_knowledge` so the
next agent (Codex, Claude, or human) can find it. Required arguments:

- `category` — one of `lesson`, `best_practice`, `convention`,
  `domain_knowledge`, `how_to`, `decision`.
- `title` — short, searchable headline (≤120 chars).
- `body` — Markdown. Lead with the rule, then `**Why:**`, then `**How to
  apply:**`. The *why* is what makes the entry useful in six months.

Strongly recommended:

- `anchors` — `[{"type": "symbol"|"file"|"package"|"table", "ref": "<id>"}]`.
  Most specific that applies. Anchored entries auto-surface inside
  `get_symbol_context`, `get_package_summary`, and `search_go_context`.
- `source` — set to `"codex"` so saved entries can be filtered by their
  originating agent. Defaults to `"agent"` if omitted.

Only save when one of the nine capture signals fires: user correction,
revealed rule, domain term clarified, non-obvious root cause, stuck →
unstuck moment, repeated task, surprise / gotcha, design decision with
rationale, or pattern observed across files. Skip ephemera, restatements,
and self-explanatory code.

## Anti-patterns

- **Grep-first** — `rg SupplierFunding` before any MCP call wastes tokens
  re-doing what the index already answers.
- **Read-then-confirm** — reading a 500-line file to find one function
  when `find_symbol` returns the exact location.
- **Re-deriving history** — `git log --oneline file.go` when
  `get_change_history` already has it indexed and ranked.
- **Ignoring stale index** — if results look wrong or empty, call
  `index_status`. If stale, surface that to the user; do not silently
  fall back to grep.

## When NOT to use ProjectLens

- Reading full file contents — use the file-read tool.
- Editing code — use the edit / write tool.
- Running commands, tests, builds — use the shell tool.
- Questions about non-Go code, configs, scripts, or prose docs outside the
  indexed surface — use grep / read.
