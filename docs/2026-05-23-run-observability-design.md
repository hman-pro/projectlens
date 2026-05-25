# Run Observability — Lean Design

**Date:** 2026-05-23
**Status:** Design — revised after review
**Predecessor:** [`docs/tasks.md`](tasks.md) priority #2
**Review:** [`docs/2026-05-23-run-observability-design-review.md`](2026-05-23-run-observability-design-review.md) — addressed in §"Review Response" below.

## Goal

Every mutating index command leaves enough evidence in `index_runs` to
answer three questions without re-running and without grepping logs:

1. **Did the last run of stage X succeed, and if not, why?**
2. **What providers (embedding model, summarizer) did that run use?**
3. **What did the run actually do?** (counts that fit the stage, not just
   the code-stage triple `files/symbols/edges`)

Anything beyond that is deferred. No config snapshot, no secrets, no
per-stage tables, no run UUIDs, no new tools. One migration, one schema
change, two surfaces.

## Non-Goals

- Run mode (`full` / `incremental`) — defer; pnphive lesson is about
  freshness *evidence*, and we already log mode at info level.
- Full config snapshot — secret-leak risk, low payoff while we are
  the only operator.
- MCP `index_status` extension — agents already see per-stage freshness;
  adding provider/error strings invites prompt-injection vectors and is
  not load-bearing for any current question.
- New `index_stages` or `index_events` table — current single-table model
  is sufficient; sub-stages are already separate rows.
- Latency histograms / Prometheus / OTEL — out of scope for a single-user
  indexer.

## Schema Change

One migration: `008_run_observability.up.sql`.

```sql
ALTER TABLE index_runs
    ADD COLUMN error_text         TEXT,
    ADD COLUMN provider_embed     TEXT,
    ADD COLUMN provider_summarize TEXT,
    ADD COLUMN metrics            JSONB NOT NULL DEFAULT '{}'::jsonb;
```

Down migration drops the four columns.

**Why no `mode`, no `branch`, no `dirty`:** branch already lives in
`git_refs`; mode and dirty can be added later behind the same migration
pattern if needed. The point of "lean" is to not pre-pour concrete.

### Column contracts

| Column | Type | Set by | Semantics |
|---|---|---|---|
| `error_text` | TEXT, nullable | `FailRun` | First 4KB of `err.Error()`. `NULL` for non-failed runs. |
| `provider_embed` | TEXT, nullable | `StartRun` / `RecordStageRun` | `"<vendor>:<model>@<dim>"` (e.g. `openai:text-embedding-3-large@1024`). `NULL` when the stage does not embed. |
| `provider_summarize` | TEXT, nullable | same | `"<vendor>:<model>"` (e.g. `anthropic:claude-sonnet-4-5`). `NULL` when the stage does not summarize. |
| `metrics` | JSONB, default `{}` | `CompleteRun` / `RecordStageRun` | Stage-specific counts. Schema-less by design — see below. |

The legacy `files_processed`, `symbols_extracted`, `edges_created`
columns stay **and continue to be populated** alongside `metrics`.
Each stage writes one representative count into `files_processed`
(see §"TUI runs view + legacy-count compatibility" for the mapping)
so the existing TUI list and pipeline views keep rendering correct
numbers. `symbols_extracted` and `edges_created` remain code-stage
only. The richer per-stage detail lives in `metrics`; report and TUI
detail panel read from there. Old code-stage rows keep their existing
fields untouched.

### `metrics` payload by stage

Convention, not enforced:

| Stage | Keys |
|---|---|
| `code` | `files`, `symbols`, `edges`, `chunks` |
| `embed` | `chunks`, `tokens`, `batches` |
| `summarize` | `packages`, `summaries`, `tokens` |
| `history` | `commits`, `file_history_rows`, `coupling_pairs` |
| `datastore` | `migrations`, `tables`, `sql_files`, `table_refs` |
| `docs` (future) | `documents`, `chunks` |

