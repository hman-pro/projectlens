# Multi-Project Isolation Design

Date: 2026-05-25
Status: Draft approved in chat, pending written-spec review

## Summary

ProjectLens should support multiple projects while keeping each project's indexed
state isolated. The first version uses one Postgres database, one schema per
project, one MCP server process, and one MCP URL path per project.

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
- Adding `project_id` to every table.

## Architecture

Project isolation is implemented with one Postgres schema per project.

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
- a Postgres schema,
- a repository path,
- optional project-specific indexing/provider config,
- isolated index state.

The MCP server loads the YAML project registry at startup. For every request,
it extracts the project slug from the path, resolves the project runtime, and
runs tool logic against that project's schema only. MCP tools do not accept a
`project` parameter.

## Project Registry

YAML is the only project registry for the first version.

Example:

```yaml
database_url: postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable

projects:
  - slug: ingest
    schema: ingest
    repo_path: /Users/hamed.zohrehvand/source/example-org/ingest
    config_path: configs/ingest.yaml

  - slug: projectlens
    schema: projectlens
    repo_path: /Users/hamed.zohrehvand/source/projectlens
    config_path: configs/projectlens.yaml
```

`config_path` is optional. It lets projects customize datastore scan paths,
provider settings, include/exclude patterns, and history settings without
duplicating the project registry.

Validation rules:

- `slug` must be unique.
- `schema` must be unique.
- `repo_path` is required.
- Slugs may contain lowercase letters, digits, `_`, and `-`.
- Schemas may contain lowercase letters, digits, and `_`.
- Schema names must never be interpolated from raw user input without
  validation.

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
- The project registry supplies `repo_path` and `schema`.
- Project-specific config may be loaded from the project's `config_path`.
- Existing single-project `--repo` behavior stays available for scratch and
  backcompat use.
- If `--project` and `--repo` are both present, the project registry wins by
  default. A separate explicit override flag can be added later if needed.
- Unknown projects fail before opening storage or running indexers.

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
3. Resolve the project runtime: schema, repo path, project config, and DB pool.
4. Run the MCP handler using that runtime.
5. Return normal MCP responses scoped to that project.

No MCP tool accepts a project argument. The endpoint is the project boundary.

Error behavior:

- Unknown project path returns HTTP `404` before entering MCP session handling.
- Known project with missing schema reports that the user must run
  `projectlens migrate --project <slug>`.
- One broken project must not prevent other project endpoints from serving.
- `index_status` and other freshness surfaces report only the selected project.

MCP startup must not create schemas or run migrations. It may validate project
configuration and report readiness problems.

## Database And Migrations

Migrations run per project schema through explicit CLI commands only:

```bash
projectlens migrate --project ingest
projectlens migrate --project projectlens
```

The migration runner should:

- create the project schema when missing,
- set the migration session's `search_path` to the project schema,
- create or migrate all ProjectLens tables inside that schema,
- store migration bookkeeping in the project schema,
- avoid mutating project schemas from MCP startup.

Runtime DB access should use a project-scoped DB handle. The preferred first
implementation is one connection pool per project with the project schema pinned
at connection setup. Existing storage code can keep using unqualified table
names such as `files`, as long as the project runtime guarantees the schema
they resolve to.

Writer locks become project-local because every project schema has its own
`index_locks` table. `projectlens index-all --project ingest` and
`projectlens index-all --project projectlens` can run concurrently.

## Safety Invariants

- Project ambiguity fails early.
- MCP tools never accept `project`.
- Storage code should receive a project-scoped DB handle, not raw schema
  strings.
- No query should use an unvalidated user-provided schema identifier.
- No request to one project falls back to another project.
- Reports, exports, TUI/status surfaces, and `index_status` should show the
  active project slug and schema.
- Schema-local migrations and writer locks are required before indexing a
  project.

## Testing

Focused coverage should prove isolation first.

Tests should cover:

- Project registry parsing and validation:
  - valid slugs and schemas,
  - duplicate slugs,
  - duplicate schemas,
  - missing repo paths,
  - unknown project lookups.
- Migration behavior:
  - two project schemas can be created in one database,
  - both schemas receive the expected tables independently,
  - migration bookkeeping is schema-local.
- Storage isolation:
  - inserts into `ingest.files` are invisible from the `projectlens` runtime,
  - `symbols`, `index_runs`, and `knowledge_entries` do not cross schemas.
- CLI behavior:
  - `--project ingest` resolves repo path and schema,
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

1. Add project registry config parsing and validation.
2. Add schema-aware DB connection and migration support.
3. Wire CLI `--project` into existing commands.
4. Wire MCP path routing into project runtimes.
5. Update docs and examples.
6. Keep old single-project `--repo` behavior until the new path is stable.

## Implementation Decisions

- The default project registry path is `configs/projects.yaml`.
- Project-specific `config_path` loads a normal single-project config, then the
  project registry overlays `repo_path` and schema. This preserves existing
  provider and indexing config behavior while making project identity explicit.
- Project-scoped pools should pin `search_path` through connection initialization
  or an equivalent pgxpool hook. Tests must prove a connection borrowed for one
  project cannot observe another project's tables.
- TUI should use the same project-resolution path as CLI commands. It should
  require `--project` once multiple projects are configured, while still allowing
  the legacy single-project config path when no project registry is in use.
