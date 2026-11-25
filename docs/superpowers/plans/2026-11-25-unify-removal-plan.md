# Unify planOrphan and planRemoval Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Install's orphan handling and uninstall's record handling share one removal-decision function, and install's orphans report uninstall's detail.

**Architecture:** `planRecordRemoval` in a new `removal.go` owns the decision; `planRemoval` is deleted and `planOrphan` becomes a thin mapper that keeps `State = orphaned` and its error prefix.

**Tech Stack:** Go, `internal/agentsetup`.

**Spec:** `docs/superpowers/specs/2026-11-25-unify-removal-plan-design.md`

## Global Constraints

- The only visible change: install orphan results carry the `Detail` uninstall would report for the same disk state. Nothing else in `lens agent` output or behavior changes; the `--json` key set `agent kind id path state owned tracked detail` stays as pinned in `cmd/lens/agent_test.go`.
- Install orphan `State` stays `orphaned` in every branch.
- Error prefixes stay `orphan <kind> <id>: ` (install) and `uninstall <kind> <id>: ` (uninstall).
- No detail or snippet string is reworded.
- `installOp` is not merged with the removal op.
- Commit messages: one concise line, no body, no attribution trailer.

## Review Focus

1. An orphan whose journaled forced removal was interrupted (Pending `remove`) is still removed on re-run, not skipped (`matchesRecord` path; existing `TestInstall_InterruptedForcedOrphanRemoval`).
2. A tracked orphan whose entry is gone is dropped, not reported manual (missing beats tracked).
3. A foreign Codex definition where an orphaned MCP record points: dropped with `defined outside the managed block`, file untouched.
4. A foreign sibling next to an orphaned skill: blocked with `foreign sibling …` in both commands.
5. A symlinked orphan path: install reports State `orphaned`, Action `blocked`; uninstall reports State `blocked` — same Detail and Snippet.

---

### Task 1: Shared removal decision

**Files:**
- Create: `internal/agentsetup/removal.go`
- Modify: `internal/agentsetup/install.go` (`planOrphan`; move out `symlinkBlock`, `removeByHandSnippet`)
- Modify: `internal/agentsetup/uninstall.go` (delete `planRemoval`, `uninstallOp`; `Uninstall`, `dropAbsent` use `removalOp`)
- Modify: `internal/agentsetup/evaluate.go` (comment on `ItemStatus`: `foreign` and `symlinkAt` are never both set)
- Modify: `docs/tasks.md` (section 3 Follow-ups: mark the unify item Done, style of the other Done bullets; Next Tasks row's Next Action points at the small-hardening items)
- Test: `internal/agentsetup/install_test.go` (or a new `removal_test.go`)

**Interfaces:**
- Produces: `type removalOp struct { ItemResult; kind opKind; disk installed }`; `planRecordRemoval(t Target, it Item, rec Record, own map[string]bool, force bool) (removalOp, error)`.

- [ ] **Step 1: Write the failing alignment test**

`TestInstall_OrphanModifiedCarriesDetail`: an orphaned skill (a manifest record for a skill ID not in the fixture's canon, e.g. `gamma`, with the recorded hash of its original content) whose directory now contains a symlink. Run install without Force. Assert for that item: `Action == ActionSkipped`, `State == StateOrphaned`, `Detail == "contains a symlink"`. Then run uninstall dry-run on the same repo and assert the same item has `Action == ActionSkipped`, `State == StateModified`, `Detail == "contains a symlink"`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentsetup/ -run TestInstall_OrphanModifiedCarriesDetail -count=1`
Expected: FAIL — install `Detail` is `""`.

- [ ] **Step 3: Write the parity test (Review Focus 3–5)**

`TestRemovalParity_InstallOrphanMatchesUninstall`, table-driven over orphan disk states: missing, foreign Codex MCP definition, tracked with entry present, modified without Force, modified with Force, symlinked parent, foreign sibling, unchanged. For each, dry-run install and dry-run uninstall on identical repos; for the orphan item assert equal `Action`, `Detail`, `Snippet`, `Tracked`; install `State == StateOrphaned`. Reuse the setups of existing tests (`TestInstall_OrphanBlockedAndManual`, `TestInstall_CodexForeignDefinition`, uninstall foreign-sibling tests) rather than inventing new fixtures.

- [ ] **Step 4: Implement per the spec's Design section**

`planRecordRemoval` is today's `planRemoval` body with unwrapped errors. `planOrphan`: `rec, _ := m.Record(o.Agent, st.Kind, st.ID)`; call; wrap error `fmt.Errorf("orphan %s %s: %w", st.Kind, st.ID, err)`; set `State = StateOrphaned`; build the `installOp`. `Uninstall` wraps `fmt.Errorf("uninstall %s %s: %w", rec.Kind, rec.ID, err)`.

- [ ] **Step 5: Verify**

Run: `gofmt -l internal/ cmd/ && go vet ./... && make test`
Expected: no gofmt output; vet clean; all PASS, existing install/uninstall tests unchanged.

- [ ] **Step 6: Update `docs/tasks.md` and commit**

```bash
git add internal/agentsetup/ docs/tasks.md docs/superpowers/specs/2026-11-25-unify-removal-plan-design.md docs/superpowers/plans/2026-11-25-unify-removal-plan.md
git commit -m "refactor(agentsetup): share one removal decision between install and uninstall"
```
