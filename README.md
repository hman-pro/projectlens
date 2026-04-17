# ProjectLens

A codebase intelligence platform that indexes Go source code, database schemas, change history, and business documentation into a unified searchable graph. Built for [Claude Code](https://claude.ai/code) via MCP.

## What It Does

ProjectLens indexes a Go monorepo and serves structured context to Claude Code so it doesn't need to rediscover architecture on every session. A single query can return code symbols, database tables, git history, and documentation — all ranked by relevance.

**Four intelligence layers:**

| Layer | What's Indexed | How It's Queried |
|-------|---------------|-----------------|
| **Code** | Functions, types, methods, call graph, interfaces | Symbol lookup, semantic search, dependency tracing |
| **Datastore** | PostgreSQL schemas, SQL queries in Go code | "What code reads/writes this table?" |
| **History** | Git commits per file, co-change coupling | "What changed recently?", "What changes together?" |
| **Docs** | Confluence pages, Jira tickets *(planned)* | Unified search across code and business context |

## Architecture

```
                    ┌─────────────┐
                    │ Claude Code │
                    └──────┬──────┘
                           │ MCP (Streamable HTTP)
                    ┌──────┴──────┐
                    │  MCP Server │  8 tools
                    │  (Go/HTTP)  │
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
        ┌─────┴─────┐ ┌───┴───┐ ┌─────┴─────┐
        │  Lexical   │ │Vector │ │   Graph   │
        │  Search    │ │Search │ │ Traversal │
        └─────┬─────┘ └───┬───┘ └─────┬─────┘
              └────────────┼────────────┘
                    ┌──────┴──────┐
                    │  Postgres   │
                    │  + pgvector │  12 tables
                    └─────────────┘
```

**Indexing pipeline (independent stages):**

```
index code       → census + parse + chunk + call graph
index datastore  → parse SQL migrations + scan Go for SQL
index history    → git log + co-change coupling detection
index docs       → Confluence + Jira fetch (planned)
index summarize  → LLM summaries for packages missing one
index embed      → embed all chunks missing embeddings
index all        → run all stages in sequence
```

## Quick Start

### Prerequisites

- Go 1.26+
- PostgreSQL 16 with [pgvector](https://github.com/pgvector/pgvector) extension
- Docker + Docker Compose (for easy Postgres setup)
- API keys (see [Configuration](#configuration))

### 1. Start the Database

```bash
cp .env.example .env
# Edit .env — set OPENAI_API_KEY and ANTHROPIC_API_KEY

cd docker && docker compose up -d
```

This starts Postgres with pgvector on port 5433.

### 2. Bootstrap the Index

```bash
export $(grep -v '^#' .env | xargs)

# Initialize schema and run full index
go run ./cmd/projectlens/ bootstrap \
  --repo /path/to/your/go/monorepo \
  --db "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"
```

### 3. Run Additional Stages

```bash
# Index database schemas and SQL queries
go run ./cmd/projectlens/ index-datastore --repo /path/to/repo --db "..."

# Index git change history and compute coupling
go run ./cmd/projectlens/ index-history --repo /path/to/repo --db "..."

# Embed any chunks missing embeddings
go run ./cmd/projectlens/ index-embed --db "..."

# Or run everything at once
go run ./cmd/projectlens/ index-all --full --repo /path/to/repo --db "..."
```

### 4. Start the MCP Server

```bash
go run ./cmd/projectlens-mcp/
```

The server starts on port 8484 (configurable via `MCP_PORT`).

### 5. Connect Claude Code

Add to your Claude Code MCP configuration:

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

## MCP Tools

| Tool | Purpose |
|------|---------|
| `find_symbol` | Find Go symbol by name (exact/fuzzy match) |
| `search_go_context` | Unified semantic search across code, tables, and docs |
| `get_symbol_context` | Symbol details + callers, callees, implementors |
| `get_package_summary` | LLM-generated package summary + exported symbols |
| `get_table_context` | Database table schema + which Go code reads/writes it |
| `get_change_history` | Recent git commits for a file or symbol |
| `get_coupling` | Co-change partners ranked by coupling strength |
| `index_status` | Index freshness and per-stage statistics |

## CLI Commands

| Command | Purpose |
|---------|---------|
| `census` | Classify repo files (handwritten/test/generated) |
| `bootstrap` | Initialize DB schema + run full index |
| `reindex [--full]` | Incremental or full reindex |
| `status` | Show index health and staleness |
| `inspect-symbol <name>` | Look up a symbol with SCIP ID and graph context |
| `inspect-package <name>` | Show package summary and exported symbols |
| `query <text>` | Search the retrieval pipeline |
| `index-datastore` | Index database schemas and SQL queries |
| `index-history` | Index git history and compute coupling |
| `index-embed` | Embed all chunks missing embeddings |
| `index-summarize` | Generate summaries for packages missing one |
| `index-all [--full]` | Run all indexing stages in sequence |

## Configuration

### Environment Variables

| Variable | Purpose | Required |
|----------|---------|----------|
| `OPENAI_API_KEY` | OpenAI API for embeddings | Yes (default provider) |
| `ANTHROPIC_API_KEY` | Claude API for package summaries | Yes (default provider) |
| `DATABASE_URL` | Postgres connection string | Yes |
| `REPO_PATH` | Path to target repository | Yes |
| `OLLAMA_ENDPOINT` | Ollama server URL (if using local embeddings) | No |
| `MCP_PORT` | MCP server port | No (default: 8484) |

### Provider Configuration

Edit `configs/index.yaml` to choose embedding and summarization providers:

```yaml
embeddings:
  provider: openai              # openai | ollama
  model: text-embedding-3-large
  dimensions: 1024

summarization:
  provider: anthropic           # anthropic | openai
  model: claude-sonnet-4-6
```

**OpenAI** embeddings with `dimensions: 1024` produce high-quality vectors at 1/3 the size of the default 3072-dim. **Claude Sonnet** produces significantly better package summaries than GPT-4o-mini.

To use fully local embeddings (no API calls), switch to Ollama:

```yaml
embeddings:
  provider: ollama
  model: mxbai-embed-large
  dimensions: 1024
  endpoint: http://localhost:11434
```

### Datastore and History Configuration

```yaml
datastore:
  engines:
    - name: postgres
      migration_paths:
        - "db/migrations/*.up.sql"
  sql_scan_paths:
    - "core/**/*.go"
    - "service/**/*.go"

history:
  window_months: 12
  min_commits_per_file: 5
  coupling_min_cochanges: 5
  coupling_exclude_max_files: 20
```

## Database Schema

12 tables in PostgreSQL with pgvector:

| Table | Purpose |
|-------|---------|
| `files` | Indexed source files with checksums |
| `symbols` | Functions, types, methods with SCIP IDs |
| `chunks` | One chunk per symbol/doc, universal across content types |
| `embeddings` | 1024-dim vectors (halfvec) for semantic search |
| `edges` | Polymorphic graph: calls, implements, reads_table, co_changes |
| `summaries` | LLM-generated package summaries |
| `datastore_tables` | Database table schemas from migrations |
| `documents` | External docs (Confluence, Jira) |
| `symbol_history` | Per-symbol change tracking |
| `file_history` | Per-file git commit history |
| `index_runs` | Indexing run history and status |
| `schema_migrations` | Migration version tracking |

The **polymorphic edges table** is the core of the intelligence graph — one table for call graphs, data flow, coupling analysis, and doc links, all traversable with recursive CTEs.

## Design Decisions

- **Polymorphic edge graph in Postgres** — no separate graph DB needed at this scale (~50K edges). Recursive CTEs handle traversal fine.
- **SCIP-style symbol IDs** — hierarchical text IDs (`go . internal/indexer . Indexer.Run()`) for debuggable cross-referencing.
- **Symbol-based chunking** — one chunk per Go symbol, not arbitrary token windows. Each chunk has full context.
- **Config-driven providers** — switch between OpenAI/Anthropic/Ollama without code changes.
- **Independent pipeline stages** — each stage reads from DB and writes to DB. Run any stage independently, in any order.
- **File-level coupling eager, symbol-level lazy** — coupling pairs precomputed from git history, but per-symbol diffs computed on demand via `git log -p`.
- **Co-change coupling detection** — CodeScene methodology: files appearing in the same commit with coupling strength = co_changes / max(changes_a, changes_b).

## Development

### Build and Test

```bash
# Build everything
go build ./...

# Run all tests (176 tests across 17 packages)
go test ./...

# Run a specific package
go test ./internal/datastore/ -v

# Run live API test (requires ANTHROPIC_API_KEY)
go test ./internal/providers/anthropic/ -v -run TestLive
```

### Project Structure

```
projectlens/
  cmd/
    projectlens/              # CLI (12 commands)
    projectlens-mcp/          # MCP server entrypoint
  internal/
    census/                  # File discovery and classification
    classifier/              # Handwritten vs generated vs test detection
    parser/                  # go/packages wrapper, symbol extraction
    graph/                   # Call graph (CHA) and edge construction
    chunks/                  # Symbol-based chunking
    summaries/               # Heuristic file + LLM package summaries
    embeddings/              # Batched embedding pipeline (provider-agnostic)
    indexer/                 # Code indexing orchestrator
    datastore/               # SQL migration parser + Go SQL scanner
    history/                 # Git log parser + coupling detector
    embed/                   # Standalone embed-missing-chunks
    summarize/               # Standalone summarize-missing-packages
    retrieval/               # Lexical, semantic, graph retrieval + router
    rerank/                  # Scoring and ranking
    mcpserver/               # MCP HTTP server (8 tools)
    storage/                 # Postgres client (pgx) for all 12 tables
    providers/
      ollama/                # Local embedding via Ollama
      anthropic/             # Claude summarization
      openai/                # OpenAI embeddings + summaries (fallback)
    config/                  # YAML config with env var overrides
  migrations/                # SQL schema migrations (003)
  configs/                   # index.yaml
  docker/                    # Dockerfile + docker-compose.yml
  docs/plans/                # Design and implementation documents
```

## License

Private — internal tool.
