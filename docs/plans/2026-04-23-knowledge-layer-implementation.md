# Knowledge Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a sixth source type (`knowledge`) to projectlens that captures lessons, best practices, conventions, domain knowledge, how-tos, and decisions via a Claude-driven MCP capture loop with deterministic Stop-hook enforcement, and surfaces them passively in the existing context tools.

**Architecture:** New `knowledge_entries` table holds metadata; bodies are written to the existing `chunks` table with `source_type='knowledge'` and `source_uri='knowledge:<id>'`, flowing through the unchanged embedding pipeline. Anchors reuse the polymorphic `edges` table with `edge_type='knowledge_about'`. Two new MCP tools (`save_knowledge`, `search_knowledge`) plus passive surfacing on three existing tools.

**Tech Stack:** Go 1.26, Postgres 16 + pgvector (halfvec), pgx/pgxpool, mark3labs/mcp-go, spf13/cobra, Ollama mxbai-embed-large.

**Spec:** `docs/plans/2026-04-23-knowledge-layer-design.md`

---

## Spec drift from design (lock these in)

The design used UUID PKs and assumed a `chunks.source_id` column. The repo's actual conventions:

- **PKs are `BIGSERIAL` (int64)** — used everywhere. Knowledge entries follow suit.
- **`chunks` has no `source_id` — it has `source_uri TEXT`.** Knowledge chunks set `source_uri = 'knowledge:' || knowledge_entries.id` and `symbol_id = NULL`.
- **Edges have a UNIQUE constraint on `(source_type, source_id, target_type, target_id, edge_type)`.** Anchors are naturally idempotent.

---

## File structure

**New files:**
- `migrations/004_knowledge_layer.up.sql` — schema
- `migrations/004_knowledge_layer.down.sql` — reversal
- `internal/storage/knowledge.go` — CRUD on `knowledge_entries`, anchor helpers, search queries
- `internal/storage/knowledge_test.go` — unit tests (no DB, via `ReadMigrationFiles`-style helpers)
- `internal/storage/knowledge_integration_test.go` — integration tests (real Postgres, `//go:build integration`)
- `internal/mcpserver/knowledge_tools.go` — `mcp.Tool` definitions for `save_knowledge` + `search_knowledge`
- `internal/mcpserver/knowledge_handlers.go` — handler implementations
- `internal/mcpserver/knowledge_surfacing.go` — helpers that fetch anchored knowledge for a symbol/package set
- `cmd/projectlens/knowledge.go` — CLI subcommand (`search`, `list`, `show`, `delete`)
- `claude/skills/capture-knowledge/SKILL.md` — skill that drives Claude's capture behavior
- `claude/settings-snippet.json` — Stop hook for users to merge into their target repo's `.claude/settings.json`

**Modified files:**
- `internal/mcpserver/server.go` — register the two new tools
- `internal/mcpserver/handlers.go` — `handleGetSymbolContext`, `handleGetPackageSummary`, `handleSearchGoContext` each call into `knowledge_surfacing.go` to append a `Related knowledge` block
- `cmd/projectlens/main.go` — register `newKnowledgeCmd()`
- `claude/CLAUDE.md.snippet` — document new tools and hook setup
- `CLAUDE.md` (this repo) — add `knowledge` to MCP tools list and pipeline section
- `docker/docker-compose.yml` — no change required

---

## Task list (16 tasks)

1. Migration: `knowledge_entries` schema (up + down)
2. Storage: types + insert with chunk-row in one transaction
3. Storage: `GetByID`, `Delete`, `List` (with category/tag filters)
4. Storage: anchor insertion (wrapping `InsertEdges`)
5. Storage: vector search for knowledge chunks
6. Storage: anchor-traversal search for knowledge entries by target
7. Storage integration test: full roundtrip (insert → embed-flag readiness → search by vector + anchor)
8. MCP tool: `save_knowledge` definition + handler
9. MCP tool: `search_knowledge` definition + handler
10. Knowledge surfacing helper used by passive tools
11. Wire surfacing into `handleGetSymbolContext`
12. Wire surfacing into `handleGetPackageSummary`
13. Wire surfacing into `handleSearchGoContext`
14. CLI: `projectlens knowledge` subcommand
15. Skill file + Stop-hook settings snippet
16. Docs: `claude/CLAUDE.md.snippet` and root `CLAUDE.md`

---

### Task 1: Migration — `knowledge_entries` schema

**Files:**
- Create: `migrations/004_knowledge_layer.up.sql`
- Create: `migrations/004_knowledge_layer.down.sql`

- [ ] **Step 1: Write the up migration**

Create `migrations/004_knowledge_layer.up.sql`:

```sql
-- Knowledge layer: structured wisdom captured during sessions.
-- Bodies live in chunks (source_type='knowledge', source_uri='knowledge:<id>').
-- Anchors live in edges (edge_type='knowledge_about').

CREATE TABLE knowledge_entries (
    id          BIGSERIAL PRIMARY KEY,
    category    TEXT NOT NULL CHECK (category IN (
                    'lesson', 'best_practice', 'convention',
                    'domain_knowledge', 'how_to', 'decision')),
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    tags        TEXT[] NOT NULL DEFAULT '{}',
    source      TEXT NOT NULL DEFAULT 'claude',
    session_id  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX knowledge_entries_category_idx ON knowledge_entries(category);
CREATE INDEX knowledge_entries_tags_idx     ON knowledge_entries USING GIN(tags);
CREATE INDEX knowledge_entries_created_idx  ON knowledge_entries(created_at DESC);
```

- [ ] **Step 2: Write the down migration**

Create `migrations/004_knowledge_layer.down.sql`:

```sql
DROP INDEX IF EXISTS knowledge_entries_created_idx;
DROP INDEX IF EXISTS knowledge_entries_tags_idx;
DROP INDEX IF EXISTS knowledge_entries_category_idx;
DROP TABLE IF EXISTS knowledge_entries;
```

- [ ] **Step 3: Apply against a scratch database to verify**

Run:
```bash
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
    -f migrations/004_knowledge_layer.up.sql
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
    -c "\d knowledge_entries"
```
Expected: table description shows 9 columns with correct types and the 3 indexes.

- [ ] **Step 4: Verify rollback works**

Run:
```bash
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
    -f migrations/004_knowledge_layer.down.sql
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
    -c "\d knowledge_entries"
```
Expected: `Did not find any relation named "knowledge_entries"`. Re-apply up migration so subsequent tasks have the table.

- [ ] **Step 5: Commit**

```bash
git add migrations/004_knowledge_layer.up.sql migrations/004_knowledge_layer.down.sql
git commit -m "feat(migrations): add knowledge_entries table for knowledge layer"
```

---

### Task 2: Storage — `KnowledgeEntry` type + transactional insert

**Files:**
- Create: `internal/storage/knowledge.go`
- Create: `internal/storage/knowledge_test.go`

- [ ] **Step 1: Write the failing unit test**

Create `internal/storage/knowledge_test.go`:

```go
package storage

import "testing"

func TestKnowledgeEntryValidate(t *testing.T) {
    cases := []struct {
        name    string
        entry   KnowledgeEntry
        wantErr string
    }{
        {"empty title", KnowledgeEntry{Category: "lesson", Body: "x"}, "title required"},
        {"empty body", KnowledgeEntry{Category: "lesson", Title: "x"}, "body required"},
        {"bad category", KnowledgeEntry{Category: "rant", Title: "x", Body: "y"}, "category"},
        {"valid", KnowledgeEntry{Category: "lesson", Title: "x", Body: "y"}, ""},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            err := tc.entry.Validate()
            if tc.wantErr == "" {
                if err != nil {
                    t.Fatalf("expected no error, got %v", err)
                }
                return
            }
            if err == nil {
                t.Fatalf("expected error containing %q, got nil", tc.wantErr)
            }
            if !strings.Contains(err.Error(), tc.wantErr) {
                t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
            }
        })
    }
}
```

Add the import: `import ("strings"; "testing")` (combine with the testing import above).

- [ ] **Step 2: Run the test and confirm it fails to compile**

Run: `go test ./internal/storage/ -run TestKnowledgeEntryValidate -v`
Expected: build failure — `undefined: KnowledgeEntry`.

- [ ] **Step 3: Implement the type, validation, and transactional insert**

Create `internal/storage/knowledge.go`:

```go
package storage

import (
    "context"
    "fmt"

    "github.com/jackc/pgx/v5"
)

var validKnowledgeCategories = map[string]struct{}{
    "lesson":           {},
    "best_practice":    {},
    "convention":       {},
    "domain_knowledge": {},
    "how_to":           {},
    "decision":         {},
}

type KnowledgeEntry struct {
    ID        int64    `json:"id"`
    Category  string   `json:"category"`
    Title     string   `json:"title"`
    Body      string   `json:"body"`
    Tags      []string `json:"tags,omitempty"`
    Source    string   `json:"source,omitempty"`
    SessionID *string  `json:"session_id,omitempty"`
}

func (e *KnowledgeEntry) Validate() error {
    if e.Title == "" {
        return fmt.Errorf("title required")
    }
    if e.Body == "" {
        return fmt.Errorf("body required")
    }
    if _, ok := validKnowledgeCategories[e.Category]; !ok {
        return fmt.Errorf("category %q not in allowed set", e.Category)
    }
    return nil
}

// InsertKnowledgeEntry inserts the entry and a paired knowledge-typed chunk
// in a single transaction. Returns the new entry ID and chunk ID.
func (db *DB) InsertKnowledgeEntry(ctx context.Context, e *KnowledgeEntry) (entryID, chunkID int64, err error) {
    if err := e.Validate(); err != nil {
        return 0, 0, fmt.Errorf("storage: knowledge: %w", err)
    }
    if e.Source == "" {
        e.Source = "claude"
    }

    tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return 0, 0, fmt.Errorf("storage: knowledge: begin tx: %w", err)
    }
    defer func() {
        if err != nil {
            _ = tx.Rollback(ctx)
        }
    }()

    const insertEntry = `
        INSERT INTO knowledge_entries (category, title, body, tags, source, session_id)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id`
    if err = tx.QueryRow(ctx, insertEntry,
        e.Category, e.Title, e.Body, e.Tags, e.Source, e.SessionID,
    ).Scan(&entryID); err != nil {
        return 0, 0, fmt.Errorf("storage: knowledge: insert entry: %w", err)
    }

    sourceURI := fmt.Sprintf("knowledge:%d", entryID)
    content := e.Title + "\n\n" + e.Body
    const insertChunk = `
        INSERT INTO chunks (symbol_id, content, token_count, source_type, source_uri)
        VALUES (NULL, $1, $2, 'knowledge', $3)
        RETURNING id`
    // token_count: rough estimate, 1 token ≈ 4 chars; embedder retruncates anyway.
    if err = tx.QueryRow(ctx, insertChunk,
        content, len(content)/4, sourceURI,
    ).Scan(&chunkID); err != nil {
        return 0, 0, fmt.Errorf("storage: knowledge: insert chunk: %w", err)
    }

    if err = tx.Commit(ctx); err != nil {
        return 0, 0, fmt.Errorf("storage: knowledge: commit: %w", err)
    }
    e.ID = entryID
    return entryID, chunkID, nil
}
```

- [ ] **Step 4: Re-run unit test**

Run: `go test ./internal/storage/ -run TestKnowledgeEntryValidate -v`
Expected: PASS (4 subtests).

- [ ] **Step 5: Verify the package builds**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/knowledge.go internal/storage/knowledge_test.go
git commit -m "feat(storage): KnowledgeEntry type + transactional insert with paired chunk"
```

---

### Task 3: Storage — `GetByID`, `Delete`, `List`

**Files:**
- Modify: `internal/storage/knowledge.go`
- Modify: `internal/storage/knowledge_test.go`

- [ ] **Step 1: Add unit tests for argument validation**

Append to `internal/storage/knowledge_test.go`:

```go
func TestKnowledgeListFiltersValidate(t *testing.T) {
    cases := []struct {
        name    string
        f       KnowledgeListFilters
        wantErr string
    }{
        {"bad category", KnowledgeListFilters{Category: "rant"}, "category"},
        {"empty ok", KnowledgeListFilters{}, ""},
        {"valid category", KnowledgeListFilters{Category: "lesson"}, ""},
        {"negative limit", KnowledgeListFilters{Limit: -1}, "limit"},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            err := tc.f.Validate()
            if (tc.wantErr == "") != (err == nil) {
                t.Fatalf("wantErr=%q got=%v", tc.wantErr, err)
            }
            if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
                t.Fatalf("expected %q in %v", tc.wantErr, err)
            }
        })
    }
}
```

- [ ] **Step 2: Run test, expect failure**

Run: `go test ./internal/storage/ -run TestKnowledgeListFiltersValidate -v`
Expected: build failure — `undefined: KnowledgeListFilters`.

- [ ] **Step 3: Implement `GetByID`, `DeleteKnowledgeEntry`, `ListKnowledgeEntries`**

Append to `internal/storage/knowledge.go`:

```go
type KnowledgeListFilters struct {
    Category string
    Tag      string
    Limit    int
}

func (f *KnowledgeListFilters) Validate() error {
    if f.Category != "" {
        if _, ok := validKnowledgeCategories[f.Category]; !ok {
            return fmt.Errorf("category %q not in allowed set", f.Category)
        }
    }
    if f.Limit < 0 {
        return fmt.Errorf("limit must be non-negative")
    }
    return nil
}

func (db *DB) GetKnowledgeEntry(ctx context.Context, id int64) (*KnowledgeEntry, error) {
    const q = `
        SELECT id, category, title, body, tags, source, session_id
        FROM knowledge_entries
        WHERE id = $1`
    var e KnowledgeEntry
    err := db.Pool.QueryRow(ctx, q, id).Scan(
        &e.ID, &e.Category, &e.Title, &e.Body, &e.Tags, &e.Source, &e.SessionID,
    )
    if err != nil {
        return nil, fmt.Errorf("storage: knowledge: get %d: %w", id, err)
    }
    return &e, nil
}

// DeleteKnowledgeEntry removes the entry, its chunk, and any anchor edges.
// Single transaction. Returns the number of entry rows deleted (0 or 1).
func (db *DB) DeleteKnowledgeEntry(ctx context.Context, id int64) (int, error) {
    tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return 0, fmt.Errorf("storage: knowledge: begin tx: %w", err)
    }
    defer func() { _ = tx.Rollback(ctx) }()

    sourceURI := fmt.Sprintf("knowledge:%d", id)

    // Delete embeddings rows for the chunk (FK cascade may already do this; explicit for clarity).
    if _, err = tx.Exec(ctx, `
        DELETE FROM embeddings
        WHERE chunk_id IN (SELECT id FROM chunks WHERE source_uri = $1)`, sourceURI); err != nil {
        return 0, fmt.Errorf("storage: knowledge: delete embeddings: %w", err)
    }
    if _, err = tx.Exec(ctx, `DELETE FROM chunks WHERE source_uri = $1`, sourceURI); err != nil {
        return 0, fmt.Errorf("storage: knowledge: delete chunk: %w", err)
    }
    if _, err = tx.Exec(ctx, `
        DELETE FROM edges
        WHERE source_type = 'knowledge' AND source_id = $1`, id); err != nil {
        return 0, fmt.Errorf("storage: knowledge: delete edges: %w", err)
    }

    res, err := tx.Exec(ctx, `DELETE FROM knowledge_entries WHERE id = $1`, id)
    if err != nil {
        return 0, fmt.Errorf("storage: knowledge: delete entry: %w", err)
    }
    if err = tx.Commit(ctx); err != nil {
        return 0, fmt.Errorf("storage: knowledge: commit: %w", err)
    }
    return int(res.RowsAffected()), nil
}

func (db *DB) ListKnowledgeEntries(ctx context.Context, f KnowledgeListFilters) ([]KnowledgeEntry, error) {
    if err := f.Validate(); err != nil {
        return nil, fmt.Errorf("storage: knowledge: %w", err)
    }
    limit := f.Limit
    if limit == 0 {
        limit = 100
    }

    args := []any{}
    where := []string{}
    if f.Category != "" {
        args = append(args, f.Category)
        where = append(where, fmt.Sprintf("category = $%d", len(args)))
    }
    if f.Tag != "" {
        args = append(args, f.Tag)
        where = append(where, fmt.Sprintf("$%d = ANY(tags)", len(args)))
    }
    args = append(args, limit)

    q := `SELECT id, category, title, body, tags, source, session_id FROM knowledge_entries`
    if len(where) > 0 {
        q += " WHERE " + strings.Join(where, " AND ")
    }
    q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

    rows, err := db.Pool.Query(ctx, q, args...)
    if err != nil {
        return nil, fmt.Errorf("storage: knowledge: list: %w", err)
    }
    defer rows.Close()

    var out []KnowledgeEntry
    for rows.Next() {
        var e KnowledgeEntry
        if err := rows.Scan(&e.ID, &e.Category, &e.Title, &e.Body, &e.Tags, &e.Source, &e.SessionID); err != nil {
            return nil, fmt.Errorf("storage: knowledge: scan: %w", err)
        }
        out = append(out, e)
    }
    return out, rows.Err()
}
```

Add `strings` to the import block at the top of the file.

- [ ] **Step 4: Run unit tests**

Run: `go test ./internal/storage/ -run TestKnowledge -v`
Expected: PASS for both `TestKnowledgeEntryValidate` and `TestKnowledgeListFiltersValidate`.

- [ ] **Step 5: Verify package builds**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/knowledge.go internal/storage/knowledge_test.go
git commit -m "feat(storage): GetByID/Delete/List for knowledge entries"
```

