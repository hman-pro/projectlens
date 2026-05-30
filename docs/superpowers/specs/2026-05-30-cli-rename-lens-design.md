# CLI Rename: `projectlens` → `lens`

**Date:** 2026-05-30
**Status:** Approved design, ready for implementation plan
**Scope:** Command-surface rename only. Product name "ProjectLens" unchanged.

## Goal

Shorten the typed command from `projectlens` to `lens`. This is a cosmetic
command-surface change. Internal identity (env vars, database, Go module,
product name, MCP server name) is intentionally left untouched to keep blast
radius small.

Backward compatibility: **hard cut**. No `projectlens` alias or symlink is
shipped. The project is in public alpha with few users, so the clean break is
acceptable.

## In Scope — what changes

### 1. Source directories (`git mv`)

These are `main` packages; nothing imports them, so renaming is clean. Only the
Makefile and Dockerfile reference the build paths.

| From | To |
|---|---|
| `cmd/projectlens` | `cmd/lens` |
| `cmd/projectlens-mcp` | `cmd/lens-mcp` |
| `cmd/projectlens-tui` | `cmd/lens-tui` |

### 2. Build (`Makefile`)

- Output path vars `CLI`/`MCP`/`TUI` → `$(BIN_DIR)/lens`, `lens-mcp`, `lens-tui`.
- Targets `build-projectlens`, `build-projectlens-mcp`, `build-projectlens-tui`
  → `build-lens`, `build-lens-mcp`, `build-lens-tui` (including `.PHONY`,
  `build` aggregate, and `go build ... ./cmd/lens*` path args).
- Run targets `cli` / `mcp` / `tui` dependency lists.
- Leave: `PROJECTLENS_DATABASE_URL`, `PROJECTLENS_REPO_PATH`, the postgres URL
  user/db (`projectlens:projectlens@.../projectlens`).

### 3. Runtime correctness (the only behavioral break if missed)

- `internal/tui/jobs/binary.go`: the CLI resolver looks for a sibling named
  `"projectlens"` and does `exec.LookPath("projectlens")`. Both → `"lens"`.
  Update the doc comment and error/not-found strings to match.
- `internal/tui/app/update.go:202`: `WithHint(...)` string referencing the
  `projectlens` binary name → `lens`.
- `cmd/lens/main.go`: cobra root `Use: "projectlens"` → `Use: "lens"`.

The `PROJECTLENS_BINARY` env var **name** stays (it belongs to the unchanged
`PROJECTLENS_*` prefix); only its conceptual default value — the binary name
the resolver falls back to — becomes `lens`.

### 4. Documentation (surgical)

Edit only occurrences of `projectlens` used as a **typed command**
(e.g. `projectlens index ...` → `lens index ...`). Files:

- `README.md`
- `docs/operations.md` (owns CLI surface)
- `docs/AGENT_SETUP.md`
- `docs/internals.md`

Leave untouched in these files: `PROJECTLENS_*` env vars, DB connection
strings, the Go module path, and the product name "ProjectLens" in prose.

### 5. Agent assets

- CLI invocation examples in `agent/skills/use-projectlens/SKILL.md` and the
  `agent/claude/*` / `agent/codex/*` snippets that call the CLI binary.
- The skill **directory** name `use-projectlens` stays — it is a skill id, not
  a typed command.

### 6. Scripts and Docker

- `docker/Dockerfile`: `go build -o /bin/lens ./cmd/lens`,
  `-o /bin/lens-mcp ./cmd/lens-mcp`, the `COPY` lines, and
  `ENTRYPOINT ["/bin/lens"]`.
- `docker/docker-compose.yml`: binary-referencing parts only —
  `entrypoint: ["/bin/lens-mcp"]` and service names that map to binaries
  (`projectlens-mcp`, `projectlens-indexer` → `lens-mcp`, `lens-indexer`).
  Leave the postgres `POSTGRES_DB`/`POSTGRES_USER`/`PROJECTLENS_DB_PASSWORD`,
  the `projectlens-data` volume, and `PROJECTLENS_DATABASE_URL` value.
- `scripts/release-smoke.sh`: any CLI invocation `projectlens <cmd>` → `lens`.
  Leave the module import path `github.com/hman-pro/projectlens/...` (line 49).

## Out of Scope — stays `projectlens`

- Env var prefix `PROJECTLENS_*` (all ~15 vars, including the name
  `PROJECTLENS_BINARY`).
- Database user, database name, and storage schema (`projectlens`).
- Go module path `github.com/hman-pro/projectlens`.
- MCP server name `"projectlens"` in `internal/mcpserver/server.go`
  (agent-facing server identity, not a typed command).
- Product name "ProjectLens" in all prose.

## Verification

- `make build` produces `bin/lens`, `bin/lens-mcp`, `bin/lens-tui`; no stale
  `bin/projectlens*` (run `make clean` first).
- `make test` and `make vet` pass.
- `bin/lens --help` shows `lens` as the command name.
- TUI launches a job successfully — proves the `binary.go` resolver fix.
  Update `internal/tui/jobs/binary_test.go` to assert the `lens` name.
- Audit: `rg 'projectlens'` — every remaining hit is an env var, DB string,
  module path, MCP server name, or the "ProjectLens" product name. Zero
  occurrences of `projectlens` used as a bare typed command.

## Risks

- **TUI → CLI resolver** is the single real break. Covered by §3 plus the
  `binary_test.go` update.
- **Stale binaries**: an old `bin/projectlens` left next to `bin/lens-tui`
  would be found by the sibling lookup under the *old* name only — harmless
  after rename, but `make clean` keeps `bin/` tidy.
- **Docker volume name**: `projectlens-data` is deliberately left as-is;
  renaming it would orphan existing local data volumes. Out of scope.
