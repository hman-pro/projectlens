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

## How to call `save_knowledge`

Required: `category`, `title`, `body`. Optional but strongly preferred: `anchors`, `tags`.

**Anchor selection** (most specific that applies):
- `symbol` — about a specific function/type. `ref` = full SCIP symbol (e.g., `go . internal/storage . DB.UpsertChunk()`).
- `package` — about a layer or subsystem. `ref` = package name (e.g., `internal/embeddings`).
- `file` — about a single non-code file (config, migration). `ref` = repo-relative path.
- `table` — about a datastore concern. `ref` = table name.
- *no anchor* — broad/cross-cutting wisdom only.

**Body content**: lead with the rule or finding. Then a "Why:" line (the reason — incident, constraint, preference). Then a "How to apply:" line (when this kicks in). The Why is what makes the entry useful in 6 months when the surrounding context has changed.

## Examples

**Lesson, anchored to a symbol:**
```
save_knowledge(
  category="lesson",
  title="halfvec(1024) ANN index needs lists ≥ √rows",
  body="When the lists parameter is too small relative to row count, recall collapses below 50%.\n\n**Why:** Hit this when we scaled past 50k chunks — top-k results stopped including obvious matches.\n**How to apply:** When tuning vector indexes, set lists ≈ √(expected rows), reindex with CONCURRENTLY.",
  tags=["pgvector", "performance"],
  anchors=[{"type":"package","ref":"internal/storage"}]
)
```

**Convention, anchored to a package:**
```
save_knowledge(
  category="convention",
  title="Provider clients live under internal/providers/<name>",
  body="Each external API gets its own subdirectory under internal/providers/. Constructor takes config + http.Client.\n\n**Why:** Keeps boundary tests isolated and makes provider swaps trivial.\n**How to apply:** Adding a new external API → create internal/providers/<name>/, expose Client struct, add config block in configs/index.yaml.",
  tags=["architecture"],
  anchors=[{"type":"package","ref":"internal/providers"}]
)
```

**Decision, no anchor (broad):**
```
save_knowledge(
  category="decision",
  title="Ollama for embeddings, Anthropic for summaries",
  body="Local Ollama mxbai-embed-large for embeddings; Claude Sonnet via Anthropic API for summaries.\n\n**Why:** Embeddings are high-volume + privacy-sensitive (run locally, free). Summaries are low-volume + benefit from quality (worth API cost).\n**How to apply:** Don't add new providers without a clear reason; prefer extending one of these two.",
  tags=["providers"]
)
```

## Silent skip

If you scan a turn and nothing qualifies, **stop silently** — do not narrate
"I checked and nothing qualified". The user will not see the check; that's the
point.