---

### Task 4: Storage — anchor insertion helper

**Files:**
- Modify: `internal/storage/knowledge.go`

- [ ] **Step 1: Implement `InsertKnowledgeAnchors`**

Append to `internal/storage/knowledge.go`:

```go
type AnchorRequest struct {
    Type string // "symbol" | "file" | "package" | "table"
    Ref  string // scip_symbol | path | package_name | table_name
}

type AnchorResolution struct {
    Anchor     AnchorRequest
    TargetID   int64 // 0 if unresolved
    Resolved   bool
}

// InsertKnowledgeAnchors resolves each anchor to an existing target and writes
// edges (knowledge → target). Unresolved anchors are returned in the result; not an error.
func (db *DB) InsertKnowledgeAnchors(ctx context.Context, knowledgeID int64, anchors []AnchorRequest) ([]AnchorResolution, error) {
    out := make([]AnchorResolution, 0, len(anchors))
    edges := make([]EdgeRecord, 0, len(anchors))

    for _, a := range anchors {
        targetID, ok, err := db.resolveAnchor(ctx, a)
        if err != nil {
            return nil, fmt.Errorf("storage: knowledge: resolve anchor %s:%s: %w", a.Type, a.Ref, err)
        }
        out = append(out, AnchorResolution{Anchor: a, TargetID: targetID, Resolved: ok})
        if !ok {
            continue
        }
        conf := float32(1.0)
        edges = append(edges, EdgeRecord{
            SourceType: "knowledge",
            SourceID:   knowledgeID,
            TargetType: a.Type,
            TargetID:   targetID,
            EdgeType:   "knowledge_about",
            Confidence: &conf,
        })
    }

    if len(edges) > 0 {
        if err := db.InsertEdges(ctx, edges); err != nil {
            return nil, fmt.Errorf("storage: knowledge: insert anchor edges: %w", err)
        }
    }
    return out, nil
}

func (db *DB) resolveAnchor(ctx context.Context, a AnchorRequest) (int64, bool, error) {
    var id int64
    var query string
    switch a.Type {
    case "symbol":
        // ref = scip_symbol
        query = `SELECT id FROM symbols WHERE scip_symbol = $1 LIMIT 1`
    case "file":
        query = `SELECT id FROM files WHERE path = $1 LIMIT 1`
    case "package":
        // packages are not first-class rows; use the smallest file id in the package
        // as a stable target. Edges target_id then references files.id with target_type='package'.
        query = `SELECT MIN(id) FROM files WHERE package_name = $1`
    case "table":
        query = `SELECT id FROM datastore_tables WHERE name = $1 LIMIT 1`
    default:
        return 0, false, fmt.Errorf("unknown anchor type %q", a.Type)
    }
    err := db.Pool.QueryRow(ctx, query, a.Ref).Scan(&id)
    if err != nil {
        if err.Error() == "no rows in result set" || strings.Contains(err.Error(), "no rows") {
            return 0, false, nil
        }
        return 0, false, err
    }
    if id == 0 {
        return 0, false, nil
    }
    return id, true, nil
}
```

Note on `package` anchors: packages aren't first-class rows. The plan resolves a package anchor to the smallest `files.id` in that package. Retrieval (Task 6) treats `target_type='package'` specially and joins via `files.package_name`. Document this in a code comment.

- [ ] **Step 2: Verify package builds**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/knowledge.go
git commit -m "feat(storage): InsertKnowledgeAnchors with type-specific resolution"
```

---

### Task 5: Storage — vector search for knowledge chunks

**Files:**
- Modify: `internal/storage/knowledge.go`

- [ ] **Step 1: Implement `SearchKnowledgeByVector`**

Append to `internal/storage/knowledge.go`:

```go
import "github.com/pgvector/pgvector-go" // add to imports if missing

type KnowledgeSearchHit struct {
    Entry KnowledgeEntry
    Score float32
}

// SearchKnowledgeByVector returns top-k knowledge entries ordered by cosine
// similarity to queryVec. Optional category filter.
func (db *DB) SearchKnowledgeByVector(
    ctx context.Context,
    queryVec []float32,
    category string,
    limit int,
) ([]KnowledgeSearchHit, error) {
    if limit <= 0 {
        limit = 10
    }
    args := []any{pgvector.NewHalfVector(queryVec)}
    where := "c.source_type = 'knowledge'"
    if category != "" {
        if _, ok := validKnowledgeCategories[category]; !ok {
            return nil, fmt.Errorf("storage: knowledge: bad category %q", category)
        }
        args = append(args, category)
        where += fmt.Sprintf(" AND k.category = $%d", len(args))
    }
    args = append(args, limit)

    q := fmt.Sprintf(`
        SELECT k.id, k.category, k.title, k.body, k.tags, k.source, k.session_id,
               1 - (e.embedding <=> $1) AS score
        FROM embeddings e
        JOIN chunks c             ON c.id = e.chunk_id
        JOIN knowledge_entries k  ON ('knowledge:' || k.id) = c.source_uri
        WHERE %s
        ORDER BY e.embedding <=> $1
        LIMIT $%d`, where, len(args))

    rows, err := db.Pool.Query(ctx, q, args...)
    if err != nil {
        return nil, fmt.Errorf("storage: knowledge: vector search: %w", err)
    }
    defer rows.Close()

    var out []KnowledgeSearchHit
    for rows.Next() {
        var h KnowledgeSearchHit
        if err := rows.Scan(
            &h.Entry.ID, &h.Entry.Category, &h.Entry.Title, &h.Entry.Body,
            &h.Entry.Tags, &h.Entry.Source, &h.Entry.SessionID, &h.Score,
        ); err != nil {
            return nil, fmt.Errorf("storage: knowledge: scan: %w", err)
        }
        out = append(out, h)
    }
    return out, rows.Err()
}
```

Operator note: `<=>` is pgvector's cosine distance (smaller = more similar); `1 - distance` gives similarity for the response.

- [ ] **Step 2: Verify package builds**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/knowledge.go
git commit -m "feat(storage): vector search over knowledge-typed chunks"
```

---

### Task 6: Storage — anchor-traversal search

**Files:**
- Modify: `internal/storage/knowledge.go`

- [ ] **Step 1: Implement `KnowledgeForAnchor` and `KnowledgeForTargets`**

Append to `internal/storage/knowledge.go`:

