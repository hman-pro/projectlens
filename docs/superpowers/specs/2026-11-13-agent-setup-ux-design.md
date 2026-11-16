# Agent Setup UX Design: `lens agent install / uninstall / status / doctor`

Date: 2026-11-13
Status: draft, awaiting approval
Task: `docs/tasks.md` section 3 (P1, Agent setup UX)

## Intent

Wiring ProjectLens into an agent today is five manual steps spread over
`docs/AGENT_SETUP.md`: symlink two skills, hand-merge a hooks JSON file, paste
an MCP entry into home-directory config, paste a prompt snippet, restart. There
is no way to check whether the result is current or why the agent is not
calling the tools.

This adds three commands so a developer can wire Claude Code or Codex into one
repository, see whether that wiring is stale, and diagnose a broken
agent-to-MCP loop.

## Goals

- `lens agent install claude|codex` puts the wiring in place with one command.
- Re-running install is safe and reports per item: written, already current,
  updated, skipped (locally modified), or needs a manual step.
- `lens agent status` reports state per item without changing any file.
- `lens agent uninstall claude|codex|all` removes exactly what install wrote.
- `lens agent doctor` checks the live loop and prints a concrete next action
  for every failure.
- `--dry-run` on install shows what would change and writes nothing.

## Non-goals

- Binary or package distribution, Homebrew, release artifacts.
- Agents other than Claude Code and Codex.
- Starting or supervising the MCP server.
- Shared, committed wiring for a whole team (see "Scope switch").
- Any change to indexing behavior.

## Anti-goals

These make the feature a failure even if it works:

- Install writes anywhere outside the target repository. The single exception
  is the repository's own git `info/exclude` file (see "Keeping the wiring out
  of git").
- Install changes what `git status` reports in the target work tree, in either
  direction: it adds no new entry (no new untracked file, no modification),
  and it hides no file that was visible before. A pre-existing untracked file
  stays listed as untracked after install, even if install merged an entry
  into it.
- Install modifies a file the target repository already tracks.
- A re-install silently overwrites a file the developer edited.
- `status` or `doctor` reports "not installed" when the real problem is a dead
  server, a wrong URL, or an untrusted project.

## Decisions made with the maintainer

| Decision | Choice |
|---|---|
| Where install may write | Inside the target repository only. Never home-directory config. |
| Who the wiring is for | Personal to the developer who ran it. Not committed. |
| How canonical assets reach the target repo | Copied from the ProjectLens checkout at run time. |
| Writing git `info/exclude` outside the target root (linked worktrees, submodules) | Allowed, as the single exception to the write boundary. |
| Uninstall | In scope: `lens agent uninstall`. |

## Approaches considered

How the canonical assets under `agent/` reach the target repository:

1. **Embed in the binary and copy, with a manifest.** Works from any directory
   and has one unambiguous reference for staleness. A skill edit needs a `lens`
   rebuild before it can reach a target repo.
2. **Symlink skills to the checkout, merge the rest.** Skill edits are live
   immediately. Hooks and MCP entries live inside other files and cannot be
   symlinked, so two mechanisms and two meanings of "stale" result. Unconfirmed
   whether Codex follows a symlinked skill directory.
3. **Copy from the checkout at run time, with a manifest. (Chosen.)** One
   mechanism for every item and both agents; a skill edit reaches a target repo
   by re-running install, with no rebuild. The cost is that `lens` must locate
   the checkout, and the staleness reference is whichever checkout it finds.

The maintainer chose 3 over 1 so that skill edits propagate without rebuilding
`lens`, and over 2 for the single mechanism.

## What gets installed where

All paths are relative to the target repository root.

| Item | Claude | Codex | Canonical source |
|---|---|---|---|
| MCP entry | `.mcp.json`, key `mcpServers.projectlens` | `.codex/config.toml`, table `[mcp_servers.projectlens]` | generated from the resolved URL |
| Skills | `.claude/skills/<name>/` | `.agents/skills/<name>/` | `agent/skills/<name>/` |
| Hooks | `.claude/settings.local.json`, under `hooks` | none | `agent/claude/settings-snippet.json` |
| Prompt snippet | not installed | not installed; printed | `agent/claude/CLAUDE.md.snippet`, `agent/codex/AGENTS.md.snippet` |

