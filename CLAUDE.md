# ProjectLens

A codebase intelligence platform that indexes Go code, database schemas, change history, and business documentation into a unified searchable graph. Serves any MCP-capable agent (Claude, Cursor, Codex, ...) over Streamable HTTP MCP.

## Project overview

ProjectLens indexes a Go monorepo (~4,150 files) and serves structured context to AI coding assistants so they don't have to rediscover architecture on every session. Beyond code symbols, it tracks data flows (SQL → tables), change history (git), and business context (Confluence, Jira).

**Target repo:** `example-org/ingest` monorepo (34 services, 71 utility packages, ~2,913 handwritten Go files)

## Documentation layout

Three audience-specific docs. Keep duplication to a minimum — each audience has one home.

| File | Audience | Owns |
|---|---|---|
| [`README.md`](README.md) | Non-tech / first-time visitors | What, why, how it helps, 5-step quick start, one-line MCP tool list |
| [`docs/AGENT_SETUP.md`](docs/AGENT_SETUP.md) | End users wiring an agent into their repo | Per-agent MCP config, skills install, hooks install, troubleshooting |
| `CLAUDE.md` (this file) | Contributors and maintainers | Architecture, schema, dev workflow, code conventions, design rationale |
| [`docs/plans/`](docs/plans/) | Maintainers | Design and implementation history (one file per phase) |

When updating something user-visible (a new MCP tool, a new env var, a new skill), update the audience doc that owns it; cross-link instead of copy-pasting.

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
    projectlens-tui/          # TUI dashboard entrypoint (read-only ops + Phase 2 action triggers)
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
    storage/                 # Postgres client (pgx) for all 14 tables
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
    005_writer_lock.up.sql
  claude/
    mcp-config.json          # MCP server config snippet (Claude Code format)
    CLAUDE.md.snippet        # guidance block to merge into target repo's CLAUDE.md
    settings-snippet.json    # PreToolUse + Stop hooks bundle (3 hooks)
    skills/
      use-projectlens/         # MANDATORY MCP-first skill — trace/debug/impact/data-flow workflows
      capture-knowledge/      # detect + persist durable knowledge during sessions
  docs/
    AGENT_SETUP.md            # end-user guide: per-agent config, skills, hooks
    plans/
      2026-04-14-projectlens-design.md
      2026-04-14-projectlens-implementation.md
      2026-04-16-intelligence-platform-design.md
      2026-04-16-intelligence-platform-implementation.md
      2026-04-17-provider-abstraction-design.md
```

## Build and test

The Makefile is the supported entrypoint. It builds binaries into
`./bin/` and runs them from there — `go run` is no longer the default
because the TUI shells out to a real `projectlens` binary (it looks for
a sibling next to `projectlens-tui`).

```bash
make help            # list all targets
make build           # build all binaries → ./bin/
make build-cli       # just ./bin/projectlens
make build-tui       # just ./bin/projectlens-tui
make build-mcp       # just ./bin/projectlens-mcp
make install         # go install ./cmd/... into $GOBIN
make clean           # rm -rf ./bin + clear test cache
make test            # go test ./...
make test-int        # integration tests (build tag: integration)
make fmt vet         # formatting / static checks

# Live Anthropic API test (requires ANTHROPIC_API_KEY)
go test ./internal/providers/anthropic/ -v -run TestLive
```

## CLI commands

Set `REPO=/path/to/target/repo` (or export `REPO_PATH`) and override
`DB_URL` if needed; defaults match the docker-compose Postgres.

```bash
make bootstrap       # init DB + full index
make reindex         # incremental reindex
make reindex-full    # full reindex (rewrites embeddings)
make reindex-dry     # dry-run reindex
make status          # show index status
make index-all       # run all stages
make index-history   # git history + coupling
make index-datastore # migrations + SQL
make index-embed     # embed missing chunks
make index-summarize # summarize missing packages