```go
// KnowledgeForAnchor finds knowledge entries anchored to a single target.
// For type="package", looks up by the package name (resolves through files).
func (db *DB) KnowledgeForAnchor(ctx context.Context, a AnchorRequest, limit int) ([]KnowledgeEntry, error) {
    if limit <= 0 {
        limit = 10
    }
    targetID, ok, err := db.resolveAnchor(ctx, a)
    if err != nil {
        return nil, err
    }
    if !ok {
        return nil, nil
    }

    var q string
    switch a.Type {
    case "package":
        // All edges with target_type='package' that point at any file in this package.
        q = `
          SELECT k.id, k.category, k.title, k.body, k.tags, k.source, k.session_id
          FROM edges e
          JOIN files f             ON f.id = e.target_id
          JOIN knowledge_entries k ON k.id = e.source_id
          WHERE e.source_type = 'knowledge'
            AND e.edge_type = 'knowledge_about'
            AND e.target_type = 'package'
            AND f.package_name = (SELECT package_name FROM files WHERE id = $1)
          ORDER BY k.created_at DESC
          LIMIT $2`
    default:
        q = `
          SELECT k.id, k.category, k.title, k.body, k.tags, k.source, k.session_id
          FROM edges e
          JOIN knowledge_entries k ON k.id = e.source_id
          WHERE e.source_type = 'knowledge'
            AND e.edge_type = 'knowledge_about'
            AND e.target_type = $1
            AND e.target_id = $2
          ORDER BY k.created_at DESC
          LIMIT $3`
    }

    var rows pgx.Rows
    if a.Type == "package" {
        rows, err = db.Pool.Query(ctx, q, targetID, limit)
    } else {
        rows, err = db.Pool.Query(ctx, q, a.Type, targetID, limit)
    }
    if err != nil {
        return nil, fmt.Errorf("storage: knowledge: anchor search: %w", err)
    }
    defer rows.Close()

    var out []KnowledgeEntry
    for rows.Next() {
        var e KnowledgeEntry
        if err := rows.Scan(&e.ID, &e.Category, &e.Title, &e.Body, &e.Tags, &e.Source, &e.SessionID); err != nil {
            return nil, fmt.Errorf("storage: knowledge: scan: %w", err)
        }
        out = append(out, e)
    }
    return out, rows.Err()
}

// KnowledgeForSymbolWithPackage convenience: returns knowledge anchored either
// directly to symbol_id, or to the symbol's enclosing package. Deduped by entry id.
func (db *DB) KnowledgeForSymbolWithPackage(ctx context.Context, symbolID int64, limit int) ([]KnowledgeEntry, error) {
    if limit <= 0 {
        limit = 10
    }
    const q = `
      WITH sym_pkg AS (
        SELECT s.id AS sid, f.package_name AS pkg
        FROM symbols s JOIN files f ON f.id = s.file_id
        WHERE s.id = $1
      )
      SELECT DISTINCT ON (k.id)
        k.id, k.category, k.title, k.body, k.tags, k.source, k.session_id
      FROM edges e
      JOIN knowledge_entries k ON k.id = e.source_id
      LEFT JOIN files pf       ON pf.id = e.target_id
      WHERE e.source_type = 'knowledge'
        AND e.edge_type = 'knowledge_about'
        AND (
          (e.target_type = 'symbol'  AND e.target_id = (SELECT sid FROM sym_pkg))
          OR
          (e.target_type = 'package' AND pf.package_name = (SELECT pkg FROM sym_pkg))
        )
      ORDER BY k.id, k.created_at DESC
      LIMIT $2`

    rows, err := db.Pool.Query(ctx, q, symbolID, limit)
    if err != nil {
        return nil, fmt.Errorf("storage: knowledge: symbol+package search: %w", err)
    }
    defer rows.Close()

    var out []KnowledgeEntry
    for rows.Next() {
        var e KnowledgeEntry
        if err := rows.Scan(&e.ID, &e.Category, &e.Title, &e.Body, &e.Tags, &e.Source, &e.SessionID); err != nil {
            return nil, fmt.Errorf("storage: knowledge: scan: %w", err)
        }
        out = append(out, e)
    }
    return out, rows.Err()
}
```

- [ ] **Step 2: Verify package builds**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/knowledge.go
git commit -m "feat(storage): anchor traversal queries for knowledge"
```

---

### Task 7: Storage integration test — full roundtrip

**Files:**
- Create: `internal/storage/knowledge_integration_test.go`

- [ ] **Step 1: Write the integration test**

Create `internal/storage/knowledge_integration_test.go`:

```go
//go:build integration

// Prerequisites: Postgres on localhost:5433, projectlens database, migrations applied.
// Run: go test ./internal/storage/ -tags integration -run TestKnowledgeRoundtrip -v

package storage

import (
    "context"
    "fmt"
    "os"
    "testing"
    "time"

    "github.com/pgvector/pgvector-go"
)

func dbURL(t *testing.T) string {
    u := os.Getenv("DATABASE_URL")
    if u == "" {
        u = "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"
    }
    return u
}