Writers pass `map[string]any`; storage marshals. Unknown keys allowed
so we don't gate forward progress on schema agreement.

## Storage API Changes

`internal/storage/indexruns.go`:

```go
type RunProviders struct {
    Embed     string // "" if not applicable
    Summarize string
}

// StartRun: add providers parameter.
func (db *DB) StartRun(ctx context.Context, commitSHA string, p RunProviders) (int64, error)

// CompleteRun: add metrics parameter; keep legacy counters for code stage.
func (db *DB) CompleteRun(ctx context.Context, runID int64,
    filesProcessed, symbolsExtracted, edgesCreated int,
    metrics map[string]any) error

// FailRun: add error string.
func (db *DB) FailRun(ctx context.Context, runID int64, errText string) error

// RecordStageRun: add providers and metrics.
func (db *DB) RecordStageRun(ctx context.Context,
    commitSHA, stage, status string,
    started, completed time.Time,
    providers RunProviders, metrics map[string]any,
    errText string) error
```

Truncation: `errText` clipped at 4096 bytes inside storage so callers
don't have to think about it.

`IndexRunRecord` gains four fields mirroring the columns. JSON tags
match column names. `metrics` exposed as `map[string]any`.

## Writer Wiring

Provider identity comes from the **constructed client**, not from
config. Config is the *intent*; the client is the *truth*. Recording
config strings risks lying when the client hardcodes the model
(`internal/providers/openai/client.go:104` and `:138` currently do).

### Provider identity interfaces

Identity methods are **role-specific**, not shared. `*openai.Client`
implements both `Embedder` and `PackageSummarizer` today
(`internal/providers/openai/client.go:101` summarize,
`internal/providers/openai/client.go:124` embed); a single `Identity()`
method on that type would have to pick one role and lie about the
other. So the interfaces carry distinct methods:

```go
// internal/embeddings/embeddings.go
type Embedder interface {
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
    EmbedIdentity() ProviderIdentity     // NEW — role-specific
}

// internal/summaries/package_summary.go
type PackageSummarizer interface {
    GeneratePackageSummary(ctx context.Context, pkg string, syms []string) (string, error)
    SummaryIdentity() ProviderIdentity   // NEW — role-specific
}

// shared (new tiny package internal/providers/identity, or co-located):
type ProviderIdentity struct {
    Vendor     string // "openai", "azure-openai", "ollama", "anthropic"
    Model      string // executed model, not configured
    Dimensions int    // 0 when not applicable (summarizers)
}

func (p ProviderIdentity) String() string {
    if p.Vendor == "" || p.Model == "" {
        return ""
    }
    if p.Dimensions > 0 {
        return fmt.Sprintf("%s:%s@%d", p.Vendor, p.Model, p.Dimensions)
    }
    return fmt.Sprintf("%s:%s", p.Vendor, p.Model)
}
```

`*openai.Client` implements both: `EmbedIdentity()` returns
`{Vendor: "openai", Model: c.embedModel, Dimensions: c.embeddingDims}`
and `SummaryIdentity()` returns
`{Vendor: "openai", Model: c.chatModel}`. The two methods read
*different* internal fields and cannot conflict. Single-role clients
(`anthropic.Client` implements only `SummaryIdentity`,
`ollama.Client` only `EmbedIdentity`) implement just the one method
their interface requires.

The string returned is whatever the client will actually send to the
upstream API this run. No silent divergence with config because the
identity comes from the same field the client passes to the SDK call.

**Required pre-work in same PR:**

- `internal/providers/openai/client.go`: stop hardcoding model names.
  Constructor accepts model strings or reads them from `Client` fields
  (already accepts `embeddingDims`; add `embedModel`, `chatModel`).
  `buildProviders` passes `cfg.Embeddings.Model` and
  `cfg.Summarization.Model` through. Implement both `EmbedIdentity()`
  and `SummaryIdentity()`. This is a fix the design depends on — without
  it, recording lies.
