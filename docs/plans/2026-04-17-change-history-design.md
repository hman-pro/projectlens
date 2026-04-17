# Change History (Phase 3) Design

**Date:** 2026-04-17
**Status:** Draft
**Goal:** Track git change history per file, detect co-change coupling, and provide on-demand symbol evolution — so the LLM understands how code evolves and what changes together.

## Approach

**File-level eager, symbol-level lazy.** Based on CodeScene/Tornhill methodology.

- **Eager (at index time):** file change log + co-change coupling pairs
- **Lazy (at query time):** symbol-level diffs computed on demand via `git log -p`

## Pipeline

```
1. git log --name-only --since=12months --format="COMMIT:%H|%an|%at|%s"
   → one call, parse all commits + changed files (~5 seconds)
2. Filter: keep only files that exist in indexed files table
3. For files with < 5 commits → git log -5 --follow -- <file>
4. Store file_history records
5. Compute co-change coupling pairs:
   - Two files in same commit = one co-change event
   - Exclude commits touching >20 files (refactors)
   - Exclude merge commits
   - Coupling strength = co_changes(A,B) / max(changes(A), changes(B))
   - Minimum 5 co-changes to store
6. Store coupling as edges (edge_type='co_changes', confidence=coupling_strength)
```

## History Depth

- **Default window:** 12 months (configurable)
- **Minimum per file:** 5 commits regardless of age
- **Rationale:** Beyond 12 months, coupling signals reflect past architecture (CodeScene research). But very old files with few recent changes still deserve some history context.

## Co-change Coupling Detection

Based on Adam Tornhill's "Your Code as a Crime Scene" methodology:

```
coupling(A, B) = co_changes(A, B) / max(changes(A), changes(B))
```

**Filters:**
- Minimum 5 co-changes to report (eliminates noise)
- Exclude commits touching >20 files (refactors, go mod updates)
- Exclude merge commits
- Coupling >= 0.3 is "notable", >= 0.5 is "strong"

**Noise exclusions:**
- Commits only touching `go.mod`/`go.sum`/generated files
- Commits with message matching "refactor", "rename", "move" (configurable)

## Storage

**`file_history` table (existing, from migration 002):**
- `file_id FK`, `commit_hash`, `author`, `committed_at`, `change_type`, `diff_snippet`
- `diff_snippet` stores commit message (not full diff — diffs computed lazily)

**`edges` table with `edge_type='co_changes'`:**
- `source_type='file'`, `source_id=file_a_id`
- `target_type='file'`, `target_id=file_b_id`
- `confidence=coupling_strength` (0.0 to 1.0)
- `properties={"co_change_count": N, "last_co_change": "2026-04-01"}`

## On-demand Symbol Evolution

When a user asks "how did CalculateFunding evolve?":

1. Look up symbol → get file path and line range
2. Run `git log -10 -p -- <file_path>`
3. Parse diff hunks, find those overlapping symbol's line range
4. Return: commit hash, author, date, message, relevant diff snippet
5. ~200ms per symbol query

This avoids precomputing diffs for 23K symbols.

## Configuration

```yaml
history:
  window_months: 12
  min_commits_per_file: 5
  coupling_min_cochanges: 5
  coupling_exclude_max_files: 20
```

## CLI

```bash
projectlens index history --repo /path --db "..."
```

## MCP Tools

- `get_change_history` — given file or symbol name → last N commits, authors, messages
- `get_coupling` — given file or symbol → co-change partners ranked by coupling strength

## Incremental Updates

On subsequent runs:
- `git log --since=<last_indexed_commit_timestamp>` → only new commits
- Append to `file_history`
- Recompute coupling from scratch over the sliding window (fast: ~3 seconds)
- This same git log feeds incremental code reindexing in the future

## Future: Unified Incremental Pipeline

```
git log --since=<last_index> --name-only
  → changed files → reindex code (parse/chunk/embed)
  → changed migrations → reindex datastore
  → new commits → update history + coupling
  → ticket IDs in messages → link docs
```