# Free-form CLI invocations through the same binary
make cli ARGS="inspect-symbol ReserveInventory"
make cli ARGS="inspect-package service/graphql"
make query ARGS='"how does inventory reservation work"'
```

## TUI dashboard

Bubbletea dashboard surfacing index health, pipeline state, storage
stats, recent runs, provider config, and triggering indexer
operations.

```bash
make tui   # builds ./bin/projectlens + ./bin/projectlens-tui then runs the TUI
```

Reads `.env` automatically (DATABASE_URL, REPO_PATH). Logs to
`PROJECTLENS_TUI_LOG_FILE` (default `/tmp/projectlens-tui.log`).

**Phase 1 keys:** ↑/↓ navigate · enter focus · esc back · r refresh · ? help · q quit.

**Phase 2 actions** (run projectlens subcommands as subprocesses):

| Key | Action            | Confirmation       |
|-----|-------------------|--------------------|
| `R` | reindex           | y/N preflight      |
| `F` | reindex --full    | typed `reindex`    |
| `E` | index-embed       | y/N preflight      |
| `S` | index-summarize   | y/N preflight      |
| `H` | index-history     | y/N preflight      |
| `D` | index-datastore   | y/N preflight      |
| `A` | index-all         | typed `all`        |
| `c` | cancel running    | -                  |
| `J` | jump to Jobs      | -                  |

Subprocesses log to `~/.projectlens/tui-runs/<RFC3339>-<action>.log`.
Binary resolution: `PROJECTLENS_BINARY` env var > sibling of `projectlens-tui`
> `PATH`. q during a running job triggers Cancel + drain (no detach;
Ctrl+C is the OS escape hatch). Action subprocesses serialize via the
writer lock above; a busy lock surfaces in the drawer's tail.

## Docker Compose

```bash
# Copy and edit environment file
cp .env.example .env
# Set: PROJECTLENS_REPO_PATH, ANTHROPIC_API_KEY (and optionally OPENAI_API_KEY)

make docker-up       # start Postgres + MCP server
make docker-logs     # tail logs
make docker-down     # stop containers (keeps volumes)
make docker-rebuild  # rebuild images + restart
make docker-clean    # stop AND delete volumes (destructive)
make docker-index    # one-shot indexer profile container
```

**Ports:**
- Postgres: `5433` (host) → `5432` (container)
- MCP server: `8484`

## Database

**14 tables:** files, symbols, chunks, embeddings, summaries, edges, index_runs, git_refs, datastore_tables, documents, symbol_history, file_history, knowledge_entries, index_locks, schema_migrations

```bash
# Connect to database
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"

# Run migrations (applied automatically by bootstrap, or manually)
psql "..." -f migrations/001_initial_schema.up.sql
psql "..." -f migrations/002_intelligence_platform.up.sql
psql "..." -f migrations/003_vector_dimensions.up.sql
psql "..." -f migrations/004_knowledge_layer.up.sql
psql "..." -f migrations/005_writer_lock.up.sql

