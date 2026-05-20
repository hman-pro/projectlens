# ProjectLens Plan Backlog

Ideas and follow-up work parked for later. Each entry is a short brief that can be promoted to a full plan when picked up.

---

## End-to-end smoke test (low priority, parked 2026-05-18)

**Goal:** A single command that proves the full ProjectLens loop is healthy — Postgres up, migrations applied, indexer can ingest a tiny fixture repo, and every MCP tool returns a sensible structured payload.

**Why it matters:** No CI gate currently exercises the *combined* indexer → storage → MCP-handler path. Unit/integration tests cover slices, but the end-to-end contract is checked only by hand. Until this exists, design changes (new fields, new stages, new providers) can silently break tool callers.

**Sketch of scope:**
- Fixture Go module under `testdata/smoke-repo/` with 3–5 files: one exported function, one interface + implementor, one SQL migration, one struct with a doc comment.
- Test driver (`internal/smoketest/` or `cmd/projectlens/smoke_test.go` with build tag `smoke`) that:
  1. Spins up Postgres via `testcontainers-go` (or reuses local 5433 if env says so).
  2. Runs all migrations in `migrations/*.up.sql`.
  3. Invokes `internal/indexer` against the fixture (or shells `./bin/projectlens bootstrap`).
  4. Starts the MCP server in-process (`Server.MCPServer()`).
  5. Calls every tool in `toolRegistry` with a representative input and asserts the `StructuredContent` field shape — not prose contents.
- CI workflow (`.github/workflows/smoke.yml` or extension of existing) that runs the smoke test on every push.
- Pass criteria: `make smoke` exits 0 within < 5 min.

**Dependencies / readiness:**
- Easier to write after the *agent-native MCP responses* work (this plan family) lands — assertions are simpler against typed structs than against prose.
- Should pin a specific embedder/summarizer stub (e.g. deterministic in-memory implementations of `embeddings.Embedder` / `summaries.PackageSummarizer`) to avoid CI flakiness against Ollama/Anthropic.

**Out of scope:** Performance benchmarks, real provider live tests (those stay under `TestLive*`), TUI smoke.

**Owner / status:** Unassigned. Re-evaluate priority after structured-response work is merged.
