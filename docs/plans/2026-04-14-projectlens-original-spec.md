# ProjectLens — Implementation Plan Specification

Version: 0.1  
Status: Draft for implementation  
Primary implementation agent: Claude Code  
Deployment model: Local-first, containerized, team-shareable later

---

## 1. Purpose

ProjectLens is a local-first repository intelligence layer for a large codebase. It is designed to reduce repeated broad file exploration by coding agents and replace it with targeted retrieval over precomputed repository structure.

The system will begin with handwritten Go code and later expand to tests, Markdown docs, Atlassian content, infrastructure artifacts, and design sources such as Figma and Miro.

The first implementation target is **Claude Code**. ProjectLens will be exposed to Claude Code via **MCP**, supported by **CLAUDE.md** guidance and a small set of reusable **skills**.

---

## 2. Problem Statement

Current coding agents often need to reopen many related files on every question in order to reconstruct dependencies, usage patterns, and architectural relationships.

That approach is:
- slow
- expensive in tokens
- inconsistent
- vulnerable to noisy corpora
- bad at staying precise in large monolithic repos

ProjectLens solves this by moving a large portion of discovery work from **query time** to **index time**.

---

## 3. Core Principles

1. **Retrieval over training**  
   Use retrieval and structured repository indexing for live knowledge. Do not train or continue-pretrain a model on the repo as a primary solution.

2. **Fine-tuning is optional and later**  
   Fine-tuning is for behavior and output style, not for keeping the model aware of the latest codebase state.

3. **Index less, but better**  
   Exclude noise aggressively. Precision matters more than raw coverage.

4. **Start narrow**  
   Start with handwritten Go only. Expand only when a new source clearly improves answer quality for real questions.

5. **Agent-friendly surface**  
   Keep the MCP tool surface small and high-leverage.

6. **Explainable answers**  
   Every answer should be grounded in exact symbols, files, package summaries, tests, docs, or design artifacts.

7. **Local-first, team-ready later**  
   The first version must run locally with Docker Compose. The architecture must support later team-wide rollout with minimal redesign.

---

## 4. Scope

### 4.1 In scope for v1
- handwritten Go code
- Go package and file summaries
- symbol-level indexing
- lexical retrieval
- semantic retrieval
- basic graph relationships
- MCP integration for Claude Code
- root `CLAUDE.md`
- a small initial skill set for Claude Code
- local Docker-based development and operation

### 4.2 In scope after v1
- Go tests
- change impact analysis
- Markdown docs
- Atlassian content via existing MCP workflows
- SQL / Proto / GraphQL / Terraform / Bazel
- selected YAML / JSON configs
- Figma / Miro metadata ingestion
- branch-aware retrieval
- team packaging

### 4.3 Out of scope for early versions
- training a model from scratch
- continued pretraining on repo data
- indexing all replay files and bulk fixtures
- full OCR-first design ingestion
- giant tool surfaces exposed directly to the coding agent
- optimizing for every language from day one

---

## 5. Initial Repository Policy

This policy is specific to the current monorepo-like codebase.

### 5.1 Primary corpus
Default search target.

- handwritten Go

### 5.2 Secondary corpus
Not in the first-pass ranking pool initially, but stored and retrievable when relevant.

- Go tests (`*_test.go`)
- generated Go

### 5.3 Deferred corpus
Not part of early indexing, except for selective later ingestion.

- Markdown docs
- SQL migrations
- Bazel
- Proto
- GraphQL
- Terraform
- YAML / JSON configs
- Atlassian docs
- Figma / Miro

### 5.4 Excluded by default
- replay files
- compressed or binary-ish artifacts
- giant fixture dumps
- bulk generated snapshots

---

## 6. Target Developer Experience

A developer asks Claude Code a question such as:

- “Where is inventory reservation implemented?”
- “What tests define expected behavior for this package?”
- “What breaks if I change this interface?”

Claude Code should:
1. call ProjectLens through MCP
2. retrieve ranked symbols, package summaries, and later graph neighbors
3. open only a few real files for confirmation
4. answer with grounded evidence

The agent should **not** need to rediscover large parts of the codebase from scratch on every task.

---

## 7. High-Level Architecture

ProjectLens consists of four local components.

### 7.1 `projectlens-indexer`
Responsible for scanning the repo, classifying files, parsing source code, extracting symbols, building summaries, and updating graph edges.

### 7.2 `projectlens-db`
Persistent storage for metadata, chunks, summaries, edges, embeddings, and index state.

### 7.3 `projectlens-mcp`
MCP server exposing a small tool surface for Claude Code.

### 7.4 `projectlens-cli`
Developer-facing CLI for local operation and debugging.

