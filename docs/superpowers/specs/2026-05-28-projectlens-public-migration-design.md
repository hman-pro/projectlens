# ProjectLens Public Migration Design

Date: 2026-05-28
Status: Approved direction, pending implementation plan

## Purpose

Prepare the private `projectlens` repository for a public alpha release as
`ProjectLens` under `github.com/hman-pro/projectlens`.

The public release should serve two goals:

- Portfolio credibility: show a polished, understandable local codebase memory
  system.
- Installability: let a motivated user run the tool against the repository
  itself with local open-weight models.

The release is not trying to become a full community project in this pass.

## Public Identity

The public product name is `ProjectLens`.

The public repository and Go module path are:

```text
github.com/hman-pro/projectlens
```

The current `ProjectLens` / `projectlens` identity should be replaced in
user-facing docs, command names, binary names, install paths, agent snippets,
Docker service names where practical, and generated examples.

Package names under `internal/` should keep their local names such as
`config`, `storage`, `mcpserver`, and `projects`. These are implementation
package names, not product identity.

## Command And Module Migration

The Go module changes from:

```text
github.com/hman-pro/projectlens
```

to:

```text
github.com/hman-pro/projectlens
```

All internal imports should be rewritten to the new module path.

Command directories and binaries should move from:

```text
cmd/projectlens       -> cmd/projectlens
cmd/projectlens-mcp   -> cmd/projectlens-mcp
cmd/projectlens-tui   -> cmd/projectlens-tui
```

The Makefile, Docker files, docs, agent snippets, and TUI job runner should use
the new command names:

```text
projectlens
projectlens-mcp
projectlens-tui
```

The first public install path should be:

```bash
go install github.com/hman-pro/projectlens/cmd/projectlens@latest
```

## Local-Only Open-Weight Defaults

ProjectLens should be fully local by default. The first-run quick start should
not require a large local LLM download.

Default embedding provider:

```yaml
embeddings:
  provider: ollama
  model: qwen3-embedding:0.6b
  dimensions: 1024
  endpoint: http://localhost:11434
```

Default summarization:

```yaml
summarization:
  enabled: false
```

`qwen3-embedding:0.6b` is the preferred public embedding default because it is
small enough for a public quick start, has a 32K context window, and supports a
1024-dimensional output contract. Larger local quality tiers can be documented
as `qwen3-embedding:4b` and `qwen3-embedding:8b`.

Package summarization is disabled by default for public quick start. The
smallest Qwen3-Coder Ollama tag is `qwen3-coder:30b`, which is a 19GB download
with a 256K context window. It is too heavy for the default demo path. Document
it as the quality opt-in profile:

```yaml
summarization:
  enabled: true
  provider: ollama
  model: qwen3-coder:30b
  endpoint: http://localhost:11434
```

The implementation must add an Ollama package summarizer. The current code only
supports Ollama embeddings plus remote summarizers.

The public-alpha codebase should remove remote summarizer provider code and
dependencies. Remote provider compatibility can be reconsidered later as an
explicit non-default extension, but it is out of scope for the first public
release.

The implementation plan must audit and update the provider blast radius:

- `cmd/projectlens` provider construction and report/status inspector wiring,
- `cmd/projectlens-mcp` provider probing,
- `internal/providers/*`,
- `internal/summaries/*`,
- `internal/tui/*` provider display/preflight text,
- `configs/*.yaml`,
- provider-related tests,
- `go.mod` and `go.sum`.

The config model must use `summarization.enabled: false` as the disabled
summarization contract. Disabled summarization must not make `index-all`,
`status`, `report`, MCP provider probing, or the TUI look degraded. Status,
report, MCP provider health, and TUI config views should render the disabled
state as `summarization: disabled`.

Ollama summarization test requirements:

- Unit test with an `httptest.Server` that verifies the request shape and
  returns a deterministic summary.
- Error-path test for non-200 and malformed responses.
- Identity/probe test so report/status output shows `ollama/qwen3-coder:30b`.
- Disabled-state test so report/status output shows `summarization: disabled`.
- Optional integration test gated by `PROJECTLENS_OLLAMA_ENDPOINT` or an
  explicit integration tag.