- `internal/providers/anthropic/client.go`: already takes model via
  constructor; implement `SummaryIdentity()`.
- `internal/providers/ollama/client.go`: takes model via constructor;
  implement `EmbedIdentity()` returning vendor=`ollama`, dim from model
  metadata where known, else 0.

### Wiring at writer sites

Each entrypoint that creates a row reads `Identity()` off the live
client(s):

```go
// internal/indexer/indexer.go: store identities so Run can record them.
type Indexer struct {
    // ...
    embedIdentity     ProviderIdentity // zero when embedder == nil
    summarizeIdentity ProviderIdentity // zero when summarizer == nil
}

// New() captures identities once. Subsequent calls to record runs use
// these without re-asking the client.
func New(...) *Indexer {
    idx := &Indexer{...}
    if embedder != nil   { idx.embedIdentity = embedder.EmbedIdentity() }
    if summarizer != nil { idx.summarizeIdentity = summarizer.SummaryIdentity() }
    return idx
}
```

Standalone stages that don't take providers (`history`, `datastore`)
record empty identity. Standalone `embed` / `summarize` stages capture
identity the same way as `Indexer.New`.

Provider string format ends up like:

```
openai:text-embedding-3-large@1024
azure-openai:text-embedding-3-large@1024
ollama:mxbai-embed-large@1024
anthropic:claude-sonnet-4-5
openai:gpt-4o-mini
```

No endpoint URLs in the string (potential secret leak if hostnames are
sensitive). If we need to distinguish Azure deployments later, add an
opt-in column rather than smuggling URLs into a string.

### Standalone stages must return typed stats

Today `recordStageRun` in `cmd/projectlens/main.go:515` takes a
`func() (int, error)`. Stage functions return a single int:
`datastore.IndexDatastore` returns table count
(`internal/datastore/indexer.go:28`); `history.IndexHistory` returns
commits (`internal/history/indexer.go:38`); `embed.Run` returns chunks
embedded (`internal/embed/embed.go:14`); `summarize.Run` returns
packages summarized (`internal/summarize/summarize.go:13`).

This shape can't carry the metrics this design promises. Widen each
to a typed stats struct co-located with the package:

```go
// internal/datastore
type Stats struct {
    Migrations int
    Tables     int
    SQLFiles   int
    TableRefs  int
}
func IndexDatastore(...) (Stats, error)

// internal/history
type Stats struct {
    Commits         int
    FileHistoryRows int
    CouplingPairs   int
}
func IndexHistory(...) (Stats, error)

// internal/embed
type Stats struct {
    Chunks  int
    Tokens  int
    Batches int
}
// internal/summarize
type Stats struct {
    Packages  int
    Summaries int
    Tokens    int
}
```

The numbers come from places those functions already compute internally
for logging (look for `logger.Info("...", "commits", n, ...)`); the
change is "stop discarding them at the return statement". If a metric
is not actually computed today (`embed.Tokens`, `summarize.Tokens`,
`datastore.SQLFiles`), zero is acceptable for v1 and the field can be
populated in a follow-up. The metrics convention table below is a
target schema; missing keys are NOT a failure.

`recordStageRun` accepts a `func() (map[string]any, error)`. CLI
wrappers in `cmd/projectlens/main.go` translate `Stats` → map via tiny
adapters (e.g. `datastoreMetrics(s)`), keeping `internal/datastore`
free of observability concerns.

### Metrics convention — relaxed wording

The earlier table stands as a *target shape*. Implementation v1 ships
whatever Stats already computes. Missing keys render as absent in
report; downstream code must tolerate sparse maps. A later PR may
backfill once a metric becomes load-bearing for some agent question.

## Failure Path

Today `cmd/projectlens/index.go` and siblings call `FailRun(ctx, id)`
inside a deferred recover or after an error. Change those sites to:

```go
if err != nil {
    if runID > 0 {
        _ = db.FailRun(ctx, runID, err.Error())
    }
    return err
}
```

