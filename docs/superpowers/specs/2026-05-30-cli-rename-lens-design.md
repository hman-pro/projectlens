# CLI Rename: `projectlens` → `lens`

**Date:** 2026-05-30
**Status:** Approved design (rev 2, incorporates self-review findings), ready for implementation plan
**Scope:** Command-surface rename only. Product name "ProjectLens" unchanged.

## Goal

Shorten the typed command from `projectlens` to `lens`. This is a
command-surface change: the three binaries, their `cmd/` source dirs, every
build/run target, and every user-facing string or doc that tells a human or
agent to *type* `projectlens`. Internal identity (env vars, database, Go module
path, product name, MCP server name) is intentionally left untouched to keep
blast radius small.

Backward compatibility: **hard cut**. No `projectlens` alias or symlink is
shipped. Public alpha with few users, so the clean break is acceptable — but
only valid if *every* generated hint, install path, and build path moves in the
same change (see §3, §4, §5).

## In Scope — what changes

### 1. Source directories (`git mv`)

`main` packages; nothing imports them, so renaming is clean. Build paths in
Makefile + Dockerfile + one integration test reference them.

| From | To |
|---|---|
| `cmd/projectlens` | `cmd/lens` |
| `cmd/projectlens-mcp` | `cmd/lens-mcp` |
| `cmd/projectlens-tui` | `cmd/lens-tui` |

### 2. Build (`Makefile`)

`build-projectlens` is a dependency of **far more than the run targets**. Every
one of these must be updated to depend on the renamed `build-lens`:

- Build defs: `build`, `build-projectlens`, `build-projectlens-mcp`,
  `build-projectlens-tui`, `.PHONY` line (`Makefile:35-47`).
- Run targets: `tui`, `mcp`, `cli` (`Makefile:75-82`).
- Convenience targets that all depend on `build-projectlens`: `bootstrap`,
  `reindex`, `reindex-full`, `reindex-dry`, `status`, `query`, `index-all`,
  `index-history`, `index-datastore`, `index-embed`, `index-summarize`,
  `graph-export`, `migrate` (`Makefile:92-133, 169`).
- Output path vars `CLI`/`MCP`/`TUI` → `bin/lens`, `lens-mcp`, `lens-tui`.
- `go build ... ./cmd/lens*` path args; help text `(uses projectlens migrate)`
  at `Makefile:169` → `lens migrate`.
- Leave: `PROJECTLENS_DATABASE_URL`, `PROJECTLENS_REPO_PATH`, the postgres URL
  user/db (`projectlens:projectlens@.../projectlens`).

### 3. Runtime user-facing guidance strings

A hard cut with no alias means **every emitted `projectlens <cmd>` hint becomes
a dead instruction**. These are operational guidance shown to users/agents, not
cosmetic comments. All must change to `lens <cmd>`, with matching test updates:

- **CLI**: `cmd/lens/main.go:237` — `Run 'projectlens bootstrap' first.`
- **Report degradation**: `internal/report/derive.go:12-16, 35` —
  `run projectlens reindex`, `index-summarize`, `index-embed`, `index-history`,
  `index-datastore`.
- **MCP tool responses / 503 hints**: `internal/mcpserver/handlers.go:348, 415,
  508, 578, 752` (`index-datastore`, `bootstrap`, `index-history`) and
  `internal/mcpserver/not_ready.go:16` (`projectlens migrate --project ...`).
- **TUI empty states**: `internal/tui/sections/health/view.go:20` and
  `internal/tui/sections/runs/view.go:22` — `run "projectlens bootstrap"`.
- Update any tests asserting these strings.

### 4. CLI resolver — the one behavioral break if missed

- `internal/tui/jobs/binary.go`: resolver looks for a sibling named
  `"projectlens"` and does `exec.LookPath("projectlens")`. Both → `"lens"`.
  Update doc comment + error/not-found strings.
- `internal/tui/app/update.go:202`: `WithHint(...)` binary-name string → `lens`.
- `cmd/lens/main.go`: cobra root `Use: "projectlens"` → `Use: "lens"`.
- Update `internal/tui/jobs/binary_test.go` to assert the `lens` name.

The `PROJECTLENS_BINARY` env var **name** stays (unchanged `PROJECTLENS_*`
prefix); only the resolver's fallback binary name becomes `lens`.

### 5. Package / source-path references (not typed commands, but go stale)

Once `cmd/projectlens*` moves, these break even though they aren't command
invocations:

- **README install paths**: `github.com/hman-pro/projectlens/cmd/projectlens@latest`,
  `cmd/projectlens-mcp`, `cmd/projectlens-tui` (`README.md:17-19`) →
  `.../cmd/lens@latest` etc. (module prefix stays, leaf dir changes).
- **Agent setup**: `go run ./cmd/projectlens/ unlock --force`
  (`docs/AGENT_SETUP.md:334`) → `./cmd/lens/`.
- **Operations / internals source refs**: `docs/operations.md:3`,
  `docs/internals.md:3, 74, 107, 203, 205` (`cmd/projectlens`,
  `cmd/projectlens/main.go::edgeProvenanceDefaults`, `cmd/projectlens/lock.go`).