The storage schema currently uses `halfvec(1024)`. The public default must keep
embedding output at 1024 dimensions by sending `dimensions: 1024` to Ollama's
`/api/embed` request and by rejecting or clearly failing any embedding response
whose vector length is not 1024.

Release acceptance criteria:

- The Ollama embedding client sends the configured `dimensions` field in the
  request body.
- A unit test asserts that `dimensions: 1024` is serialized for
  `qwen3-embedding:0.6b`.
- A local smoke test embeds one string and verifies `len(vector) == 1024`.
- The smoke test writes and queries a `halfvec(1024)` row before release.

## Demo Target

The public quick start should index ProjectLens itself.

Examples should use:

```bash
make index-all REPO=.
```

or the project-registry equivalent with:

```yaml
default_project: projectlens
projects:
  - slug: projectlens
    storage_schema: projectlens
    repo_path: .
    config_path: configs/index.yaml
```

Docs should not use private repositories, private local paths, private company
names, or private domain concepts as the default examples.

## Environment And Runtime Names

Public docs and examples should use `PROJECTLENS_*` variables for
ProjectLens-specific configuration.

Required public variable names:

| Old/private name | Public name |
|---|---|
| `PROJECTLENS_REPO_PATH` | `PROJECTLENS_REPO_PATH` |
| `PROJECTLENS_DB_PASSWORD` | `PROJECTLENS_DB_PASSWORD` |
| `PROJECTLENS_DB_PORT` | `PROJECTLENS_DB_PORT` |
| `PROJECTLENS_MCP_PORT` / `MCP_PORT` | `PROJECTLENS_MCP_PORT` |
| `PROJECTLENS_MCP_URL` | `PROJECTLENS_MCP_URL` |
| `PROJECTLENS_BINARY` | `PROJECTLENS_BINARY` |
| `PROJECTLENS_TUI_LOG_FILE` | `PROJECTLENS_TUI_LOG_FILE` |
| `PROJECTLENS_TUI_RUNS_DIR` | `PROJECTLENS_TUI_RUNS_DIR` |
| `PROJECTLENS_DEBUG_HOLD_LOCK` | `PROJECTLENS_DEBUG_HOLD_LOCK` |
| `CONFIG_PATH` | `PROJECTLENS_CONFIG` |
| `PROJECTS_PATH` | `PROJECTLENS_PROJECTS` |
| `PROJECT` | `PROJECTLENS_PROJECT` |
| `REPO_PATH` | `PROJECTLENS_REPO_PATH` |
| `DATABASE_URL` | `PROJECTLENS_DATABASE_URL` |
| `OLLAMA_ENDPOINT` | `PROJECTLENS_OLLAMA_ENDPOINT` |

The public implementation should drop generic runtime aliases from default
lookup. Use `PROJECTLENS_DATABASE_URL`, `PROJECTLENS_REPO_PATH`,
`PROJECTLENS_CONFIG`, `PROJECTLENS_PROJECTS`, `PROJECTLENS_PROJECT`, and
`PROJECTLENS_OLLAMA_ENDPOINT` instead of generic names. This avoids precedence
rules and collisions with other local tools. `PROJECTLENS_*` names and `~/.projectlens`
paths should not appear in the public alpha.

`.env.example` must contain only public, local defaults:

- `PROJECTLENS_DATABASE_URL`
- `PROJECTLENS_REPO_PATH=.`
- `PROJECTLENS_OLLAMA_ENDPOINT=http://localhost:11434`
- `PROJECTLENS_DB_PASSWORD`
- `PROJECTLENS_DB_PORT`
- `PROJECTLENS_MCP_PORT`
- optional TUI paths under `~/.projectlens/`

Default local storage names should become:

```text
Postgres database: projectlens
Postgres user: projectlens
Docker volume: projectlens-data
Default project slug: projectlens
Default storage schema: projectlens
TUI run logs: ~/.projectlens/tui-runs
Fallback temp logs: projectlens-tui-runs
```