func TestKnowledgeRoundtrip(t *testing.T) {
    ctx := context.Background()
    db, err := Connect(ctx, dbURL(t))
    if err != nil { t.Fatalf("connect: %v", err) }
    defer db.Close()

    marker := fmt.Sprintf("knowledge-test-%d", time.Now().UnixNano())

    // 1. seed a fake file + symbol so we have a target to anchor to
    fileID, err := db.UpsertFile(ctx, &FileRecord{
        Path: marker + "/foo.go", PackageName: marker + "_pkg",
        Checksum: "x", Language: "go", LineCount: 1, CommitSHA: "deadbeef",
        IndexedAt: time.Now(),
    })
    if err != nil { t.Fatalf("upsert file: %v", err) }
    scip := "go . " + marker + "_pkg . Foo()"
    if err := db.InsertSymbols(ctx, []SymbolRecord{{
        FileID: fileID, Name: "Foo", Kind: "func", PackageName: marker + "_pkg",
        Signature: "func Foo()", LineStart: 1, LineEnd: 1, Checksum: "y",
        IndexedAt: time.Now(), ScipSymbol: &scip,
    }}); err != nil { t.Fatalf("insert symbol: %v", err) }

    var symID int64
    if err := db.Pool.QueryRow(ctx,
        `SELECT id FROM symbols WHERE scip_symbol = $1`, scip).Scan(&symID); err != nil {
        t.Fatalf("lookup symbol: %v", err)
    }

    t.Cleanup(func() {
        _, _ = db.Pool.Exec(ctx, `DELETE FROM symbols WHERE file_id = $1`, fileID)
        _, _ = db.Pool.Exec(ctx, `DELETE FROM files   WHERE id = $1`, fileID)
        _, _ = db.Pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE title LIKE $1`, marker+"%")
    })

    // 2. insert knowledge entry + chunk
    entry := &KnowledgeEntry{
        Category: "lesson",
        Title:    marker + " title",
        Body:     "Discovered that Foo behaves like X.",
        Tags:     []string{marker, "test"},
    }
    entryID, chunkID, err := db.InsertKnowledgeEntry(ctx, entry)
    if err != nil { t.Fatalf("insert knowledge: %v", err) }
    if entryID == 0 || chunkID == 0 {
        t.Fatalf("expected non-zero ids, got entry=%d chunk=%d", entryID, chunkID)
    }

    // 3. anchor it to the symbol
    res, err := db.InsertKnowledgeAnchors(ctx, entryID, []AnchorRequest{
        {Type: "symbol", Ref: scip},
        {Type: "symbol", Ref: "doesnotexist"},
    })
    if err != nil { t.Fatalf("anchor: %v", err) }
    if len(res) != 2 || !res[0].Resolved || res[1].Resolved {
        t.Fatalf("expected first resolved, second not: %+v", res)
    }

    // 4. fake an embedding (1024-dim zeros) so vector search returns the row
    vec := make([]float32, 1024)
    if err := db.UpsertEmbedding(ctx, &EmbeddingRecord{
        ChunkID: chunkID, ModelVersion: "test", Embedding: pgvector.NewHalfVector(vec),
    }); err != nil { t.Fatalf("upsert embedding: %v", err) }

    // 5. vector search hits it
    hits, err := db.SearchKnowledgeByVector(ctx, vec, "", 10)
    if err != nil { t.Fatalf("vector search: %v", err) }
    foundVec := false
    for _, h := range hits {
        if h.Entry.ID == entryID { foundVec = true; break }
    }
    if !foundVec { t.Fatalf("vector search did not return entry %d", entryID) }

    // 6. anchor traversal hits it
    anchored, err := db.KnowledgeForAnchor(ctx, AnchorRequest{Type: "symbol", Ref: scip}, 10)
    if err != nil { t.Fatalf("anchor search: %v", err) }
    if len(anchored) != 1 || anchored[0].ID != entryID {
        t.Fatalf("expected one anchored entry, got %+v", anchored)
    }

    // 7. delete cleans up
    n, err := db.DeleteKnowledgeEntry(ctx, entryID)
    if err != nil { t.Fatalf("delete: %v", err) }
    if n != 1 { t.Fatalf("expected 1 row deleted, got %d", n) }

    var count int
    if err := db.Pool.QueryRow(ctx,
        `SELECT count(*) FROM edges WHERE source_type='knowledge' AND source_id=$1`,
        entryID).Scan(&count); err != nil {
        t.Fatalf("count edges: %v", err)
    }
    if count != 0 { t.Fatalf("expected anchor edges deleted, found %d", count) }
}
```

- [ ] **Step 2: Run the integration test**

Run: `go test ./internal/storage/ -tags integration -run TestKnowledgeRoundtrip -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/knowledge_integration_test.go
git commit -m "test(storage): integration roundtrip for knowledge insert+anchor+search+delete"
```

---

### Task 8: MCP — `save_knowledge` tool

**Files:**
- Create: `internal/mcpserver/knowledge_tools.go`
- Create: `internal/mcpserver/knowledge_handlers.go`
- Modify: `internal/mcpserver/server.go`

- [ ] **Step 1: Define the `save_knowledge` MCP tool**

Create `internal/mcpserver/knowledge_tools.go`:

```go
package mcpserver

import "github.com/mark3labs/mcp-go/mcp"

func saveKnowledgeTool() mcp.Tool {
    return mcp.NewTool("save_knowledge",
        mcp.WithDescription(
            "Persist a piece of durable knowledge captured during a Claude session. "+
                "Use only when one of the 9 capture-knowledge signals fires "+
                "(see capture-knowledge skill). Anchors are optional but greatly improve "+
                "retrieval — prefer symbol > package > file > table > none."),
        mcp.WithReadOnlyHintAnnotation(false),
        mcp.WithDestructiveHintAnnotation(false),
        mcp.WithString("category",
            mcp.Required(),
            mcp.Enum("lesson", "best_practice", "convention",
                "domain_knowledge", "how_to", "decision"),
            mcp.Description("Knowledge category"),
        ),
        mcp.WithString("title", mcp.Required(),
            mcp.Description("Short, searchable headline (≤120 chars)")),
        mcp.WithString("body", mcp.Required(),
            mcp.Description("Markdown body. Include the *why*, not just the *what*.")),
        mcp.WithArray("tags",
            mcp.Description("Free-form tags for filtering (lowercase, hyphenated)"),
            mcp.Items(map[string]any{"type": "string"}),
        ),
        mcp.WithArray("anchors",
            mcp.Description("Optional anchors. Each: {type: symbol|file|package|table, ref: string}"),
            mcp.Items(map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "type": map[string]any{"type": "string", "enum": []string{"symbol", "file", "package", "table"}},
                    "ref":  map[string]any{"type": "string"},
                },
                "required": []string{"type", "ref"},
            }),
        ),
        mcp.WithString("session_id",
            mcp.Description("Optional session identifier (caller-supplied)")),
    )
}

func searchKnowledgeTool() mcp.Tool {
    return mcp.NewTool("search_knowledge",
        mcp.WithDescription(
            "Search captured knowledge entries by natural-language query, "+
                "optional category, and optional anchor."),
        mcp.WithReadOnlyHintAnnotation(true),
        mcp.WithDestructiveHintAnnotation(false),
        mcp.WithString("query",
            mcp.Description("Natural-language query (optional if anchor given)")),
        mcp.WithString("category",
            mcp.Description("Optional category filter"),
            mcp.Enum("lesson", "best_practice", "convention",
                "domain_knowledge", "how_to", "decision"),
        ),
        mcp.WithString("anchor_type",
            mcp.Description("Anchor type: symbol|file|package|table"),
            mcp.Enum("symbol", "file", "package", "table"),
        ),
        mcp.WithString("anchor_ref",
            mcp.Description("Anchor reference (scip_symbol|path|package_name|table_name)")),
        mcp.WithNumber("limit",
            mcp.Description("Max results (default 10)")),
    )
}
```

- [ ] **Step 2: Implement the `save_knowledge` handler**

Create `internal/mcpserver/knowledge_handlers.go`:

```go
package mcpserver

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"

    "github.com/mark3labs/mcp-go/mcp"
    "github.com/hman-pro/projectlens/internal/storage"
)

type saveKnowledgeResponse struct {
    ID                 int64    `json:"id"`
    Embedded           bool     `json:"embedded"`
    AnchorsResolved    int      `json:"anchors_resolved"`
    AnchorsUnresolved  []string `json:"anchors_unresolved"`
}

func (s *Server) handleSaveKnowledge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    category, err := req.RequireString("category")
    if err != nil {
        return mcp.NewToolResultError("save_knowledge: category required"), nil
    }
    title, err := req.RequireString("title")
    if err != nil {
        return mcp.NewToolResultError("save_knowledge: title required"), nil
    }
    body, err := req.RequireString("body")
    if err != nil {
        return mcp.NewToolResultError("save_knowledge: body required"), nil
    }

    var tags []string
    if raw := req.GetArguments()["tags"]; raw != nil {
        if arr, ok := raw.([]any); ok {
            for _, v := range arr {
                if str, ok := v.(string); ok {
                    tags = append(tags, str)
                }
            }
        }
    }

    var anchors []storage.AnchorRequest
    if raw := req.GetArguments()["anchors"]; raw != nil {
        if arr, ok := raw.([]any); ok {
            for _, v := range arr {
                m, ok := v.(map[string]any)
                if !ok {
                    continue
                }
                t, _ := m["type"].(string)
                r, _ := m["ref"].(string)
                if t == "" || r == "" {
                    continue
                }
                anchors = append(anchors, storage.AnchorRequest{Type: t, Ref: r})
            }
        }
    }

    sessionID := req.GetString("session_id", "")
    var sessPtr *string
    if sessionID != "" { sessPtr = &sessionID }

    entry := &storage.KnowledgeEntry{
        Category: category, Title: title, Body: body,
        Tags: tags, Source: "claude", SessionID: sessPtr,
    }
    entryID, _, err := s.db.InsertKnowledgeEntry(ctx, entry)
    if err != nil {
        return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: %v", err)), nil
    }

    resolutions, err := s.db.InsertKnowledgeAnchors(ctx, entryID, anchors)
    if err != nil {
        return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: anchors: %v", err)), nil
    }

    resp := saveKnowledgeResponse{
        ID:       entryID,
        Embedded: false, // embedding happens out-of-band; chunk is queued.
    }
    for _, r := range resolutions {
        if r.Resolved {
            resp.AnchorsResolved++
        } else {
            resp.AnchorsUnresolved = append(resp.AnchorsUnresolved,
                fmt.Sprintf("%s:%s", r.Anchor.Type, r.Anchor.Ref))
        }
    }
    out, _ := json.Marshal(resp)
    return mcp.NewToolResultText(string(out)), nil
}
```

(The unused `strings` import will be added by the search handler in Task 9; remove if your linter complains in this commit and re-add later.)

- [ ] **Step 3: Register the tool in the server**

Modify `internal/mcpserver/server.go` — locate the block where existing tools are added with `mcpServer.AddTool(...)` and append:

```go
mcpServer.AddTool(saveKnowledgeTool(), s.handleSaveKnowledge)
```

- [ ] **Step 4: Verify the package builds**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 5: Smoke-test via the MCP server**

In one terminal:
```bash
go run ./cmd/projectlens-mcp/
```
In another terminal:
```bash
curl -sX POST http://localhost:8484/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call",
       "params":{"name":"save_knowledge","arguments":{
         "category":"lesson","title":"smoke test",
         "body":"this verifies the tool round-trips"
       }}}' | jq
```
Expected: `result.content[0].text` is JSON containing `"id": <number>`, `"embedded": false`, `"anchors_resolved": 0`.

Verify in DB:
```bash
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
  -c "SELECT id, category, title FROM knowledge_entries WHERE title='smoke test';"
```
Then clean up: `DELETE FROM knowledge_entries WHERE title='smoke test';` plus the matching chunk.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/knowledge_tools.go internal/mcpserver/knowledge_handlers.go internal/mcpserver/server.go
git commit -m "feat(mcp): save_knowledge tool"
```

---

### Task 9: MCP — `search_knowledge` tool

**Files:**
- Modify: `internal/mcpserver/knowledge_handlers.go`
- Modify: `internal/mcpserver/server.go`

- [ ] **Step 1: Implement the `search_knowledge` handler**

Append to `internal/mcpserver/knowledge_handlers.go`:

```go
type knowledgeHit struct {
    ID         int64    `json:"id"`
    Category   string   `json:"category"`
    Title      string   `json:"title"`
    Body       string   `json:"body"`
    Tags       []string `json:"tags,omitempty"`
    Score      float32  `json:"score,omitempty"`
    MatchedVia string   `json:"matched_via"` // "vector" | "anchor" | "both"
}

func (s *Server) handleSearchKnowledge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    query := req.GetString("query", "")
    category := req.GetString("category", "")
    anchorType := req.GetString("anchor_type", "")
    anchorRef := req.GetString("anchor_ref", "")
    limit := int(req.GetFloat("limit", 10))
    if query == "" && anchorType == "" {
        return mcp.NewToolResultError("search_knowledge: provide query and/or anchor"), nil
    }

    byID := map[int64]*knowledgeHit{}

    // Vector path
    if query != "" {
        vec, err := s.router.EmbedQuery(ctx, query)
        if err != nil {
            return mcp.NewToolResultError(fmt.Sprintf("search_knowledge: embed: %v", err)), nil
        }
        hits, err := s.db.SearchKnowledgeByVector(ctx, vec, category, limit)
        if err != nil {
            return mcp.NewToolResultError(fmt.Sprintf("search_knowledge: vector: %v", err)), nil
        }
        for _, h := range hits {
            byID[h.Entry.ID] = &knowledgeHit{
                ID: h.Entry.ID, Category: h.Entry.Category, Title: h.Entry.Title,
                Body: h.Entry.Body, Tags: h.Entry.Tags,
                Score: h.Score, MatchedVia: "vector",
            }
        }
    }

    // Anchor path
    if anchorType != "" && anchorRef != "" {
        entries, err := s.db.KnowledgeForAnchor(ctx,
            storage.AnchorRequest{Type: anchorType, Ref: anchorRef}, limit)
        if err != nil {
            return mcp.NewToolResultError(fmt.Sprintf("search_knowledge: anchor: %v", err)), nil
        }
        for _, e := range entries {
            if existing, ok := byID[e.ID]; ok {
                existing.MatchedVia = "both"
                existing.Score += 0.1 // small bonus for combined match
                continue
            }
            byID[e.ID] = &knowledgeHit{
                ID: e.ID, Category: e.Category, Title: e.Title,
                Body: e.Body, Tags: e.Tags,
                Score: 1.0, MatchedVia: "anchor",
            }
        }
    }

    // collect + sort by score desc
    out := make([]*knowledgeHit, 0, len(byID))
    for _, h := range byID {
        out = append(out, h)
    }
    sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
    if len(out) > limit {
        out = out[:limit]
    }

    var b strings.Builder
    if len(out) == 0 {
        b.WriteString("No matching knowledge entries.\n")
    } else {
        for _, h := range out {
            fmt.Fprintf(&b, "[%s] (#%d, %s, score=%.2f)\n%s\n%s\n\n",
                h.MatchedVia, h.ID, h.Category, h.Score, h.Title, h.Body)
        }
    }
    return mcp.NewToolResultText(b.String()), nil
}
```

Update the imports at the top of `knowledge_handlers.go`:
```go
import (
    "context"
    "encoding/json"
    "fmt"
    "sort"
    "strings"

    "github.com/mark3labs/mcp-go/mcp"
    "github.com/hman-pro/projectlens/internal/storage"
)
```

- [ ] **Step 2: Verify `router.EmbedQuery` exists**

Run: `grep -rn "func .*EmbedQuery" internal/retrieval/`
Expected: a method on the retrieval Router that embeds a query string. If the method has a different name (e.g., `Embed`, `EmbedText`), update the handler call accordingly. If it doesn't exist, add the smallest possible wrapper:

```go
// in internal/retrieval/router.go
func (r *Router) EmbedQuery(ctx context.Context, q string) ([]float32, error) {
    out, err := r.embedder.EmbedBatch(ctx, []string{q})
    if err != nil { return nil, err }
    if len(out) == 0 { return nil, fmt.Errorf("retrieval: empty embedding") }
    return out[0], nil
}
```

- [ ] **Step 3: Register the tool**

In `internal/mcpserver/server.go`, alongside Task 8's registration:
```go
mcpServer.AddTool(searchKnowledgeTool(), s.handleSearchKnowledge)
```

- [ ] **Step 4: Verify the package builds**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 5: Smoke-test**

Save an entry first, run the embed pass to give it a vector, then query:
```bash
go run ./cmd/projectlens/ reindex --db "postgres://...?sslmode=disable" --repo /path/to/repo
# (the reindex picks up unembedded knowledge chunks via GetUnembeddedChunks)
```

Then via curl:
```bash
curl -sX POST http://localhost:8484/mcp -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call",
       "params":{"name":"search_knowledge","arguments":{"query":"smoke test"}}}' | jq
```
Expected: text result mentioning the entry saved earlier.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpserver/knowledge_handlers.go internal/mcpserver/server.go internal/retrieval/router.go
git commit -m "feat(mcp): search_knowledge tool with vector + anchor paths"
```

---

### Task 10: Knowledge surfacing helper

**Files:**
- Create: `internal/mcpserver/knowledge_surfacing.go`

- [ ] **Step 1: Implement `surfaceKnowledgeForSymbol` and `surfaceKnowledgeForPackage`**

Create `internal/mcpserver/knowledge_surfacing.go`:

```go
package mcpserver

import (
    "context"
    "fmt"
    "strings"

    "github.com/hman-pro/projectlens/internal/storage"
)

const surfacingLimit = 3

// surfaceKnowledgeForSymbol returns up to 3 entries anchored to symbolID
// (or its enclosing package), formatted as a short text block.
// Returns "" when nothing is anchored — callers append unconditionally.
func (s *Server) surfaceKnowledgeForSymbol(ctx context.Context, symbolID int64) string {
    entries, err := s.db.KnowledgeForSymbolWithPackage(ctx, symbolID, surfacingLimit)
    if err != nil || len(entries) == 0 {
        return ""
    }
    return formatSurfacedKnowledge(entries)
}

// surfaceKnowledgeForPackage returns up to 3 entries anchored to a package.
func (s *Server) surfaceKnowledgeForPackage(ctx context.Context, packageName string) string {
    entries, err := s.db.KnowledgeForAnchor(ctx,
        storage.AnchorRequest{Type: "package", Ref: packageName}, surfacingLimit)
    if err != nil || len(entries) == 0 {
        return ""
    }
    return formatSurfacedKnowledge(entries)
}

func formatSurfacedKnowledge(entries []storage.KnowledgeEntry) string {
    var b strings.Builder
    b.WriteString("\n## Related knowledge\n")
    for _, e := range entries {
        fmt.Fprintf(&b, "- [%s] (#%d) **%s**\n", e.Category, e.ID, e.Title)
        // 1-line summary: first line of body, capped at 200 chars.
        firstLine := e.Body
        if i := strings.IndexByte(firstLine, '\n'); i >= 0 {
            firstLine = firstLine[:i]
        }
        if len(firstLine) > 200 {
            firstLine = firstLine[:200] + "…"
        }
        fmt.Fprintf(&b, "  %s\n", firstLine)
    }
    return b.String()
}
```

- [ ] **Step 2: Verify the package builds**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/mcpserver/knowledge_surfacing.go
git commit -m "feat(mcp): knowledge surfacing helper for passive context tools"
```

---

### Task 11: Wire surfacing into `handleGetSymbolContext`

**Files:**
- Modify: `internal/mcpserver/handlers.go`

- [ ] **Step 1: Locate the symbol-context handler and the point where it builds its response**

Run: `grep -n "handleGetSymbolContext" internal/mcpserver/handlers.go`
Identify the spot just before `return mcp.NewToolResultText(b.String()), nil` (where `b` is the response `strings.Builder`).

- [ ] **Step 2: Append the surfaced knowledge block**

Just before the final `return`, add:

```go
if symbolID > 0 {
    if extra := s.surfaceKnowledgeForSymbol(ctx, symbolID); extra != "" {
        b.WriteString(extra)
    }
}
```

If the handler does not currently expose a `symbolID` variable, capture it from the lookup it performs (e.g., the resolved `symbols.id` after the name lookup). Use the smallest possible diff — pull the ID from the existing query result.

- [ ] **Step 3: Verify build + run a smoke test**

Run: `go build ./...`
Expected: no output.

Smoke test via MCP:
```bash
curl -sX POST http://localhost:8484/mcp -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call",
       "params":{"name":"get_symbol_context","arguments":{"name":"<some symbol with anchored knowledge>"}}}' | jq -r '.result.content[0].text'