Notes:

- MCP entries use the direct Streamable HTTP form (`"type": "http"` plus `url`
  for Claude; `url = "..."` for Codex). The `npx mcp-remote` bridge is dropped.
- Skill directories are copied verbatim, byte for byte. No templating, so a
  hash comparison is meaningful. The skill body's prose mention of
  `http://localhost:8484/mcp` is left as is.
- Prompt snippets are not installed because they need a tracked file
  (`CLAUDE.md`, `AGENTS.md`). `install codex` prints the snippet with a note
  that Codex has no hooks, so pasting it is the only always-on guidance.
  `install claude` does not print one; hooks cover that role.

## Target repository and URL resolution

Registry lookup, first match wins:

1. `--projects <path>` if passed explicitly.
2. `configs/projects.yaml` in the current directory (existing CLI default).
3. `configs/projects.yaml` under the baked checkout path (see "Locating the
   canonical assets"), so `--project` works from inside a target repo.
4. None found: there is no registry (legacy single-project mode).

An absolute `repo_path` is used as is. A relative `repo_path` needs a base, and
the base depends only on where the registry file is, never on where the
command happened to be run:

| Registry file location | Base for relative `repo_path` |
|---|---|
| Inside the baked checkout path (cases 1 and 3, and case 2 when run from the checkout) | the baked checkout root |
| `configs/projects.yaml` directly under the current directory, no baked path available | the current directory (today's behaviour) |
| Anywhere else | none: stop with an error asking for an absolute `repo_path` in that registry |

The third row is the guard: `--projects /src/projectlens/configs/projects.yaml`
run from `/src/shop` must never resolve `repo_path: .` to `/src/shop`. With a
baked path it resolves to the checkout; without one it is refused.

After resolution the target must be the root of a git work tree; otherwise
stop with an error. The existing code passes `repo_path` through untouched;
this feature does not change that for other commands.

Target repository, first match wins:

1. `--project <slug>`: the resolved `repo_path` from the registry.
2. Otherwise the git root of the current directory.
3. Not inside a git repository: stop with an error naming both options.

MCP URL at install time, first match wins:

1. `--url <url>`.
2. `--project <slug>`: `http://localhost:<port>/<slug>/mcp`.
3. A registry exists and exactly one project's resolved `repo_path` equals the
   target root: that project's slug URL.
4. A registry exists and no project matches: stop with an error that says the
   server only serves `/<slug>/mcp` in multi-project mode, and names
   `--project` and `--url`. The legacy URL would be dead.
5. No registry: the legacy `http://localhost:<port>/mcp`.

`<port>` is `--port`, else `PROJECTLENS_MCP_PORT`, else 8484.

Install records the resolved URL in the manifest. `status` and `doctor` do not
re-resolve: the recorded URL is the reference for the MCP item, and it is the
URL doctor probes. Re-running install with different flags changes it.

The `agent` commands must not require `configs/index.yaml` or a database
connection, so they work when run from inside a target repo.

## Locating the canonical assets

First match wins:

1. `--assets <dir>` (a directory shaped like `agent/`).
2. A checkout path baked into the binary at build time through the Makefile's
   `-ldflags -X`.
3. Neither resolves to a directory containing `skills/`: stop with an error
   that prints the exact `--assets` flag to pass.

`make build` and `make install` both bake the path.

## Keeping the wiring out of git

- Only paths that install itself created are added to the repository's
  `info/exclude`. A file or directory that already existed, tracked or not, is
  never excluded, so the developer's own untracked config stays visible in
  `git status`. Merging an entry into a pre-existing untracked file leaves its
  status line unchanged (`??` before and after).
- Exclude entries are as narrow as the thing created: the skill directory
  `/.claude/skills/use-projectlens/`, not `/.claude/`. A parent directory that
  install created only to hold its own files is covered by those narrow
  entries, not by an entry for the parent.
- The manifest records, per path, whether install created it. That record
  drives both the exclude entry and what uninstall may delete.
- `.gitignore` is never edited: it is tracked, so editing it would itself be a
  shared diff.
- **One explicit exception to "inside the target repository only".** The
  exclude file lives in the repository's own git directory, wherever git
  places it (`git rev-parse --git-common-dir`). For a linked worktree or a
  submodule that directory is outside the target root. Install may write that
  one file and nothing else there. This is authorized because the rule exists
  to keep install out of home-directory config and out of shared diffs, and
  the exclude file is neither.
- **Known side effect, reported by install.** In a repository with linked
  worktrees the exclude file is shared, so the entries apply to every
  worktree. They are root-anchored paths (`/.mcp.json`) and cannot hide a file
  another branch tracks. They can hide an untracked file of the same name in a
  sibling worktree; the "hides no file" guarantee therefore holds for the
  target work tree only. Install prints a notice naming the excluded paths
  when it detects this layout. The installed files and the manifest are per
  worktree: each worktree needs its own install.
- Before writing any path, install checks whether git tracks it
  (`git ls-files --error-unmatch`). A tracked file is never modified; see
  "Tracked paths" below for how it is evaluated.
- The exclude entries are written inside a marked block so they can be found
  and updated on re-install.

## Manifest and item states

Install records what it wrote in `.projectlens/agent-manifest.json` in the
target repo (also excluded from git). Per item: agent, kind, path, and the
content hash of what was written. For entries merged into a file that holds
other content (`.mcp.json`, `.codex/config.toml`, `settings.local.json`), the
hash covers only the ProjectLens-owned entry, in a canonical serialization.

`status` derives one state per item from three values: canonical (checkout),
recorded (manifest), and installed (on disk).

| State | Condition | Install does |
|---|---|---|
| missing | not on disk | writes it |
| current | installed = canonical | nothing |
| stale | installed = recorded, canonical differs | updates it |
| modified | installed differs from recorded | skips, reports; `--force` overwrites |
| unmanaged | on disk, no manifest record | skips, reports; `--force` adopts |

Tracked paths. Whether install may write a file and whether its content is
correct are separate questions. For a path git tracks, the manifest is not
consulted and install never writes, with or without `--force`. The state comes
from comparing the ProjectLens-owned entry on disk against canonical:

| State | Condition | Install does |
|---|---|---|
| current (protected) | owned entry present and = canonical | nothing; counts as current for exit codes |
| manual | owned entry absent or different | prints the exact entry to add or replace |

So a developer who follows the printed manual step gets a passing `status` and
`doctor`, and the file stays protected from writes.

Two more cases:

- **Orphaned** (in the manifest, no longer in the canonical assets, for example
  a renamed skill): re-install removes it and its exclude entry when installed
  = recorded; otherwise it is reported and left, and `--force` removes it.
- **Symlinked path** (a skill directory that is a symlink, as the old manual
  instructions created): treated as unmanaged. Install never writes through a
  symlink, because that would modify the checkout. `--force` replaces the link
  itself with a copy.

Wiring left by the old manual instructions is detected and reported, never
migrated automatically. Doctor flags ProjectLens hook commands that also appear
in `.claude/settings.json` (they would fire twice) and prints which entries to
delete.

Ownership inside merged files:

- `.mcp.json`: the `mcpServers.projectlens` key. Other keys are preserved.
- `.codex/config.toml`: the `[mcp_servers.projectlens]` table. Other content is
  preserved byte for byte.
- `settings.local.json`: the hook command entries that carry a ProjectLens
  hook ID. Other hooks and all other keys are preserved.

Hook identity. A hook is identified by a stable ID, not by its command text, so
an edited hook is still recognizably ours. Each canonical hook command in
`agent/claude/settings-snippet.json` gains a leading shell no-op that carries
the ID, for example `: projectlens-hook=pre-edit-impact; echo '...'`. The
manifest records the ID and the hash of the full command.

- ID present, command = canonical: current.
- ID present, command = recorded, canonical differs: stale.
- ID present, command differs from recorded: modified. Install skips it and
  never appends a second copy.
- ID recorded but no longer present in the file: the developer removed the
  marker or the hook. Reported as "removed locally"; install does not re-add
  it without `--force`.

IDs are unique per hook and never reused for a different hook.

## Commands

```
lens agent install <claude|codex> [--project <slug>] [--url <url>] [--port <n>]
                                  [--assets <dir>] [--dry-run] [--force]
lens agent uninstall <claude|codex|all> [--project <slug>] [--dry-run] [--force]
lens agent status  [claude|codex] [--project <slug>] [--assets <dir>] [--json]
lens agent doctor  [claude|codex] [--project <slug>] [--assets <dir>] [--json]
```

- `status` and `doctor` with no agent argument cover the agents recorded in the
  manifest. An agent that was never installed is not a failure. With no
  manifest at all they report "nothing installed", print the install command,
  and exit 1.
- `doctor` accepts `--url` to probe a different endpoint than the recorded one.
- An unsupported agent name is an error that lists the supported ones.
- Exit codes: 0 when everything is current or healthy; 1 when `status` finds
  anything other than current, or `doctor` finds a failure; 2 for usage errors.

What the developer sees from install:

```
$ lens agent install claude
Target: /Users/me/src/shop   URL: http://localhost:8484/mcp

  written   .mcp.json                         (mcpServers.projectlens)
  written   .claude/skills/use-projectlens/
  written   .claude/skills/capture-knowledge/
  written   .claude/settings.local.json       (4 hooks)
  written   .git/info/exclude                 (5 paths)

Next: restart Claude Code in this repo and approve the "projectlens" server.
      Then run: lens agent doctor claude
```

## Uninstall

`uninstall` removes exactly what the manifest records for the named agent and
nothing else. It does not need the canonical assets.

- An item whose content on disk equals the recorded hash is removed: whole
  files and skill directories are deleted; owned entries are cut out of merged
  files, leaving all other content intact.
- A merged file that install created and that is left empty of other content
  (`{}` or an empty `mcpServers` / `hooks` object, an empty TOML file) is
  deleted. A file that existed before install, or holds anything else, stays.
- A modified item is skipped and reported; `--force` removes it.
- Tracked paths are never touched; any ProjectLens entry in one is reported
  with the lines to delete by hand.
- The matching `info/exclude` entries are removed. When the last agent is
  uninstalled, the marked exclude block and the manifest are removed too, and
  `.projectlens/` is deleted if empty.
- `--dry-run` prints what would be removed and writes nothing.
- Nothing recorded for that agent: report "nothing installed", exit 0.

To support the "created by install" rule, the manifest records per merged file
whether install created it.

## Doctor checks

Each check prints pass, fail, or skipped, and every failure carries one
concrete next action. In order:

1. **Assets found.** Canonical asset directory resolves.
2. **Items current.** Same evaluation as `status`.
3. **Server reachable.** The configured URL answers. HTTP 404 on a slug URL
   maps to "slug not registered"; 503 maps to "run `lens migrate --project
   <slug>`"; connection refused maps to "start the server".
4. **Tools match.** Connect with the MCP client already in `go.mod`, list
   tools, and compare names against the server's own tool registry in
   `internal/mcpserver`. No second hand-maintained list.
5. **Installed URL matches.** The URL in the installed MCP entry equals the
   resolved one.
6. **Not shadowed.** Read-only look at `~/.claude.json` and
   `~/.codex/config.toml` for a `projectlens` server that points somewhere
   else. Doctor never writes these.
7. **Agent will load it.** Codex: the project must be trusted, or
   `.codex/config.toml` is ignored. Claude: the project-scoped server must be
   approved. Where the state cannot be read reliably, print the manual check
   instead of guessing.
8. **`lens` on PATH**, for the commands doctor recommends.

## Scope switch

Shared, committed wiring is deferred, not rejected. Item placement must live in
one table keyed by scope, so adding a shared scope later changes that table
(for example `settings.json` instead of `settings.local.json`, no git exclude)
and not the install, status, or doctor logic.

## Constraints

- Follow repo conventions: internal packages only, wrapped errors, no global
  state, constructor-injected dependencies.
- Core logic lives in a new internal package and takes the target root, asset
  root, and a filesystem as inputs so it is testable against temp directories.
  `cmd/lens/` holds only the cobra wiring.
- No new runtime dependency for JSON. A TOML library may be added if the
  builder judges it necessary to preserve unrelated `.codex/config.toml`
  content safely.

## Tests required

- Idempotency: a second install changes nothing and reports everything current.
- Dry run writes nothing, including the manifest and the git exclude.
- Every state in the table above, per item kind.
- Tracked-file refusal for each merged file, and a tracked file holding the
  exact expected entry passing `status` and `doctor` as current.
- A hook whose command was edited, followed by re-install: reported modified,
  exactly one copy in the file afterwards.
- A hook whose ID marker was removed, followed by re-install: not re-added.
- Registry outside the checkout with a relative `repo_path`, run from a
  different repository: refused, nothing written.
- Linked worktree: files land in the worktree, the exclude entry lands in the
  common git directory, the notice is printed, nothing else is written outside
  the target root.
- Preservation of unrelated keys, hooks, and TOML content.
- Unsupported agent, missing assets, not a git repository.
- `git status --porcelain` is byte-identical before and after install, in a
  clean repo and in a repo that already has an untracked `.mcp.json` and
  `.claude/settings.local.json` of the developer's own.
- Uninstall: install then uninstall leaves the work tree and `info/exclude`
  byte-identical to before; pre-existing merged files keep their other
  content; modified items are skipped without `--force`; uninstalling one of
  two agents leaves the other fully working.
- Doctor against an in-process MCP server: healthy, wrong URL, connection
  refused, tool-name mismatch.

## Documentation to update

- `docs/AGENT_SETUP.md`: replace the manual steps and the `mcp-remote`
  instructions with the commands; keep manual steps only for other agents.
- `docs/operations.md`: the new subcommands and flags.
- `docs/tasks.md`: move the task to Done when shipped.
- `agent/claude/mcp-config.json`, `agent/codex/config.toml.snippet`: align
  with the direct-HTTP form, or remove if install generates them entirely.
- `agent/codex/AGENTS.md.snippet`: rewrite once, as a static file, so it can be
  printed verbatim. Remove the hardcoded URL, the `agent/skills/...` paths that
  do not exist in a target repo, the claim that Codex does not load skills, and
  the hand-maintained tool list; refer to the two installed skills by name.
- `agent/claude/CLAUDE.md.snippet`: remove the instruction to merge hooks into
  `.claude/settings.json`.

## Left to the builder

- Package name and internal layout.
- Manifest schema details and the canonical serialization used for hashing.
- Whether to parse TOML with a library or manage a marked block.
- Exact wording of output lines and next actions.
- How to detect Claude approval and Codex trust state, within check 7's rule
  of printing the manual check when detection is unreliable.

## Calls made by the author, not the maintainer

- The untracked manifest at `.projectlens/agent-manifest.json`.
- `--force` for modified and unmanaged items.
- Dropping `mcp-remote` in favor of direct HTTP entries.
- Not installing the prompt snippets; printing the Codex one.
- Doctor reading, never writing, home-directory config.
- Baking the checkout path via ldflags, with `--assets` as the override.
- Exit codes.
- Inferring the slug from the registry when `--project` is omitted, and
  refusing the legacy URL when a registry exists but nothing matches.
- Recording the URL in the manifest as the reference for `status` and `doctor`.
- Registry fallback to the baked checkout path, and resolving relative
  `repo_path` against the checkout root.
- No-argument `status` and `doctor` covering only installed agents.
- Detect-and-report for old manual wiring; no automatic migration.
- Removing unmodified orphaned items on re-install.
- Rewriting the Codex snippet as a static file.
- Uninstall details: deleting only merged files that install created, and
  exit 0 when nothing is installed.
- Hook IDs carried in a leading shell no-op, and not re-adding a hook whose
  marker was removed.
- Refusing a relative `repo_path` when the registry is outside the checkout.
- Tracked files with matching content counting as current.
