# Review: CLI Rename `projectlens` -> `lens`

**Date:** 2026-05-30
**Target:** `docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md`
**Reviewed revision:** Rev 2, committed alongside this review
**Verdict:** Nearly ready. Rev 2 folds in the five original findings and applies both branding decisions. One live-doc scope gap remains before turning this into an implementation plan.

## Remaining Finding

### Major: Live owner docs still missing from the doc/source-path sweep

Rev 2 correctly adds the Makefile dependency sweep, runtime hint-string pass, package/source-path references, `release-demo.sh`, integration verification, targeted audits, `ProjectLens · dashboard`, and unchanged Docker Compose service names. The remaining gap is that the doc/source-path sweep still names `README.md`, `docs/operations.md`, `docs/AGENT_SETUP.md`, and `docs/internals.md`, but misses two non-historical owner docs that will go stale after the `cmd/` and binary rename.

`docs/architecture.md` owns runtime components and component diagrams per `AGENTS.md` / `CLAUDE.md` (`AGENTS.md:25`, `AGENTS.md:43`, `CLAUDE.md:25`, `CLAUDE.md:43`). It currently contains:

- component labels `projectlens CLI`, `projectlens-tui`, `projectlens-mcp` (`docs/architecture.md:14`, `docs/architecture.md:15`, `docs/architecture.md:16`)
- entry points `cmd/projectlens`, `cmd/projectlens-mcp`, `cmd/projectlens-tui` (`docs/architecture.md:35`, `docs/architecture.md:36`, `docs/architecture.md:37`)
- the multi-project MCP process label and link to `cmd/projectlens-mcp/main.go` (`docs/architecture.md:63`, `docs/architecture.md:81`)
- the source-of-truth row `cmd/projectlens/*.go` (`docs/architecture.md:147`)

`docs/tasks.md` is the canonical current task list per `AGENTS.md` / `CLAUDE.md` (`AGENTS.md:31`, `CLAUDE.md:31`), not a historical plan. It still has current entries for `projectlens report`, `projectlens export graph`, `cmd/projectlens/report.go`, `cmd/projectlens/export.go`, and a future smoke-test path under `cmd/projectlens/smoke_test.go` (`docs/tasks.md:40`, `docs/tasks.md:41`, `docs/tasks.md:194`).

Add both files to §5 or §6. The targeted audit would probably catch these later, but the implementation plan should not rely on a final audit to discover source-of-truth docs.

## Resolved From Rev 1

- **Makefile dependency sweep:** Rev 2 now enumerates all `build-projectlens` dependents, not just run targets (`docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:34`).
- **Runtime guidance strings:** Rev 2 now covers CLI, report, MCP, TUI empty states, and matching tests (`docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:52`).
- **Package/source-path references:** Rev 2 now separates moved `cmd/` leaf dirs from typed command docs and includes AGENTS/CLAUDE plus the writelock integration test (`docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:81`).
- **Release scripts:** Rev 2 correctly moves the action item to `scripts/release-demo.sh` and leaves `scripts/release-smoke.sh` import paths alone (`docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:113`).
- **Verification:** Rev 2 replaces broad `rg 'projectlens'` with targeted audits and adds integration coverage for the build-path test (`docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:149`).
- **Branding calls:** Rev 2 keeps the TUI title as product brand `ProjectLens · dashboard` and keeps Docker Compose service names unchanged (`docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:125`, `docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md:132`).

## Notes

- The MD060 table-spacing lint warnings are cosmetic if they match the existing repo style.
- Historical plans/specs can stay historical unless a final audit intentionally includes them. The remaining finding above is about current owner docs, not archived implementation history.