## Documentation Migration

All docs should be reviewed and updated for public alpha:

- `README.md` becomes the ProjectLens product entrypoint.
- `docs/operations.md` owns CLI, TUI, Docker, migrations, reports, and
  troubleshooting.
- `docs/architecture.md` owns runtime and data-flow diagrams.
- `docs/internals.md` owns storage and implementation architecture.
- `docs/AGENT_SETUP.md` owns agent wiring.
- `CLAUDE.md` becomes maintainer guidance for ProjectLens, not private target
  repo guidance. Public `CLAUDE.md` should treat ProjectLens itself as the demo
  indexed repo and must remove private target-repo references.
- Planning docs that contain private examples should either be removed from the
  public history or rewritten/generalized if they remain useful.

The public docs must clearly state the data boundary:

- Source repositories are read locally.
- Postgres storage is local.
- Default embeddings use local Ollama models.
- Summaries are disabled by default; if enabled, public-alpha summaries use
  local Ollama models.
- Generated reports and graph exports are egress surfaces if the user shares
  them.
- Optional remote providers are not part of the default public-alpha path.

README install docs should include a binary matrix:

| Binary | Install |
|---|---|
| CLI | `go install github.com/hman-pro/projectlens/cmd/projectlens@latest` |
| MCP server | `go install github.com/hman-pro/projectlens/cmd/projectlens-mcp@latest` |
| TUI | `go install github.com/hman-pro/projectlens/cmd/projectlens-tui@latest` |

The quick start may still prefer a local clone plus `make build` so users get
all three binaries consistently.

## Branding Sweep

The implementation plan must include a whole-repo branding sweep, not only docs
and command directories.

Required sweep targets:

- Go module path and all internal imports.
- Cobra command `Use` strings, help text, status headings, suggested actions,
  and error messages.
- MCP server name string currently registered as `projectlens`.
- Agent MCP server names in Claude/Codex snippets. Public snippets should use
  `projectlens`, producing client-side tool prefixes such as
  `mcp__projectlens__find_symbol`.
- Skill names and prose. The public mandatory skill should become
  `use-projectlens` unless the plan intentionally keeps `use-projectlens` as a
  private-only artifact.
- TUI title strings, empty-state hints, binary lookup errors, log paths, and
  tests.
- Report/degradation suggestions such as `run projectlens reindex`.
- Docker service names, compose variables, DB/user/volume defaults, and image
  entrypoints.
- Config examples and project registry examples.
- Script names/help text, including graph export helpers.
- Test fixtures that embed old names, paths, or database names.

Existing agents will need re-wiring. The public alpha will not provide a
client-side MCP server-name alias for `projectlens`; `docs/AGENT_SETUP.md` must
document the new `projectlens` server name and explain that old MCP tool prefixes
change when the client config name changes.

## Artifact Policy

Generated artifacts should not be committed.

Add or keep ignore rules for:

```gitignore
artifacts/
*.graphml
projectlens-graph*.json
projectlens-graph*.json
.understand-anything/
```

Tracked generated artifacts such as graph JSON or GraphML exports should be
moved to `artifacts/` only as local working files. They must be removed from the
public tree and from public git history.

## History Rewrite

The private repository remains the archival source of truth.

The public release should be produced from a rewritten public branch or mirror.
The rewrite should:

- Preserve useful project history where practical.
- Remove tracked generated artifacts from every public commit.
- Remove private repository names, private company names, private local paths,
  and private indexed data from every public commit.
- Remove legacy remote-provider references and dependency footprint from public
  history when those providers are removed from public code.
- Rewrite commit messages to a clean one-line style.

Use `git-filter-repo` for the public mirror. BFG is too coarse for the required
string and message rewrites, and a new orphan branch loses more history than
necessary.

The filter-repo run should include:

- path removal for tracked generated graph/export artifacts and private scratch
  directories,
- replacement text rules for private names, private paths, old module path,
  old binary names, old DB names, and legacy remote-provider names,