For `RecordStageRun`-style stages (history, datastore) that don't have
a separate "running" row, the existing post-hoc insert path takes
`errText` as a parameter and writes the row with `status='failed'`.

Stage failures already get logged via charmbracelet; nothing changes
about logs. The DB row is the durable artifact.

## Surfaces

### Split DTO — MCP `index_status` stays narrow

`internal/report` currently uses
`map[string]indexstate.StageFreshness` for its `Stages` map, and MCP
`index_status` aliases the same type
(`internal/mcpserver/types.go:12`, `internal/mcpserver/handlers.go:437`).
Extending `StageFreshness` with providers/metrics/error would leak the
new shape into MCP — which §Non-Goals forbids.

Resolution: keep `indexstate.StageFreshness` unchanged. Add a
report-only DTO in `internal/report`:

```go
// internal/report/stage.go
type StageDetail struct {
    indexstate.StageFreshness                 // embed the narrow shape
    Providers       StageProviders            `json:"providers"`
    Metrics         map[string]any            `json:"metrics"`
    Error           string                    `json:"error,omitempty"`
    DurationSeconds float64                   `json:"duration_seconds,omitempty"`
}

type StageProviders struct {
    Embed     string `json:"embed,omitempty"`
    Summarize string `json:"summarize,omitempty"`
}
```

Change `Report.Stages` from `map[string]indexstate.StageFreshness` to
`map[string]report.StageDetail`. The MCP handler keeps building
`map[string]indexstate.StageFreshness` directly from a *separate*
inspector method (it already does — `indexstate.Inspector.Freshness`
or equivalent). The two paths now diverge cleanly: MCP reads the
narrow inspector, report reads a new builder method that joins
`index_runs` rows with the four new columns.

Concretely:

- `indexstate` package: no change.
- `internal/mcpserver`: no change to types or handler payload.
- `internal/report`: new `StageDetail` type, new builder query that
  selects the four new columns plus the freshness fields.
- Tests cover both shapes side-by-side so a future refactor that
  consolidates them must do so explicitly.

### `projectlens report`

Add a **Stages** section between **Repo / Git** and **Top Packages**:

```
## Stages

| Stage     | Status     | Last Run             | Age   | Provider                                      | Metrics                                | Error |
|-----------|------------|----------------------|-------|-----------------------------------------------|----------------------------------------|-------|
| code      | completed  | 2026-05-23 09:14 UTC | 12m   | embed=openai:...@1024 sum=anthropic:...       | files=2913 symbols=23104 edges=217k    |       |
| embed     | completed  | 2026-05-23 09:18 UTC | 8m    | embed=openai:text-embedding-3-large@1024      | chunks=25104 tokens=4.1M batches=98    |       |
| summarize | completed  | 2026-05-22 22:01 UTC | 11h   | sum=anthropic:claude-sonnet-4-5               | packages=772 summaries=772             |       |
| history   | completed  | 2026-05-23 07:00 UTC | 2h    | -                                             | commits=10543 file_history=14512       |       |
| datastore | failed     | 2026-05-23 07:02 UTC | 2h    | -                                             | migrations=123                         | parse: …unterminated string… |
```

JSON shape mirrors the table. Each stage entry includes:

```json
{
  "stage": "datastore",
  "status": "failed",
  "started_at": "...",
  "completed_at": "...",
  "duration_seconds": 4.1,
  "providers": {"embed": "", "summarize": ""},
  "metrics": {"migrations": 123},
  "error": "parse: ..."
}
```

Truncate `error` to 200 chars in markdown; full text in JSON.

### TUI runs view + legacy-count compatibility

Current TUI store reads only the legacy columns:

- `internal/tui/store/pg.go:91` — pipeline view scans
  `files_processed` as the per-stage count.
- `internal/tui/store/pg.go:205` — runs list scans
  `files_processed, symbols_extracted, edges_created`.
- `internal/tui/sections/runs/update.go:68` and
  `internal/tui/sections/runs/view.go:36` render those.