```
Expected: response ends with a `## Related knowledge` block (only if knowledge was anchored to that symbol or its package).

- [ ] **Step 4: Commit**

```bash
git add internal/mcpserver/handlers.go
git commit -m "feat(mcp): surface anchored knowledge in get_symbol_context"
```

---

### Task 12: Wire surfacing into `handleGetPackageSummary`

**Files:**
- Modify: `internal/mcpserver/handlers.go`

- [ ] **Step 1: Locate the handler**

Run: `grep -n "handleGetPackageSummary" internal/mcpserver/handlers.go`

- [ ] **Step 2: Append the surfaced knowledge block**

Just before the final `return mcp.NewToolResultText(...)`:

```go
if pkgName != "" {
    if extra := s.surfaceKnowledgeForPackage(ctx, pkgName); extra != "" {
        b.WriteString(extra)
    }
}
```

(Use whichever variable holds the package name in that handler — replace `pkgName` accordingly.)

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpserver/handlers.go
git commit -m "feat(mcp): surface anchored knowledge in get_package_summary"
```

---

### Task 13: Wire surfacing into `handleSearchGoContext`

**Files:**
- Modify: `internal/mcpserver/handlers.go`

- [ ] **Step 1: Locate the handler and identify the top-N returned chunks**

Run: `grep -n "handleSearchGoContext" internal/mcpserver/handlers.go`

The handler returns a list of chunks; for the top results (cap at 5), gather the union of distinct `package_name`s and surface anchored knowledge once per package.

- [ ] **Step 2: Append the related-knowledge block**

After the loop that formats code chunks, before the final `return`:

```go
seen := map[string]struct{}{}
var pkgs []string
for i, hit := range results {
    if i >= 5 { break }
    if hit.PackageName == "" { continue }
    if _, ok := seen[hit.PackageName]; ok { continue }
    seen[hit.PackageName] = struct{}{}
    pkgs = append(pkgs, hit.PackageName)
}
for _, p := range pkgs {
    if extra := s.surfaceKnowledgeForPackage(ctx, p); extra != "" {
        b.WriteString(extra)
    }
}
```

(Adjust `results` / `hit.PackageName` to the actual variable names used in the handler.)

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpserver/handlers.go
git commit -m "feat(mcp): surface related knowledge alongside search_go_context results"
```

