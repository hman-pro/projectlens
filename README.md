# ProjectLens

**A local memory and search layer for Go codebases that AI coding assistants can share.**

ProjectLens indexes your code, database shape, git history, summaries, embeddings, and captured project knowledge into Postgres, then serves it through [MCP](https://modelcontextprotocol.io). Claude, Codex, Cursor, and other MCP-capable agents can ask grounded questions about the repo instead of re-reading files and rebuilding context every session.

Your source stays on your machine. Agents talk to the ProjectLens MCP server, and the server answers from a local index.

## Start Here

| Need | Read |
|---|---|
| Set it up quickly | [Quick Start](#quick-start) |
| Wire an agent into a target repo | [`docs/AGENT_SETUP.md`](docs/AGENT_SETUP.md) |
| Understand the architecture | [`docs/architecture.md`](docs/architecture.md) |
| Run CLI, TUI, Docker, reports, or troubleshooting | [`docs/operations.md`](docs/operations.md) |
| Work on ProjectLens internals | [`CLAUDE.md`](CLAUDE.md), then [`docs/internals.md`](docs/internals.md) |

## Why It Exists

AI assistants forget. Every new chat starts cold, so the agent has to grep, re-open files, infer call paths, and guess which conventions matter. On a large monorepo, that is slow, expensive, and easy to get wrong.

ProjectLens does the reading once and keeps the result queryable:

- Find symbols, packages, callers, callees, and interface implementations.
- Trace which Go code reads or writes a database table.
- Search code semantically when the exact symbol name is unknown.
- Check index freshness before trusting an answer.
- Look up recent git history and files that tend to change together.
- Capture durable lessons during a session and surface them later.

## What It Can Answer

Examples an agent can answer through ProjectLens:

- *"Where is supplier funding approval implemented?"*
- *"Which Go code writes to the `supplier_funding` table?"*
- *"What calls `ReserveInventory`, and what does it call?"*
- *"What changed in the supplier onboarding package recently?"*
- *"If I edit this file, what historically changes with it?"*
- *"What lessons have we saved about this package?"*

The MCP server exposes 10 tools for these workflows. The authoritative tool list and the CLI/TUI command guide live in [`docs/operations.md`](docs/operations.md).

## What Gets Indexed

| Layer | Indexed State | Used For |
|---|---|---|
| Code | Go files, packages, symbols, signatures, docs, chunks, call/interface graph | Symbol lookup, dependency tracing, semantic code search |
| Datastore | PostgreSQL schemas, migration-derived tables, SQL references in Go code | Table context and read/write flow |
| History | Git commits per file/symbol, co-change coupling | Recent-change context and impact hints |
| Knowledge | Durable lessons, conventions, decisions, how-tos, anchored to code/data where possible | Agent memory that survives sessions |
| Embeddings and summaries | Vectorized chunks plus package/file summaries | Natural-language retrieval and high-level package context |

Confluence/Jira-style document ingestion is represented in the storage model and planned as future runtime behavior; captured knowledge is the current docs/knowledge path.

## Quick Start

The normal flow is: start Postgres, index a target Go repo, start the MCP server, then connect your agent.

### Prerequisites

- Go 1.26+
- Docker + Docker Compose
- PostgreSQL 16 with [pgvector](https://github.com/pgvector/pgvector) extension, provided by the included Docker Compose setup
- `ANTHROPIC_API_KEY` for the default summarizer
- Optional `OPENAI_API_KEY` only if `configs/index.yaml` uses OpenAI for embeddings or summarization

### 1. Start Postgres

```bash
cp .env.example .env
# Edit .env and set ANTHROPIC_API_KEY.

cd docker && docker compose up -d
```

This starts Postgres with pgvector on host port `5433`.

### 2. Index A Repo

```bash
export $(grep -v '^#' .env | xargs)
make index-all REPO=/path/to/your/go/monorepo
```

The first run parses Go code, scans datastore references, walks git history, summarizes packages, and embeds missing chunks. Incremental runs are much faster.

### 3. Start The MCP Server

```bash
make build-mcp
./bin/projectlens-mcp
```

Default endpoint:

```text
http://localhost:8484/mcp
```

### 4. Connect An Agent

Use [`docs/AGENT_SETUP.md`](docs/AGENT_SETUP.md) for Claude Code, Cursor, Codex, and other MCP clients.

A quick wiring check:

> Use projectlens to find where supplier funding approval is implemented.

The agent should call `find_symbol` or `search_go_context` and return indexed results.

## Daily Use

Most operator commands are exposed through the Makefile:

```bash
make help
make status
make reindex REPO=/path/to/repo
make tui
make mcp
make cli ARGS="report --top 10"
```

For raw CLI commands, TUI keys, Docker targets, report/export usage, MCP tools, migrations, writer-lock behavior, and troubleshooting, see [`docs/operations.md`](docs/operations.md).

## Documentation

| Doc | Purpose |
|---|---|
| [`README.md`](README.md) | Product entrypoint and quick start |
| [`docs/architecture.md`](docs/architecture.md) | Runtime components, data layers, and system diagrams |
| [`docs/operations.md`](docs/operations.md) | Commands, TUI, MCP, Docker, migrations, and troubleshooting |
| [`docs/internals.md`](docs/internals.md) | Indexing pipeline, storage model, MCP query flow, and TUI job execution |
| [`docs/AGENT_SETUP.md`](docs/AGENT_SETUP.md) | Per-agent MCP config, skills, hooks, and verification |
| [`CLAUDE.md`](CLAUDE.md) | Maintainer conventions and source-of-truth rules |
| [`docs/plans/`](docs/plans/) | Design and implementation history |

## License

Private — internal tool.
