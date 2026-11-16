# Agent Setup

How to wire ProjectLens into your AI coding assistant. This is the **end-user
guide** — you've already followed the [Quick Start](../README.md#quick-start)
and your MCP server is running on `http://localhost:8484` (path depends on
single- vs. multi-project mode — see [Connect Your Agent](#connect-your-agent)).

If you're here to **contribute** to ProjectLens itself, see
[`CLAUDE.md`](../CLAUDE.md) instead. If you need the system map before
wiring an agent, see [`architecture.md`](architecture.md). For daily
server, CLI, TUI, Docker, and troubleshooting commands, see
[`operations.md`](operations.md).

---

## Table of Contents

- [Connect Your Agent](#connect-your-agent)
- [Install the Skills](#install-the-skills)
- [Install the Hooks](#install-the-hooks)
- [Check, Repair, and Uninstall](#check-repair-and-uninstall)
- [Limitations](#limitations)
- [How It All Fits Together](#how-it-all-fits-together)
- [Verifying It Works](#verifying-it-works)
- [Troubleshooting](#troubleshooting)

---

## Connect Your Agent

ProjectLens works with any agent that supports the [Model Context
Protocol](https://modelcontextprotocol.io). The MCP URL depends on whether
you've set up a project registry:

| Mode | URL shape |
|---|---|
| Multi-project (`configs/projects.yaml` present) | `http://localhost:8484/<slug>/mcp` — one endpoint per registered project. |
| Legacy single-project (no registry) | `http://localhost:8484/mcp`. |

See [`docs/operations.md#projects`](operations.md#projects) for the
registry format and per-project commands.

`lens agent install` wires Claude Code or Codex into one repository for you
alone. Run it from inside the target repository (or pass `--project <slug>`
to pick a registered project). It needs a built `lens` binary; see
[`operations.md`](operations.md) for the Make targets.

The URL it writes comes from, in order: `--url`; `--project <slug>` with
`--port` (or `$PROJECTLENS_MCP_PORT`, default 8484); the project in
`configs/projects.yaml` whose `repo_path` is this repository; otherwise the
legacy `http://localhost:8484/mcp`. In multi-project mode, if no registered
project matches the repository, install stops and asks for `--project` or
`--url`.

### What install writes

Install is personal: nothing it writes shows up in `git status`.

| Agent | MCP entry | Skills | Hooks |
|---|---|---|---|
| Claude Code | `.mcp.json` (`mcpServers.projectlens`) | `.claude/skills/` | `.claude/settings.local.json` |
| Codex | `.codex/config.toml` (`mcp_servers.projectlens`) | `.agents/skills/` | none (Codex has no hooks) |

It also records what it owns in `.projectlens/agent-manifest.json`.

Example (Claude Code):

```text
$ lens agent install claude
  written   .mcp.json                          (mcpServers.projectlens)
  written   .claude/skills/capture-knowledge/
  written   .claude/skills/use-projectlens/
  written   .claude/settings.local.json        (4 hooks)
  written   .git/info/exclude                  (5 paths)
```

Add `--dry-run` to see what would change without writing anything. Re-running
install is safe: items that are already current are left alone and stale
ones are updated.

### Keeping the wiring out of git

- Every path install creates is listed in a marked block in your
  repository's `.git/info/exclude`, never in `.gitignore`. That file is
  local to your clone, so teammates see nothing.
- **Do not hand-edit the marked block.** It belongs to lens; lines you add
  inside it are dropped on the next run. Put your own excludes outside the
  block.
- A file git already tracks is never written. Install prints the entry for
  you to add by hand and reports the item as `manual`.
- If you already had an untracked `.mcp.json`, `.claude/settings.local.json`,
  or `.codex/config.toml`, install edits it in place. It keeps its own
  visibility in `git status` and all its other content. JSON files are
  re-serialized (2-space indent, sorted keys), so expect a formatting diff.
- A hand-written `mcp_servers.projectlens` definition in
  `.codex/config.toml` outside the managed block is never rewritten: the same
  URL counts as `current`, a different URL is `blocked`.
- Content you edited is never overwritten or removed without `--force`.
  `--force` still never writes a tracked file and never writes through a
  symlink.

### Linked worktrees

All worktrees of a repository share one `.git/info/exclude`. Each worktree
gets its own marked block, and install only wires the worktree you run it in.
Run `lens agent install` in every worktree you use.

### Claude Code

```bash
lens agent install claude
```

Then restart Claude Code in the repo, run `/mcp`, and approve the
`projectlens` server (Claude Code asks before using a project-scoped
server). Finish with `lens agent doctor claude`.

[`agent/claude/CLAUDE.md.snippet`](../agent/claude/CLAUDE.md.snippet) is
optional always-on prompt text you can paste into your `CLAUDE.md`. Install
does not write it.

To wire several projects, run install in each repository; each gets the URL
of its own registered project.

### Cursor

Cursor is not covered by `lens agent`. Settings → MCP → **Add new MCP
server**. Fill in:

- **Name:** `projectlens` (or `projectlens-<slug>` when running multiple projects)
- **Type:** `streamable-http`
- **URL:** `http://localhost:8484/<slug>/mcp` (multi-project) or
  `http://localhost:8484/mcp` (legacy single-project mode)

Cursor will list the ProjectLens tools the next time you open the agent
panel.

### Codex

```bash
lens agent install codex
```

Then open Codex in the repo and **trust the project**: Codex ignores
`.codex/config.toml` in projects it does not trust. Finish with
`lens agent doctor codex`.

Codex has no hooks, so install prints an AGENTS.md snippet at the end. Paste
it into your repo's `AGENTS.md` for always-on guidance (the source is
[`agent/codex/AGENTS.md.snippet`](../agent/codex/AGENTS.md.snippet)). The
snippet tells Codex to pass `source: "codex"` to `save_knowledge` so the
audit trail distinguishes agents.

### Other MCP-compatible agents

These are manual steps; `lens agent` does not cover them. Any client that
supports Streamable HTTP MCP works (Cline, Continue, Windsurf, Zed, custom
agents built on the Anthropic / OpenAI SDKs, ...). Point it at
`http://localhost:8484/<slug>/mcp` (multi-project mode) or
`http://localhost:8484/mcp` (legacy single-project mode) — that one URL is
everything the agent needs. The agent loop can call the ProjectLens tools
like any function-calling tool.

---

## Install the Skills

Skills tell the agent **when** and **how** to use ProjectLens. Without them,
the agent has the tools but might not reach for them. Two skills ship with
ProjectLens:

| Skill | What it does |
|---|---|
| `use-projectlens` | **Mandatory.** Forces the agent to call ProjectLens BEFORE Read/Grep/Glob for any question about code structure, behavior, history, data flow, or impact. Bundles workflows for trace, debug-test, change-impact, and data-flow questions. |
| `capture-knowledge` | Detects durable lessons / conventions / decisions during a session and persists them via `save_knowledge`, so future sessions can search them. |

### Install

`lens agent install claude` or `lens agent install codex` installs both
skills (see [Connect Your Agent](#connect-your-agent) for where they go).
There are no manual copy or symlink steps.

### `use-projectlens` at a glance

The skill encodes one rule and four workflows:

**The rule.** Before opening files, grepping, or listing directories to
answer a question about what the code does, where it lives, who calls it,
what it touches in the database, how it has changed, or what depends on it
— call ProjectLens first.

**The four workflows:**

| Question type | Tools, in order |
|---|---|
| "Where is X implemented?" / "How does Y work?" | `find_symbol` → `get_symbol_context` → `get_package_summary` |
| "Why is this test failing?" | `search_go_context` → `get_symbol_context` → read test + production file |
| "What breaks if I change X?" | `find_symbol` → `get_symbol_context` → `get_coupling` → `get_package_summary` (per affected pkg) |
| "Which code reads/writes table T?" | `get_table_context` → `get_symbol_context` on top writer |

Full details in [`agent/skills/use-projectlens/SKILL.md`](../agent/skills/use-projectlens/SKILL.md).

### `capture-knowledge` at a glance

The skill defines **9 trigger signals** that mean "this is worth
remembering" — user corrections, revealed rules, domain-term clarifications,
non-obvious root causes, design decisions, and so on. When a signal fires,
the agent calls `save_knowledge` with a category, title, body (rule + Why +
How to apply), and an anchor (symbol / package / file / table / none).

Saved entries flow through the normal embedding pipeline. The next time
someone asks about the same symbol or package, the entry surfaces
automatically inside `get_symbol_context`, `get_package_summary`, and
`search_go_context` — no extra call needed.

Full details in [`agent/skills/capture-knowledge/SKILL.md`](../agent/skills/capture-knowledge/SKILL.md).

---

## Install the Hooks

Hooks are shell commands the agent harness runs automatically at specific
moments. They turn the skills' guidance into **enforced** behavior. Four
hooks ship in [`agent/claude/settings-snippet.json`](../agent/claude/settings-snippet.json):

| Event | Matcher | What it does |
|---|---|---|
| `SessionStart` | (any) | Reminds the agent to call `index_status` before the first high-impact task this session (change-impact, refactor, data-flow audit, migration planning, deep debug). Quick lookups skip. |
| `PreToolUse` | `Edit \| Write \| MultiEdit` | Reminds the agent: if this edit touches an exported symbol, interface, or migration, you must have called `get_symbol_context` / `get_table_context` / `get_coupling` first. STOP and run them if you skipped. |
| `Stop` | (any) | Scans the turn for capture-worthy lessons (the 9 signals). `search_knowledge` first to dedup, then `save_knowledge` (with `source` set) if no matching entry exists. Otherwise stops silently. |
| `Stop` | (any) | `use-projectlens` compliance audit. Flags turns that answered a structural question via Read/Grep/Glob without a prior ProjectLens call. Silent when compliance is clean. |

The hooks are **soft nudges** — they inject `<system-reminder>` blocks the
agent reads on its next step. They don't block tool calls. This is
deliberate: the agent self-corrects without breaking legitimate edits to
private code.

### Install

`lens agent install claude` merges the four hooks into
`.claude/settings.local.json`, keeping any other settings in that file.
Codex has no hooks. Each hook carries an ID prefix so lens can find, update,
and remove its own hooks without touching yours.

---

## Check, Repair, and Uninstall

### Status

```bash
lens agent status [claude|codex]   # add --json for scripts
```

`status` compares what is on disk with what install would write, and
changes nothing. With no argument it covers every agent you installed.

| State | Meaning | What to do |
|---|---|---|
| `current` | Matches what install would write. | Nothing. |
| `missing` | Not on disk. | `lens agent install <agent>`. |
| `stale` | Lens wrote it, and the canonical asset has since changed. | `lens agent install <agent>` updates it. |
| `modified` | You edited content lens wrote. | Keep it, or `install --force` to overwrite. |
| `unmanaged` | Something is at that path that lens did not write (including a symlinked skill directory). | `install --force` to take it over (a symlink is replaced by a copy), or leave it. |
| `manual` | The file is tracked by git, so lens will not write it. | Add the entry install printed, by hand. |
| `orphaned` | Lens recorded it, but it is no longer in the canonical assets (for example a renamed skill). | `lens agent install <agent>` removes it if you did not change it; `--force` otherwise. |
| `removed-locally` | You deleted a hook lens installed. | Install leaves it removed; `install --force` restores it. |
| `blocked` | A symlink, a foreign definition, or a scratch directory is in the way, and lens will not write through it. | Move the obstacle, then re-run install. |

### Exit codes

| Command | Exit |
|---|---|
| `status` | 0 when everything is `current`; 1 when anything is not, and also 1 when nothing is installed. |
| `doctor` | 0 when no check failed; 1 when a check failed, and also 1 when nothing is installed. |
| any | 2 for a usage error (unknown subcommand, bad flag, wrong arguments). |
| `install`, `uninstall` | 0, even when some items were skipped, blocked, or need a manual step. Run `lens agent status` to check the result. |

### Doctor

```bash
lens agent doctor [claude|codex]   # add --json, or --url to check another URL
```

`doctor` checks, in order: the canonical assets are found, the installed
items are current, the server answers at the configured URL, the server's
tool list matches, the installed URL matches the expected one, no
user-level config shadows the project entry, no ProjectLens hook is also
in `.claude/settings.json` (Claude Code only: the old manual setup put them
there, and they would fire twice; doctor names them and you delete them),
the agent will load the entry
(Claude Code approval, Codex trust), and `lens` is on your `PATH`. A
failure prints one next action. A check that cannot be read reliably (the
approval and trust steps) prints a manual instruction.

### Uninstall

```bash
lens agent uninstall <claude|codex|all> [--dry-run] [--force]
```

Uninstall removes only what the manifest says install created or edited:
skills, the lens hooks and MCP entry, and the exclude entries. Your other
settings in shared files stay. Anything you changed is kept unless you pass
`--force`, which still never touches a tracked file or a symlink. A config
file that install created and that is empty after removal is deleted.

---

## Limitations

- If `.projectlens/agent-manifest.json` is deleted, the next install
  recovers ownership from the marked block in `.git/info/exclude`. Uninstall
  after that may leave empty parent directories (`.claude/`, `.agents/`,
  `.codex/`).
- If you commit a file install created, the manifest and exclude entry stay
  until you delete that file.
- If a directory of yours is named `.<skill>.lens-tmp` or
  `.<skill>.lens-old` beside an installed skill, that skill is reported
  `blocked` until you move it.
- A config file that install created and you leave empty is deleted on
  uninstall.
- A hard crash while writing a config file can leave a `.tmp-*` file that
  shows in `git status`. Delete it and re-run install.

---

## How It All Fits Together

This section covers agent-side wiring. The full runtime architecture lives in
[`architecture.md`](architecture.md).

```
┌────────────────────────────────────────────────────────────┐
│                       Your IDE / Agent                     │
│                                                            │
│  ┌──────────────┐    ┌─────────────┐    ┌──────────────┐   │
│  │   Skills     │    │   Hooks     │    │  MCP Tools   │   │
│  │              │    │             │    │              │   │
│  │ use-         │    │ PreToolUse  │    │ find_symbol  │   │
│  │  projectlens │    │   on Edit   │    │ search_go_…  │   │
│  │ capture-     │    │ Stop x2     │    │ get_…        │   │
│  │   knowledge  │    │             │    │ save_…       │   │
│  └──────┬───────┘    └──────┬──────┘    └──────┬───────┘   │
│         │                   │                   │           │
│         │ guide             │ enforce           │ execute   │
│         └─────────┬─────────┴───────┬───────────┘           │
│                   │                 │                       │
│            ┌──────┴────────┐   ┌────┴──────┐                │
│            │ "Use          │   │ MCP call  │                │
│            │ ProjectLens   │   │ over HTTP │                │
│            └───────────────┘   └────┬──────┘                │
└─────────────────────────────────────┼───────────────────────┘
                                      │
                          ┌───────────▼────────────┐
                          │   ProjectLens server    │
                          │   localhost:8484/mcp   │
                          └────────────────────────┘
```

**Skills** are documentation the agent reads. **Hooks** are runtime nudges
the harness injects. **MCP tools** are the actual function-calling
surface. All three layers point the agent at ProjectLens; remove any one
and the system still works, just less reliably.

---

## Verifying It Works

After `lens agent install` and `lens agent doctor`, test the loop end to
end:

1. **MCP connectivity.** Ask the agent: *"Call `index_status` and show me
   the result."* You should see a freshness summary with timestamps per
   stage.
2. **Skill activation.** Ask: *"Where is supplier funding approval
   implemented?"* The agent should call `find_symbol` (or
   `search_go_context`) first, **before** any Read/Grep. If it greps
   first, the skill isn't loaded — run `lens agent status`.
3. **Hook activation.** Ask the agent to make a small edit to an exported
   Go function. Before the edit, the PreToolUse hook should trigger a
   `<system-reminder>` reminding the agent about impact-checking. (You
   won't see the reminder text directly — you'll see the agent reach for
   `get_symbol_context` instead of editing immediately.)
4. **Capture loop.** Have a session where you correct the agent on a
   convention (*"we don't import internal/foo from service/, please use
   the adapter"*). When the conversation ends, the agent should call
   `save_knowledge` with a `convention`-category entry.

---

## Troubleshooting

This section focuses on agent setup. General operational failures such as
database connection errors, provider failures, stale index state, writer-lock
busy errors, and missing TUI binaries are covered in
[`operations.md#troubleshooting`](operations.md#troubleshooting).

### The agent isn't using ProjectLens

- Run `lens agent doctor`. It checks the install, the server URL, the tool
  list, and the approval or trust step, and prints one next action per
  failure.
- To check by hand, confirm the MCP server is reachable:
  `curl http://localhost:8484/<slug>/mcp` (multi-project) or
  `curl http://localhost:8484/mcp` (legacy single-project)
  should respond (probably with a 405 or upgrade hint — that's fine, it
  means the port is open and the server is alive). HTTP 503 from a
  multi-project URL means the schema has not been migrated yet — run
  `lens migrate --project <slug>`. HTTP 404 means the slug is not
  registered in `configs/projects.yaml`.
- Confirm the agent sees the tools. In Claude Code, run `/mcp` and look
  for `projectlens` with its tools. If it is listed as pending, approve it.
  For Codex, check that the project is trusted.
- Confirm the skill is installed: `lens agent status`.
- Some agents need a session restart after adding MCP servers.

### Results look stale

- Call `index_status`. If `last_run` is days old, run `make reindex` (or
  `make index-all` for a full rebuild) in the ProjectLens directory.
- The default history window is 12 months — older commits are excluded by
  design. Edit `configs/index.yaml` (`history.window_months`) to widen it.

### Writer lock is stuck

If a `make reindex` or other mutating command exits with code 75 and
"another writer holds the lock":

```bash
# Auto-recovery normally handles crashed holders. If it doesn't:
go run ./cmd/lens/ unlock --force
```

`unlock --force` kills the holder's DB session, which auto-releases the
advisory lock, then deletes the bookkeeping row. Use only when
auto-recovery has failed (e.g. a recycled client PID makes the row look
live).

### Hooks aren't firing

- Run `lens agent doctor claude`, then `lens agent status claude`. A hook
  reported `modified` or `removed-locally` is not what lens installed.
- Confirm `.claude/settings.local.json` parses: `python3 -m json.tool .claude/settings.local.json`.
- For Claude Code: run `/hooks` and check the PreToolUse and Stop entries
  are listed.
- The `<system-reminder>` blocks are intentionally invisible to the user
  — you'll only see the *effect* (agent course-correcting). To verify
  the hook fires at all, temporarily change `echo` to `echo "$(date) HOOK
  FIRED" >> /tmp/projectlens-hook.log` in the hook command (this makes the
  hook `modified`; `lens agent install claude --force` restores it).
