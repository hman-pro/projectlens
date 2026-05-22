# ProjectLens Operations

Last verified against: `make help`, `cmd/projectlens`, `internal/mcpserver/tools.go`, `internal/tui/app/keys.go`, and `internal/tui/jobs/registry.go` on 2026-05-22.

This guide is for daily use: setup, indexing, querying, reporting, TUI operation, MCP server operation, Docker, migrations, and common failures.

## Make Targets Vs CLI Commands

`make` targets are repo-local conveniences. They build binaries into `./bin/` and then run them with common flags.

Raw `projectlens` subcommands are the product surface. Use them directly when you already have `./bin/projectlens` on disk or installed in `PATH`.

| Use | Make target | Raw CLI |
|---|---|---|
| Build everything | `make build` | `go build ./cmd/...` or targeted `go build` |
| Run index status | `make status` | `projectlens status --db "$DATABASE_URL"` |
| Incremental reindex | `make reindex REPO=/path/to/repo` | `projectlens reindex --repo /path/to/repo --db "$DATABASE_URL"` |
| Free-form command | `make cli ARGS="inspect-symbol Foo"` | `projectlens inspect-symbol Foo` |

## Everyday Commands

### Setup

```bash
cp .env.example .env
cd docker && docker compose up -d
export $(grep -v '^#' .env | xargs)
```

Required for normal local runs:

| Variable | Purpose |
|---|---|
| `REPO_PATH` or `--repo` | Target Go repository to index. |
| `DATABASE_URL` or `--db` | Postgres connection string. Make targets default to `postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable`. |
| `ANTHROPIC_API_KEY` | Required when summarization provider is `anthropic`. |
| `OPENAI_API_KEY` | Required only when embeddings or summarization provider is `openai`. |
| `OLLAMA_ENDPOINT` | Optional, default `http://localhost:11434`. |
| `CONFIG_PATH` or `--config` | Optional config path, default `configs/index.yaml`. |

### Build And Test

```bash
make help
make build
make build-cli
make build-mcp
make build-tui
make test
make test-int
make fmt
make vet
make clean
```

`make install` runs `go install ./cmd/...` into `$GOBIN`.

### Index

```bash
make bootstrap REPO=/path/to/repo       # migrations + full initial index
make reindex REPO=/path/to/repo         # incremental code reindex
make reindex-full REPO=/path/to/repo    # full code reindex
make reindex-dry REPO=/path/to/repo     # show changed-file work only
make index-all REPO=/path/to/repo       # code + datastore + history + summarize + embed
make index-datastore REPO=/path/to/repo # migrations + SQL references
make index-history REPO=/path/to/repo   # git history + coupling
make index-embed                        # chunks missing embeddings
make index-summarize                    # packages missing summaries
make cli ARGS="index-backfill-provenance" # edge provenance backfill
```

Raw equivalents:

```bash
./bin/projectlens bootstrap --repo /path/to/repo --db "$DATABASE_URL"
./bin/projectlens reindex --repo /path/to/repo --db "$DATABASE_URL"
./bin/projectlens reindex --full --repo /path/to/repo --db "$DATABASE_URL"
./bin/projectlens reindex --dry-run --repo /path/to/repo --db "$DATABASE_URL"
./bin/projectlens index-all --repo /path/to/repo --db "$DATABASE_URL"
./bin/projectlens index-history --full --repo /path/to/repo --db "$DATABASE_URL"
./bin/projectlens index-backfill-provenance --db "$DATABASE_URL"
```

Mutating indexer commands acquire the writer lock: `bootstrap`, `reindex`, `index-datastore`, `index-history`, `index-embed`, `index-summarize`, `index-all`, and `index-backfill-provenance`.

### Inspect And Query

