# Multi-Project Isolation Design

Date: 2026-05-25
Status: Draft approved in chat, pending written-spec review

## Summary

ProjectLens should support multiple projects while keeping each project's indexed
state isolated. The first version uses one Postgres database, one storage schema
per project, one MCP server process, and one MCP URL path per project.

The design intentionally does not add cross-project search, shared global
knowledge, or a database-backed project registry. Each coding project behaves
like a separate agent context.

## Goals

- Run ProjectLens for multiple local projects such as `ingest` and `projectlens`.
- Keep code facts, datastore facts, git history, chunks, embeddings, summaries,
  knowledge, run records, reports, exports, and MCP answers isolated by project.
- Serve all projects from one MCP server process.
- Use one Postgres database for local operations.
- Select projects through explicit CLI flags and MCP URL paths.
- Keep the first implementation close to the current single-project storage
  and query model.

## Non-Goals

- Cross-project search or comparison.
- Shared global knowledge across projects.
- A `public.projects` or other database-backed project registry.
- Automatic schema creation or migration from MCP server startup.
- Project switching inside one MCP endpoint.
- A full TUI multi-project dashboard in the first pass.
- Adding `project_id` to every table. Schema isolation is enough for v1;
  `project_id` would add index bloat and query risk without improving the
  isolated-agent workflow.
- Endpoint authentication beyond local-development assumptions.

## Architecture

Project isolation is implemented with one ProjectLens storage schema per
project. This spec uses `storage_schema` for the schema that stores ProjectLens
tables. This is distinct from `datastore_tables.schema_name`, which records the
schema name discovered inside a target database such as an application's
Postgres schema.

```text
Postgres database: projectlens

Schemas:
  ingest.files
  ingest.symbols
  ingest.chunks
  ingest.index_runs
  projectlens.files
  projectlens.symbols
  projectlens.chunks
  projectlens.index_runs

MCP:
  http://localhost:8484/ingest/mcp
  http://localhost:8484/projectlens/mcp
```

Each project has:

- a slug used by CLI flags and MCP URL paths,
- a storage schema,
- a repository path,
- optional project-specific indexing/provider config,
- isolated index state.

The MCP server loads the YAML project registry at startup. For every request,
it extracts the project slug from the path, resolves the project runtime, and
runs tool logic against that project's storage schema only. MCP tools do not
accept a `project` parameter.

## Project Registry

YAML is the only project registry for the first version.

Example:

```yaml
database_url: postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable
default_project: ingest

projects:
  - slug: ingest
    storage_schema: ingest
    repo_path: /Users/hamed.zohrehvand/source/example-org/ingest
    config_path: configs/ingest.yaml

  - slug: projectlens
    storage_schema: projectlens
    repo_path: /Users/hamed.zohrehvand/source/projectlens
    config_path: configs/projectlens.yaml
```

`config_path` is optional. It lets projects customize datastore scan paths,
provider settings, include/exclude patterns, and history settings without
duplicating the project registry.

`default_project` is optional. It is mainly for daily-use surfaces such as TUI
startup. Commands that mutate or report on storage should still display the
resolved project identity.

Validation rules:

- `slug` must be unique.
- `storage_schema` must be unique.
- `repo_path` is required.
- Slugs may contain lowercase letters, digits, `_`, and `-`.
- Storage schemas may contain lowercase letters, digits, and `_`, must not
  start with a digit, and must not be `public` or start with `pg_`.
- Slug-to-schema mapping is explicit. The implementation must not silently
  derive `projectlens` from `projectlens`; both `slug` and `storage_schema` are
  required in YAML so the storage boundary is visible.
- Storage schema names must never be interpolated from raw user input without
  validation and identifier quoting. Any SQL that substitutes a schema name must
  use `pgx.Identifier{storageSchema}.Sanitize()` after validation, or an
  equivalent identifier-quoting helper. Bind parameters are not valid for SQL
  identifiers such as `search_path`.

## CLI Behavior

Project selection becomes the normal CLI path:

