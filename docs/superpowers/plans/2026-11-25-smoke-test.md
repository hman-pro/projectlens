# End-to-End Smoke Test Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `make smoke` proves the full loop (migrate, index a fixture repo, serve MCP, call every tool) against the real built binaries in under 5 minutes, locally and in CI.

**Architecture:** A Go test under build tag `smoke` in `internal/smoketest/` drives `bin/lens` and `bin/lens-mcp` as subprocesses. It creates a throwaway Postgres database, builds a temp git repo from `testdata/smoke-repo/` with three commits, points both binaries at an in-process fake Ollama server (deterministic 1024-dim vectors), then calls all 10 MCP tools over Streamable HTTP with the mcp-go client and decodes each `StructuredContent` into the real payload types from `internal/mcpserver/types.go`.

**Tech Stack:** Go `testing`, `net/http/httptest`, `os/exec`, `github.com/mark3labs/mcp-go` v0.48.0 `client.NewStreamableHttpClient`, pgx v5, GitHub Actions service container `pgvector/pgvector:pg16`.

**Spec:** `docs/tasks.md` section 4 "End-to-End Smoke Test". The user skipped a separate design doc; the decisions below are the design.

## Global Constraints

- Drive the real binaries (`bin/lens migrate`, `bin/lens index-all`, `bin/lens-mcp`). No in-process indexer or server.
- Embeddings come from a fake Ollama `httptest.Server`; no test may require a real Ollama, network, or model download.
- Summarization is disabled in the smoke config (`summarization.enabled: false`).
- Never touch the developer's index: every run uses a fresh database `smoke_<random hex>` created from the admin URL and dropped with `DROP DATABASE ... WITH (FORCE)` in `t.Cleanup`.
- Admin URL: `PROJECTLENS_SMOKE_DATABASE_URL`, else `PROJECTLENS_DATABASE_URL`. If neither is set, the test **fails** (not skips) with `set PROJECTLENS_DATABASE_URL (e.g. make docker-up)`. A skipped smoke test would report green without checking anything.
- Subprocess env: start from `os.Environ()`, drop every `PROJECTLENS_*` var, apply `agentsetup.ScrubGitEnv`, then set only the vars the test needs. The Makefile exports `.env`, and `PROJECTLENS_REPO_PATH` there would otherwise override the fixture path.
- Assertions target decoded payload structs, never prose text.
- `make smoke` runs `go test -tags smoke -count=1 -timeout 5m ./internal/smoketest/`; the timeout enforces the 5-minute pass criterion.
- Out of scope (from the brief): performance benchmarks, real-provider tests, TUI.
- If a semantic assertion fails because ProjectLens returns wrong data, stop and report it as a product bug. Do not loosen the assertion to make it pass.

## Review Focus

1. **Developer's global git config** (e.g. `commit.gpgsign=true`, missing `user.email`) makes fixture commits fail or prompt. Expect commits to work on any machine → Task 2 sets `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1`, and explicit author/committer env; test `TestFixtureRepo_ThreeCommits`.
2. **`configs/projects.yaml` present on the dev machine** flips `lens-mcp` into multi-project mode, so `/mcp` 404s. Expect the smoke test to ignore it → Task 3 sets `PROJECTLENS_PROJECTS` to a nonexistent temp path.
3. **Embed stage fails silently**: `index-all` only logs a warning when embedding fails and still exits 0. Expect the smoke test to catch it → Task 3 asserts `index_status` stage `embed` is `completed` and `search_go_context` is not degraded.
4. **A new MCP tool is added without smoke coverage.** Expect the smoke test to fail → Task 3 compares `ListTools` names to a hardcoded expected list of exactly 10 (deliberate duplication of `tools.go`).
5. **Binaries missing or stale** (`go test` run directly without `make build`). Expect a clear message, not an exec error → Task 2 `binPath` fails with `run make build first`; `make smoke` depends on `build`.

---

### Task 1: Fake Ollama server

