# ProjectLens

A codebase intelligence platform that indexes Go code, database schemas, change history, and business documentation into a unified searchable graph. Designed for Claude Code via MCP.

## Project overview

ProjectLens indexes a Go monorepo (~4,150 files) and serves structured context to Claude Code so it doesn't need to rediscover architecture on every session. Beyond code symbols, it tracks data flows (SQL → tables), change history (git), and business context (Confluence, Jira).

**Target repo:** `example-org/ingest` monorepo (34 services, 71 utility packages, ~2,913 handwritten Go files)

## Tech stack

- **Language:** Go 1.26
- **Storage:** Postgres 16 + pgvector (halfvec for 1024-dim vectors)
- **Embeddings:** Ollama `mxbai-embed-large` (local, 1024-dim) — OpenAI fallback available
- **Summarization:** Claude Sonnet via Anthropic API — OpenAI fallback available
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
    embeddings/              # batched embedding pipeline (provider-agnostic)
    indexer/                 # full indexing orchestrator
    retrieval/               # lexical, semantic, graph retrieval + router
    rerank/                  # scoring and ranking
    mcpserver/               # MCP HTTP server with 10 tools
    storage/                 # Postgres client (pgx) for all 13 tables
    providers/
      ollama/                # Ollama embedding client (local)
      anthropic/             # Anthropic/Claude summarization client
      openai/                # OpenAI client (embeddings + summaries, fallback)
    config/                  # YAML config with env var overrides
  configs/
    index.yaml               # provider config, classification rules, excluded paths
  docker/
    Dockerfile               # multi-stage Go build
    docker-compose.yml       # postgres + mcp-server + indexer
  migrations/
    001_initial_schema.up.sql
    002_intelligence_platform.up.sql
    003_vector_dimensions.up.sql
    004_knowledge_layer.up.sql
  claude/
    mcp-config.json          # Claude Code MCP configuration
    CLAUDE.md.snippet         # guidance to add to target repo's CLAUDE.md
    settings-snippet.json    # Stop-hook snippet to install in target repo
    skills/
      trace-go-flow/          # locate implementation paths
      debug-go-test/          # investigate test behavior
      explain-go-impact/      # estimate change impact
      capture-knowledge/      # detect + persist durable knowledge during sessions
  docs/
    plans/
      2026-04-14-projectlens-design.md
      2026-04-14-projectlens-implementation.md
      2026-04-16-intelligence-platform-design.md
      2026-04-16-intelligence-platform-implementation.md
      2026-04-17-provider-abstraction-design.md
```

## Build and test

```bash
# Build everything
go build ./...

# Run all tests
go test ./...

# Run tests with verbose output
go test ./... -v

# Run a specific package's tests
go test ./internal/parser/ -v

# Run live Anthropic API test (requires ANTHROPIC_API_KEY)
go test ./internal/providers/anthropic/ -v -run TestLive
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
# Set: PROJECTLENS_REPO_PATH, ANTHROPIC_API_KEY (and optionally OPENAI_API_KEY)

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

**13 tables:** files, symbols, chunks, embeddings, summaries, edges, index_runs, git_refs, datastore_tables, documents, symbol_history, file_history, knowledge_entries, schema_migrations

```bash
# Connect to database
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"

# Run migrations (applied automatically by bootstrap, or manually)
psql "..." -f migrations/001_initial_schema.up.sql
psql "..." -f migrations/002_intelligence_platform.up.sql
psql "..." -f migrations/003_vector_dimensions.up.sql
psql "..." -f migrations/004_knowledge_layer.up.sql

# Check table sizes
SELECT relname, n_live_tup FROM pg_stat_user_tables ORDER BY n_live_tup DESC;
```

### Schema highlights

- **Polymorphic edges table** — `(source_type, source_id, target_type, target_id, edge_type, properties, confidence)`. One table for call graph, data flow, coupling, and doc links.
- **SCIP-style symbol IDs** — `symbols.scip_symbol` with hierarchical naming: `go . internal/indexer . Indexer.Run()`
- **Universal chunks** — `chunks.source_type` discriminator: `code`, `confluence`, `jira`, `migration`, `knowledge`. All types share the same embedding vector space.
- **Schema migrations tracker** — `schema_migrations` table prevents re-applying migrations.

## MCP tools (10)

| Tool | Purpose |
|------|---------|
| `find_symbol` | Find Go symbol by name (exact/fuzzy) |
| `search_go_context` | Natural language search over indexed code (+ docs when available) |
| `get_symbol_context` | Symbol + callers, callees, implementors, SCIP ID |
| `get_package_summary` | LLM-generated package summary + exports |
| `get_table_context` | SQL table → reading/writing Go code, columns, migrations |
| `get_change_history` | Git history for a file or symbol |
| `get_coupling` | Files that change together with the target |
| `index_status` | Index freshness and per-stage statistics |
| `save_knowledge` | Persist a durable lesson/best_practice/convention/etc. with optional anchors |
| `search_knowledge` | Search captured knowledge by query, category, and/or anchor |