---

### Task 14: CLI — `projectlens knowledge` subcommand

**Files:**
- Create: `cmd/projectlens/knowledge.go`
- Modify: `cmd/projectlens/main.go`

- [ ] **Step 1: Implement the subcommand tree**

Create `cmd/projectlens/knowledge.go`:

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "strconv"
    "strings"

    "github.com/spf13/cobra"

    "github.com/hman-pro/projectlens/internal/storage"
)

func newKnowledgeCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "knowledge",
        Short: "Query the captured knowledge layer (read-only; capture is MCP-only)",
    }
    cmd.AddCommand(newKnowledgeListCmd(), newKnowledgeShowCmd(),
        newKnowledgeDeleteCmd(), newKnowledgeSearchCmd())
    return cmd
}

func newKnowledgeListCmd() *cobra.Command {
    var category, tag string
    var limit int
    cmd := &cobra.Command{
        Use:   "list",
        Short: "List knowledge entries (most recent first)",
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := context.Background()
            cfg, _, err := loadCmdConfig(cmd); if err != nil { return err }
            db, err := storage.Connect(ctx, cfg.DatabaseURL); if err != nil { return err }
            defer db.Close()

            entries, err := db.ListKnowledgeEntries(ctx, storage.KnowledgeListFilters{
                Category: category, Tag: tag, Limit: limit,
            })
            if err != nil { return err }

            for _, e := range entries {
                fmt.Printf("#%d [%s] %s\n", e.ID, e.Category, e.Title)
                if len(e.Tags) > 0 {
                    fmt.Printf("    tags: %s\n", strings.Join(e.Tags, ", "))
                }
            }
            return nil
        },
    }
    cmd.Flags().StringVar(&category, "category", "", "filter by category")
    cmd.Flags().StringVar(&tag, "tag", "", "filter by tag")
    cmd.Flags().IntVar(&limit, "limit", 50, "max entries")
    return cmd
}

func newKnowledgeShowCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "show <id>",
        Short: "Print a knowledge entry as JSON",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            id, err := strconv.ParseInt(args[0], 10, 64)
            if err != nil { return fmt.Errorf("invalid id: %w", err) }

            ctx := context.Background()
            cfg, _, err := loadCmdConfig(cmd); if err != nil { return err }
            db, err := storage.Connect(ctx, cfg.DatabaseURL); if err != nil { return err }
            defer db.Close()

            e, err := db.GetKnowledgeEntry(ctx, id); if err != nil { return err }
            out, _ := json.MarshalIndent(e, "", "  ")
            fmt.Println(string(out))
            return nil
        },
    }
}

func newKnowledgeDeleteCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "delete <id>",
        Short: "Delete a knowledge entry (and its chunk + anchor edges)",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            id, err := strconv.ParseInt(args[0], 10, 64)
            if err != nil { return fmt.Errorf("invalid id: %w", err) }

            ctx := context.Background()
            cfg, _, err := loadCmdConfig(cmd); if err != nil { return err }
            db, err := storage.Connect(ctx, cfg.DatabaseURL); if err != nil { return err }
            defer db.Close()

            n, err := db.DeleteKnowledgeEntry(ctx, id); if err != nil { return err }
            if n == 0 { return fmt.Errorf("no entry with id %d", id) }
            fmt.Printf("deleted entry %d\n", id)
            return nil
        },
    }
}

func newKnowledgeSearchCmd() *cobra.Command {
    var category, anchor string
    var limit int
    cmd := &cobra.Command{
        Use:   "search <query>",
        Short: "Search knowledge by query and/or anchor",
        Args:  cobra.MinimumNArgs(0),
        RunE: func(cmd *cobra.Command, args []string) error {
            query := strings.Join(args, " ")
            if query == "" && anchor == "" {
                return fmt.Errorf("provide a query or --anchor")
            }

            ctx := context.Background()
            cfg, _, err := loadCmdConfig(cmd); if err != nil { return err }
            db, err := storage.Connect(ctx, cfg.DatabaseURL); if err != nil { return err }
            defer db.Close()

            // anchor path
            if anchor != "" {
                parts := strings.SplitN(anchor, ":", 2)
                if len(parts) != 2 { return fmt.Errorf("--anchor must be type:ref") }
                hits, err := db.KnowledgeForAnchor(ctx,
                    storage.AnchorRequest{Type: parts[0], Ref: parts[1]}, limit)
                if err != nil { return err }
                for _, e := range hits {
                    fmt.Printf("#%d [%s] %s\n", e.ID, e.Category, e.Title)
                }
                return nil
            }

            // vector path (CLI uses the same router via storage; we keep CLI thin
            // and skip embedding for now — recommend MCP for vector search)
            return fmt.Errorf("vector search is MCP-only; use --anchor here, or call search_knowledge via MCP")
        },
    }
    cmd.Flags().StringVar(&category, "category", "", "filter by category")
    cmd.Flags().StringVar(&anchor, "anchor", "", "anchor in form type:ref (e.g., symbol:Foo)")
    cmd.Flags().IntVar(&limit, "limit", 10, "max results")
    return cmd
}
```

- [ ] **Step 2: Register the subcommand**

In `cmd/projectlens/main.go`, find the `rootCmd.AddCommand(...)` block and add `newKnowledgeCmd(),` to the list.

- [ ] **Step 3: Verify the binary builds**

Run: `go build ./cmd/projectlens/`
Expected: no output.

- [ ] **Step 4: Smoke-test**

```bash
go run ./cmd/projectlens/ knowledge list \
  --db "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"
