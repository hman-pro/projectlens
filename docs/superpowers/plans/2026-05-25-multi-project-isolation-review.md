# Multi-Project Isolation Plan Review

Date: 2026-05-25
Target: `docs/superpowers/plans/2026-05-25-multi-project-isolation.md`
Spec: `docs/superpowers/specs/2026-05-25-multi-project-isolation-design.md`

## Verdict

Not ready to execute as-is. The storage-schema foundation is directionally solid, but the plan leaves several user-facing entry points on the legacy `public` path and misses required behavior for broken MCP projects and the TUI. If executed literally, multi-project isolation would be partial: some commands and surfaces would still read or mutate the wrong schema.

ProjectLens MCP index was stale and returned unrelated code results during this review, so findings below are grounded in direct reads from the current checkout.

## Findings

### 1. Blocking: project resolution is not wired into every storage-opening CLI command

The spec requires every CLI subcommand that opens storage to accept `--project` and resolve through the registry, including reports, export graph, knowledge commands, status, TUI-launched jobs, and debug/maintenance commands (`docs/superpowers/specs/2026-05-25-multi-project-isolation-design.md:152`). The plan's main CLI wiring task only changes persistent flags, `loadCmdConfig`, and the write-lock wrappers (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:1183`), while Task 15 only adds identity output for `status`, `report`, `export graph`, and `index_status` (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:2031`).

Current code has many storage-opening paths that do not use the lock wrappers:

- `query` calls `loadCmdConfig` then `storage.Connect` directly (`cmd/projectlens/main.go:420`, `cmd/projectlens/main.go:425`).
- `status` does the same (`cmd/projectlens/main.go:208`, `cmd/projectlens/main.go:213`).
- `report` does the same (`cmd/projectlens/report.go:38`, `cmd/projectlens/report.go:42`).
- `export graph` does the same (`cmd/projectlens/export.go:44`, `cmd/projectlens/export.go:48`).
- every knowledge subcommand does the same (`cmd/projectlens/knowledge.go:33`, `cmd/projectlens/knowledge.go:37`, `cmd/projectlens/knowledge.go:77`, `cmd/projectlens/knowledge.go:81`, `cmd/projectlens/knowledge.go:113`, `cmd/projectlens/knowledge.go:117`, `cmd/projectlens/knowledge.go:150`, `cmd/projectlens/knowledge.go:154`).
- `unlock` still opens storage through the legacy config path (`cmd/projectlens/lock.go:115`, `cmd/projectlens/lock.go:123`).

Executing the plan would make the mutating index commands project-aware, but read/report/knowledge/maintenance paths would still hit `public`. That violates the isolation guarantee and can surface or delete data from the wrong project. Add a shared helper that returns either a project runtime or legacy DB/config/repo tuple, then migrate every direct `storage.Connect` call and add CLI tests for each command family.

### 2. Blocking: `migrate --project` bypasses the `--project`/`--repo` conflict check

The spec says `--project` makes the registry's `repo_path` and `storage_schema` authoritative and a conflicting `--repo` must fail loudly (`docs/superpowers/specs/2026-05-25-multi-project-isolation-design.md:145`). The plan implements that conflict check only inside `resolveProjectRuntime` (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:1206`), but `migrate --project` does not call that resolver. Task 9 branches on `--project` and calls `migrateProjectSchema` directly (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:1430`), and `migrateProjectSchema` loads the registry without checking `--repo` (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:1384`).

That leaves `projectlens migrate --project ingest --repo /wrong/path` silently accepted. Even if `repo_path` is not needed for migrations today, accepting a forbidden flag combination creates inconsistent CLI semantics right at the command users run first. Make the conflict check central, not resolver-local, or have `migrateProjectSchema` call the same validation path before opening storage.

### 3. Blocking: broken configured MCP projects are skipped, so known project requests become 404s

The spec distinguishes unknown projects from known-but-not-ready projects: unknown project paths return HTTP 404, while a known project with a missing storage schema reports that the user must run `projectlens migrate --project <slug>` (`docs/superpowers/specs/2026-05-25-multi-project-isolation-design.md:203`). The plan's multi-project startup resolves each runtime and, on error, logs a warning and `continue`s without registering any route for that project (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:1729`).

That means a configured project whose schema is missing falls through the mux like an unknown project. The user gets a 404 instead of the actionable migration error required by the spec. Task 13 is titled "unknown-project 404 + broken-project tolerance", but the test only exercises `/nope/mcp` and never checks a configured broken project (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:1885`).

Keep a route for configured-but-broken projects that returns a clear readiness error, or resolve the runtime lazily per request so known projects can report their specific failure. Add a test where the registry contains project `b` with a missing schema and `/b/mcp` does not return unknown-project 404.

### 4. Blocking: TUI project resolution is explicitly omitted despite being a spec requirement

The design allows deferring a full multi-project dashboard, but it still requires TUI to use the same project-resolution path as CLI commands, use `default_project` when present, require `--project` when multiple projects have no default, and keep legacy single-project mode when no registry is in use (`docs/superpowers/specs/2026-05-25-multi-project-isolation-design.md:353`). It also lists TUI/status surfaces in the active project identity invariant (`docs/superpowers/specs/2026-05-25-multi-project-isolation-design.md:284`).

The plan calls this out as a gap instead of scheduling it: "TUI integration is NOT included in this plan" (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:2141`). Current TUI startup loads only `CONFIG_PATH`, opens `pgxpool.New(ctx, cfg.DatabaseURL)`, and builds a legacy runner target with config/database/repo path (`cmd/projectlens-tui/main.go:39`, `cmd/projectlens-tui/main.go:48`, `cmd/projectlens-tui/main.go:71`). Its launched jobs will therefore keep targeting the legacy config/public schema after the CLI/MCP work lands.