`get_symbol_context`, `get_package_summary`, and `search_go_context` automatically append a `Related knowledge` block when entries are anchored to the target — no extra call needed.

## Indexer pipeline

```
projectlens index code      — census + parse + chunk + call graph
projectlens index datastore — scan migrations + SQL in code (planned)
projectlens index history   — git log + coupling detection (planned)
projectlens index docs      — Confluence + Jira fetch (planned)
projectlens index embed     — embed all chunks missing embeddings (planned)
projectlens index summarize — generate missing summaries (planned)
projectlens index all       — run all stages (planned)
```

Currently, `bootstrap` and `reindex` run code + summarize + embed as a monolithic pipeline. Independent stages are planned.

### Pipeline steps (current)

1. **Census** — walk repo, classify files (handwritten/test/generated/excluded)
2. **Parse** — `go/packages` with full type checking, extract symbols + SCIP IDs
3. **Chunk** — one chunk per symbol (signature + doc + body + package context)
4. **Graph** — CHA call graph, interface implementations, polymorphic edges
5. **Summarize** — heuristic for files, Claude Sonnet for packages
6. **Embed** — Ollama mxbai-embed-large (1024 dims, halfvec, local)
7. **Store** — persist all to Postgres + pgvector

Incremental reindex compares checksums — only changed files are reprocessed.

Knowledge entries (captured via the `save_knowledge` MCP tool) flow through the existing chunk + embedding pipeline — no separate stage. Each entry creates a `knowledge_entries` row and a paired `chunks` row with `source_type='knowledge'`; the next embed pass picks it up automatically.

## Provider configuration

```yaml
# configs/index.yaml
embeddings:
  provider: ollama              # ollama | openai
  model: mxbai-embed-large
  dimensions: 1024
  endpoint: http://localhost:11434

summarization:
  provider: anthropic           # anthropic | openai
  model: claude-sonnet-4-6
```

## Environment variables

| Variable | Purpose | Required |
|----------|---------|----------|
| `DATABASE_URL` | Postgres connection string | Yes |
| `ANTHROPIC_API_KEY` | Anthropic API key for Claude summarization | Yes (default provider) |
| `OPENAI_API_KEY` | OpenAI API key (fallback provider) | Only if provider=openai |
| `OLLAMA_ENDPOINT` | Ollama server URL | No (default: http://localhost:11434) |
| `REPO_PATH` | Path to target repository | Yes |
| `PROJECTLENS_REPO_PATH` | Docker Compose: host path to mount | Yes (Docker) |
| `PROJECTLENS_DB_PASSWORD` | Docker Compose: Postgres password | No (default: projectlens) |
| `PROJECTLENS_DB_PORT` | Docker Compose: host port for Postgres | No (default: 5433) |
| `PROJECTLENS_MCP_PORT` | Docker Compose: host port for MCP | No (default: 8484) |
| `MCP_PORT` | MCP server port (non-Docker) | No (default: 8484) |
| `CONFIG_PATH` | Path to index.yaml | No (default: configs/index.yaml) |

## Design decisions

- **Ollama for embeddings** — local, free, no data leaves the machine, 1024-dim sufficient for code search
- **Claude for summarization** — higher quality than gpt-4o-mini, user has enterprise Anthropic plan
- **OpenAI as fallback** — config-driven provider selection, kept for users with API access
- **Polymorphic edge graph** — one table for all relationship types, traversable with recursive CTEs
- **SCIP-style symbol IDs** — debuggable hierarchical text IDs for cross-referencing
- **Symbol-based chunking** — no arbitrary token windows, one chunk per Go symbol
- **halfvec(1024)** — 3x smaller index, 3x faster search vs 3072-dim, negligible quality difference for code search
- **CHA call graph** — faster than RTA, sufficient precision for most queries
- **Streamable HTTP MCP** — persistent service, no cold start per Claude session
- **Read-only repo mount** — indexer never writes to the target repository
- **Independent pipeline stages** — each stage reads from DB, writes to DB, no in-memory coupling

## Code conventions

- Internal packages only (no public API)
- pgx connection pool for all database access
- Provider interfaces for testability (Embedder, PackageSummarizer)
- Config-driven provider selection (ollama/anthropic/openai)
- Integration tests gated behind `//go:build integration` tag
- Live API tests skip when env var not set (e.g., `TestLive*`)
- Errors propagate with `fmt.Errorf("context: %w", err)` wrapping
- No global state — all dependencies injected via constructors
- Batch inserts respect Postgres 65535 parameter limit
- Oversized chunks truncated to 30000 chars before embedding