```
Expected: list of entries (or empty if none yet).

- [ ] **Step 5: Commit**

```bash
git add cmd/projectlens/knowledge.go cmd/projectlens/main.go
git commit -m "feat(cli): projectlens knowledge {list,show,delete,search} subcommand"
```

---

### Task 15: Skill file + Stop-hook settings snippet

**Files:**
- Create: `claude/skills/capture-knowledge/SKILL.md`
- Create: `claude/settings-snippet.json`

- [ ] **Step 1: Write the skill**

Create `claude/skills/capture-knowledge/SKILL.md`:

````markdown
---
name: capture-knowledge
description: Detect and persist durable lessons, best practices, conventions, domain knowledge, how-tos, and decisions encountered during a session via the save_knowledge MCP tool
---

## When to use

Whenever any of the 9 trigger signals fires during a session. The Stop hook
will remind you to scan; this skill tells you *what* to scan for and *how* to
record it.

## Categories

| Category | Use when |
|---|---|
| `lesson` | Postmortem-flavored: "I ran into X, here's what I learned/fixed" |
| `best_practice` | Forward-looking rule: "when doing X, prefer Y" |
| `convention` | Repo-specific style/taste rule |
| `domain_knowledge` | Terminology, conceptual model, business semantics |
| `how_to` | Step-by-step recipe for a recurring task |
| `decision` | "We picked X over Y because Z" (ADR-style) |

## Trigger signals (the 9)

| Signal | Likely category |
|---|---|
| User correction ("don't do that", "we don't do X here") | convention / best_practice |
| User reveals a rule ("from now on…", "always X", "never Y") | convention |
| Domain term clarified ("X is not the same as Y") | domain_knowledge |
| Non-obvious root cause found (symptom → cause not derivable from code) | lesson |
| Stuck → unstuck moment (workaround, flag, tool combo broke a blocker) | how_to / lesson |
| Repeated task (you or user did it before) | how_to |
| Surprise / gotcha ("looks right but breaks because…") | lesson |
| Design decision made with rationale | decision |
| Pattern observed across files ("every service does X the same way") | convention |

## When NOT to save

- Current-session ephemera ("the bug we just fixed in this PR") — that goes in the commit message.
- Restating CLAUDE.md content — it's already there.
- Rephrasing self-explanatory code — well-named identifiers already document it.
- Exploratory wandering — only after a clear signal, not "just in case".
- Duplicates within the same session — one capture per insight.

## How to call `save_knowledge`

Required: `category`, `title`, `body`. Optional but strongly preferred: `anchors`, `tags`.

**Anchor selection** (most specific that applies):
- `symbol` — about a specific function/type. `ref` = full SCIP symbol (e.g., `go . internal/storage . DB.UpsertChunk()`).
- `package` — about a layer or subsystem. `ref` = package name (e.g., `internal/embeddings`).
- `file` — about a single non-code file (config, migration). `ref` = repo-relative path.
- `table` — about a datastore concern. `ref` = table name.
- *no anchor* — broad/cross-cutting wisdom only.

**Body content**: lead with the rule or finding. Then a "Why:" line (the reason — incident, constraint, preference). Then a "How to apply:" line (when this kicks in). The Why is what makes the entry useful in 6 months when the surrounding context has changed.

## Examples

**Lesson, anchored to a symbol:**
```
save_knowledge(
  category="lesson",
  title="halfvec(1024) ANN index needs lists ≥ √rows",
  body="When the lists parameter is too small relative to row count, recall collapses below 50%.\n\n**Why:** Hit this when we scaled past 50k chunks — top-k results stopped including obvious matches.\n**How to apply:** When tuning vector indexes, set lists ≈ √(expected rows), reindex with CONCURRENTLY.",
  tags=["pgvector", "performance"],
  anchors=[{"type":"package","ref":"internal/storage"}]
)
```

**Convention, anchored to a package:**
```
save_knowledge(
  category="convention",
  title="Provider clients live under internal/providers/<name>",
  body="Each external API gets its own subdirectory under internal/providers/. Constructor takes config + http.Client.\n\n**Why:** Keeps boundary tests isolated and makes provider swaps trivial.\n**How to apply:** Adding a new external API → create internal/providers/<name>/, expose Client struct, add config block in configs/index.yaml.",
  tags=["architecture"],
  anchors=[{"type":"package","ref":"internal/providers"}]
)
```

**Decision, no anchor (broad):**
```
save_knowledge(
  category="decision",
  title="Ollama for embeddings, Anthropic for summaries",
  body="Local Ollama mxbai-embed-large for embeddings; Claude Sonnet via Anthropic API for summaries.\n\n**Why:** Embeddings are high-volume + privacy-sensitive (run locally, free). Summaries are low-volume + benefit from quality (worth API cost).\n**How to apply:** Don't add new providers without a clear reason; prefer extending one of these two.",
  tags=["providers"]
)
```

## Silent skip

If you scan a turn and nothing qualifies, **stop silently** — do not narrate
"I checked and nothing qualified". The user will not see the check; that's the
point.
````

- [ ] **Step 2: Write the Stop-hook settings snippet**

Create `claude/settings-snippet.json`:

```json
{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "echo '<system-reminder>Before stopping: scan this turn against the capture-knowledge skill (.claude/skills/capture-knowledge/SKILL.md). If any of the 9 signals fired, call save_knowledge now. If nothing qualifies, stop silently. Do not narrate this check.</system-reminder>'"
          }
        ]
      }
    ]
  }
}
```

- [ ] **Step 3: Document the install path**

The user merges this into the **target repo's** `.claude/settings.json` (project scope, so the hook only fires when working in a repo equipped with projectlens). Add an installation note in Task 16 docs.

- [ ] **Step 4: Commit**

```bash
git add claude/skills/capture-knowledge/SKILL.md claude/settings-snippet.json
git commit -m "feat(skills): capture-knowledge skill + Stop-hook settings snippet"
```

---

### Task 16: Documentation

**Files:**
- Modify: `claude/CLAUDE.md.snippet`
- Modify: `CLAUDE.md` (this repo)

- [ ] **Step 1: Document the new tools in the user-facing CLAUDE.md.snippet**

Open `claude/CLAUDE.md.snippet`. Add to the existing "MCP tools" section (or wherever tools are enumerated):

```markdown
**Knowledge layer**
- "Save what we just learned" / "remember that X" → `save_knowledge`
- "What do we know about <topic>?" / "what's the lesson about Y?" → `search_knowledge`
- Knowledge anchored to symbols/packages auto-surfaces in `get_symbol_context`,
  `get_package_summary`, and `search_go_context` results — no extra call needed.
```

Add an "Install the capture loop" section:

````markdown
### Install the capture loop (optional but recommended)

1. Symlink or copy `claude/skills/capture-knowledge/` into your repo's `.claude/skills/`.
2. Merge `claude/settings-snippet.json` into your repo's `.claude/settings.json`.

The skill tells Claude *what* counts as a capture-worthy moment; the Stop hook
ensures Claude actually checks every turn. Both work standalone — installing
just one is a graceful degradation.
````

- [ ] **Step 2: Update repo-root `CLAUDE.md`**

Modify `CLAUDE.md`:
- In the **MCP tools** table, add two rows (`save_knowledge`, `search_knowledge`) and a note that surfacing is automatic for `get_*_context` tools.
- In the **Database** section, add `knowledge_entries` to the table list (now 13 tables).
- In the **Indexer pipeline** section, add the line: knowledge entries flow through the existing chunk + embedding pipeline (no new stage).

- [ ] **Step 3: Commit**

```bash
git add claude/CLAUDE.md.snippet CLAUDE.md
git commit -m "docs: knowledge layer tools, install steps, and pipeline updates"
```

---

## Self-review

**Spec coverage:**
- §2 Categories → Task 1 schema CHECK + Task 15 skill table.
- §3 Trigger signals → Task 15 skill table.
- §4 Architecture → matches end-to-end across Tasks 1–15.
- §5 Schema → Task 1 (table), Task 2 (chunk insert), Task 4 (anchor edges).
- §6.1 `save_knowledge` MCP tool → Task 8.
- §6.2 Skill → Task 15.
- §6.3 Stop hook → Task 15.
- §7.1 `search_knowledge` → Task 9.
- §7.2 Passive surfacing in 3 tools → Tasks 11, 12, 13.
- §7.3 CLI parity → Task 14 (note: CLI vector search deferred — flagged).
- §8 Testing → Task 2 unit + Task 7 integration.
- §9 Error handling → Task 4 (unresolved anchors), Task 8 (validation), Task 9 (input checks), Task 1 (CHECK constraint).
- §10 Files to add/modify → matches the file structure section above.

**Placeholder scan:** No "TBD"/"TODO"/"add appropriate error handling" patterns — every step has actual code or commands. Two intentional flexibility points are explicit:
- Task 9 Step 2 — verifies `EmbedQuery` exists, gives the wrapper to add if not.
- Tasks 11–13 — handler-internal variable names noted as "adjust to match" because we haven't pinned every existing handler shape; the diff size is one block.

**Type consistency:** `KnowledgeEntry`, `AnchorRequest`, `AnchorResolution`, `KnowledgeListFilters`, `KnowledgeSearchHit` are defined once each (Tasks 2–5), referenced consistently afterwards (Tasks 8, 9, 10, 14). Edge `confidence` field is `*float32` everywhere, matching `EdgeRecord` from the existing codebase.

**Known deferral (intentional, called out in spec §11 for plan stage):**
- CLI `knowledge search` vector path returns "MCP-only" rather than constructing an embedder. Reason: keeps CLI thin in v1; adding an embedder factory is a larger change. Anchor-based CLI search works fully. Documented in Task 14 Step 1.
