# Resource-Safety Lessons From pnphive

Date: 2026-05-21
Status: lessons and future-spec guidance
Source: `docs/2026-05-21-pnphive-comparison.md`

## Purpose

Capture pnphive's hard-earned resource-safety practices so ProjectLens can avoid accidental laptop saturation as indexing, provider usage, tests, and future document ingestion grow.

This document is not an implementation plan. It defines the operating principles and future acceptance criteria for resource-heavy ProjectLens workflows.

## Decision

ProjectLens should adopt explicit resource-safety guardrails for heavyweight local work.

This matters even if ProjectLens is not being packaged as a local user-facing product yet. Maintainers still run:

- full and incremental indexing,
- embeddings,
- summarization,
- TUI-triggered stage jobs,
- integration tests,
- future PR/Jira/Confluence ingestion,
- report/export over large indexes.

pnphive shows that these workflows can overwhelm a developer machine unless concurrency, model loading, and thread pools are controlled deliberately.

## pnphive Lessons To Reuse

### 1. Heavy Commands Need Supported Wrappers

pnphive routes common operations through scripts such as `scripts/pnphive` and `scripts/test.sh`.

ProjectLens should continue using supported entrypoints rather than expecting maintainers to remember safe low-level commands.

Good future pattern:

- `make test`
- `make test-int`
- `make index-all`
- `make reindex`
- `make tui`
- future `projectlens history` / `projectlens report`

The wrapper should own safe defaults, not the user's shell.

### 2. Thread Pools Must Be Capped Explicitly

pnphive caps common ML/BLAS/thread-pool variables before importing heavyweight libraries:

- `OMP_NUM_THREADS`
- `MKL_NUM_THREADS`
- `OPENBLAS_NUM_THREADS`
- `NUMEXPR_NUM_THREADS`
- `VECLIB_MAXIMUM_THREADS`
- `TOKENIZERS_PARALLELISM`

ProjectLens is Go-first, but it still talks to local embedding providers and may add rerankers or helper processes later. Any future local ML integration should set and document safe thread/concurrency defaults.

### 3. Prevent Accidental Concurrent Heavy Jobs

pnphive uses lockfiles for test and ingest workflows after concurrent runs caused serious resource pressure.

ProjectLens already has a DB-backed writer lock for mutating index operations. That is the right core primitive. Future work should ensure every mutating stage uses it consistently, including:

- TUI-triggered actions,
- CLI stage commands,
- future document ingestion,
- future snapshot restore or import operations.

Tests that start heavyweight providers or databases should also guard against accidental parallel execution when parallelism is unsafe.

### 4. Model Warmup Is Operational State

pnphive warms embedder/reranker models in the MCP process so the first query does not pay the full load cost.

ProjectLens should treat provider warmup and health as part of operational state:

- report provider reachability,
- distinguish configured vs reachable vs not configured,
- avoid surprising first-call latency where possible,
- surface provider failures in `index_status`, TUI, and report output.

### 5. Background Priority Is Better Than Raw Speed

pnphive intentionally slows embedder work to keep the laptop usable.

ProjectLens should prefer predictable interactive usability over maximum throughput for local maintenance workflows. A full index that runs politely is better than a fast index that disrupts active development.

## Proposed ProjectLens Direction

### CLI and Make Targets

Every heavyweight operation should have one supported entrypoint with safe defaults:

- indexing,
- embedding,
- summarization,
- datastore indexing,
- history indexing,
- future PR/Jira/Confluence ingestion,
- integration tests.

Avoid documenting ad hoc commands as the normal path when a wrapper can enforce safer behavior.

### Writer Lock Discipline

All mutating index/document operations should acquire the writer lock or an equivalent stage lock.

Expected behavior:

- a second mutating job fails clearly or queues only if queueing is explicitly implemented,
- stale lock cleanup remains DB-backed and liveness-aware,
- TUI and report can show active writer identity,
- no future ingestion path bypasses locking for convenience.

### Test Safety

Integration and provider-heavy tests should:

- avoid uncontrolled parallel provider loading,
- skip cleanly when required services are unavailable,
- isolate database rows,
- document when a test requires Postgres or live provider access,
- avoid starting multiple expensive jobs from one test process unless that is the behavior under test.

### Provider Safety

Provider integrations should expose:

- configured state,
- reachable state,
- error text safe for display,
- model identity,
- dimension where relevant,
- timeout behavior.

Provider health should remain reusable across MCP, report, and TUI.

## Minimum Future Acceptance Criteria

Before adding heavyweight document ingestion or local reranking:

- Mutating stage commands use writer-lock discipline.
- Test wrappers or test code prevent unsafe parallel provider-heavy runs.
- Provider health is visible through report/status/TUI.
- Resource-related defaults are documented.
- Long-running jobs record start/end/failure in run observability.
- Cancellation or interruption leaves a failed/cancelled run record rather than a silent hanging state.

## Risks

- Over-constraining concurrency can slow development unnecessarily.
- Locking only at process level can miss DB-level concurrent writers.
- Resource caps can hide performance regressions if no benchmark path exists.
- Provider error messages can leak configuration or path details if copied blindly into reports.

## Non-Goals

- Building a full job scheduler.
- Optimizing indexing for maximum throughput.
- Supporting arbitrary parallel indexing jobs.
- Packaging local install/distribution as an immediate priority.

## Recommendation

Use pnphive's resource-safety lessons as guardrails for future ProjectLens work:

1. Keep heavyweight work behind supported wrappers.
2. Ensure every mutating stage participates in writer-lock semantics.
3. Make provider health and long-running job state visible.
4. Add test/resource guards before adding broad document ingestion or local reranking.

