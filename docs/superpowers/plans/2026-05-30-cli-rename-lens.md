# CLI Rename `projectlens` → `lens` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the typed command and its three binaries from `projectlens` to `lens`, leaving product name, env vars, DB, module path, and MCP server identity unchanged.

**Architecture:** Five sequential tasks. Task 1 moves the `cmd/` dirs and fixes every build/path reference atomically so the tree always builds. Task 2 fixes the TUI→CLI binary resolver (the one runtime break). Task 3 rewrites user-facing hint strings. Task 4 sweeps docs/agent assets/scripts. Task 5 is the final audit. Each task ends green (`make build` + `make test` + `make vet`) and is committed.

**Tech Stack:** Go 1.26, Make, Docker, Bubble Tea TUI, pgx.

**Spec:** `docs/superpowers/specs/2026-05-30-cli-rename-lens-design.md`

**Conventions:** Commit subjects are one line only (no body). Run all `make`/`go`/`git` from repo root `/Users/hamed.zohrehvand/source/projectlens`.

---

## Task 1: Move source dirs and fix all build wiring

This task is atomic — the dir move breaks the build until every build path is updated. Do not commit until `make build` is green.

**Files:**
- Move: `cmd/projectlens` → `cmd/lens`, `cmd/projectlens-mcp` → `cmd/lens-mcp`, `cmd/projectlens-tui` → `cmd/lens-tui`
- Modify: `Makefile`, `docker/Dockerfile`, `cmd/lens/main.go:33`, `internal/storage/writelock/cli_integration_test.go:26-30`

- [ ] **Step 1: Move the three source directories**

```bash
git mv cmd/projectlens cmd/lens
git mv cmd/projectlens-mcp cmd/lens-mcp
git mv cmd/projectlens-tui cmd/lens-tui
```

- [ ] **Step 2: Update Makefile output vars (lines 3-5)**

Replace:
```make
CLI := $(BIN_DIR)/projectlens
MCP := $(BIN_DIR)/projectlens-mcp
TUI := $(BIN_DIR)/projectlens-tui
```
with:
```make
CLI := $(BIN_DIR)/lens
MCP := $(BIN_DIR)/lens-mcp
TUI := $(BIN_DIR)/lens-tui
```

- [ ] **Step 3: Rename Makefile build targets, their bodies, and every dependent target**

In `Makefile`, replace every token `build-projectlens` → `build-lens` (covers `build-projectlens`, `build-projectlens-mcp`, `build-projectlens-tui`). This updates: the `.PHONY` line (35), the `build` aggregate (37), the three build target definitions (39-48), the run targets `tui`/`mcp`/`tui` (75-82), and all convenience targets `bootstrap`, `reindex`, `reindex-full`, `reindex-dry`, `status`, `query`, `index-all`, `index-history`, `index-datastore`, `index-embed`, `index-summarize`, `graph-export`, `migrate` (92-133, 169).

```bash
sed -i '' 's/build-projectlens/build-lens/g' Makefile
```

Then fix the build target `go build` path args and the `migrate` help text (line 169 `uses projectlens migrate`):
```bash
sed -i '' 's#\./cmd/projectlens-mcp#./cmd/lens-mcp#g; s#\./cmd/projectlens-tui#./cmd/lens-tui#g; s#\./cmd/projectlens#./cmd/lens#g; s/uses projectlens migrate/uses lens migrate/g' Makefile
```

Verify no stale references remain:
```bash
rg -n 'projectlens' Makefile
```
Expected: only `PROJECTLENS_DATABASE_URL`, `PROJECTLENS_REPO_PATH`, and the postgres URL `projectlens:projectlens@.../projectlens`. No `cmd/projectlens`, no `bin/projectlens`, no `build-projectlens`.

- [ ] **Step 4: Update Dockerfile build/copy/entrypoint paths**

In `docker/Dockerfile` replace lines 13-14, 21-22, 26:
```dockerfile
RUN CGO_ENABLED=0 go build -o /bin/lens       ./cmd/lens
RUN CGO_ENABLED=0 go build -o /bin/lens-mcp   ./cmd/lens-mcp
```
```dockerfile
COPY --from=builder /bin/lens      /bin/lens
COPY --from=builder /bin/lens-mcp  /bin/lens-mcp
```
```dockerfile
ENTRYPOINT ["/bin/lens"]
```

