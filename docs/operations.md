# ProjectLens Operations

Last verified against: `make help`, `cmd/projectlens`, `internal/mcpserver/tools.go`, `internal/tui/app/keys.go`, and `internal/tui/jobs/registry.go` on 2026-05-22.

This guide is for daily use: setup, indexing, querying, reporting, TUI operation, MCP server operation, Docker, migrations, and common failures.

## Make Targets Vs CLI Commands

`make` targets are repo-local conveniences. They build binaries into `./bin/` and then run them with common flags.

Raw `projectlens` subcommands are the product surface. Use them directly when you already have `./bin/projectlens` on disk or installed in `PATH`.

| Use | Make target | Raw CLI |
|---|---|---|
| Build everything | `make build` | `go build ./cmd/...` or targeted `go build` |
| Run index status | `make status` | `projectlens status --db "$PROJECTLENS_DATABASE_URL"` |
| Incremental reindex | `make reindex REPO=/path/to/repo` | `projectlens reindex --repo /path/to/repo --db "$PROJECTLENS_DATABASE_URL"` |
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
| `PROJECTLENS_REPO_PATH` or `--repo` | Target Go repository to index. The public alpha defaults to indexing this repository (`.`). |
| `PROJECTLENS_DATABASE_URL` or `--db` | Postgres connection string. Make targets default to `postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable`. |
| `OLLAMA_ENDPOINT` | Optional, default `http://localhost:11434`. |
| `CONFIG_PATH` or `--config` | Optional config path, default `configs/index.yaml`. |

ProjectLens is local-first: embeddings come from Ollama and summarization is disabled by default. The public alpha ships no remote provider integrations, so no API keys are required.

### Build And Test

```bash
make help
make build
make build-projectlens
make build-projectlens-mcp
make build-projectlens-tui
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
./bin/projectlens bootstrap --repo /path/to/repo --db "$PROJECTLENS_DATABASE_URL"
./bin/projectlens reindex --repo /path/to/repo --db "$PROJECTLENS_DATABASE_URL"
./bin/projectlens reindex --full --repo /path/to/repo --db "$PROJECTLENS_DATABASE_URL"
./bin/projectlens reindex --dry-run --repo /path/to/repo --db "$PROJECTLENS_DATABASE_URL"
./bin/projectlens index-all --repo /path/to/repo --db "$PROJECTLENS_DATABASE_URL"
./bin/projectlens index-history --full --repo /path/to/repo --db "$PROJECTLENS_DATABASE_URL"
./bin/projectlens index-backfill-provenance --db "$PROJECTLENS_DATABASE_URL"
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

Each stage is rendered under `## Stages` with status, last run age, the provider identity used (e.g. `embed=ollama:qwen3-embedding:0.6b@1024 sum=disabled`), a sorted `key=value` metrics summary, and the (sanitized, 200-char-clipped in Markdown / full in JSON) `error_text` from the most recent failure. Stage trust comes from migration 008; see [`docs/internals.md`](internals.md) for the schema.

`export graph --edges` accepts `all` or a comma-separated edge type list. `--include-evidence` includes `properties.evidence` blobs.

Convenience Make targets wrap the export and Gephi conversion:

```bash
make graph-export                                            # writes projectlens-graph.json
make graph-export GRAPH_EDGES=calls,implements GRAPH_JSON=calls.json
make graph-gephi                                             # projectlens-graph.json -> projectlens.graphml
make graph-gephi EDGES=calls,implements GRAPH_OUT=calls.graphml
make graph-gephi GRAPH_FORMAT=gexf GRAPH_OUT=graph.gexf
```

`graph-gephi` calls `scripts/graph_to_gephi.py`, which converts the JSON export to GraphML (default) or GEXF for Gephi, Cytoscape, or any GraphML-aware viewer. Variables:

| Variable | Default | Purpose |
|---|---|---|
| `GRAPH_JSON` | `projectlens-graph.json` | Input/output path for `graph-export`; input for `graph-gephi`. |
| `GRAPH_OUT` | `projectlens.graphml` | Output path for `graph-gephi`. |
| `GRAPH_FORMAT` | `graphml` | `graphml` or `gexf`. |
| `GRAPH_EDGES` | `all` | Edge types passed to `export graph --edges`. |
| `EDGES` | unset | Edge-type filter applied during conversion (comma list, e.g. `calls,implements`). |
| `PYTHON` | `python3` | Python interpreter. Use a venv with `PYTHON=.venv/bin/python`. |