Suggested commands:
- `projectlens census`
- `projectlens bootstrap`
- `projectlens reindex`
- `projectlens status`
- `projectlens inspect-symbol`
- `projectlens inspect-package`
- `projectlens query`

---

## 8. Storage and Runtime Choices

### 8.1 Initial storage choice
Use **Postgres + pgvector**.

Reasons:
- simple local setup
- supports structured metadata and vectors in one place
- easy to containerize
- reduces infrastructure complexity in early phases

### 8.2 Retrieval modes
Support three retrieval modes internally.

1. **Lexical retrieval**  
   For exact matches: symbols, paths, package names.

2. **Semantic retrieval**  
   For intent-based natural language lookup over chunks and summaries.

3. **Graph expansion**  
   For relationships such as callers, callees, implementations, and tests.

### 8.3 Embeddings
Use embeddings only for retrieval, not as a substitute for source-of-truth metadata.

Requirements:
- embedding model version stored with every chunk
- re-embedding should be possible without corrupting data
- semantic retrieval should never be the only retrieval path

---

## 9. Data Model

ProjectLens must support multiple future source types, even though v1 is Go-only.

### 9.1 Canonical entity types
- `code_file`
- `code_symbol`
- `package`
- `test_case`
- `doc_section`
- `design_node`
- `service_boundary`
- `config_artifact`

### 9.2 Canonical edge types
- `imports`
- `calls`
- `implements`
- `test_of`
- `documents`
- `designs`
- `configures`
- `depends_on`
- `co_changed_with`

### 9.3 Minimum fields for indexed code symbols
- repo name
- branch
- commit SHA
- file path
- language
- category
- generated flag
- test flag
- package name
- symbol name
- symbol kind
- line range
- checksum
- summary version
- embedding version
- index timestamp

### 9.4 Minimum tables or collections
- `files`
- `symbols`
- `chunks`
- `summaries`
- `edges`
- `embeddings`
- `index_runs`
- `git_refs`

---

## 10. Go-First Parsing and Chunking Strategy

### 10.1 Initial source coverage
Only `.go` files in handwritten code paths.

### 10.2 Symbol extraction targets
- packages
- functions
- methods
- structs
- interfaces
- constants
- variables

### 10.3 Chunking rules
Chunk by **symbol**, not by arbitrary token window.

Each code chunk should contain:
- symbol signature
- nearby comments / docs
- symbol body
- package context
- line range metadata

### 10.4 Summary generation
Create:
- file summary
- package summary

Package summaries are important because the repo is somewhat monolithic and package boundaries are one of the main navigational units.

---

## 11. Ranking and Retrieval Policy

### 11.1 Query classification
Classify incoming questions into likely retrieval patterns:
- exact symbol lookup
- implementation search
- package ownership
- dependency tracing
- test lookup
- change impact
- architecture intent

### 11.2 Retrieval execution
Run retrieval in parallel:
- lexical search
- semantic search
- graph expansion where available

### 11.3 Initial ranking features
- exact symbol match bonus
- same package bonus
- file path match bonus
- handwritten Go bonus
- generated-code penalty
- test penalty unless test-related question
- semantic relevance score

### 11.4 Verification model
The agent should verify final answers by opening only the top files or symbols, not by broad exploration.

---

## 12. MCP Surface for Claude Code

Keep the tool surface small and stable.

### 12.1 Initial MCP tools
- `find_symbol`
- `search_go_context`
- `get_symbol_context`
- `get_package_summary`

### 12.2 Phase 2 MCP tools
- `find_related_tests`
- `find_callers`
- `find_callees`
- `explain_change_impact`

### 12.3 Phase 3+ MCP tools
- `find_docs_for_symbol`
- `search_architecture_knowledge`
- `trace_feature_across_code_and_docs`
- `find_design_context`

### 12.4 MCP design rule
Push retrieval intelligence into ProjectLens, not into agent prompt logic.

The ideal pattern is that Claude Code calls high-value tools rather than manually orchestrating dozens of low-level search steps.

---

## 13. Claude Code Integration Strategy

ProjectLens is being optimized for Claude Code as the primary agent interface.

### 13.1 Use `CLAUDE.md` for
- repo overview
- important directories
- build and test commands
- generated file rules
- guidance to use ProjectLens first
- guidance about when tests should be consulted

### 13.2 Use skills for
- repeatable workflows
- investigation playbooks
- repo-specific procedures

### 13.3 Use MCP for
- structured retrieval
- summaries
- graph lookups
- impact tracing

### 13.4 Use hooks later for
- deterministic checks
- session bootstrap
- automatic lightweight reindex triggers
- completion verification

---

## 14. Required Claude Code Files

### 14.1 Root `CLAUDE.md`
Must include:
- repo map
- important directories
- build / test / lint commands
- generated code patterns
- how and when to use ProjectLens
- preferred order of investigation