- [ ] **Step 4b: Update docker-compose entrypoint binary path (service names stay)**

In `docker/docker-compose.yml`, change only the MCP entrypoint binary path (line ~22):
```yaml
    entrypoint: ["/bin/lens-mcp"]
```
Leave the service names (`projectlens-mcp`, `projectlens-indexer`), the `projectlens-data` volume, `POSTGRES_DB`/`POSTGRES_USER`, `PROJECTLENS_DB_PASSWORD`, and `PROJECTLENS_DATABASE_URL` unchanged. Verify:
```bash
rg -n '/bin/projectlens|build-projectlens' docker/docker-compose.yml
```
Expected: no hits.

- [ ] **Step 5: Update the writelock integration test build path**

In `internal/storage/writelock/cli_integration_test.go` (lines 26-30):
```go
	binPath := filepath.Join(dir, "lens")
	build := exec.Command("go", "build", "-o", binPath, "../../../cmd/lens/")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build lens: %v", err)
	}
```

- [ ] **Step 6: Update cobra root command name**

In `cmd/lens/main.go:33`:
```go
		Use:   "lens",
```

- [ ] **Step 7: Build, vet, and test**

Run:
```bash
make clean && make build && make vet && make test
```
Expected: `bin/lens`, `bin/lens-mcp`, `bin/lens-tui` exist; `make vet` clean; `make test` PASS. (Resolver still references the old name — that is fixed in Task 2 and has no failing unit test yet.)

```bash
ls bin/
```
Expected: `lens  lens-mcp  lens-tui` (no `projectlens*`).

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor: move cmd dirs and build wiring to lens"
```

---

## Task 2: Fix the TUI→CLI binary resolver

The resolver in `internal/tui/jobs/binary.go` finds the CLI by the name `"projectlens"`. After Task 1 the binary is `lens`, so the TUI cannot launch jobs until this changes. TDD: add a sibling-resolution test first.

**Files:**
- Test: `internal/tui/jobs/binary_test.go`
- Modify: `internal/tui/jobs/binary.go`, `internal/tui/app/update.go:202`

- [ ] **Step 1: Write a failing test for sibling resolution by name `lens`**

Append to `internal/tui/jobs/binary_test.go`:
```go
func TestResolveBinary_SiblingNamedLens(t *testing.T) {
	// A sibling executable named "lens" next to a temp dir on PATH should
	// resolve via PATH lookup (proves the resolver looks for "lens").
	dir := t.TempDir()
	bin := filepath.Join(dir, "lens")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROJECTLENS_BINARY", "")
	t.Setenv("PATH", dir)
	got, err := jobs.ResolveBinary()
	if err != nil {
		t.Fatal(err)
	}
	if got != bin {
		t.Fatalf("got %q, want %q", got, bin)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
go test ./internal/tui/jobs/ -run TestResolveBinary_SiblingNamedLens -v
```
Expected: FAIL — resolver does `exec.LookPath("projectlens")`, so a `lens` binary on PATH is not found.

- [ ] **Step 3: Update the resolver to use `lens`**

Replace the body of `ResolveBinary` and its doc comment in `internal/tui/jobs/binary.go`:
```go
// ResolveBinary returns the absolute path to the lens binary the
// runner should invoke. Resolution order:
//  1. PROJECTLENS_BINARY env var (must be executable).
//  2. A sibling of os.Executable() named "lens".
//  3. PATH lookup for "lens".
func ResolveBinary() (string, error) {
	if v := os.Getenv("PROJECTLENS_BINARY"); v != "" {
		if err := isExecutable(v); err != nil {
			return "", fmt.Errorf("PROJECTLENS_BINARY=%q: %w", v, err)
		}
		return v, nil
	}
	if self, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(self), "lens")
		if err := isExecutable(sibling); err == nil {
			return sibling, nil
		}
	}
	if path, err := exec.LookPath("lens"); err == nil {
		return path, nil
	}
	return "", errors.New("lens binary not found (set PROJECTLENS_BINARY, place it next to lens-tui, or add to PATH)")
}
```
(The `PROJECTLENS_BINARY` env var name is intentionally unchanged.)

- [ ] **Step 4: Update the TUI hint string**

In `internal/tui/app/update.go:202`, replace:
```go
				WithHint("set PROJECTLENS_BINARY, place a lens binary next to lens-tui, or add it to PATH")
