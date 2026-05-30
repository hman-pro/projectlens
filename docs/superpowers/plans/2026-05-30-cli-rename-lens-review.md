# Review: CLI Rename `projectlens` -> `lens` Implementation Plan

**Date:** 2026-05-30
**Target:** `docs/superpowers/plans/2026-05-30-cli-rename-lens.md`
**Verdict:** Not ready to execute as written. The task coverage is strong and now matches the rev 2 spec, but the verification and commit mechanics will fail or risk staging unrelated work.

## Findings

### 1. Blocker: final audits cannot pass from repo root as written

Task 5 runs broad repo-root audits and expects zero hits (`docs/superpowers/plans/2026-05-30-cli-rename-lens.md:471`, `docs/superpowers/plans/2026-05-30-cli-rename-lens.md:479`, `docs/superpowers/plans/2026-05-30-cli-rename-lens.md:487`). Those commands will match the implementation plan itself, the rev 2 design spec, the design review, and older historical specs/plans, all of which intentionally contain old names as before/after context. For example, the plan itself contains `cmd/projectlens`, `build-projectlens`, and `/bin/projectlens` in its instructions (`docs/superpowers/plans/2026-05-30-cli-rename-lens.md:21`, `docs/superpowers/plans/2026-05-30-cli-rename-lens.md:48`, `docs/superpowers/plans/2026-05-30-cli-rename-lens.md:61`), so the moved-path audit at line 483 cannot produce zero hits after implementation unless the plan is also rewritten or excluded.

The same applies to the command-guidance audit: older specs under `docs/superpowers/specs/` and plans under `docs/superpowers/plans/` contain historical typed-command examples. Rev 2 explicitly treats historical plans/specs as historical unless final audit intentionally includes them. Scope the audits to current source and owner docs, or add explicit `--glob` exclusions for historical planning artifacts and the current plan/review/spec files.

### 2. Blocker: `git add -A` can stage unrelated dirty work

Every commit step uses `git add -A` (`docs/superpowers/plans/2026-05-30-cli-rename-lens.md:126`, `docs/superpowers/plans/2026-05-30-cli-rename-lens.md:220`, `docs/superpowers/plans/2026-05-30-cli-rename-lens.md:349`, `docs/superpowers/plans/2026-05-30-cli-rename-lens.md:437`, `docs/superpowers/plans/2026-05-30-cli-rename-lens.md:503`). The current worktree already has an unrelated modification in `docs/tasks.md` (`git -c core.fsmonitor=false status --short` shows `M docs/tasks.md` before executing the plan). If an implementer follows Task 1 literally, that unrelated change can be swept into the first rename commit.

Add a preflight `git -c core.fsmonitor=false status --short` step and require a clean worktree or an explicit list of known pre-existing files. Replace `git add -A` with path-specific staging per task.

### 3. Major: intermediate Makefile grep has a false-failure expectation

Task 1 Step 3 says `rg -n 'projectlens' Makefile` should leave only env vars and the postgres URL (`docs/superpowers/plans/2026-05-30-cli-rename-lens.md:61`). Current `Makefile` also intentionally contains graph artifact defaults `projectlens-graph.json` and `projectlens.graphml` (`Makefile:127`, `Makefile:128`). Rev 2 preserves `projectlens-graph/v2` as graph schema identity, and previous ProjectLens guidance treats graph export filenames as ProjectLens graph artifacts, not CLI binary names.

Either update the expected output to include `GRAPH_JSON` / `GRAPH_OUT`, or make the grep target the actual stale build/binary patterns:

```bash
rg -n 'cmd/projectlens|build-projectlens|/bin/projectlens|uses projectlens migrate' Makefile
```

### 4. Major: final allowlist omits intentional retained lowercase identities and unresolved log-path choices

Task 5 Step 5 filters out some intentional identities, but not all retained lowercase `projectlens` strings (`docs/superpowers/plans/2026-05-30-cli-rename-lens.md:487`). It will still print intentional Docker Compose service names (`projectlens-mcp`, `projectlens-indexer`) even though Task 1/§8 explicitly keeps them (`docs/superpowers/plans/2026-05-30-cli-rename-lens.md:82`). It will also print intentional graph output names like `projectlens.graphml` because only `projectlens-graph` is allowlisted (`Makefile:128`).

There are also live runtime/log strings that the plan neither updates nor explicitly preserves:

- TUI log default `/tmp/projectlens-tui.log` (`internal/tui/app/log.go:16`)
- fallback run-log dir `/tmp/projectlens-tui-runs` (`internal/tui/jobs/runner.go:163`, `internal/tui/sections/jobs/loader.go:23`)
- startup comments/logs such as `projectlens binary not resolvable` (`cmd/projectlens-tui/main.go:105`, `cmd/projectlens-tui/main.go:109`)
- MCP listen logs `projectlens MCP server listening` (`cmd/projectlens-mcp/main.go:82`, `internal/mcpserver/server.go:99`)
- agent hook text referring to `projectlens MCP tool` as the MCP server/tool identity (`agent/claude/settings-snippet.json:20`, `agent/claude/settings-snippet.json:35`)

Decide which of these are product/server identity and which are binary-command surface. Then either update them in Tasks 2-4 or add precise allowlist entries. As written, the final audit reports them and leaves the implementer to improvise.

## What Looks Good

- The plan now includes the architecture/tasks owner-doc gap from the rev 2 review (`docs/superpowers/plans/2026-05-30-cli-rename-lens.md:390`, `docs/superpowers/plans/2026-05-30-cli-rename-lens.md:402`).
- The compose entrypoint gap is covered while preserving service names (`docs/superpowers/plans/2026-05-30-cli-rename-lens.md:82`).
- The report-string TDD sequence is useful and locally scoped (`docs/superpowers/plans/2026-05-30-cli-rename-lens.md:237`).
- The integration compile fallback for `internal/storage/writelock` is the right fallback when local Postgres is unavailable (`docs/superpowers/plans/2026-05-30-cli-rename-lens.md:459`).

## Recommendation

Patch the plan before execution:

1. Add a clean-worktree/staging-scope preflight and replace `git add -A`.
2. Scope final audits away from historical `docs/superpowers/**` artifacts and the current plan/review/spec, or explicitly include those files in a deliberate historical-doc rewrite.
3. Fix the Makefile grep expectation for graph artifact defaults.
4. Decide/update/allowlist lowercase product/server/log identities before Task 5.