Add a TUI task to resolve project/default project before constructing the store and jobs runner. This does not need a dashboard; it needs a single active project identity and scoped storage handle, plus clear failure behavior when multiple projects exist without a default.

### 5. High: project log-field task is not implementation-ready and does not match the current logger shape

The spec requires every structured log line emitted from a project request or indexer job to carry `project_slug` and `storage_schema` (`docs/superpowers/specs/2026-05-25-multi-project-isolation-design.md:288`). Task 14 is mostly an exploratory note: it says to "read `internal/logger/logger.go` first", guesses at `logger.With(k, v)`, and says to defer if the package does not already support structured logging (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:1986`, `docs/superpowers/plans/2026-05-25-multi-project-isolation.md:2002`).

The current logger package is a global charmbracelet/log wrapper with package-level `Info/Warn/Error` functions and no logger-returning helper (`internal/logger/logger.go:13`, `internal/logger/logger.go:32`). The proposed call shape `logger.WithProject(...).Info(...)` (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:2008`) does not correspond to an existing API, and the plan does not show the actual changes needed at call sites.

Turn this into a concrete task: define the exact logger API, update the command wrappers and MCP server construction to carry project identity, and include tests or at least compile-checked snippets. Do not leave a spec-required invariant behind a "defer this task" escape hatch.

## Additional Test Gaps

- Storage isolation tests only prove `files` isolation (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:602`), while the spec explicitly asks for `symbols`, `index_runs`, and `knowledge_entries` not crossing schemas (`docs/superpowers/specs/2026-05-25-multi-project-isolation-design.md:311`).
- CLI tests are mostly build/manual smoke tests. The risky contract here is command routing, so add focused command tests for unknown project, `--project` plus `--repo`, and read-only commands using `ConnectScoped` rather than `Connect`.
- The MCP test only verifies mux fallthrough for unknown routes. It should also prove per-project session manager separation by mounting two handlers and making at least one initialize/tool call against each endpoint.

## Revision Review Addendum

Date: 2026-05-25
Reviewed revision section: `docs/superpowers/plans/2026-05-25-multi-project-isolation.md:2147`

The revision section closes most original findings in shape: central CLI routing, migrate mutex validation, broken-project 503, TUI active-project resolution, and expanded storage isolation coverage are now present. Two execution blockers remain.

### 6. Blocking: Task 7.5 is not self-contained in the revised execution order

The revised execution order runs Phase B task 7, then Task 7.5, then revised Task 8 (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:3129`). Task 7.5 creates `cmd/projectlens/projectctx.go`, and its `openCmdStorage` implementation calls `resolveProjectRuntime` (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:2311`). But `resolveProjectRuntime` is not added until revised Task 8 (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:2368`).

That means Task 7.5 cannot pass its own Step 4 package compile after implementing `projectctx.go`; `go test ./cmd/projectlens/ -run validateMutex` must compile the whole package, including `openCmdStorage`, and will fail with `undefined: resolveProjectRuntime`. Move `resolveProjectRuntime` into Task 7.5, move Task 7.5 after revised Task 8, or split `validateMutex` into its own earlier file/task and add `openCmdStorage` only after the resolver exists.

### 7. Blocking: revised logging task still does not satisfy project-scoped indexer logs

The spec requires every structured log line emitted from a project request or indexer job to carry `project_slug` and `storage_schema` (`docs/superpowers/specs/2026-05-25-multi-project-isolation-design.md:288`). Revised Task 14 adds `logger.WithProject` and uses it in lock wrappers after `openCmdStorage` (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:2843`), and it updates MCP logging hooks (`docs/superpowers/plans/2026-05-25-multi-project-isolation.md:2855`). That still does not propagate the scoped logger into the command bodies executed by `run(ctx, cmd, db, cfg, repoPath)`.

Current indexing command bodies emit package-level logs directly, for example `index-all` calls `logger.Stage("Running all indexing stages")`, `logger.Stage("Stage 1: Code")`, and subsequent stage logs inside the callback (`cmd/projectlens/main.go:734`). `recordStageRun` also logs via package-level `logger.Warn` (`cmd/projectlens/main.go:541`). Those lines will remain unscoped even if the wrapper logs `"storage ready"` with a project logger.

Make the plan concrete here: either pass a scoped logger through the command execution path, attach project identity to context and have logger helpers read it, or explicitly relax the spec invariant. As written, Task 14 only adds some project-scoped log lines; it does not ensure all project request/indexer job logs carry project identity.
