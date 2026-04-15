# ProjectLens

A local-first, containerized Go code intelligence layer that provides fast, ranked retrieval over symbols, packages, and dependency graphs. Designed for Claude Code via MCP.

## Project overview

ProjectLens indexes a Go monorepo (~4,150 files) and serves structured context to Claude Code so it doesn't need to rediscover architecture on every session.

**Target repo:** `example-org/ingest` monorepo (34 services, 71 utility packages, ~2,878 handwritten Go files)

## Tech stack

- **Language:** Go 1.26
- **Storage:** Postgres 16 + pgvector (halfvec for 3072-dim vectors)
- **Embeddings:** OpenAI `text-embedding-3-large`
- **Summarization:** OpenAI `gpt-4o-mini` (package summaries)
- **Go parsing:** `golang.org/x/tools/go/packages` + `go/callgraph` (CHA)
- **MCP:** `github.com/mark3labs/mcp-go` (Streamable HTTP)
- **CLI:** `github.com/spf13/cobra`

## Repository structure

```
projectlens/
  cmd/
    projectlens/              # CLI entrypoint (7 commands)
    projectlens-mcp/          # MCP server entrypoint
  internal/
    census/                  # file discovery and classification
    classifier/              # handwritten vs generated vs test detection
    parser/                  # go/packages wrapper, symbol extraction
    graph/                   # call graph (CHA) and edge construction
    chunks/                  # symbol-based chunking
    summaries/               # heuristic file + LLM package summaries
    embeddings/              # batched OpenAI embedding pipeline
    indexer/                 # full indexing orchestrator
    retrieval/               # lexical, semantic, graph retrieval + router
    rerank/                  # scoring and ranking
    mcpserver/               # MCP HTTP server with 5 tools
    storage/                 # Postgres client (pgx) for all tables
    openai/                  # OpenAI client (embeddings + completions)
    config/                  # YAML config with env var overrides
  configs/
    index.yaml               # classification rules, excluded paths
  docker/
    Dockerfile               # multi-stage Go build
    docker-compose.yml       # postgres + mcp-server + indexer
  migrations/
    001_initial_schema.up.sql
    001_initial_schema.down.sql
  claude/
    mcp-config.json          # Claude Code MCP configuration
    CLAUDE.md.snippet         # guidance to add to target repo's CLAUDE.md
    skills/
      trace-go-flow/          # locate implementation paths
      debug-go-test/          # investigate test behavior
      explain-go-impact/      # estimate change impact
  docs/
    plans/
      2026-04-14-projectlens-design.md
      2026-04-14-projectlens-implementation.md
```

## Build and test

```bash
# Build everything
go build ./...

# Run all tests (108 tests across 13 packages)
go test ./...

# Run tests with verbose output
go test ./... -v

# Run a specific package's tests
go test ./internal/parser/ -v
```

## CLI commands

```bash
# Scan repo and report file classification
go run ./cmd/projectlens/ census --repo /path/to/target/repo

# Initialize database + run full index
go run ./cmd/projectlens/ bootstrap --repo /path/to/repo --db "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"

# Incremental reindex (changed files only)
go run ./cmd/projectlens/ reindex --repo /path/to/repo --db "..."

# Full reindex
go run ./cmd/projectlens/ reindex --full --repo /path/to/repo --db "..."

# Dry run (show what would change)
go run ./cmd/projectlens/ reindex --dry-run --repo /path/to/repo --db "..."

# Show index status
go run ./cmd/projectlens/ status --db "..."

# Look up a symbol
go run ./cmd/projectlens/ inspect-symbol ReserveInventory --db "..."

# Show package summary
go run ./cmd/projectlens/ inspect-package "service/graphql" --db "..."

# Query the retrieval pipeline
go run ./cmd/projectlens/ query "how does inventory reservation work" --db "..."
go run ./cmd/projectlens/ query "ReserveInventory" --mode lexical --db "..."
```

## Docker Compose

```bash
# Copy and edit environment file
cp .env.example .env
# Set: PROJECTLENS_REPO_PATH, OPENAI_API_KEY

# Start Postgres and MCP server
cd docker && docker compose up -d

# Run indexer (on demand)
docker compose --profile index run projectlens-indexer

# Stop everything
docker compose down

# Stop and remove data
docker compose down -v
```

**Ports:**
- Postgres: `5433` (host) → `5432` (container)
- MCP server: `8484`

## Database

**8 tables:** files, symbols, chunks, embeddings, summaries, edges, index_runs, git_refs

```bash
# Connect to database
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"

# Run migration manually
psql "..." -f migrations/001_initial_schema.up.sql

# Check table sizes
SELECT relname, n_live_tup FROM pg_stat_user_tables ORDER BY n_live_tup DESC;
```

## MCP tools (5)

| Tool | Purpose |
|------|---------|
| `find_symbol` | Find Go symbol by name (exact/fuzzy) |
| `search_go_context` | Natural language search over indexed code |
| `get_symbol_context` | Symbol + callers, callees, implementors |
| `get_package_summary` | LLM-generated package summary + exports |
| `index_status` | Index freshness and statistics |

## Indexer pipeline

1. **Census** — walk repo, classify files (handwritten/test/generated/excluded)
2. **Parse** — `go/packages` with full type checking, extract symbols
3. **Chunk** — one chunk per symbol (signature + doc + body + package context)
4. **Graph** — CHA call graph, interface implementations, import/dependency edges
5. **Summarize** — heuristic for files, OpenAI gpt-4o-mini for packages
6. **Embed** — OpenAI text-embedding-3-large (3072 dims, halfvec)
7. **Store** — persist all to Postgres + pgvector

Incremental reindex compares checksums — only changed files are reprocessed.

## Environment variables

| Variable | Purpose | Required |
|----------|---------|----------|
| `DATABASE_URL` | Postgres connection string | Yes |
| `OPENAI_API_KEY` | OpenAI API key for embeddings + summaries | Yes (for full index) |
| `REPO_PATH` | Path to target repository | Yes |
| `PROJECTLENS_REPO_PATH` | Docker Compose: host path to mount | Yes (Docker) |
| `PROJECTLENS_DB_PASSWORD` | Docker Compose: Postgres password | No (default: projectlens) |
| `PROJECTLENS_DB_PORT` | Docker Compose: host port for Postgres | No (default: 5433) |
| `PROJECTLENS_MCP_PORT` | Docker Compose: host port for MCP | No (default: 8484) |
| `MCP_PORT` | MCP server port (non-Docker) | No (default: 8484) |
| `CONFIG_PATH` | Path to index.yaml | No (default: configs/index.yaml) |

## Design decisions

- **OpenAI for all indexing** — Claude API is reserved for interactive coding, not batch processing
- **Symbol-based chunking** — no arbitrary token windows, one chunk per Go symbol
- **halfvec(3072)** — pgvector HNSW limits vector to 2000 dims, halfvec supports up to 4000
- **CHA call graph** — faster than RTA, sufficient precision for most queries
- **Streamable HTTP MCP** — persistent service, no cold start per Claude session
- **Read-only repo mount** — indexer never writes to the target repository

## Code conventions

- Internal packages only (no public API)
- pgx connection pool for all database access
- Integration tests gated behind `//go:build integration` tag
- OpenAI calls are behind interfaces for testability (Embedder, PackageSummarizer)
- Errors propagate with `fmt.Errorf("context: %w", err)` wrapping
- No global state — all dependencies injected via constructors