```bash
make status
make cli ARGS="census --repo /path/to/repo"
make cli ARGS="inspect-symbol ReserveInventory"
make cli ARGS="inspect-package github.com/example/project/pkg/foo"
make query ARGS='"how does inventory reservation work"'
make cli ARGS='query "ReserveInventory" --mode lexical'
make cli ARGS='query "how does inventory reservation work" --mode semantic'
make cli ARGS="knowledge list --limit 20"
make cli ARGS="knowledge show 123"
make cli ARGS='knowledge search --anchor symbol:ReserveInventory'
```

`knowledge search` vector search is MCP-only today; the CLI supports anchor search.

### Report And Export

```bash
make cli ARGS="report --top 10"
make cli ARGS="report --format json --out /tmp/projectlens-report.json"
make cli ARGS="report --out /tmp/projectlens-report.md"
make cli ARGS="export graph --edges all --out /tmp/projectlens-graph.json"
make cli ARGS="export graph --edges calls,implements --include-evidence"
```

`report --format` accepts `markdown` or `json`; without `--format`, it is inferred from `--out` or defaults to Markdown on stdout.

`export graph --edges` accepts `all` or a comma-separated edge type list. `--include-evidence` includes `properties.evidence` blobs.

### TUI

```bash
make tui
```

The TUI builds `./bin/projectlens` and `./bin/projectlens-tui`, then runs the dashboard. It reads `.env` through the Go autoload path used by the binaries.

Views:

| View | Purpose |
|---|---|
| Health | Database and overall state. |
| Pipeline | Stage freshness and action shortcuts. |
| Storage | Table sizes and indexed-state shape. |
| Runs | Recent `index_runs`. |
| Config | Repository, database, MCP URL, provider config/probes. |
| Jobs | Live and historical TUI-launched subprocess runs. |

Navigation keys:

| Key | Action |
|---|---|
| `up`/`k`, `down`/`j` | Move selection. |
| `enter` | Focus selected section. |
| `esc`/`h` | Back. |
| `tab`, `shift+tab` | Next/previous section. |
| `r` | Refresh focused section. |
| `?` | Help. |
| `q`, `ctrl+c` | Quit. If a job is running, quit requests cancellation and drains completion. |

Indexer action keys:

| Key | Subcommand | Confirmation |
|---|---|---|
| `R` | `reindex` | `y/N` after changed-file preflight. |
| `F` | `reindex --full` | Type `reindex`. |
| `E` | `index-embed` | `y/N` after pending-chunk preflight. |
| `S` | `index-summarize` | `y/N` after pending-package preflight. |
| `H` | `index-history` | `y/N` after new-commit preflight. |
| `D` | `index-datastore` | `y/N` after datastore-table preflight. |
| `A` | `index-all` | Type `all`. |
| `c` | Cancel running job | No confirmation. |
| `J` | Jump to Jobs | No confirmation. |

TUI operational details:

| Topic | Behavior |
|---|---|
| Main TUI log | `PROJECTLENS_TUI_LOG_FILE`, default `/tmp/projectlens-tui.log`. |
| Subprocess logs | `PROJECTLENS_TUI_RUNS_DIR`, default `~/.projectlens/tui-runs`, fallback `/tmp/projectlens-tui-runs`. |
| Binary resolution | `PROJECTLENS_BINARY` first, then sibling `projectlens` next to `projectlens-tui`, then `PATH`. |
| Missing binary | Blocking error modal with hint to set `PROJECTLENS_BINARY`, place sibling binary, or add to `PATH`. |
| Writer lock busy | The launched CLI exits with code 75; the Jobs view and drawer show the log tail containing the holder line. |
| Refresh after actions | Successful terminal messages refresh the action's configured sections plus Jobs. |

### MCP

```bash
make mcp
# or
make build-mcp && ./bin/projectlens-mcp
```

Default endpoint: `http://localhost:8484/mcp`.

MCP tools, from `internal/mcpserver/tools.go`:

| Tool | Purpose |
|---|---|
| `find_symbol` | Find a Go symbol by name, optionally filtered by kind. |
| `search_go_context` | Natural-language search for relevant Go code. |
| `get_symbol_context` | Symbol, callers, callees, and implementors. |
| `get_package_summary` | Package purpose, exports, and dependencies. |
| `get_table_context` | Table schema plus Go readers/writers. |
| `index_status` | Per-stage freshness, git state, and provider health. |
| `get_change_history` | Recent commits for a file or symbol. |
| `get_coupling` | Files that frequently change with a target file. |
| `save_knowledge` | Persist durable lessons, conventions, decisions, and similar entries. |
| `search_knowledge` | Search captured knowledge by query, category, or anchor. |

Agent-specific MCP wiring belongs in `docs/AGENT_SETUP.md`.

### Docker

```bash
make docker-up
make docker-logs
make docker-down
make docker-build
make docker-rebuild
make docker-index
make docker-clean
```

`docker-clean` runs `docker compose down -v` and deletes volumes.

Docker environment:

| Variable | Purpose |
|---|---|
| `PROJECTLENS_REPO_PATH` | Host path mounted into containers for indexing. |
| `PROJECTLENS_DB_PASSWORD` | Postgres password, default `projectlens`. |
| `PROJECTLENS_DB_PORT` | Host Postgres port, default `5433`. |
| `PROJECTLENS_MCP_PORT` | Host MCP port, default `8484`. |

### Migration

```bash
make migrate
# or
./bin/projectlens migrate --db "$DATABASE_URL"
```

`bootstrap` also applies migrations before indexing. Use `migrate` to catch up an existing database without reindexing.

`index-backfill-provenance` is an idempotent post-migration repair command for edge rows inserted before migration 006. It fills NULL `provenance` and `confidence_class` values using fixed defaults from `docs/2026-05-22-confidence-and-provenance-design.md`.

## Troubleshooting

| Symptom | Check | Fix |
|---|---|---|
| Missing repo path | CLI says `repository path required: use --repo flag or set repo_path in config`. | Pass `--repo`, set `REPO_PATH`, or set `repo_path` in config. |
| DB connection failure | `connecting to database` error. | Start Docker Postgres, verify `DATABASE_URL`, and confirm port `5433` is reachable. |
| Provider failure | `OPENAI_API_KEY required`, missing Anthropic key, or provider probe failure. | Match `configs/index.yaml` provider settings to available credentials and local Ollama state. |
| Stale index | `index_status` shows old stage ages, wrong git commit, or dirty/stale state. | Run `make reindex` for code changes or `make index-all` for all stages. |
| Writer lock busy | Exit code 75 plus `another writer holds the lock: pid=<n> host=<h> cmd="<c>" started=<RFC3339>`. | Wait for the holder to finish. Use `projectlens unlock --force` only after confirming auto-recovery failed. |
| Missing TUI binary | TUI says `projectlens binary not found`. | Run `make build-cli`, set `PROJECTLENS_BINARY`, keep `projectlens` next to `projectlens-tui`, or add it to `PATH`. |
| Agent does not use ProjectLens | Agent sees no tools or greps first. | Confirm MCP server is running, agent config points at `/mcp`, and skills/hooks from `docs/AGENT_SETUP.md` are installed. |

## Docs Review Checklist

After changing any of these, update the owning doc in the same PR:

| Changed surface | Owning doc |
|---|---|
| Make target or CLI subcommand/flag | `docs/operations.md` |
| MCP tool name, parameters, or behavior | `docs/operations.md` and `agent/skills/use-projectlens/SKILL.md` |
| TUI key, view, action, log, or binary behavior | `docs/operations.md` |
| Runtime component or data layer | `docs/architecture.md` |
| Index pipeline, schema, storage relationship, or query internals | `docs/internals.md` |
| Agent installation instructions | `docs/AGENT_SETUP.md` |
| Maintainer convention | `CLAUDE.md` |