### 14.2 Initial Claude skills
Create these first:

#### `trace-go-flow`
Purpose:
- locate implementation path for a behavior or symbol

Suggested flow:
1. use ProjectLens to find symbol or package
2. retrieve symbol context and package summary
3. inspect top files
4. summarize implementation flow

#### `debug-go-test`
Purpose:
- investigate failing or relevant tests

Suggested flow:
1. find relevant tests
2. map tests to production symbols
3. inspect key code paths
4. explain expected behavior vs actual behavior

#### `explain-go-impact`
Purpose:
- estimate what breaks if a change is made

Suggested flow:
1. identify target symbol
2. find callers and package dependents
3. inspect top impacted files
4. summarize likely impact and uncertainty

---

## 15. Containerization Specification

The first version must run locally via Docker Compose.

### 15.1 Initial services
- `postgres`
- `projectlens`
- optional embedding worker if split later

### 15.2 Requirements
- repo path mountable into container
- persistent database volume
- configuration via environment files
- ability to run `bootstrap`, `reindex`, and `serve-mcp`

### 15.3 Team-ready future
When the system is later shared across the team, reuse the same images with:
- shared database
- optional shared ProjectLens service
- optional local MCP client config per developer

---

## 16. Suggested Repository Structure for ProjectLens

```text
projectlens/
  cmd/
    projectlens/
    projectlens-indexer/
    projectlens-mcp/
  internal/
    census/
    classifier/
    parser/
    symbols/
    summaries/
    graph/
    retrieval/
    rerank/
    mcpserver/
    storage/
  configs/
    index.yaml
    retrieval.yaml
  docker/
    Dockerfile
    docker-compose.yml
  migrations/
  scripts/
  README.md
```

Inside the target codebase repository:

```text
CLAUDE.md
.claude/
  skills/
    trace-go-flow/
      SKILL.md
    debug-go-test/
      SKILL.md
    explain-go-impact/
      SKILL.md
```

---

## 17. Phase Plan

## Phase 0 — Foundation and Census

### Goal
Establish the project skeleton and define the actual corpus that will be indexed.

### Build
- CLI skeleton
- Docker Compose skeleton
- Postgres + pgvector setup
- repo census command
- file classifier
- generated / test / noise detection
- first `CLAUDE.md`

### Deliverables
- `projectlens census`
- `projectlens bootstrap`
- a clear include / exclude policy
- local containers running

### Acceptance criteria
- the repo can be scanned locally
- the system can report indexed vs excluded files
- classification rules are explicit and reproducible

---

## Phase 1 — Go Symbol Intelligence

### Goal
Answer the first useful class of Go implementation questions.

### Build
- parse handwritten Go
- extract symbols
- create symbol chunks
- generate file summaries
- generate package summaries
- lexical retrieval
- semantic retrieval
- initial MCP server

### Deliverables
- `find_symbol`
- `search_go_context`
- `get_symbol_context`
- `get_package_summary`

### Acceptance criteria
- Claude Code can answer most “where is this implemented?” questions with few file opens
- generated code does not dominate the ranking
- package summaries are useful enough for navigation

---

## Phase 2 — Tests and Dependency Tracing

### Goal
Make ProjectLens useful for change analysis and behavioral understanding.

### Build
- index Go tests in a separate namespace
- build basic call relationships
- build interface implementation relationships
- map tests to production packages or symbols
- add impact tracing

### Deliverables
- `find_related_tests`
- `find_callers`
- `find_callees`
- `explain_change_impact`

### Acceptance criteria
- the agent can identify likely affected areas for a symbol change
- the agent can retrieve relevant tests as behavioral evidence
- production code still outranks tests for implementation questions

---

## Phase 3 — Claude Workflow Hardening

### Goal
Make Claude Code use ProjectLens consistently and predictably.

### Build
- refine `CLAUDE.md`
- add first three skills
- optionally add session and verification hooks
- add index freshness checks

### Deliverables
- reusable Claude skills
- optional lightweight hooks
- stable local Claude workflow

### Acceptance criteria
- Claude Code naturally consults ProjectLens first
- common investigation flows are repeatable and low-friction
- index freshness problems are visible to the user

---

## Phase 4 — Markdown and Docs

### Goal
Bring in architecture intent and textual system knowledge.

### Build
- local Markdown parsing
- section-based chunking
- document summaries
- link docs to code packages and symbols
- integrate usage with Atlassian MCP for broader docs

### Deliverables
- doc section indexing
- code-to-doc linking
- documentation-aware Claude workflows

### Acceptance criteria
- implementation questions can be enriched by design or architecture rationale
- docs support answers without outranking code as implementation evidence

---

## Phase 5 — Team Packaging

### Goal
Prepare ProjectLens for team-wide use without changing the core architecture.

