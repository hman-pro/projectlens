# Agent Setup

How to wire ProjectLens into your AI coding assistant. This is the **end-user
guide** — you've already followed the [Quick Start](../README.md#quick-start)
and your MCP server is running at `http://localhost:8484/mcp`.

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
- [How It All Fits Together](#how-it-all-fits-together)
- [Verifying It Works](#verifying-it-works)
- [Troubleshooting](#troubleshooting)

---

## Connect Your Agent

ProjectLens works with any agent that supports the [Model Context
Protocol](https://modelcontextprotocol.io). The exact configuration step
depends on which agent you use, but the URL is always the same:
**`http://localhost:8484/mcp`** (Streamable HTTP transport).

### Claude Code

Add to your Claude Code MCP configuration (typically `~/.claude.json` or per-
project `.claude/mcp.json`):

```json
{
  "mcpServers": {
    "projectlens": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "http://localhost:8484/mcp"]
    }
  }
}
```

A ready-to-copy version lives at [`agent/claude/mcp-config.json`](../agent/claude/mcp-config.json).

### Cursor

Settings → MCP → **Add new MCP server**. Fill in:

- **Name:** `projectlens`
- **Type:** `streamable-http`
- **URL:** `http://localhost:8484/mcp`

Cursor will list the 10 ProjectLens tools the next time you open the agent
panel.

### Codex

Codex spawns MCP servers as stdio subprocesses, so wrap the Streamable
HTTP endpoint with `mcp-remote` (same bridge Claude Code uses
implicitly).

1. Install the bridge once: `npm i -g mcp-remote` — or rely on `npx -y`
   in the config and skip the global install.
2. Add to `~/.codex/config.toml` (see
   [`agent/codex/config.toml.snippet`](../agent/codex/config.toml.snippet)
   in this repo):

   ```toml
   [mcp_servers.projectlens]
   command = "npx"
   args = ["-y", "mcp-remote", "http://localhost:8484/mcp"]
   ```

3. Restart Codex. The 10 ProjectLens tools appear in the tool list.

Codex does **not** auto-load `.claude/skills/*.md` the way Claude Code
does. Instead, paste the contents of
[`agent/codex/AGENTS.md.snippet`](../agent/codex/AGENTS.md.snippet)
into your target repo's `AGENTS.md` (or the Codex system-prompt
override). It compresses the mandatory rule, tool picker, and
knowledge-capture instructions into the prompt body so Codex actually
reaches for the tools. The canonical skill bodies live at
[`agent/skills/use-projectlens/SKILL.md`](../agent/skills/use-projectlens/SKILL.md)
and
[`agent/skills/capture-knowledge/SKILL.md`](../agent/skills/capture-knowledge/SKILL.md);
read them directly when you need the longer playbook (workflows,
anti-patterns, structured-content fields, writer-lock awareness).

When Codex calls `save_knowledge`, pass `source: "codex"` so the
audit trail distinguishes agents.

### Other MCP-compatible agents

Any client that supports Streamable HTTP MCP works (Cline, Continue,
Windsurf, Zed, custom agents built on the Anthropic / OpenAI SDKs, ...).
Point it at `http://localhost:8484/mcp` — that one URL is everything the
agent needs. The server exposes 10 tools the agent loop can call like any
function-calling tool.

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

In your target repo:

```bash
# Symlink the skills so they auto-update with ProjectLens
mkdir -p .claude/skills
ln -s /path/to/projectlens/agent/skills/use-projectlens    .claude/skills/use-projectlens
ln -s /path/to/projectlens/agent/skills/capture-knowledge .claude/skills/capture-knowledge
```

(Or copy the directories instead of symlinking if your agent doesn't follow
symlinks — copies need manual re-sync when ProjectLens updates the skills.)

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

Merge the snippet into your repo's `.claude/settings.json`. If the file
doesn't exist yet, just copy it:

```bash
cp /path/to/projectlens/agent/claude/settings-snippet.json .claude/settings.json
```

If the file already has hooks, merge the `hooks` keys by hand — JSON does
not deep-merge automatically.

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
│  │ use-repo-    │    │ PreToolUse  │    │ find_symbol  │   │
│  │   intel      │    │   on Edit   │    │ search_go_…  │   │
│  │ capture-     │    │ Stop x2     │    │ get_…        │   │
│  │   knowledge  │    │             │    │ save_…       │   │
│  └──────┬───────┘    └──────┬──────┘    └──────┬───────┘   │
│         │                   │                   │           │
│         │ guide             │ enforce           │ execute   │
│         └─────────┬─────────┴───────┬───────────┘           │
│                   │                 │                       │
│            ┌──────┴────────┐   ┌────┴──────┐                │
│            │ "Use Repo     │   │ MCP call  │                │
│            │  Intel first" │   │ over HTTP │                │
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

After connecting the agent and installing skills + hooks, test the loop end
to end:

1. **MCP connectivity.** Ask the agent: *"Call `index_status` and show me
   the result."* You should see a freshness summary with timestamps per
   stage.
2. **Skill activation.** Ask: *"Where is supplier funding approval
   implemented?"* The agent should call `find_symbol` (or
   `search_go_context`) first, **before** any Read/Grep. If it greps
   first, the skill isn't loaded — check `.claude/skills/use-projectlens/`
   exists.
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

- Confirm the MCP server is reachable: `curl http://localhost:8484/mcp`
  should respond (probably with a 405 or upgrade hint — that's fine, it
  means the port is open and the server is alive).
- Confirm the agent sees the tools. In Claude Code, run `/mcp` and look
  for `projectlens` with 10 tools.
- Confirm the skill is installed: `ls .claude/skills/use-projectlens/SKILL.md`.
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
go run ./cmd/projectlens/ unlock --force
```

`unlock --force` kills the holder's DB session, which auto-releases the
advisory lock, then deletes the bookkeeping row. Use only when
auto-recovery has failed (e.g. a recycled client PID makes the row look
live).

### Hooks aren't firing

- Confirm `.claude/settings.json` parses: `python3 -m json.tool .claude/settings.json`.
- For Claude Code: run `/hooks` and check the PreToolUse and Stop entries
  are listed.
- The `<system-reminder>` blocks are intentionally invisible to the user
  — you'll only see the *effect* (agent course-correcting). To verify
  the hook fires at all, temporarily change `echo` to `echo "$(date) HOOK
  FIRED" >> /tmp/projectlens-hook.log` in the snippet and watch the file.