- a commit-message callback that keeps one clean subject line and applies the
  same replacement rules,
- author metadata review if any private email/name should not be public.

If a post-filter scan still finds private data, prefer another filter pass. Use
an orphan single-commit public branch only as the fallback if filtering cannot
prove the rewritten history clean.

History rewrite should happen last: first make public `HEAD` build and pass
tests in the private repository, then create the rewritten mirror, then scan the
rewritten mirror before publishing.

All commit SHAs will change. That is acceptable for the public mirror.

Old intermediate commits may not all build after aggressive cleanup. The release
requirement is that public `HEAD` builds, tests, and documents the intended
product correctly.

Before publishing, run history-level scans against the rewritten mirror for:

- private organization and repository names,
- private local paths,
- generated graph/export filenames,
- API key patterns,
- legacy remote-provider names,
- old module path and binary names.

## Public Repository Metadata

Add public-alpha metadata:

- `LICENSE` with MIT license text.
- `SECURITY.md` with local-data and vulnerability-reporting guidance.
- `CONTRIBUTING.md` with lightweight contributor setup and test commands.
- `.github/workflows/ci.yml` running formatting/build/test/vet checks.
- `.github/ISSUE_TEMPLATE/` with a minimal bug report and feature request.

Release automation and package managers are out of scope for the first public
alpha unless they become necessary during release prep.

`SECURITY.md` should point reporters to GitHub private vulnerability reporting
if enabled, otherwise to a public contact email chosen before release.

## CI

The first public CI workflow should use the current Go version from `go.mod`.

Required jobs:

- `gofmt` check.
- `go test ./...`.
- `go vet ./...`.
- `make build`.

Integration tests that require Postgres or Ollama should remain opt-in in the
first public alpha. A later workflow can add a Postgres service container for
`go test -tags integration ./...`, but that is not required for the first public
release.

## Verification

Public `HEAD` must pass:

```bash
go test ./...
go vet ./...
make build
```

Public demo path must pass on a clean machine with Ollama running:

```bash
ollama pull qwen3-embedding:0.6b
cp .env.example .env
cd docker && docker compose up -d
cd ..
make migrate
make index-all REPO=.
make build-projectlens-mcp
./bin/projectlens-mcp
```

This assumes Ollama is already installed and running on the host. The default
demo path does not pull or require the optional summarization model.

The final release checklist must include a clean-history scan before the GitHub
repository is made public. It must also confirm whether GitHub private
vulnerability reporting is enabled; if not, `SECURITY.md` must include a public
contact email.

Existing private/local users should wipe and reindex for the public alpha. No
`projectlens` to `projectlens` database migration is required for this release:
drop the old local schema/database and rerun `projectlens migrate` plus
`projectlens index-all` against the new `projectlens` schema.

Implementation sequencing:

1. Add local provider support and tests on the current private tree: disabled
   summarization, Ollama summarizer, the 1024-dimensional embedding contract,
   and removal of remote summarizer code, dependencies, and tests.
2. Rename module, commands, binaries, env vars, runtime paths, docs, agent
   snippets, and branding.
3. Add public metadata and CI.
4. Verify public `HEAD` with unit checks and local demo smoke tests.
5. Rewrite history into the public mirror with `git-filter-repo`.
6. Build and test the rewritten mirror `HEAD`, run final history scans, then
   publish.

## Non-Goals

- Full community governance.
- Release automation.
- Homebrew or package-manager distribution.
- Remote hosted service mode.
- Private-data migration into the public repo.
- Preserving every private intermediate commit exactly as-is.
- Third-party license aggregation or a generated NOTICE file; defer until after
  public alpha unless GitHub/license scanning flags a blocker.

## Resolved Decisions

- Public name: `ProjectLens`.
- Public GitHub owner: `hman-pro`.
- Public module path: `github.com/hman-pro/projectlens`.
- Public defaults: fully local, open-weight Ollama embeddings with
  summarization disabled for the quick start.
- Public alpha excludes remote provider integrations.
- Old binary names do not need public compatibility aliases.
- Private archival history remains private; public history is rewritten.