```

- [ ] **Step 5: Run tests to verify pass**

Run:
```bash
go test ./internal/tui/jobs/ -v && make vet
```
Expected: PASS (including the new and existing resolver tests).

- [ ] **Step 6: Commit**

```bash
git add internal/tui/jobs/binary.go internal/tui/jobs/binary_test.go internal/tui/app/update.go
git commit -m "fix(tui): resolve CLI binary as lens"
```

---

## Task 3: Rewrite user-facing guidance strings

After the hard cut every emitted `projectlens <cmd>` hint is a dead instruction. The report-package hints are test-gated (update tests first); the CLI/MCP/TUI strings have no asserting tests (edit + grep verify).

**Files:**
- Test: `internal/report/derive_test.go`, `internal/report/markdown_test.go:36`, `internal/report/json_test.go:34`
- Modify: `internal/report/derive.go:12-16,35`, `cmd/lens/main.go:237`, `internal/mcpserver/handlers.go:348,415,508,578,752`, `internal/mcpserver/not_ready.go:16`, `internal/tui/sections/health/view.go:20`, `internal/tui/sections/runs/view.go:22`, `internal/tui/app/view.go:66`

- [ ] **Step 1: Update report tests to expect `lens` (failing)**

In `internal/report/derive_test.go` replace lines 25-28 and 48-49 and 98 — every `run projectlens` → `run lens`:
```go
		"summarize": "run lens index-summarize",
		"embed":     "run lens index-embed",
		"history":   "run lens index-history",
		"datastore": "run lens index-datastore",
