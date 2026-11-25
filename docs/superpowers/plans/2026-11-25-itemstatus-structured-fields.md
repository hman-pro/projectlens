# Structured ItemStatus Fields Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `lens agent install` reads the blocked-snippet and foreign-definition facts from `ItemStatus` fields instead of parsing detail text and re-reading `.codex/config.toml`.

**Architecture:** `evaluateItem` records two unexported facts on `ItemStatus`; `planCanonical` and `blockedSnippet` consume them; `isForeignCodex` is deleted. No output change.

**Tech Stack:** Go, `internal/agentsetup`.

**Spec:** `docs/superpowers/specs/2026-11-25-itemstatus-structured-fields-design.md`

## Global Constraints

- No change to `lens agent` behavior or output, including `--json`; the key set `agent kind id path state owned tracked detail` stays exactly as pinned in `cmd/lens/agent_test.go`.
- New fields are unexported: `foreign bool`, `symlinkAt string`. No JSON tags.
- Detail strings are not reworded anywhere.
- Do not touch `planOrphan`, `symlinkBlock`, `uninstall.go`, or the `recovered && st.Detail == ""` check in `planCanonical`.
- Commit messages: one concise line, no body, no attribution trailer.

## Review Focus

1. A tracked Codex `config.toml` with a foreign `mcp_servers.projectlens` at the canonical URL and an existing manifest record: install must still report `current` without re-recording (today `isForeignCodex` answers for tracked files, so `foreign` must be set before the tracked short-circuit).
2. A foreign definition at the canonical URL, untracked, `--force`: still `current`, `pre-existing, not managed`, never adopted (existing `TestInstall_CodexForeignDefinition/canonical url` covers it).
3. Symlinked parent vs symlinked file: the snippet names the exact component (`.agents`, or the file path), same text as today.
4. A non-blocked status with an empty `symlinkAt` and `foreign == false` yields an empty snippet.
5. Orphan items keep their current snippets (untouched path; existing `TestInstall_OrphanBlockedAndManual` covers it).

---

### Task 1: Carry `foreign` and `symlinkAt` on ItemStatus

**Files:**
- Modify: `internal/agentsetup/evaluate.go` (`ItemStatus`, `evaluateItem`)
- Modify: `internal/agentsetup/install.go` (`planCanonical`, `blockedSnippet`; delete `isForeignCodex`)
- Modify: `docs/tasks.md` (section 3 Follow-ups: mark the `ItemStatus` item Done; Next Tasks row's Next Action points at the `planOrphan`/`planRemoval` unification)
- Test: `internal/agentsetup/evaluate_test.go`, `internal/agentsetup/install_test.go`

**Interfaces:**
- Produces: `ItemStatus.foreign bool`, `ItemStatus.symlinkAt string`, `blockedSnippet(st ItemStatus) string`.

- [ ] **Step 1: Write the failing evaluate tests**

In `evaluate_test.go`:
- `TestEvaluate_CodexForeign`: canonical-URL case asserts `st.foreign`; other-URL case asserts `st.foreign`; a status with no foreign definition (managed block placed via `place`) asserts `!st.foreign`.
- New subtest in `TestEvaluate_TrackedPaths` or a new `TestEvaluate_TrackedCodexForeign`: Codex `config.toml` containing `[mcp_servers.projectlens]\nurl = "<evalURL>"\n`, committed with `git add -f`; assert `State == StateCurrent && Tracked && foreign`.
- `TestEvaluate_SymlinkedAncestorBlocked`: the `.agents` case asserts each blocked skill has `symlinkAt == ".agents"`; add a case for a symlinked non-skill file (e.g. `.mcp.json` as a symlink) asserting `State == StateBlocked && symlinkAt == ".mcp.json"`.
- `TestEvaluate_States` (or one spot check): a non-blocked status has `symlinkAt == ""`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentsetup/ -run 'TestEvaluate_' -count=1`
Expected: build failure, `st.foreign undefined` / `symlinkAt undefined`.

- [ ] **Step 3: Write the install pinning test (passes on current code)**

`TestInstall_TrackedCodexForeignNotRerecorded` in `install_test.go`: Codex repo with `.codex/config.toml` = `[mcp_servers.projectlens]\nurl = "<evalURL>"\n`, committed; a saved manifest holding a Codex MCP record with `Hash: MCPHash(evalURLOther)`. Run install with and without `Force`. Assert: result `Action == ActionCurrent`; the manifest record's `Hash` is still `MCPHash(evalURLOther)`; `config.toml` bytes unchanged.

Add one direct unit test `TestBlockedSnippet_UsesFields`: `blockedSnippet(ItemStatus{symlinkAt: "x", Detail: "unrelated text"}) == symlinkSnippet("x")`; `blockedSnippet(ItemStatus{foreign: true}) == snippetForeign`; `blockedSnippet(ItemStatus{Detail: "symlinked parent x"}) == ""`.

- [ ] **Step 4: Implement**

- `ItemStatus`: add `foreign bool` and `symlinkAt string` with one-line comments.
- `evaluateItem`: set `st.symlinkAt` in the two step-1 blocked returns (the component from `firstSymlink`, or `c.Path`); set `st.foreign = disk.foreign` immediately after `installedHash`, before the tracked check.
- `blockedSnippet(st ItemStatus) string`: `foreign` → `snippetForeign`; `symlinkAt != ""` → `symlinkSnippet(st.symlinkAt)`; else `""`. Caller passes `st`.
- `planCanonical` `StateCurrent`: replace the `isForeignCodex` call with `st.foreign`; delete `isForeignCodex`. Drop the `strings` import from `install.go` only if now unused.

- [ ] **Step 5: Verify**

Run: `gofmt -l internal/ cmd/ && go vet ./... && make test`
Expected: no gofmt output; vet clean; all tests PASS (including `cmd/lens` JSON key-set tests).

- [ ] **Step 6: Update `docs/tasks.md` and commit**

```bash
git add internal/agentsetup/evaluate.go internal/agentsetup/install.go internal/agentsetup/evaluate_test.go internal/agentsetup/install_test.go docs/tasks.md docs/superpowers/specs/2026-11-25-itemstatus-structured-fields-design.md docs/superpowers/plans/2026-11-25-itemstatus-structured-fields.md
git commit -m "refactor(agentsetup): carry foreign and symlink facts on ItemStatus"
```
