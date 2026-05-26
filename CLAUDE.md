# ProjectLens

A codebase intelligence platform that indexes Go code, database schemas,
change history, and business documentation into a unified searchable graph.
It serves any MCP-capable agent (Claude, Cursor, Codex, ...) over
Streamable HTTP MCP.

## Project Overview

ProjectLens indexes a Go monorepo and serves structured context to AI coding
assistants so they do not rediscover architecture on every session. Beyond
code symbols, it tracks data flows, git history, captured knowledge, and
planned business-document context.

**Target repo:** `example-org/ingest` monorepo.

## Documentation Layout

Keep duplication to a minimum. Each topic has one canonical home; other docs
link there.

| File | Audience | Owns |
|---|---|---|
| [`README.md`](README.md) | Non-tech / first-time visitors | What, why, quick start, top-level navigation |
| [`docs/architecture.md`](docs/architecture.md) | First-time maintainers and agents | Runtime components, data layers, component diagrams |
| [`docs/operations.md`](docs/operations.md) | Operators and contributors | Make targets, CLI, TUI, MCP, Docker, migrations, troubleshooting |
| [`docs/internals.md`](docs/internals.md) | Contributors | Indexing pipeline, storage model, MCP query flow, TUI job execution |
| [`docs/AGENT_SETUP.md`](docs/AGENT_SETUP.md) | End users wiring an agent into their repo | Per-agent MCP config, skills install, hooks install |
| `CLAUDE.md` (this file) | Contributors and maintainers | Maintainer conventions, source-of-truth rules, design rationale |
| [`docs/plans/`](docs/plans/) | Maintainers | Design and implementation history |
| [`docs/tasks.md`](docs/tasks.md) | Maintainers | Canonical current task list, priorities, parked work, and next actions |
| `docs/YYYY-MM-DD-*.md` | Maintainers | Dated planning artifacts, comparisons, lessons, and historical priority inputs |

When updating something user-visible, update the owning doc and cross-link
instead of copy-pasting. Examples:

| Changed surface | Owning doc |
|---|---|
| New Make target or CLI subcommand/flag | `docs/operations.md` |
| New MCP tool | `docs/operations.md` and `agent/skills/use-projectlens/SKILL.md` |
| New TUI view, key, action, log, or binary behavior | `docs/operations.md` |
| New schema table or storage relationship | `docs/internals.md` |
| New runtime component or data layer | `docs/architecture.md` |
| New agent setup step | `docs/AGENT_SETUP.md` |
| Maintainer convention or durable repo rule | `CLAUDE.md` |

## Source-Of-Truth Files

Use these before editing docs or behavior:

| Topic | Source |
|---|---|
| Make targets | `Makefile` |
| CLI subcommands and flags | `cmd/projectlens/*.go` |
| Edge provenance defaults | `cmd/projectlens/main.go` and `docs/2026-05-22-confidence-and-provenance-design.md` |
| MCP tools | `internal/mcpserver/tools.go` |
| TUI navigation keys | `internal/tui/app/keys.go` |
| TUI action keys | `internal/tui/jobs/registry.go` |
| Schema | `migrations/*.up.sql` |
| Provider config defaults | `configs/index.yaml` |
| Project registry | `configs/projects.yaml` + `internal/projects/` |
| Agent skills | `agent/skills/*/SKILL.md` |
| Claude/Codex wiring | `agent/claude/`, `agent/codex/` |

## Development Workflow

The Makefile is the supported entrypoint. It builds binaries into `./bin/`
and runs them from there because the TUI shells out to a real `projectlens`
binary.

Daily command details live in [`docs/operations.md`](docs/operations.md).
Common checks:

```bash
make help
make build
make test
make fmt
make vet
```

Integration tests use `//go:build integration`. Live provider tests skip when
their required environment variables are not set.

## Agent Assets

The `agent/` directory is canonical.

| Path | Role |
|---|---|
| `agent/skills/use-projectlens/SKILL.md` | Mandatory MCP-first workflow for code structure, behavior, history, data flow, and impact questions. |
| `agent/skills/capture-knowledge/SKILL.md` | Durable knowledge capture workflow using `save_knowledge`. |
| `agent/claude/` | Claude Code MCP config, hooks, and snippet. |
| `agent/codex/` | Codex MCP config and AGENTS.md snippet. |

Vendor adapters should reference canonical skill bodies instead of copying
them. When changing a skill or hook in a way users need to notice, update
[`docs/AGENT_SETUP.md`](docs/AGENT_SETUP.md).

## Design Decisions

- **Ollama for default embeddings**: local, free, and keeps code on the machine.
- **Claude for default summarization**: quality-first package summaries, with OpenAI fallback.
- **Provider abstraction**: embedding and summarization providers are config-driven and constructor-injected.
- **Polymorphic edge graph**: one table represents calls, implementations, datastore references, coupling, document links, and knowledge anchors.
- **Two-axis edge trust**: every edge carries `provenance` (parser, callgraph, sql_scanner, history, knowledge, docs) and `confidence_class` (extracted, inferred, ambiguous), in addition to the numeric `confidence` score. Both are CHECK-constrained — new producers must extend the CHECK in the same migration. Graphify-style vocabulary; see `docs/2026-05-22-confidence-and-provenance-design.md`.
- **SCIP-style symbol IDs**: debuggable hierarchical text IDs for cross-referencing.
- **Symbol-based chunking**: chunks follow Go symbols instead of arbitrary token windows.
- **halfvec(1024)**: smaller/faster vector index for the current embedding model.
- **CHA call graph**: fast enough for regular indexing with acceptable precision for agent context.
- **Streamable HTTP MCP**: persistent server, no cold start per agent session.
- **Read-only target repo**: indexers read the target repository and write only to ProjectLens storage.
- **TUI launches CLI jobs**: the TUI should not duplicate indexer behavior.

## Code Conventions

- Internal packages only; this repo does not expose a public Go API.
- Use pgx connection pools for database access.
- Keep provider interfaces testable and constructor-injected.
- Keep errors wrapped with context using `fmt.Errorf("context: %w", err)`.
- Avoid global state; pass dependencies explicitly.
- Respect Postgres parameter limits in batch inserts.
- Keep oversized chunks bounded before embedding.
- Treat `internal/mcpserver/tools.go` as the authoritative MCP tool list.
- Treat schema migrations as append-only history unless a migration is still local and intentionally being revised.
- Storage schemas are quoted via `pgx.Identifier{}.Sanitize()` after passing
  `projects.ValidateStorageSchema`. Never splice an unvetted schema name into SQL.