### Build
- package shared images
- package standard Claude config
- package standard skills
- document installation flow

### Deliverables
- team-ready setup script or docs
- reusable container images
- consistent Claude setup

### Acceptance criteria
- a teammate can install and run ProjectLens with minimal manual steps

---

## Phase 6 — Infra and Config Awareness

### Goal
Expand beyond Go when it clearly improves answers.

### Build
- SQL migration indexing
- Proto / GraphQL indexing
- selected YAML / JSON config indexing
- infra/source relationships

### Acceptance criteria
- the system can trace more than just code symbols
- the additional corpus improves concrete question classes without diluting overall precision

---

## Phase 7 — Design Awareness

### Goal
Add design intent from Figma and Miro.

### Build
- thin metadata ingestion for Figma
- thin metadata ingestion for Miro
- link design nodes to docs or code features where possible

### Acceptance criteria
- the system can help answer cross-source questions spanning code, docs, and design artifacts

---

## 18. Incremental Update Strategy

### 18.1 Initial update mode
Manual or CLI-triggered reindex.

Commands:
- `projectlens reindex`
- `projectlens reindex --changed-only`

### 18.2 Later update mode
Automatic lightweight reindex for changed Go files.

Possible triggers:
- file change hooks
- Git diff against current branch
- explicit manual refresh from CLI

### 18.3 Nightly or periodic maintenance later
- summary refresh
- embedding refresh if needed
- stale data cleanup
- index consistency checks

---

## 19. Evaluation Plan

Evaluation is required early. Do not rely on intuition.

### 19.1 Build an eval set
Create 100+ real questions from the repo, grouped by:
- symbol lookup
- implementation search
- package ownership
- dependency tracing
- test lookup
- change impact
- code-vs-doc questions later

### 19.2 Metrics
- top-k retrieval hit rate
- answer grounding rate
- average files opened per answer
- latency
- ranking failures due to generated code noise
- ranking failures due to missing relationships

### 19.3 Success condition
The main success condition is not just answer correctness. It is also:

**the agent no longer needs broad code spelunking for common questions**

---

## 20. Risks and Mitigations

### Risk: Too much noise in the index
Mitigation:
- start with handwritten Go only
- keep tests separate
- penalize generated code

### Risk: MCP tool sprawl
Mitigation:
- keep v1 tool surface very small
- push logic into ProjectLens, not agent prompts

### Risk: vague package boundaries in a semi-monolith
Mitigation:
- invest in package summaries early
- later introduce service or domain grouping inferred from repo structure

### Risk: stale index
Mitigation:
- expose freshness status in CLI and later in Claude workflow
- keep reindex path simple first

### Risk: docs or design artifacts overpower code evidence
Mitigation:
- preserve source-aware ranking
- code remains primary evidence for implementation questions

---

## 21. Non-Goals

ProjectLens is not trying to be:
- a replacement for source control
- a replacement for real code reading
- a full code execution environment
- a model training pipeline
- a universal enterprise search system in v1

It is a **context and retrieval layer** for coding agents.

---

## 22. Immediate Implementation Backlog

### Milestone A — Bootstrap
- initialize projectlens project
- create Docker Compose for Postgres + service
- add pgvector migration
- implement `projectlens census`
- implement file classification
- write initial `CLAUDE.md`

### Milestone B — Go Indexing
- parse handwritten Go files
- extract symbols
- persist symbol metadata
- persist chunks
- generate file and package summaries
- implement lexical search
- implement semantic search

### Milestone C — MCP
- implement MCP server process
- expose four v1 tools
- connect Claude Code to local MCP
- validate first question-answer loop

### Milestone D — Smarter Relationships
- add callers / callees
- add tests namespace
- add impact tracing
- add Claude skills

### Milestone E — Docs
- add Markdown indexing
- connect doc chunks to packages or symbols
- integrate Atlassian workflows through existing MCP

---

## 23. Open Questions

These are currently resolved enough to proceed, but may later need explicit design choices.

1. Which Go parser or extraction library will be used in the first version?
2. Which embedding model will be used locally or via API?
3. How much summarization should be precomputed vs generated on demand?
4. Will team rollout eventually use a shared MCP endpoint or only shared images plus local services?
5. When branch-aware indexing is introduced, should working tree changes be included or only committed state?

---

## 24. Final Decision Summary

ProjectLens will be implemented as a **local-first, containerized, Go-first repository intelligence layer** optimized for **Claude Code**.

The system will:
- start with handwritten Go only
- expose a small MCP tool surface
- guide Claude Code via `CLAUDE.md` and reusable skills
- add tests second
- add docs third
- add infra/config and design awareness later

The implementation strategy is:

**index once, retrieve smartly many times, verify only what matters**