Untagged so plain `go test ./...` (CI's existing job) checks wire compatibility with the real client.

**Files:**
- Create: `internal/smoketest/doc.go` (package comment only)
- Create: `internal/smoketest/fakeollama.go`
- Test: `internal/smoketest/fakeollama_test.go`

**Interfaces:**
- Produces: `func NewFakeOllama(dim int) *httptest.Server` and `func FakeVector(text string, dim int) []float64`.

- [ ] **Step 1: Write the failing tests**

```go
func TestFakeOllama_RealClientRoundTrip(t *testing.T) {
	srv := NewFakeOllama(1024)
	defer srv.Close()
	c := ollama.NewClient(srv.URL, "qwen3-embedding:0.6b", 1024)
	vecs, err := c.EmbedBatch(context.Background(), []string{"a", "b", "a"})
	// err == nil; len(vecs) == 3; len(vecs[0]) == 1024
	// vecs[0] equals vecs[2] element-wise (deterministic); vecs[0] differs from vecs[1]
	// c.Ping(ctx) == nil
}

func TestFakeVector_UnitLength(t *testing.T) {
	// sum of squares of FakeVector("x", 1024) is within 1e-6 of 1.0
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/smoketest/ -run 'TestFake' -v`
Expected: FAIL, `undefined: NewFakeOllama`.

- [ ] **Step 3: Implement in `internal/smoketest/fakeollama.go`**

`POST /api/embed` decodes `{"model","input":[...]}` and returns `{"model":<same>,"embeddings":[FakeVector(input[i], dim)...]}`. `GET /api/tags` returns `{"models":[{"name":"qwen3-embedding:0.6b"}]}`. Any other path returns 404. `FakeVector`: seed `math/rand/v2` PCG from the first 16 bytes of `sha256(text)`, draw `dim` values from `NormFloat64`, then L2-normalize.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/smoketest/ -v && go vet ./internal/smoketest/`
Expected: PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add internal/smoketest/
git commit -m "test(smoke): add deterministic fake Ollama server"
```

---

### Task 2: Fixture repo, throwaway database, and indexing

**Files:**
- Create: `testdata/smoke-repo/go.mod` (`module example.com/smokerepo`, `go 1.22`, no deps)
- Create: `testdata/smoke-repo/widget/widget.go`
- Create: `testdata/smoke-repo/main.go`
- Create: `testdata/smoke-repo/migrations/001_widgets.up.sql`
- Create: `internal/smoketest/helpers_test.go` (`//go:build smoke`)
- Test: `internal/smoketest/smoke_test.go` (`//go:build smoke`)

Fixture content (exact names matter; Task 3 asserts on them):
- `widget/widget.go`, package `widget`:
  - `// Widget is a named item stored in the widgets table.` `type Widget struct { ID int64; Name string }`
  - `type Store interface { Save(w Widget) error }`
  - `type SQLStore struct{}`, with `func (SQLStore) Save(w Widget) error` containing the literal `"INSERT INTO widgets (id, name) VALUES ($1, $2)"`
  - `func (SQLStore) Load(id int64) (Widget, error)` containing `"SELECT id, name FROM widgets WHERE id = $1"`
  - `func NewWidget(name string) Widget`
- `main.go`, package `main`: calls `widget.NewWidget("demo")` and `widget.SQLStore{}.Save(...)`.
- `migrations/001_widgets.up.sql`: `CREATE TABLE widgets (id BIGINT PRIMARY KEY, name TEXT NOT NULL);`

**Interfaces** (all in `helpers_test.go`):
- Consumes: `NewFakeOllama` (Task 1); `agentsetup.ScrubGitEnv(env []string) []string`.
- Produces:
  - `repoRoot(t *testing.T) string`: directory containing `go.mod`, walking up from the test's cwd.
  - `binPath(t *testing.T, name string) string`: `<repoRoot>/bin/<name>`; `t.Fatalf("%s not found: run make build first", ...)` if missing.
  - `freshDatabase(t *testing.T) string`: creates `smoke_<8 random hex>` via the admin URL, registers `DROP DATABASE IF EXISTS <name> WITH (FORCE)` in `t.Cleanup`, and returns the admin URL with the database path swapped. Quote the name with `pgx.Identifier{}.Sanitize()`.
  - `buildFixtureRepo(t *testing.T) string`: copies `testdata/smoke-repo` into `t.TempDir()`, `git init -b main`, then makes 3 commits: (1) all files, "add widget store"; (2) append `// rev 2` to `widget/widget.go` and `main.go`, "rev 2"; (3) the same with `// rev 3`, "rev 3". Git env: `subprocEnv` plus `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_AUTHOR_NAME=smoke`, `GIT_AUTHOR_EMAIL=smoke@example.com`, and the same for `GIT_COMMITTER_*`. Leave dates at now so they fall inside `window_months`.
  - `subprocEnv(extra ...string) []string`: the env rule from Global Constraints, plus `extra`.
  - `writeConfig(t *testing.T, ollamaURL, repoPath string) string`: writes a temp `index.yaml` and returns its path. Copy `configs/index.yaml`, with these changes: `repo_path: <repoPath>`, `embeddings.endpoint: <ollamaURL>`, `summarization.enabled: false`, `history.min_commits_per_file: 1`, `history.coupling_min_cochanges: 2`, and drop the `**/testdata/**` exclude.
  - `runLens(t *testing.T, env []string, args ...string) string`: runs `binPath("lens")` with `cmd.Dir = repoRoot` (so `findMigrationsDir` resolves `migrations/`) and returns combined output. On a non-zero exit it fails with the full output.

- [ ] **Step 1: Write the failing tests in `smoke_test.go`**

```go
func TestFixtureRepo_ThreeCommits(t *testing.T) {
	dir := buildFixtureRepo(t)
	// `git -C dir rev-list --count HEAD` == "3"
	// `git -C dir status --porcelain` == ""
}

func TestSmoke_IndexesFixture(t *testing.T) {
	ollama := NewFakeOllama(1024); defer ollama.Close()
	db := freshDatabase(t)
	repo := buildFixtureRepo(t)
	cfg := writeConfig(t, ollama.URL, repo)
	env := subprocEnv()
	runLens(t, env, "migrate", "--config", cfg, "--db", db)
	out := runLens(t, env, "index-all", "--config", cfg, "--db", db, "--repo", repo)
	// out does not contain "embedding failed", "datastore indexing failed", or "history indexing failed"
}
```

- [ ] **Step 2: Run to verify failure**

Run: `make build && go test -tags smoke -count=1 ./internal/smoketest/ -run 'TestFixtureRepo|TestSmoke_Indexes' -v`
Expected: FAIL, `undefined: buildFixtureRepo`.

- [ ] **Step 3: Add the fixture files and implement the helpers listed above**

- [ ] **Step 4: Run to verify pass**

Run: same command as Step 2, with `make docker-up` running.
Expected: PASS. After the run, `psql "$PROJECTLENS_DATABASE_URL" -c '\l'` shows no leftover `smoke_*` database.

- [ ] **Step 5: Confirm the main module ignores the fixture**

Run: `go build ./... && go vet ./...`
Expected: clean (`testdata/` is skipped by the go tool).

- [ ] **Step 6: Commit**

```bash
git add testdata/smoke-repo internal/smoketest
git commit -m "test(smoke): index a fixture repo with the real lens binary"
```

---

### Task 3: Serve MCP and call every tool

**Files:**
- Modify: `internal/smoketest/helpers_test.go`
- Modify: `internal/smoketest/smoke_test.go` (extend `TestSmoke_IndexesFixture` and rename it to `TestSmoke_FullLoop`)

**Interfaces:**
- Consumes: everything Task 2 produces; payload types from `internal/mcpserver/types.go`; `indexstate.StageFreshness` and `indexstate.ProviderHealth`.
- Produces:
  - `startMCP(t *testing.T, cfgPath, dbURL, repoPath string) string`: picks a free port (`net.Listen("tcp","127.0.0.1:0")`, then close), starts `bin/lens-mcp` with `subprocEnv("PROJECTLENS_CONFIG="+cfgPath, "PROJECTLENS_DATABASE_URL="+dbURL, "PROJECTLENS_REPO_PATH="+repoPath, "PROJECTLENS_MCP_PORT="+port, "PROJECTLENS_PROJECTS="+<tempdir>/absent.yaml)`, waits up to 15s for TCP accept, and returns `http://127.0.0.1:<port>/mcp`. `t.Cleanup` sends SIGTERM, waits, and logs captured stdout/stderr if the test failed.
  - `callTool[T any](t *testing.T, c *client.Client, name string, args map[string]any) T`: calls the tool, fails if `res.IsError` (include the text content in the message), re-marshals `res.StructuredContent` to JSON, and strictly decodes into `T` (`DisallowUnknownFields`, so a renamed field fails).
  - Local type `statusPayload` mirroring the unexported `indexStatusPayload`: `Stages map[string]indexstate.StageFreshness`, `Git struct{ Head string; Dirty bool }`, `Providers []indexstate.ProviderHealth`, plus the `project_slug` and `storage_schema` fields so strict decoding passes.

- [ ] **Step 1: Extend the test with these assertions (after indexing)**

```go
url := startMCP(t, cfg, db, repo)
c, _ := client.NewStreamableHttpClient(url) // Start + Initialize; fail on error
// ListTools: sorted names == {find_symbol, get_change_history, get_coupling, get_package_summary,
//   get_symbol_context, get_table_context, index_status, save_knowledge, search_go_context, search_knowledge}

st := callTool[statusPayload](t, c, "index_status", nil)
// for each of code, datastore, history, embed: st.Stages[s].Status == "completed"; st.Git.Head != ""
// every st.Providers[i].Error == ""

fs := callTool[mcpserver.FindSymbolPayload](t, c, "find_symbol", map[string]any{"name": "NewWidget"})
// fs.Total >= 1; fs.Hits[0].Evidence.FilePath == "widget/widget.go"

sg := callTool[mcpserver.SearchGoContextPayload](t, c, "search_go_context", map[string]any{"query": "save a widget"})
// !sg.Degradation.Degraded; sg.Total >= 1

sc := callTool[mcpserver.SymbolContextPayload](t, c, "get_symbol_context", map[string]any{"name": "NewWidget"})
// !sc.NotFound; some sc.Callers[i].Name == "main"
si := callTool[mcpserver.SymbolContextPayload](t, c, "get_symbol_context", map[string]any{"name": "Store"})
// some si.Implementors[i].Name == "SQLStore"

ps := callTool[mcpserver.PackageSummaryPayload](t, c, "get_package_summary", map[string]any{"package_name": "widget"})
// ps.PackageName == "widget"

tc := callTool[mcpserver.TableContextPayload](t, c, "get_table_context", map[string]any{"table_name": "widgets"})
// !tc.NotFound; a column "id" with IsPrimaryKey; len(tc.ReadBy)+len(tc.WrittenBy) >= 1

ch := callTool[mcpserver.ChangeHistoryPayload](t, c, "get_change_history", map[string]any{"name": "widget/widget.go"})
// len(ch.Records) == 3

cp := callTool[mcpserver.CouplingPayload](t, c, "get_coupling", map[string]any{"name": "widget/widget.go"})
// some cp.Coupled[i].FilePath == "main.go"

sk := callTool[mcpserver.SaveKnowledgePayload](t, c, "save_knowledge", map[string]any{
	"category": "lesson", "title": "smoke: widgets", "body": "Widgets are saved via SQLStore.",
	"anchors": []map[string]string{{"type": "symbol", "ref": "NewWidget"}},
})
// sk.ID > 0; sk.Embedded; sk.AnchorsResolved == 1

kq := callTool[mcpserver.SearchKnowledgePayload](t, c, "search_knowledge",
	map[string]any{"anchor_type": "symbol", "anchor_ref": "NewWidget"})
// some kq.Entries[i].Title == "smoke: widgets"
```

- [ ] **Step 2: Run to verify failure**

Run: `make build && go test -tags smoke -count=1 ./internal/smoketest/ -run TestSmoke_FullLoop -v`
Expected: FAIL, `undefined: startMCP`.

- [ ] **Step 3: Implement `startMCP`, `callTool`, and `statusPayload`**

- [ ] **Step 4: Run to verify pass**

Run: same as Step 2.
Expected: PASS. If an assertion fails on real data (for example the caller name is qualified differently), check the handler. Fix the expectation only if the payload is correct and the plan guessed the format wrong; otherwise report a product bug (Global Constraints).

- [ ] **Step 5: Commit**

```bash
git add internal/smoketest
git commit -m "test(smoke): call every MCP tool against the indexed fixture"
```

---

### Task 4: `make smoke`, CI job, docs

**Files:**
- Modify: `Makefile` (new `smoke` target next to `test-int`)
- Modify: `.github/workflows/ci.yml` (new `smoke` job)
- Modify: `docs/operations.md` (Make target table and a short "Smoke test" note)
- Modify: `docs/tasks.md` (move the smoke test to Recently Done; update Suggested Sequence)

- [ ] **Step 1: Add the Make target**

```make
smoke: build ## End-to-end smoke test: migrate, index fixture, call every MCP tool (requires DB)
	PROJECTLENS_DATABASE_URL="$(PROJECTLENS_DATABASE_URL)" go test -tags smoke -count=1 -timeout 5m ./internal/smoketest/
```

Add `smoke` to the `.PHONY` line that `test-int` uses.

- [ ] **Step 2: Verify locally**

Run: `make docker-up && time make smoke`
Expected: `ok  .../internal/smoketest`, wall time well under 5 minutes.

- [ ] **Step 3: Add the CI job**

A new job `smoke` in `ci.yml` (same triggers). `services.postgres`: image `pgvector/pgvector:pg16`; env `POSTGRES_USER/POSTGRES_PASSWORD/POSTGRES_DB=projectlens`; ports `5433:5432`; health check `pg_isready -U projectlens`, interval 5s, retries 10. Steps: checkout, setup-go (same as `build-and-test`), then `make smoke` with `PROJECTLENS_DATABASE_URL: postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable`.

- [ ] **Step 4: Update docs**

`docs/operations.md`: add `make smoke` to the Make targets section, with its DB requirement and the env override `PROJECTLENS_SMOKE_DATABASE_URL`. `docs/tasks.md`: move the row to Recently Done, with evidence `internal/smoketest/`, `make smoke`, CI `smoke` job.

- [ ] **Step 5: Verify the whole repo**

Run: `make fmt && make vet && make test && make smoke`
Expected: all pass; `git status` shows only the intended files.

- [ ] **Step 6: Commit and confirm CI**

```bash
git add Makefile .github/workflows/ci.yml docs/operations.md docs/tasks.md
git commit -m "ci: run end-to-end smoke test on every push"
```

After pushing the branch, the `smoke` job is green in GitHub Actions.
