# Review: CLI Rename `projectlens` -> `lens`

**Date:** 2026-05-30
**Target:** `docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md`
**Verdict:** Not ready for implementation plan. The direction is sound, but the current scope misses several current-tree references that will either break build/run targets or keep emitting dead `projectlens ...` commands after the hard cut.

## Findings

### 1. Blocker: Makefile target rename is incomplete for existing convenience targets

The spec renames `build-projectlens*` to `build-lens*` and explicitly calls out `.PHONY`, the `build` aggregate, `go build` path args, and only the `cli` / `mcp` / `tui` run target dependency lists (`docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:31`). Current `Makefile` has many non-run targets that also depend on `build-projectlens`: `bootstrap`, `reindex`, `reindex-full`, `reindex-dry`, `status`, `query`, every `index-*` shortcut, `graph-export`, and `migrate` (`Makefile:92`, `Makefile:95`, `Makefile:98`, `Makefile:101`, `Makefile:104`, `Makefile:108`, `Makefile:111`, `Makefile:114`, `Makefile:117`, `Makefile:120`, `Makefile:123`, `Makefile:133`, `Makefile:169`).

After deleting/renaming `build-projectlens`, those targets fail before they can run `./$(CLI)`. Add an explicit Makefile pass that updates every dependency on renamed build targets, plus help text like `uses projectlens migrate` at `Makefile:169`.

### 2. Blocker: User-facing generated guidance still tells people to run the removed command

Section 3 says the TUI resolver is "the only behavioral break if missed" (`docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:41`). That is too narrow for a hard cut with no alias. Current code emits `projectlens ...` commands from multiple runtime surfaces:

- CLI status when no runs exist: `Run 'projectlens bootstrap' first.` (`cmd/projectlens/main.go:237`)
- Report degradation suggestions: `run projectlens reindex`, `run projectlens index-*` (`internal/report/derive.go:11`)
- MCP tool responses and 503 hints: `projectlens index-datastore`, `projectlens bootstrap`, `projectlens index-history`, `projectlens migrate --project ...` (`internal/mcpserver/handlers.go:348`, `internal/mcpserver/handlers.go:415`, `internal/mcpserver/handlers.go:508`, `internal/mcpserver/handlers.go:578`, `internal/mcpserver/handlers.go:752`, `internal/mcpserver/not_ready.go:16`)
- TUI empty-state text: `run "projectlens bootstrap"` (`internal/tui/sections/health/view.go:20`, `internal/tui/sections/runs/view.go:22`)

These are not cosmetic comments; they are operational guidance shown to users and agents. The implementation plan should include a runtime string pass across CLI, MCP, report output, TUI views, and the matching tests.

### 3. Blocker: `cmd/*` directory moves invalidate package/install/test paths, not just typed commands

The spec says documentation should edit only `projectlens` occurrences used as a typed command (`docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:54`). That misses references that are not typed commands but become wrong once `cmd/projectlens*` is moved:

- README install paths still point at `github.com/hman-pro/projectlens/cmd/projectlens@latest`, `cmd/projectlens-mcp`, and `cmd/projectlens-tui` (`README.md:17`, `README.md:18`, `README.md:19`).
- Agent setup still uses `go run ./cmd/projectlens/ unlock --force` (`docs/AGENT_SETUP.md:334`).
- Operations and internals source references point at `cmd/projectlens` and `cmd/projectlens/*.go` (`docs/operations.md:3`, `docs/internals.md:3`, `docs/internals.md:74`, `docs/internals.md:107`, `docs/internals.md:203`, `docs/internals.md:205`).
- Contributor source-of-truth docs in `AGENTS.md` and `CLAUDE.md` still name `cmd/projectlens/*.go` and `cmd/projectlens/main.go` as canonical paths (`AGENTS.md:54`, `AGENTS.md:55`, `CLAUDE.md:54`, `CLAUDE.md:55`).
- The integration writelock test builds `../../../cmd/projectlens/` into a temp `projectlens` binary (`internal/storage/writelock/cli_integration_test.go:26`, `internal/storage/writelock/cli_integration_test.go:27`).

This needs a separate "package path/source path references" work item. It should update docs, tests, and source-of-truth tables even when the occurrence is not a bare command invocation.

### 4. Major: Scripts section names the wrong smoke script and misses the release demo path

The spec calls out `scripts/release-smoke.sh` for CLI invocation replacement (`docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:84`), but the current `release-smoke.sh` only embeds a temporary Go file and uses the module import path, which should remain `github.com/hman-pro/projectlens/...` (`scripts/release-smoke.sh:39`, `scripts/release-smoke.sh:49`, `scripts/release-smoke.sh:66`). The script that currently invokes renamed build/binary surfaces is `scripts/release-demo.sh`: `make build-projectlens-mcp`, `./bin/projectlens-mcp`, and `/tmp/projectlens-mcp.log` (`scripts/release-demo.sh:44`, `scripts/release-demo.sh:45`, `scripts/release-demo.sh:56`).

Keep the `release-smoke.sh` module import path note, but move the actual binary-invocation action item to `release-demo.sh`.

### 5. Major: Verification misses integration coverage and uses an over-broad audit

The verification section requires `make build`, `make test`, and `make vet` (`docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:97`), but a current integration test has a hardcoded `go build ../../../cmd/projectlens/` path (`internal/storage/writelock/cli_integration_test.go:27`). That will not be exercised by `make test` because the file is behind `//go:build integration` (`internal/storage/writelock/cli_integration_test.go:1`). Add `make test-int` or at least a focused integration compile/test command for the affected package.

Also, `rg 'projectlens'` as the final audit (`docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:105`) is not precise enough for this repo. It will intentionally find the product name, env vars, DB names, module paths, MCP server identity, historical docs, graph filenames, temp log names, skill IDs, and agent MCP names. Use targeted audits instead, for example:

- `rg -n '(^|[ ./"])projectlens( |$| [a-z-]|")'` for bare command-like guidance.
- `rg -n 'cmd/projectlens|cmd/projectlens-mcp|cmd/projectlens-tui|build-projectlens|projectlens-mcp|projectlens-tui|/bin/projectlens|go run ./cmd/projectlens'` for moved package/build/binary paths.
- A second allowlist review for intentional identities: `PROJECTLENS_*`, `github.com/hman-pro/projectlens`, DB/schema/user names, MCP server registration name, skill IDs, and product prose.

## Open Questions

- Should the TUI title stay `projectlens · dashboard` as a product-style label (`internal/tui/app/view.go:66`), or should it change to `lens · dashboard` to match the launched binary? The spec preserves product name "ProjectLens", but this lowercase in-app label reads closer to binary branding than prose.
- Should Docker Compose service names be renamed when that may break existing local workflows like `docker compose logs projectlens-mcp`? The spec chooses to rename service names while preserving the volume; that is coherent, but it deserves explicit migration notes in `docs/operations.md`.

## What Looks Good

- Keeping `PROJECTLENS_*`, DB names, module path, storage schema, and MCP server identity unchanged is the right blast-radius control.
- Calling out `PROJECTLENS_BINARY` as an unchanged env var with a new fallback/default binary name prevents an easy over-rename.
- The hard cut is acceptable for public alpha, but only if every generated hint and current install/build path is updated in the same change.