Requires `pip install networkx`. The script drops nodes with no edges after filtering; pass `--keep-isolated` (via direct invocation) to retain them.

Once a `.graphml` or `.gexf` is written, open it in Gephi (`File -> Open`) and start with a filtered view instead of the full graph:

- Layout with ForceAtlas2 or OpenOrd.
- Color nodes by `type` or `attr_package`.
- Size nodes by degree or PageRank.
- Hide low-degree nodes before labeling.
- Label only selected or high-degree nodes.

Useful slices:

| Edge filter | Shows |
|---|---|
| `calls,implements` | Code dependency shape. |
| `co_changes` | Historical coupling. |
| `knowledge_about` | Agent memory and knowledge anchors. |
| `reads_table,writes_table` | Code-to-datastore access, when those edges are present in the export. |

For docs, pair a Gephi screenshot with a small legend:

| Shape | Edge types |
|---|---|
| `symbol -> symbol` | `calls`, `implements`, `imports` |
| `file -> file` | `co_changes` |
| `symbol -> table` | `reads_table`, `writes_table` |
| `knowledge -> target` | `knowledge_about` |

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
make build-projectlens-mcp && ./bin/projectlens-mcp
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

### Projects

ProjectLens supports indexing several Go repositories from a single Postgres
database by giving each project its own storage schema. The single-process
MCP server then mounts one endpoint per project under `/{slug}/mcp`.

If no `configs/projects.yaml` is present, ProjectLens runs in legacy
single-project mode against the `public` schema — `--repo` / `PROJECTLENS_REPO_PATH`
keep working as before and the MCP server keeps serving `/mcp`.

#### Project registry

The registry lives at `configs/projects.yaml`. The shape (see
[`configs/projects.example.yaml`](../configs/projects.example.yaml)) is:

```yaml
database_url: postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable
default_project: projectlens
projects:
  - slug: projectlens              # lowercase URL/CLI identifier
    storage_schema: projectlens    # Postgres schema (must match ^[a-z][a-z0-9_]*$, not `public`/`pg_*`)
    repo_path: /Users/you/source/projectlens
    config_path: configs/index.yaml   # optional; provider/indexing settings
```

Each project gets its own Postgres schema. `repo_path` and `storage_schema`
always come from the registry; `config_path` supplies indexing/provider
settings only, never identity.

Inspect the registry:

```bash
./bin/projectlens projects list --projects configs/projects.yaml
./bin/projectlens projects validate --projects configs/projects.yaml
```

`--projects` defaults to `configs/projects.yaml`; pass it only when the
registry lives elsewhere.

#### Project-aware write commands

Every mutating CLI (`bootstrap`, `reindex`, `index-all`, `index-datastore`,
`index-history`, `index-embed`, `index-summarize`, `index-backfill-provenance`,
`migrate`, `unlock`) accepts `--project <slug>`:

```bash
./bin/projectlens migrate --project projectlens
./bin/projectlens bootstrap --project projectlens
./bin/projectlens index-all --project projectlens
./bin/projectlens reindex --project projectlens
```

`--project` and `--repo` are mutually exclusive — the CLI rejects requests
that pass both. Omit `--project` and skip `configs/projects.yaml` to keep
the legacy `--repo`/`public` flow.

`projectlens migrate --project <slug>` creates the schema if missing,
records `schema_migrations` rows inside that schema, and is idempotent.
The `pgvector` extension is database-global (lives in `public`); only the
project tables are schema-local.

#### MCP routing

When `configs/projects.yaml` exists, `projectlens-mcp` mounts one MCP
endpoint per project:

```text
http://localhost:8484/projectlens/mcp
http://localhost:8484/<slug>/mcp
```

Without a registry, the server keeps the legacy `http://localhost:8484/mcp`
mount. Behavior summary:

