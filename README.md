# ProjectLens

**Local-first codebase intelligence for AI coding agents.**

ProjectLens indexes your Go code, database schemas, git history, package summaries, embeddings, and captured project knowledge into Postgres, then serves it over [MCP](https://modelcontextprotocol.io). Claude, Codex, Cursor, and any other MCP-capable agent can ask grounded questions about the repo instead of re-reading files and rebuilding context every session.

Source stays on your machine. Agents talk to the ProjectLens MCP server, and the server answers from a local index. Public alpha ships no remote provider integrations — embeddings run on a local Ollama endpoint and package summarization is disabled by default.

## Install

| Binary | Install |
|---|---|
| CLI | `go install github.com/hman-pro/projectlens/cmd/projectlens@latest` |
| MCP server | `go install github.com/hman-pro/projectlens/cmd/projectlens-mcp@latest` |
| TUI | `go install github.com/hman-pro/projectlens/cmd/projectlens-tui@latest` |

The quick start below uses a local clone plus `make build` to get all three binaries consistently.

## Quick start

Requires Ollama, Postgres-capable Docker, and Go 1.26+.

```bash
ollama pull qwen3-embedding:0.6b
cp .env.example .env
cd docker && docker compose up -d
cd ..
make migrate
make index-all REPO=.
make build-projectlens-mcp
./bin/projectlens-mcp
```

Summarization is disabled by default. To enable it, edit `configs/index.yaml`:

```yaml
summarization:
  enabled: true
  provider: ollama
  model: qwen3-coder:30b
  endpoint: http://localhost:11434
```

This downloads ~19 GB and requires a strong machine.

## Start Here

| Need | Read |
|---|---|
| Set it up quickly | [Quick start](#quick-start) |
| Wire an agent into a target repo | [`docs/AGENT_SETUP.md`](docs/AGENT_SETUP.md) |
| Understand the architecture | [`docs/architecture.md`](docs/architecture.md) |
| Run CLI, TUI, Docker, reports, or troubleshooting | [`docs/operations.md`](docs/operations.md) |
| Work on ProjectLens internals | [`CLAUDE.md`](CLAUDE.md), then [`docs/internals.md`](docs/internals.md) |

## Why it exists

AI assistants forget. Every new chat starts cold, so the agent has to grep, re-open files, infer call paths, and guess which conventions matter. On a large monorepo, that is slow, expensive, and easy to get wrong.

ProjectLens does the reading once and keeps the result queryable:

- Find symbols, packages, callers, callees, and interface implementations.
- Trace which Go code reads or writes a database table.
- Search code semantically when the exact symbol name is unknown.
- Check index freshness before trusting an answer.
- Look up recent git history and files that tend to change together.
- Capture durable lessons during a session and surface them later.

## Status

Public alpha. APIs and on-disk schemas may break between tagged releases.
