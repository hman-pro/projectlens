# Unify planOrphan and planRemoval — Short Design

Date: 2026-11-25
Status: approved (approach 1, align)
Source: `docs/tasks.md` section 3, Follow-ups, second Structure item.

## Intent

The decision "what happens to a recorded item lens no longer wants" is written
twice: `planOrphan` (`install.go`, an orphaned record during install) and
`planRemoval` (`uninstall.go`, every record during uninstall). The two chains
are the same step for step; a fix to one silently misses the other. Write the
decision once.

## Goals

- One function owns the removal decision; `planOrphan` and `planRemoval`
  both use it.
- Install's skipped/manual/removed orphans report the same `Detail` uninstall
  reports for the same disk state (alignment, user-picked).

## Non-goals

- Install still reports an orphan's `State` as `orphaned` in every branch.
- Error prefixes stay: `orphan <kind> <id>: …` from install,
  `uninstall <kind> <id>: …` from uninstall.
- No change to Action, Snippet, Tracked, journal effects, or the `--json` key set.
- `installOp` and `uninstallOp` are not merged (approach 3, rejected: install
  carries install-only fields, nothing gained).

## Approaches

1. **One shared decision function, each caller maps the result** — picked.
2. Install calls `planRemoval` and patches `State` afterwards — rejected:
   install would depend on an uninstall function, and post-patching drifts.
3. Also merge the op types — rejected, see Non-goals.

## Visible change (alignment)

Today install leaves `Detail` empty for an orphan that differs from its record
(`StateModified` in uninstall's terms). After this change it carries
uninstall's detail: `disk.detail` (`symlink`, `contains a symlink`) or
`duplicate entries` for a duplicated hook. Detail text is added where it was
empty; no string is reworded and no JSON key changes.

## Design

- New file `internal/agentsetup/removal.go` holding:
  - `type removalOp struct { ItemResult; kind opKind; disk installed }`
    (the current `uninstallOp`, renamed; `uninstallOp` is removed).
  - `planRecordRemoval(t Target, it Item, rec Record, own map[string]bool, force bool) (removalOp, error)`:
    today's `planRemoval` body, returning unwrapped errors; `State` is the
    record-level state (`blocked`, `missing`, `current`, `modified`).
  - `symlinkBlock` and `removeByHandSnippet`, moved from `install.go` (only
    removal planning uses them).
- `planRemoval` is deleted; `Uninstall` calls `planRecordRemoval` and wraps
  errors with `uninstall <kind> <id>: `.
- `planOrphan` looks up the record, calls `planRecordRemoval`, wraps errors
  with `orphan <kind> <id>: `, sets `State = StateOrphaned`, and copies
  `ItemResult`, `kind`, `disk` into its `installOp`.
- `ItemStatus` gets a one-line comment: `foreign` and `symlinkAt` are never
  both set.

## Left to the builder

Test names and placement; comment wording.
