# Review: Run Observability Design

**Date:** 2026-05-23
**Reviewed spec:** [`docs/2026-05-23-run-observability-design.md`](2026-05-23-run-observability-design.md)
**Status:** No blocking findings

## Summary

The current revision addresses the second-pass blockers in the body of the design, not only in the response table.

- Provider identity is now role-specific: `EmbedIdentity()` on `embeddings.Embedder`, `SummaryIdentity()` on `summaries.PackageSummarizer`. That resolves the dual-role `*openai.Client` ambiguity, because OpenAI can return separate identities for its embedding and chat paths while single-role providers implement only their relevant method (`docs/2026-05-23-run-observability-design.md:131`).
- The TUI section now explicitly changes `store.IndexRun`, `PG.Runs`, fake-store data, and the runs detail panel while leaving only `PG.Pipeline` on the legacy `files_processed` shim (`docs/2026-05-23-run-observability-design.md:434`).
- The column contract now says writers keep populating `files_processed` alongside `metrics`, with `symbols_extracted` and `edges_created` remaining code-stage only (`docs/2026-05-23-run-observability-design.md:63`).

The design is coherent enough to implement.

## Implementation Watchpoints

1. **Make OpenAI model plumbing one source of truth.**

   The design depends on changing `internal/providers/openai/client.go` so the executed model fields and identity methods read the same values (`docs/2026-05-23-run-observability-design.md:184`). Current code still hardcodes the chat and embedding models (`internal/providers/openai/client.go:104`, `internal/providers/openai/client.go:138`) and `buildProviders` still does not pass `cfg.Embeddings.Model` / `cfg.Summarization.Model` into the OpenAI client (`cmd/projectlens/main.go:754`, `cmd/projectlens/main.go:771`). Treat that as required implementation work, not optional cleanup.

2. **Keep `PG.Pipeline` and `PG.Runs` deliberately different.**

   The design now says `PG.Pipeline` stays on `files_processed` while `PG.Runs` selects provider, metrics, and error columns for the detail panel (`docs/2026-05-23-run-observability-design.md:451`). That matches the current split: pipeline currently selects only stage freshness plus `files_processed` (`internal/tui/store/pg.go:91`), while runs currently selects the detail row fields (`internal/tui/store/pg.go:205`). The implementation should update only the runs/detail path for rich fields and avoid expanding the pipeline query unnecessarily.

3. **Test both stale-row and sparse-metric rendering.**

   Old rows will have `NULL` provider strings, `{}` metrics, and `NULL` errors (`docs/2026-05-23-run-observability-design.md:513`). The report and TUI detail tests should include those old-row defaults plus sparse metrics, because the design intentionally allows missing keys in v1 (`docs/2026-05-23-run-observability-design.md:298`).

## Cleared Findings

- Pass 1 finding 1, MCP/report shape leak: cleared by report-only `StageDetail`.
- Pass 1 finding 2, provider strings can lie: cleared by role-specific provider identity plus OpenAI model plumbing requirement.
- Pass 1 finding 3, metrics exceed stage signatures: cleared by typed per-stage stats and sparse maps.
- Pass 1 finding 4, TUI zeros: cleared by the `files_processed` compatibility shim and concrete runs-detail store changes.
- Pass 2 finding 1, dual-role identity: cleared by `EmbedIdentity()` / `SummaryIdentity()`.
- Pass 2 finding 2, TUI store contradiction: cleared by the explicit `IndexRun` / `PG.Runs` / detail-panel changes.
- Pass 2 finding 3, column-contract contradiction: cleared by the rewritten legacy-column contract.