| Request | Response |
|---|---|
| `/{slug}/mcp` where `<slug>` is registered and migrated | Streamable HTTP MCP for that project. |
| `/{slug}/mcp` where `<slug>` is registered but the storage schema is missing | HTTP 503 with a JSON `hint` pointing at `projectlens migrate --project <slug>`. |
| Any other path (unknown slug or stray URL) | HTTP 404. |

A broken project does not prevent the other endpoints from serving.

The MCP server is local-only and unauthenticated; do not expose it to a
network you do not control.

#### TUI

The TUI honors two environment variables:

| Variable | Effect |
|---|---|
| `PROJECT` | Resolves the active project slug. If unset, the TUI falls back to `default_project` from the registry, or to legacy mode when no registry exists. |
| `PROJECTS_PATH` | Overrides the registry path (default `configs/projects.yaml`). |

The TUI threads `--project <slug>` into every job it launches, so writer-
lock and migration semantics match the CLI path.

### Migration

```bash
make migrate
# or
./bin/projectlens migrate --db "$PROJECTLENS_DATABASE_URL"
# or per project
./bin/projectlens migrate --project <slug>
```

`bootstrap` also applies migrations before indexing. Use `migrate` to catch up an existing database without reindexing. With `--project`, migrations run inside the project's storage schema; without it, the legacy `public` schema is used.

`index-backfill-provenance` is an idempotent post-migration repair command for edge rows that pre-date migration 006 or were written by older/broken producers. It performs partial-field repair via `COALESCE` — rows are touched when **either** `provenance` or `confidence_class` is NULL, and an already-set value on the other column is preserved. Per-edge-type defaults live in `cmd/projectlens/main.go::edgeProvenanceDefaults`; see `docs/2026-05-22-confidence-and-provenance-design.md` for the design rationale and full vocabulary. Re-runs on a fully-filled set update zero rows.

Verify the trust invariant after the backfill:

```bash
psql "$PROJECTLENS_DATABASE_URL" -c \
  "SELECT COUNT(*) FROM edges WHERE provenance IS NULL OR confidence_class IS NULL;"
```

A non-zero result indicates a writer that is not yet attaching trust fields; add it to the writer table in `docs/internals.md` and re-run the backfill.

## Troubleshooting

| Symptom | Check | Fix |
|---|---|---|
| Missing repo path | CLI says `repository path required: use --repo flag or set repo_path in config`. | Pass `--repo`, set `PROJECTLENS_REPO_PATH`, or set `repo_path` in config. |
| DB connection failure | `connecting to database` error. | Start Docker Postgres, verify `PROJECTLENS_DATABASE_URL`, and confirm port `5433` is reachable. |
| Provider failure | Ollama probe failure or `connection refused` when embedding. | Confirm Ollama is running (`ollama serve`), the configured model is pulled, and `OLLAMA_ENDPOINT` is reachable. |
| Stale index | `index_status` shows old stage ages, wrong git commit, or dirty/stale state. | Run `make reindex` for code changes or `make index-all` for all stages. |
| Writer lock busy | Exit code 75 plus `another writer holds the lock: pid=<n> host=<h> cmd="<c>" started=<RFC3339>`. | Wait for the holder to finish. Use `projectlens unlock --force` only after confirming auto-recovery failed. |
| Missing TUI binary | TUI says `projectlens binary not found`. | Run `make build-projectlens`, set `PROJECTLENS_BINARY`, keep `projectlens` next to `projectlens-tui`, or add it to `PATH`. |
| Agent does not use ProjectLens | Agent sees no tools or greps first. | Confirm MCP server is running, agent config points at `/mcp`, and skills/hooks from `docs/AGENT_SETUP.md` are installed. |
| Edge trust NULLs in report | Edge Trust section shows non-zero `Unknown` column, or the NULL-count psql query above returns > 0. | Run `projectlens index-backfill-provenance`. If non-zero rows remain, a writer is not attaching trust fields — add it to the writer table in `docs/internals.md`. |
| Edge CHECK constraint violation | INSERT fails with `edges_confidence_class_check` or `edges_provenance_check`. | A writer is emitting a value outside the documented vocabulary. Fix the writer or extend the CHECK in a new migration (and document the new producer). |

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