```bash
projectlens migrate --project ingest
projectlens index-all --project ingest
projectlens status --project ingest
projectlens report --project projectlens
```

Resolution rules:

- `--project` resolves against `--projects`, defaulting to the standard project
  registry path.
- The project registry supplies `repo_path` and `storage_schema`.
- Project-specific config may be loaded from the project's `config_path`.
- Existing single-project `--repo` behavior stays available for scratch and
  backcompat use.
- If `--project` is present, the registry's `repo_path` and `storage_schema` are
  authoritative. A conflicting `--repo` should fail loudly instead of being
  ignored.
- If a project `config_path` also contains `repo_path`, the registry value wins.
  The project config supplies indexing/provider settings, not project identity.
- Unknown projects fail before opening storage or running indexers.

Every CLI subcommand that opens storage must accept `--project` and resolve
through the project registry, including incremental indexing, datastore
indexing, history indexing, embed/summarize, reports, export graph, knowledge
commands, status, TUI-launched jobs, and debug/maintenance commands. The CLI
should also add:

```bash
projectlens projects list
projectlens projects validate
```

Project removal is out of scope for the first implementation. The documented
manual escape hatch can be `DROP SCHEMA <storage_schema> CASCADE` after the
project is removed from YAML. A safer `projectlens projects archive/drop` command
can be designed later.

## MCP Routing

One MCP process serves all configured projects:

```bash
projectlens-mcp --projects configs/projects.yaml --port 8484
```

Endpoint shape:

```text
/{project}/mcp
```

Examples:

```text
/ingest/mcp
/projectlens/mcp
```

Request flow:

1. Parse `{project}` from the URL path.
2. Validate it exists in the YAML registry.
3. Resolve the project runtime: storage schema, repo path, project config, and
   DB pool.
4. Run the MCP handler using that runtime.
5. Return normal MCP responses scoped to that project.

No MCP tool accepts a project argument. The endpoint is the project boundary.
Each mounted `/{project}/mcp` endpoint should have its own MCP session manager
and project runtime. Sharing a session manager across project mounts is a leak
risk because Streamable HTTP sessions are endpoint-shaped.

Error behavior:

- Unknown project path returns HTTP `404` before entering MCP session handling.
- Known project with missing storage schema reports that the user must run
  `projectlens migrate --project <slug>`.
- One broken project must not prevent other project endpoints from serving.
- `index_status` and other freshness surfaces report only the selected project.

MCP startup must not create schemas or run migrations. It may validate project
configuration and report readiness problems.

The MCP endpoint is intended for local development and local agent integrations.
The first version does not add authentication. It should continue to bind to
local addresses by default and docs must not imply it is internet-safe.

## Database And Migrations

Migrations run per project storage schema through explicit CLI commands only:

```bash
projectlens migrate --project ingest
projectlens migrate --project projectlens
```

The migration runner should:

- create the project storage schema when missing,
- ensure the pgvector extension exists at database scope with
  `CREATE EXTENSION IF NOT EXISTS vector`,
- set the migration session's `search_path` to `<storage_schema>,public`,
- create or migrate all ProjectLens tables inside that schema,
- store migration bookkeeping in the project storage schema,
- avoid mutating project storage schemas from MCP startup.

The pgvector extension is database-global, not project-schema-local. The
`vector` and `halfvec` types are installed into `public` in the local setup, so
project migration and runtime connections must include `public` in
`search_path` after the storage schema. The extension creation statement remains
idempotent, but table creation must happen in the project storage schema.

Runtime DB access should use a project-scoped DB handle. The preferred first
implementation is one connection pool per project with the project storage
schema pinned at connection setup. Existing storage code can keep using
unqualified table names such as `files`, as long as the project runtime
guarantees the storage schema they resolve to.

For pgxpool, the intended shape is:

```go
quoted := pgx.Identifier{storageSchema}.Sanitize()
cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
    if err := assertSchemaExists(ctx, conn, storageSchema); err != nil {
        return err
    }
    _, err := conn.Exec(ctx, "SET search_path TO "+quoted+",public")
    return err
}
```