- **Source-of-truth tables**: `CLAUDE.md:54-55` and `AGENTS.md:54-55`
  (`cmd/projectlens/*.go`, `cmd/projectlens/main.go`).
- **Integration test build path**: `internal/storage/writelock/cli_integration_test.go:27`
  `go build ... ../../../cmd/projectlens/` → `../../../cmd/lens/` (also the
  temp binary name at line 26 if it embeds `projectlens`).

### 6. Documentation — typed-command occurrences

Edit `projectlens` used as a **typed command** (`projectlens index ...` →
`lens index ...`) across `README.md`, `docs/operations.md` (owns CLI surface),
`docs/AGENT_SETUP.md`, `docs/internals.md`. Leave `PROJECTLENS_*`, DB strings,
module path, product prose "ProjectLens".

### 7. Agent assets

- CLI invocation examples in `agent/skills/use-projectlens/SKILL.md` and the
  `agent/claude/*` / `agent/codex/*` snippets that call the CLI binary.
- Skill **directory** name `use-projectlens` stays (skill id, not a command).

### 8. Scripts and Docker

- **`scripts/release-demo.sh`** (this is the script with binary invocations):
  `make build-projectlens-mcp` → `build-lens-mcp`, `./bin/projectlens-mcp` →
  `./bin/lens-mcp`, `/tmp/projectlens-mcp.log` → `lens-mcp.log`
  (`release-demo.sh:44, 45, 56`).
- **`scripts/release-smoke.sh`**: keep the module import path
  `github.com/hman-pro/projectlens/...` unchanged (it embeds a temp Go file,
  no CLI invocation).
- **`docker/Dockerfile`**: build `-o /bin/lens ./cmd/lens`,
  `-o /bin/lens-mcp ./cmd/lens-mcp`, the `COPY` lines, and
  `ENTRYPOINT ["/bin/lens"]`.
- **`docker/docker-compose.yml`**: change binary path only —
  `entrypoint: ["/bin/lens-mcp"]`. **Service names stay**
  (`projectlens-mcp`, `projectlens-indexer`) — decided, to avoid breaking
  `docker compose logs projectlens-mcp` workflows. Postgres
  `POSTGRES_DB`/`POSTGRES_USER`/`PROJECTLENS_DB_PASSWORD`, the `projectlens-data`
  volume, and `PROJECTLENS_DATABASE_URL` value all stay.

### 9. TUI title

`internal/tui/app/view.go:66` ` projectlens · dashboard ` →
` ProjectLens · dashboard ` — decided: keep the **product brand** (capitalized)
in the in-app title, since only the command changed, not the product.

## Out of Scope — stays `projectlens`

- Env var prefix `PROJECTLENS_*` (all ~15 vars, including `PROJECTLENS_BINARY`).
- Database user, name, and storage schema (`projectlens`); `projectlens-data`
  Docker volume.
- Docker Compose service names (`projectlens-mcp`, `projectlens-indexer`).
- Go module path `github.com/hman-pro/projectlens` (only `cmd/` leaf dirs move).
- MCP server name `"projectlens"` in `internal/mcpserver/server.go`.
- Product name "ProjectLens" in prose and the TUI title.
- `scripts/release-smoke.sh` module import path.

## Verification

- `make clean && make build` → `bin/lens`, `bin/lens-mcp`, `bin/lens-tui` exist;
  no stale `bin/projectlens*`.
- `make test` and `make vet` pass.
- `make test-int` (or a focused integration run of
  `internal/storage/writelock`) — the CLI integration test builds
  `../../../cmd/lens/` and is behind `//go:build integration`, so it is **not**
  covered by plain `make test`.
- `bin/lens --help` shows `lens` as the command name.
- TUI launches a job successfully — proves the §4 resolver fix.
- **Targeted audits** (plain `rg 'projectlens'` is too broad — it legitimately
  matches env vars, DB names, module path, MCP server identity, the volume,
  historical docs, skill ids, and product prose):
  - `rg -n '(^|[ ./"])projectlens( |$| [a-z-]|")'` — bare command-like guidance;
    expect zero hits that are typed-command instructions.
  - `rg -n 'cmd/projectlens|build-projectlens|/bin/projectlens|go run ./cmd/projectlens'`
    — moved package/build/binary paths; expect zero.
  - Allowlist pass: confirm every remaining `projectlens` hit is an intentional
    identity (`PROJECTLENS_*`, module path, DB/schema/user, compose service
    name, MCP server registration, skill id, or "ProjectLens" prose).

## Risks

- **TUI → CLI resolver** (§4) is the single real behavioral break. Covered by
  the resolver fix + `binary_test.go` update.
- **Dead hints after hard cut** (§3) — mitigated by the runtime-string pass; the
  bare-command audit is the safety net.
- **Stale binaries**: `make clean` before build keeps `bin/` tidy; an orphaned
  old `bin/projectlens` would only be found under its old name (harmless).
- **Compose volume**: `projectlens-data` deliberately untouched; renaming would
  orphan existing local data volumes.