# Check table sizes
SELECT relname, n_live_tup FROM pg_stat_user_tables ORDER BY n_live_tup DESC;
```

### Schema highlights

- **Polymorphic edges table** — `(source_type, source_id, target_type, target_id, edge_type, properties, confidence)`. One table for call graph, data flow, coupling, and doc links.
- **SCIP-style symbol IDs** — `symbols.scip_symbol` with hierarchical naming: `go . internal/indexer . Indexer.Run()`
- **Universal chunks** — `chunks.source_type` discriminator: `code`, `confluence`, `jira`, `migration`, `knowledge`. All types share the same embedding vector space.
- **Schema migrations tracker** — `schema_migrations` table prevents re-applying migrations.

### Writer lock

Mutating indexer commands (`bootstrap`, `reindex`, `index-datastore`,
`index-history`, `index-embed`, `index-summarize`, `index-all`) acquire
a single Postgres advisory lock (`LockID = 9876543210`) at the start
of each invocation. Holder identity (client_pid, backend_pid, hostname,
cmd, started_at) is recorded in `index_locks`. Read-only commands
(`status`, `query`, `inspect-*`, `census`, `knowledge`) and the MCP
server bypass the lock.

When the lock is held by another process, a second writer exits with
code **75** (sysexits `EX_TEMPFAIL`) and a stderr line of the form:

```
another writer holds the lock: pid=<n> host=<h> cmd="<c>" started=<RFC3339>
```

**Auto-recovery:** if a holder is killed (kill -9, OOM, panic), its
DB session drops, Postgres auto-releases the advisory lock, and the
next `Acquire` reaps the orphaned `index_locks` row via a
`pg_stat_activity` join on `backend_pid`.

**Escape hatch:** `projectlens unlock --force` reads the holder's
backend pid from `index_locks`, calls `pg_terminate_backend(pid)` to
drop the holder's session (which auto-releases the advisory lock),
then deletes the bookkeeping row. Use only when auto-recovery has
failed (e.g. a recycled client PID makes the row look live). Logs the
previous holder identity for audit. Note: this kills the holder's DB
session — any in-flight transactions in that process roll back.

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

## Skills and hooks (bundled in `claude/`)

The `claude/` directory ships **agent integration assets** — skills and
hooks that end users install into their target repo to make their AI
assistant reliably reach for ProjectLens. We maintain these as part of the
ProjectLens project; users symlink them into `.claude/` in their own repo
(see [`docs/AGENT_SETUP.md`](docs/AGENT_SETUP.md) for the install path).

### Skills

Two skills, one role each:

- **`use-projectlens`** (`claude/skills/use-projectlens/SKILL.md`) — primary
  forcing skill. The rule: *"Before opening files, grepping, or listing
  directories to answer a question about code structure, behavior,
  history, data flow, or impact — call ProjectLens first."* Contains a
  decision-flow diagram, tool-picker table, and four canonical workflows
  (trace, debug-test, change-impact, data-flow). When adding a new MCP
  tool, also extend the tool-picker table here.

- **`capture-knowledge`** (`claude/skills/capture-knowledge/SKILL.md`) —
  write-back skill. Defines 9 trigger signals and 6 categories
  (`lesson`, `best_practice`, `convention`, `domain_knowledge`, `how_to`,
  `decision`). When the agent detects a signal, it calls
  `save_knowledge` with required `category`/`title`/`body` and an optional
  anchor. Body format is rule + `**Why:**` + `**How to apply:**`.

When you change a skill, the change reaches users on their next pull /
symlink refresh — there's no separate publish step.

### Hooks (`claude/settings-snippet.json`)

Three hooks, all soft `<system-reminder>` nudges (no exit-code blocking):

| Event | Matcher | Purpose |
|---|---|---|
| `PreToolUse` | `Edit \| Write \| MultiEdit` | Pre-edit impact-check reminder. Tells the agent: if editing an exported symbol, interface, or `migrations/*.sql`, you must have called `get_symbol_context` / `get_table_context` / `get_coupling` first. |
| `Stop` | (any) | capture-knowledge scan: if any of the 9 signals fired this turn, call `save_knowledge` before stopping. |
| `Stop` | (any) | use-projectlens compliance audit: flag turns that answered structural questions via Read/Grep/Glob without a prior ProjectLens call. |

**Why soft (reminder) instead of blocking (exit code 2):** blocking would
also stop legitimate edits to private/test code where the impact-check
doesn't apply. Per-edit detection of "is this an exported symbol?" is
expensive at the harness level. Soft nudges trade enforcement strength
for low false-positive rate.

**Editing the hooks.** Keep commands one-line `echo
'<system-reminder>...</system-reminder>'`. The harness pipes the echo
output back to the agent as a system-reminder block. JSON validates with
`python3 -m json.tool claude/settings-snippet.json`.

### Distribution

`claude/` is the **canonical** copy. Users either symlink it into their
target repo's `.claude/` or copy it. We don't publish to a registry — the
repo URL is the artifact. When making a breaking change to a skill or
hook, bump the description briefly in `docs/AGENT_SETUP.md` so users
notice on their next pull.

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
| `PROJECTLENS_MCP_URL` | Full MCP URL the TUI Config section probes (overrides `MCP_PORT`) | No (default `http://localhost:<MCP_PORT>/mcp`) |
| `CONFIG_PATH` | Path to index.yaml | No (default: configs/index.yaml) |
| `PROJECTLENS_TUI_LOG_FILE` | TUI log file path (default `/tmp/projectlens-tui.log`) | No |
| `PROJECTLENS_BINARY` | Explicit path to the `projectlens` binary the TUI invokes (overrides sibling/PATH lookup) | No |
| `PROJECTLENS_TUI_RUNS_DIR` | Override directory for TUI subprocess log files (default `~/.projectlens/tui-runs`) | No |

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
