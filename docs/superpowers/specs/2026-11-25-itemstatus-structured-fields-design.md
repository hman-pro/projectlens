# Structured ItemStatus Fields — Short Design

Date: 2026-11-25
Status: approved (option 1)
Source: `docs/tasks.md` section 3, Follow-ups, first Structure item.

## Intent

`agentsetup.ItemStatus` carries the facts `install` needs only as human
detail text, so `install` re-derives them: it pattern-matches detail strings
to build the blocked-item snippet, and re-reads `.codex/config.toml` to learn
whether a `current` Codex MCP entry is a foreign definition. Rewording a
detail string would silently drop a snippet; the re-read duplicates what
`Evaluate` already knew. Carry the facts as fields instead.

## Goals

- `install` stops parsing `ItemStatus.Detail` (`blockedSnippet`).
- `install` stops re-reading `config.toml` (`isForeignCodex` is deleted).

## Non-goals

- No change to `lens agent` behavior or output, including `--json` (its key
  set is pinned by `cmd/lens/agent_test.go`).
- No reason-code enum; detail strings stay as written (option 3, rejected:
  ~4x the edits, forces JSON-contract and vocabulary decisions, overlaps the
  `planOrphan`/`planRemoval` unification).
- `planOrphan`'s own `symlinkBlock` path and `uninstall.go` are untouched;
  they belong to the unification follow-up.
- `recovered && st.Detail == ""` in `planCanonical` stays: it means "do not
  overwrite an existing detail", which is not parsing.

## Approaches

1. **Unexported fields on `ItemStatus`** — picked. Invisible to JSON and to
   `cmd/lens`; smallest diff.
2. Exported fields tagged `json:"-"` — same effect, needless exported surface.
3. Typed reason code with `Detail` rendered from it — rejected, see Non-goals.

## Design

- `ItemStatus` gains two unexported fields:
  - `foreign bool` — the Codex MCP entry on disk is defined outside the
    managed block (copied from `installed.foreign`).
  - `symlinkAt string` — the symlinked path component that blocks the item;
    `""` when none. Set by the two step-1 blocked returns in `evaluateItem`
    (`symlinked parent <comp>` → `<comp>`, `symlinked file <path>` → `<path>`).
- `foreign` is set immediately after `installedHash`, before the tracked
  check, because today's `isForeignCodex` answers for tracked items too.
- `blockedSnippet(st ItemStatus) string`: `foreign` → `snippetForeign`;
  `symlinkAt != ""` → `symlinkSnippet(symlinkAt)`; else `""`.
- `planCanonical` `StateCurrent` uses `st.foreign`; `isForeignCodex` is deleted.

## Left to the builder

Test placement and names; whether `blockedSnippet` keeps its name.