```
```go
			if d.SuggestedAction != "run lens reindex" {
				t.Errorf("code action: got %q want %q", d.SuggestedAction, "run lens reindex")
```
```go
		if d.Stage == "code" && d.SuggestedAction == "run lens reindex" {
```

In `internal/report/markdown_test.go:36`:
```go
		"run lens index-embed",
```

In `internal/report/json_test.go:34`:
```go
		Degraded:    []StageDegradation{{Stage: "embed", Reason: "missing", SuggestedAction: "run lens index-embed"}},
```

- [ ] **Step 2: Run report tests to verify they fail**

Run:
```bash
go test ./internal/report/ -run 'Derive|Markdown|JSON' -v
```
Expected: FAIL — production still emits `run projectlens ...`.

- [ ] **Step 3: Update report production strings**

In `internal/report/derive.go` replace the map (lines 12-16) and line 35:
```go
	"code":      "run lens reindex",
	"summarize": "run lens index-summarize",
	"embed":     "run lens index-embed",
	"history":   "run lens index-history",
	"datastore": "run lens index-datastore",
```
```go
				SuggestedAction: "run lens reindex",
```

- [ ] **Step 4: Run report tests to verify they pass**

Run:
```bash
go test ./internal/report/ -v
```
Expected: PASS.

- [ ] **Step 5: Update CLI status hint**

In `cmd/lens/main.go:237`:
```go
				fmt.Println("No index runs found. Run 'lens bootstrap' first.")
```

- [ ] **Step 6: Update MCP handler hints**

In `internal/mcpserver/handlers.go`, replace the five hint strings:
```go
			fmt.Sprintf("No table found matching %q. Run 'lens index-datastore' to index database schemas.", tableName),
```
```go
		b.WriteString("\nNo code references discovered. Run 'lens index-datastore' to scan for SQL usage.\n")
```
```go
		b.WriteString("No index runs found. Run 'lens bootstrap' to create the initial index.\n")
```
```go
			fmt.Fprintf(&b, "No change history found for %s. Run 'lens index-history' to index git history.", name)
```
```go
		fmt.Fprintf(&b, "No co-change coupling found for %s (min strength: %.1f). Run 'lens index-history' to build coupling data.", name, minStrength)
```

In `internal/mcpserver/not_ready.go:16`:
```go
		hint := "lens migrate --project " + slug
```

- [ ] **Step 7: Update TUI empty-state strings and title**

In `internal/tui/sections/health/view.go:20`:
```go
		return theme.MutedStyle().Render("no runs yet — run \"lens bootstrap\"")
```
In `internal/tui/sections/runs/view.go:22`:
```go
		return theme.MutedStyle().Render("no runs yet — run \"lens bootstrap\"")
```
In `internal/tui/app/view.go:66` (keep product brand, capitalized):
```go
	left := theme.TitleStyle().Render(" ProjectLens · dashboard ")
```

- [ ] **Step 8: Build, vet, test, and grep-verify**

Run:
```bash
make build && make vet && make test
rg -n "projectlens (bootstrap|reindex|index|migrate)|run projectlens|projectlens · dashboard" cmd/ internal/
```
Expected: tests PASS; the `rg` returns no hits (all runtime hints now say `lens`).

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "refactor: emit lens in user-facing CLI/MCP/TUI hints"
```

---

## Task 4: Sweep docs, agent assets, source-of-truth tables, and scripts

No tests here — verify with targeted greps. Edit only typed-command and moved-path occurrences; leave env vars, DB strings, module path, MCP server name, mount paths, schema names, and "ProjectLens" prose.

**Files:** `README.md`, `docs/operations.md`, `docs/AGENT_SETUP.md`, `docs/internals.md`, `docs/architecture.md`, `docs/tasks.md`, `CLAUDE.md`, `AGENTS.md`, `agent/skills/use-projectlens/SKILL.md`, `agent/claude/*`, `agent/codex/*`, `scripts/release-demo.sh`

- [ ] **Step 1: README install paths and command examples**

In `README.md`, change `go install`/`go build` leaf dirs and typed commands (module prefix stays):
```bash
sed -i '' 's#/cmd/projectlens-mcp#/cmd/lens-mcp#g; s#/cmd/projectlens-tui#/cmd/lens-tui#g; s#/cmd/projectlens#/cmd/lens#g' README.md
```
Then manually change typed commands `projectlens <subcommand>` → `lens <subcommand>` in code fences. Verify:
```bash
rg -n 'projectlens' README.md
```
Expected remaining: only `PROJECTLENS_*` env vars, `github.com/hman-pro/projectlens` module root (without `/cmd/...`), and "ProjectLens" prose.

- [ ] **Step 2: operations.md, AGENT_SETUP.md, internals.md typed commands + source paths**

For each file, replace `cmd/projectlens` leaf paths and typed `projectlens <cmd>` invocations:
```bash
for f in docs/operations.md docs/AGENT_SETUP.md docs/internals.md; do
  sed -i '' 's#cmd/projectlens-mcp#cmd/lens-mcp#g; s#cmd/projectlens-tui#cmd/lens-tui#g; s#cmd/projectlens#cmd/lens#g' "$f"
done
```
Then manually replace typed commands `projectlens <cmd>` → `lens <cmd>` (e.g. `projectlens index`, `projectlens bootstrap`, `go run ./cmd/lens/ unlock --force` in AGENT_SETUP.md:334). Leave `PROJECTLENS_*`, DB URLs, `PROJECTLENS_BINARY`. Verify each:
```bash
rg -n '(^|[ ./"`])projectlens( |$|[ ./"`-])' docs/operations.md docs/AGENT_SETUP.md docs/internals.md
```
Expected: no typed-command or `cmd/projectlens` hits remain.

- [ ] **Step 3: architecture.md (owner doc)**

In `docs/architecture.md`: component labels `projectlens CLI`/`projectlens-tui`/`projectlens-mcp` (14-16, 63), entry-point table `cmd/projectlens*` (35-37), the `cmd/projectlens-mcp/main.go` link (81), source-of-truth row `cmd/projectlens/*.go` (147), and typed commands `projectlens report`/`projectlens export graph` (96).
```bash
sed -i '' 's#cmd/projectlens-mcp#cmd/lens-mcp#g; s#cmd/projectlens-tui#cmd/lens-tui#g; s#cmd/projectlens#cmd/lens#g; s/projectlens CLI/lens CLI/g; s/projectlens-tui/lens-tui/g; s/projectlens-mcp/lens-mcp/g; s/`projectlens report`/`lens report`/g; s/`projectlens export graph`/`lens export graph`/g' docs/architecture.md
```
Verify the protected strings survived:
```bash
rg -n '/projectlens/mcp|schema: projectlens|projectlens-graph/v2' docs/architecture.md
```
Expected: those three still present (mount path, schema, graph schema name — must NOT change).

- [ ] **Step 4: tasks.md (canonical task list)**

In `docs/tasks.md`: file paths `cmd/projectlens/report.go`, `cmd/projectlens/export.go`, `cmd/projectlens/smoke_test.go` (40, 41, 194) and typed commands `projectlens report`/`projectlens export graph` (40, 41, 56, 60):
```bash
sed -i '' 's#cmd/projectlens#cmd/lens#g; s/`projectlens report`/`lens report`/g; s/`projectlens export graph`/`lens export graph`/g; s/^- `projectlens report`/- `lens report`/; s/^- `projectlens export graph`/- `lens export graph`/' docs/tasks.md
rg -n 'projectlens (report|export)|cmd/projectlens' docs/tasks.md
```
Expected: no hits.

- [ ] **Step 5: Source-of-truth tables in CLAUDE.md and AGENTS.md**

Lines 54-55 in each reference `cmd/projectlens/*.go` and `cmd/projectlens/main.go`:
```bash
sed -i '' 's#cmd/projectlens/main.go#cmd/lens/main.go#g; s#cmd/projectlens/\*.go#cmd/lens/*.go#g' CLAUDE.md AGENTS.md
rg -n 'cmd/projectlens' CLAUDE.md AGENTS.md
```
Expected: no hits.

- [ ] **Step 6: Agent skill + vendor snippets**

In `agent/skills/use-projectlens/SKILL.md` and `agent/claude/*` / `agent/codex/*`, replace typed CLI invocations `projectlens <cmd>` → `lens <cmd>`. Leave the skill dir name, `PROJECTLENS_*` env, and the MCP server name `projectlens` in `mcp-config.json` (server identity, not a command).
```bash
rg -n 'projectlens' agent/
```
Manually edit only command invocations; confirm afterward that remaining hits are the skill id `use-projectlens`, env vars, and the MCP server name.

- [ ] **Step 7: release-demo.sh**

In `scripts/release-demo.sh` (44-45, 56):
```bash
sed -i '' 's/build-projectlens-mcp/build-lens-mcp/g; s#\./bin/projectlens-mcp#./bin/lens-mcp#g; s#/tmp/projectlens-mcp.log#/tmp/lens-mcp.log#g' scripts/release-demo.sh
rg -n 'projectlens' scripts/release-demo.sh
```
Expected: no hits. (Leave `scripts/release-smoke.sh` untouched — its `github.com/hman-pro/projectlens/...` import path stays.)

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "docs: rename projectlens command to lens across docs, agents, scripts"
```

---

## Task 5: Final audit and integration verification

**Files:** none modified unless the audit finds a miss.

- [ ] **Step 1: Clean build + unit tests + vet**

Run:
```bash
make clean && make build && make test && make vet
ls bin/
```
Expected: `bin/lens lens-mcp lens-tui`; all green.

- [ ] **Step 2: Integration test for the moved build path**

The writelock CLI integration test builds `../../../cmd/lens/` and is behind `//go:build integration`:
```bash
make test-int
```
Expected: PASS (requires `PROJECTLENS_DATABASE_URL` / local Postgres per `docs/operations.md`). If the DB is unavailable, at minimum compile-check:
```bash
go test -tags integration -run xxx ./internal/storage/writelock/
```
Expected: builds cleanly (no test selected).

- [ ] **Step 3: Targeted command-guidance audit**

Run:
```bash
rg -n '(^|[ ./"])projectlens( |$| [a-z-]|")'
```
Expected: zero hits that are typed-command instructions. Acceptable remaining: none of this form (env vars use `PROJECTLENS_` uppercase and won't match).

- [ ] **Step 4: Moved-path audit**

Run:
```bash
rg -n 'cmd/projectlens|build-projectlens|/bin/projectlens|go run ./cmd/projectlens'
```
Expected: zero hits.

- [ ] **Step 5: Allowlist confirmation**

Run:
```bash
rg -n 'projectlens' | rg -v 'PROJECTLENS_|github.com/hman-pro/projectlens|projectlens:projectlens|POSTGRES_|projectlens-data|schema: projectlens|projectlens-graph|/projectlens/mcp|use-projectlens|NewMCPServer\("projectlens"|ProjectLens'
```
Expected: zero hits. Every surviving `projectlens` is an intentional identity (env prefix, module path, DB user/name, Docker volume + compose service, schema names, MCP mount/server name, skill id, or "ProjectLens" prose). Investigate any line that prints.

- [ ] **Step 6: Smoke the CLI help output**

Run:
```bash
bin/lens --help
```
Expected: usage shows `lens` as the command name.

- [ ] **Step 7: Commit (only if the audit fixed anything)**

```bash
git add -A
git commit -m "chore: finalize lens rename audit fixes"
```
If nothing changed, skip — the rename is complete.