If new stages (`embed`, `summarize`, `history`, `datastore`) write
their counts only to `metrics`, the pipeline view shows `0` for every
non-`code` stage. That's a regression.

Resolution: writers populate **both** `files_processed` (legacy) and
`metrics` (new). The CLI wrapper for each stage picks the metric that
best maps to "items processed this stage" and writes it to
`files_processed` as a *compatibility shim*:

| Stage | `files_processed` (legacy compat) | `metrics` (full detail) |
|---|---|---|
| code | files | files, symbols, edges, chunks |
| embed | chunks | chunks, tokens, batches |
| summarize | packages | packages, summaries, tokens |
| history | commits | commits, file_history_rows, coupling_pairs |
| datastore | tables | migrations, tables, sql_files, table_refs |

The legacy shim keeps the TUI **list and pipeline views correct
without query changes** for those two paths. The detail panel,
however, must show the new columns — and that does require store
changes. Concrete diff:

- `internal/tui/store/types.go` — extend `IndexRun`:

  ```go
  type IndexRun struct {
      // ... existing fields ...
      ProviderEmbed     string         // "" when not applicable
      ProviderSummarize string         // ""
      Metrics           map[string]any // nil when empty
      ErrorText         string         // "" when not failed or no message
  }
  ```

- `internal/tui/store/pg.go` — `PG.Runs` (line ~205) selects four new
  columns:

  ```sql
  SELECT id, started_at, completed_at, commit_sha, stage, status,
         files_processed, symbols_extracted, edges_created,
         provider_embed, provider_summarize, metrics, error_text
  FROM index_runs ORDER BY id DESC LIMIT $1
  ```

  Scan `metrics` into `[]byte` then `json.Unmarshal` into the map;
  `provider_*` and `error_text` are nullable, scan via `sql.NullString`
  or pgx equivalents.

  `PG.Pipeline` (line ~91) does **not** change — it keeps using the
  legacy `files_processed` shim.

- `internal/tui/store/fake.go` — `Fake.SetRuns` accepts the same
  extended `IndexRun` (the type change covers it; if a builder helper
  exists, extend it).

- `internal/tui/sections/runs/view.go` — detail panel (around line 36)
  renders the new fields when populated:
  - Providers line: `embed=<id> sum=<id>` (omit empty halves; omit
    whole line if both empty)
  - Metrics line: sorted `key=value` pairs from `Metrics`
  - Error block: red text, truncated to terminal width with a "…"
    suffix when the full text would overflow; full text accessible
    via existing scroll/expand if any.

- `internal/tui/sections/runs/update.go` — no semantic change; passes
  through the extended `IndexRun`.

No new keybinding. Existing list navigation suffices.

## Out of Scope (will reject in review)

- New tables (`index_events`, `index_stages_v2`, etc.)
- Run UUIDs replacing `BIGSERIAL`
- Prometheus / OTEL exporters
- Provider call-level retry/latency metrics inside `metrics` blob
- Config-hash columns
- Branch / dirty-tree columns
- MCP `index_status` shape changes (separate decision)
- Backfilling provider strings into old rows — leave NULL, report
  renders "-" for unknown

## Migration & Rollout

One PR:

1. Migration 008 up/down.
2. Storage API change (additive method signatures break callers — fix
   the small handful of call sites in same commit).
3. Wire providers + metrics + errText at each writer.
4. Report Stages section + JSON.
5. TUI detail panel addition.
6. Update `docs/operations.md` (report output sample), `docs/internals.md`
   (index_runs columns), `CLAUDE.md` source-of-truth table.
7. Update `agent/skills/use-projectlens/SKILL.md` only if MCP shape
   changes — it doesn't, so skip.

No data migration needed. Old rows have `NULL` provider strings,
`'{}'::jsonb` metrics by default, `NULL` error_text. Report tolerates
all three.

## Error Text Sanitization

`error_text` lands in DB and JSON. Provider/database errors can echo
credentials, signed URLs, or headers. Boundary policy:

1. **Storage layer truncates to 4096 bytes.** Caller doesn't think
   about it.
2. **Storage layer redacts known secret patterns** before insert:
   - `Bearer <token>` → `Bearer [REDACTED]`
   - `sk-...` (OpenAI key prefix) → `sk-[REDACTED]`
   - `Authorization: ...` header substrings → `Authorization: [REDACTED]`
   - Anthropic key prefix `sk-ant-...` → `sk-ant-[REDACTED]`
   - Postgres URL passwords (`postgres://user:PWD@`) → `postgres://user:[REDACTED]@`
3. **No env-var scrubbing** — too broad, false positives. If a caller
   wraps an error with raw env values, that's a bug to fix at the
   wrap site.

Redaction implemented as a single `sanitizeErrText(string) string`
helper in `internal/storage`, unit-tested with the five patterns
above. Failing the redaction step does NOT fail the run record; it
falls back to truncation only. This is a best-effort hygiene step,
not a security boundary — the DB is already on a trusted host.

## Open Questions

- Do we want `metrics` keys validated by stage at write time, or trust
  callers? **Proposal:** trust now, add a small `validateMetrics(stage,
  map)` helper later if drift becomes a problem.
- Should `provider_embed` include the configured dimension (`@1024`) or
  not? **Proposal:** yes — dim is the most operationally relevant bit
  after model name and is already in the embedding chain. Mismatched
  dim breaks search.
- Truncate error to 4KB or store full and clip in report? **Proposal:**
  store 4KB, clip in markdown to 200 chars, JSON full.

## Review Response (2026-05-23, pass 2)

Second-pass review raised three blockers; all addressed in-place above.

| Finding | Resolution | Section |
|---|---|---|
| 1. Single `Identity()` is ambiguous for `*openai.Client` (both embedder + summarizer) | Split into role-specific methods: `EmbedIdentity()` on `Embedder`, `SummaryIdentity()` on `PackageSummarizer`. `*openai.Client` implements both reading different fields (`embedModel`/`embeddingDims` vs `chatModel`). | §"Provider identity interfaces" |
| 2. "Store unchanged" contradicts the detail-panel promise | Dropped the "unchanged" claim. Specified concrete diffs: `IndexRun` gains four fields; `PG.Runs` selects four new columns; `Fake.SetRuns` follows; detail panel renders providers/metrics/error. `PG.Pipeline` stays on the legacy shim. | §"TUI runs view + legacy-count compatibility" |
| 3. Column-contract paragraph still said "stop populating" legacy columns | Rewritten to say writers populate both legacy and `metrics`, with `files_processed` taking the representative count per stage. | §"Column contracts" |

## Review Response (2026-05-23, pass 1)

First-pass findings — kept for history.

| Finding | Resolution | Section |
|---|---|---|
| 1. MCP `index_status` would leak the report's richer shape via shared `StageFreshness` | New report-only `StageDetail` DTO; `indexstate.StageFreshness` unchanged; MCP handler untouched | §"Split DTO" |
| 2. Provider from config can lie because clients hardcode models and config fields are ignored | `Identity()` method on `Embedder` / `PackageSummarizer`; pre-work in same PR stops `openai/client.go` hardcoding `gpt-4o-mini` / `text-embedding-3-large`; identity captured at client construction, not from config | §"Provider identity interfaces" |
| 3. `metrics` shape exceeds current stage signatures (`func() (int, error)`) | Widen each standalone stage to return a typed `Stats` struct; `recordStageRun` callback becomes `func() (map[string]any, error)`; convention table is target shape, sparse maps allowed in v1 | §"Standalone stages must return typed stats" |
| 4. Legacy column zeros would break TUI pipeline + list | Writers populate both `files_processed` (single representative count) and `metrics` (full); TUI store unchanged; detail panel reads `metrics` | §"TUI runs view + legacy-count compatibility" |

Plus reviewer's note on error sanitization → §"Error Text Sanitization".
