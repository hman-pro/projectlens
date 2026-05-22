# ProjectLens

**A memory and search layer for your codebase that any AI coding assistant can plug into.**

ProjectLens reads your Go project — the code, the database, the change history, and your docs — and turns it into a single place an AI agent can ask questions about. It speaks [MCP](https://modelcontextprotocol.io) (Model Context Protocol), so it works with **any MCP-compatible agent**: Claude, Cursor, Codex, and more. The agent connects once and gets a shared brain for your repo.

> **New here? Read on.**
> **Understanding the system?** → [`docs/architecture.md`](docs/architecture.md)
> **Running or troubleshooting it?** → [`docs/operations.md`](docs/operations.md)
> **Wiring an agent into your repo?** → [`docs/AGENT_SETUP.md`](docs/AGENT_SETUP.md)
> **Contributing to ProjectLens itself?** → [`CLAUDE.md`](CLAUDE.md)

---

## Why It Exists

AI assistants forget. Every new chat starts from zero — the agent has to re-read files, re-trace call graphs, and re-guess at architecture. On a large monorepo this is slow, expensive, and often wrong.

ProjectLens does the reading once, keeps the result in a database, and lets agents ask focused questions:

- *"Where is supplier funding approval implemented?"*
- *"Which Go code writes to the `supplier_funding` table?"*
- *"What changed in the supplier onboarding package recently, and what tends to change with it?"*
- *"Summarise the `service/supplier` package."*

The agent gets a precise answer in milliseconds instead of grepping through thousands of files.

## What Gets Indexed

Four layers of intelligence, all searchable together:

| Layer | What's Indexed | How It's Queried |
|-------|---------------|-----------------|
| **Code** | Functions, types, methods, call graph, interfaces | Symbol lookup, semantic search, dependency tracing |
| **Datastore** | PostgreSQL schemas, SQL queries in Go code | "What code reads/writes the `supplier_funding` table?" |
| **History** | Git commits per file, co-change coupling | "What changed in supplier code recently?", "What changes with it?" |
| **Docs / knowledge** | Captured lessons today; Confluence/Jira ingestion is planned | Unified search across code and business context |

For the component map, data layers, and indexing/query diagrams, see
[`docs/architecture.md`](docs/architecture.md). For the detailed storage
and pipeline internals, see [`docs/internals.md`](docs/internals.md).

## Quick Start

The setup has two phases: **index your repo once** (slow, but you only do it when things change), then **point your agent at it** (fast, used every conversation).

### Prerequisites

- Go 1.26+
- PostgreSQL 16 with [pgvector](https://github.com/pgvector/pgvector) extension
- Docker + Docker Compose (easiest way to get Postgres running)
- An `ANTHROPIC_API_KEY` for the default summarizer (Claude Sonnet). Embeddings use local Ollama by default and need no key. An `OPENAI_API_KEY` is only required if you switch either provider to OpenAI in `configs/index.yaml`.

### 1. Start the database

```bash
cp .env.example .env
# Edit .env — set ANTHROPIC_API_KEY (OPENAI_API_KEY only needed if you swap providers)

cd docker && docker compose up -d
```

This starts Postgres with pgvector on port 5433.

### 2. Index your repo

```bash
export $(grep -v '^#' .env | xargs)

# One command runs all stages
make index-all REPO=/path/to/your/go/monorepo
```

That's the slow part — it parses every Go file, scans migrations, walks git history, summarizes packages, and embeds everything. For a monorepo this takes a while the first time but only minutes for incremental updates.

### 3. Start the MCP server

```bash
make build-mcp && ./bin/projectlens-mcp
```

The server listens on `http://localhost:8484/mcp`.

### 4. Connect your agent

The URL above is everything any MCP-compatible agent needs. Per-agent config (Claude Code, Cursor, Codex, others) plus optional **skills** and **hooks** that make the agent reliably reach for ProjectLens are documented in [`docs/AGENT_SETUP.md`](docs/AGENT_SETUP.md).

A first-test prompt to confirm wiring:

> *"Use projectlens to find where supplier funding approval is implemented."*

The agent should call `find_symbol` or `search_go_context` and return real results.

## What Your Agent Can Do

The MCP server exposes 10 tools. You don't invoke them directly — the
agent picks the right one based on what you're asking for. The current
tool list, CLI commands, TUI actions, report/export commands, and
troubleshooting guide live in [`docs/operations.md`](docs/operations.md).

## Documentation

| Doc | For |
|---|---|
| [`README.md`](README.md) (this file) | First-time visitors — what, why, and how to get started |
| [`docs/architecture.md`](docs/architecture.md) | First-time maintainers and agents — component map and data layers |
| [`docs/operations.md`](docs/operations.md) | Operators and contributors — commands, TUI, MCP, Docker, troubleshooting |
| [`docs/internals.md`](docs/internals.md) | Contributors — indexing pipeline, storage model, MCP query flow |
| [`docs/AGENT_SETUP.md`](docs/AGENT_SETUP.md) | Users wiring agents into their repo — per-agent config, skills, hooks |
| [`CLAUDE.md`](CLAUDE.md) | Contributors and maintainers — repo-specific conventions and update rules |
| [`docs/plans/`](docs/plans/) | Design and implementation history |

## License

Private — internal tool.