`assertSchemaExists` should query `information_schema.schemata` before setting
`search_path`. PostgreSQL accepts `SET search_path TO missing_schema`; without a
separate existence check, the first real query fails later with an opaque table
error. This fast-fail path should be used by CLI and MCP runtime creation and
should tell the user to run `projectlens migrate --project <slug>`.

Writer locks become project-local because every project storage schema has its
own `index_locks` table. `projectlens index-all --project ingest` and
`projectlens index-all --project projectlens` can run concurrently.

Project-local writer locks do not isolate external providers. Concurrent jobs
for different projects still share global resources such as Ollama capacity,
Anthropic/OpenAI rate limits, CPU, memory, and the single Postgres instance.

## Safety Invariants

- Project ambiguity fails early.
- MCP tools never accept `project`.
- Storage code should receive a project-scoped DB handle, not raw schema
  strings.
- No query should use an unvalidated user-provided schema identifier.
- No request to one project falls back to another project.
- Reports, exports, TUI/status surfaces, and `index_status` should show the
  active project slug and storage schema.
- Storage-schema-local migrations and writer locks are required before indexing
  a project.
- Every structured log line emitted from a project request or indexer job should
  carry `project_slug` and `storage_schema` so interleaved multi-project logs can
  be audited.

## Testing

Focused coverage should prove isolation first.

Tests should cover:

- Project registry parsing and validation:
  - valid slugs and storage schemas,
  - duplicate slugs,
  - duplicate storage schemas,
  - missing repo paths,
  - unknown project lookups.
- Migration behavior:
  - two project storage schemas can be created in one database,
  - both storage schemas receive the expected tables independently,
  - migration bookkeeping is storage-schema-local,
  - `CREATE EXTENSION vector` is idempotent and does not create project-local
    extension objects,
  - `search_path` includes the project storage schema and `public`.
- Storage isolation:
  - inserts into `ingest.files` are invisible from the `projectlens` runtime,
  - `symbols`, `index_runs`, and `knowledge_entries` do not cross schemas.
- Connection-pool isolation:
  - a connection borrowed for project A, returned, recycled, and then borrowed
    for project B still uses project B's `search_path`,
  - a missing storage schema fails during pool/runtime initialization.
- CLI behavior:
  - `--project ingest` resolves repo path and storage schema,
  - unknown projects fail clearly,
  - backcompat `--repo` still works for single-project scratch use.
- MCP behavior:
  - `/ingest/mcp` and `/projectlens/mcp` resolve different runtimes,
  - unknown project paths fail clearly,
  - one broken project does not break another endpoint.
- Output behavior:
  - `status`, `report`, `export graph`, and `index_status` include active
    project identity.

## Rollout Sequence

1. Add schema-aware DB connection and migration primitives with direct
   storage-schema inputs.
2. Add project registry config parsing and validation.
3. Wire CLI `--project` into every storage-opening command.
4. Wire MCP path routing into project runtimes.
5. Update docs and examples.
6. Keep old single-project `--repo` behavior until the new path is stable.

## Implementation Decisions

- The default project registry path is `configs/projects.yaml`.
- Project-specific `config_path` loads a normal single-project config, then the
  project registry overlays `repo_path` and `storage_schema`. This preserves
  existing provider and indexing config behavior while making project identity
  explicit.
- Existing single-project installs that do not provide `--projects` keep using
  the current `public` schema as an implicit legacy project. Named projects
  cannot use `public` as their `storage_schema`. A later migration can copy
  `public` into a named schema, but that cutover is not required for v1.
- Project-scoped pools pin `search_path` with pgxpool `AfterConnect`, schema
  existence checks, and `pgx.Identifier` quoting as described above.
- TUI should use the same project-resolution path as CLI commands. It should
  use `default_project` when present, require `--project` when multiple projects
  are configured without a default, and still allow the legacy single-project
  config path when no project registry is in use.
