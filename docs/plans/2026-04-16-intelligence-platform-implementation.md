# Intelligence Platform Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Refactor ProjectLens from a code-only indexer into a unified intelligence platform with four layers: code, datastore, history, and docs.

**Architecture:** Incremental migration — Phase 1 refactors existing schema and code to support the polymorphic graph model. Phases 2-4 add new capabilities (datastore, history, docs) as independent pipeline stages. Each phase leaves the system in a working state.

**Tech Stack:** Go 1.26, Postgres 16 + pgvector, OpenAI API, Atlassian REST API (Confluence + Jira)

**Design doc:** `docs/plans/2026-04-16-intelligence-platform-design.md`

---

## Phase 1: Schema Refactoring (Core)

Refactor existing tables to support the unified graph model. After this phase, code indexing works exactly as before but on the new schema.

### Task 1: Write migration 002 — schema changes

**Files:**
- Create: `migrations/002_intelligence_platform.up.sql`
- Create: `migrations/002_intelligence_platform.down.sql`

**Step 1: Write the up migration**

```sql
-- migrations/002_intelligence_platform.up.sql

-- 1. Extend symbols with SCIP-style ID and role bitset
ALTER TABLE symbols ADD COLUMN scip_symbol TEXT;
ALTER TABLE symbols ADD COLUMN roles INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_symbols_scip ON symbols(scip_symbol);

-- 2. Extend chunks to support multiple content types
ALTER TABLE chunks ALTER COLUMN symbol_id DROP NOT NULL;
ALTER TABLE chunks ADD COLUMN source_type TEXT NOT NULL DEFAULT 'code';
ALTER TABLE chunks ADD COLUMN source_uri TEXT;
CREATE INDEX idx_chunks_source_type ON chunks(source_type);

-- 3. Refactor edges to polymorphic graph model
--    Drop old edges table and recreate with new schema.
--    Edges are derived data (rebuilt from code), safe to drop.
DROP TABLE IF EXISTS edges;
CREATE TABLE edges (
    id              BIGSERIAL PRIMARY KEY,
    source_type     TEXT NOT NULL,
    source_id       BIGINT NOT NULL,
    target_type     TEXT NOT NULL,
    target_id       BIGINT NOT NULL,
    edge_type       TEXT NOT NULL,
    properties      JSONB,
    confidence      REAL,
    UNIQUE (source_type, source_id, target_type, target_id, edge_type)
);
CREATE INDEX idx_edges_source ON edges(source_type, source_id);
CREATE INDEX idx_edges_target ON edges(target_type, target_id);
CREATE INDEX idx_edges_type ON edges(edge_type);

-- 4. New table: datastore_tables
CREATE TABLE datastore_tables (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    engine          TEXT NOT NULL,
    schema_name     TEXT,
    columns         JSONB,
    source_file_id  BIGINT REFERENCES files(id) ON DELETE SET NULL,
    indexed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (name, engine)
);

-- 5. New table: documents
CREATE TABLE documents (
    id              BIGSERIAL PRIMARY KEY,
    source_type     TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    title           TEXT NOT NULL,
    url             TEXT,
    body_text       TEXT,
    last_synced_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata        JSONB,
    UNIQUE (source_type, external_id)
);

-- 6. New table: symbol_history
CREATE TABLE symbol_history (
    id              BIGSERIAL PRIMARY KEY,
    symbol_id       BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    commit_hash     TEXT NOT NULL,
    author          TEXT NOT NULL,
    committed_at    TIMESTAMPTZ NOT NULL,
    change_type     TEXT NOT NULL,
    diff_snippet    TEXT,
    UNIQUE (symbol_id, commit_hash)
);
CREATE INDEX idx_symbol_history_symbol ON symbol_history(symbol_id);
CREATE INDEX idx_symbol_history_commit ON symbol_history(committed_at);

-- 7. New table: file_history
CREATE TABLE file_history (
    id              BIGSERIAL PRIMARY KEY,
    file_id         BIGINT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    commit_hash     TEXT NOT NULL,
    author          TEXT NOT NULL,
    committed_at    TIMESTAMPTZ NOT NULL,
    change_type     TEXT NOT NULL,
    diff_snippet    TEXT,
    UNIQUE (file_id, commit_hash)
);
CREATE INDEX idx_file_history_file ON file_history(file_id);
CREATE INDEX idx_file_history_commit ON file_history(committed_at);

-- 8. Extend index_runs to track per-stage status
ALTER TABLE index_runs ADD COLUMN stage TEXT NOT NULL DEFAULT 'code';
```

**Step 2: Write the down migration**

```sql
-- migrations/002_intelligence_platform.down.sql

-- Remove new tables
DROP TABLE IF EXISTS file_history;
DROP TABLE IF EXISTS symbol_history;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS datastore_tables;

-- Restore original edges table
DROP TABLE IF EXISTS edges;
CREATE TABLE edges (
    id                  BIGSERIAL PRIMARY KEY,
    source_symbol_id    BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    target_symbol_id    BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    edge_type           TEXT NOT NULL,
    UNIQUE (source_symbol_id, target_symbol_id, edge_type)
);
CREATE INDEX idx_edges_source ON edges(source_symbol_id);
CREATE INDEX idx_edges_target ON edges(target_symbol_id);
CREATE INDEX idx_edges_type ON edges(edge_type);

-- Remove added columns from chunks
ALTER TABLE chunks DROP COLUMN IF EXISTS source_uri;
ALTER TABLE chunks DROP COLUMN IF EXISTS source_type;
ALTER TABLE chunks ALTER COLUMN symbol_id SET NOT NULL;

-- Remove added columns from symbols
DROP INDEX IF EXISTS idx_symbols_scip;
ALTER TABLE symbols DROP COLUMN IF EXISTS roles;
ALTER TABLE symbols DROP COLUMN IF EXISTS scip_symbol;

-- Remove added column from index_runs
ALTER TABLE index_runs DROP COLUMN IF EXISTS stage;
```

**Step 3: Verify migration applies cleanly**

Run:
```bash
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
  -f migrations/001_initial_schema.down.sql
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
  -f migrations/001_initial_schema.up.sql
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
  -f migrations/002_intelligence_platform.up.sql
```

Expected: no errors. Verify with `\dt` — should show 12 tables.

**Step 4: Verify down migration rolls back cleanly**

Run:
```bash
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
  -f migrations/002_intelligence_platform.down.sql
```

Expected: no errors. Verify with `\dt` — should show 8 tables (original schema).

Re-apply for next tasks:
```bash
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
  -f migrations/002_intelligence_platform.up.sql
```

**Step 5: Commit**

```bash
git add migrations/002_intelligence_platform.up.sql migrations/002_intelligence_platform.down.sql
git commit -m "feat: add migration 002 for intelligence platform schema"
```

---

### Task 2: Update storage layer — EdgeRecord and edge functions

The edges table changed from `(source_symbol_id, target_symbol_id, edge_type)` to `(source_type, source_id, target_type, target_id, edge_type, properties, confidence)`. Update the Go types and all edge CRUD functions.

**Files:**
- Modify: `internal/storage/edges.go`
- Modify: `internal/storage/records_test.go`

**Step 1: Write tests for new EdgeRecord**

Add to `internal/storage/records_test.go`:
```go
// Verify new EdgeRecord fields compile and are usable.
_ = EdgeRecord{
    SourceType: "symbol",
    SourceID:   1,
    TargetType: "symbol",
    TargetID:   2,
    EdgeType:   "calls",
    Properties: nil,
    Confidence: nil,
}
```

Run: `go test ./internal/storage/ -v -run TestRecordStructs`
Expected: FAIL — EdgeRecord doesn't have these fields yet.

**Step 2: Update EdgeRecord struct**

Replace in `internal/storage/edges.go`:
```go
// EdgeRecord maps to a row in the edges table.
type EdgeRecord struct {
    ID         int64    `json:"id"`
    SourceType string   `json:"source_type"`
    SourceID   int64    `json:"source_id"`
    TargetType string   `json:"target_type"`
    TargetID   int64    `json:"target_id"`
    EdgeType   string   `json:"edge_type"`
    Properties *[]byte  `json:"properties,omitempty"` // JSONB
    Confidence *float32 `json:"confidence,omitempty"`
}
```

**Step 3: Update InsertEdges**

Replace the batch insert to use 8 columns instead of 3:
```go
const cols = 5 // source_type, source_id, target_type, target_id, edge_type
const maxBatch = 65535 / cols
```

Insert query changes to:
```sql
INSERT INTO edges (source_type, source_id, target_type, target_id, edge_type)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (source_type, source_id, target_type, target_id, edge_type) DO NOTHING
```

Note: `properties` and `confidence` are optional — set via separate update or a new `InsertEdgesWithMetadata` function. The basic insert only needs 5 columns for the call graph.

**Step 4: Update EdgeResult and query functions**

`EdgeResult` stays structurally similar but the JOIN changes. `GetCallers`, `GetCallees`, `GetImplementors` now filter on `source_type='symbol'` and `target_type='symbol'`.

Replace GetCallers query:
```sql
SELECT e.id, e.edge_type, s.id, s.name, s.kind, s.package_name,
       f.path, s.line_start, s.line_end
FROM edges e
JOIN symbols s ON s.id = e.source_id
JOIN files f ON f.id = s.file_id
WHERE e.target_type = 'symbol' AND e.target_id = $1
  AND e.source_type = 'symbol'
ORDER BY s.name
```

Same pattern for GetCallees (JOIN on `e.target_id`), GetImplementors (add `AND e.edge_type = 'implements'`).

**Step 5: Update DeleteEdgesBySymbolID**

```sql
DELETE FROM edges
WHERE (source_type = 'symbol' AND source_id = $1)
   OR (target_type = 'symbol' AND target_id = $1)
```

**Step 6: Run tests**

Run: `go test ./internal/storage/ -v`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/storage/edges.go internal/storage/records_test.go
git commit -m "refactor: update EdgeRecord and edge functions for polymorphic graph"
```

---

### Task 3: Update storage layer — SymbolRecord with SCIP + roles

**Files:**
- Modify: `internal/storage/symbols.go`
- Modify: `internal/storage/records_test.go`

**Step 1: Add scip_symbol and roles to SymbolRecord**

```go
type SymbolRecord struct {
    // ... existing fields ...
    ScipSymbol *string   `json:"scip_symbol,omitempty"`
    Roles      int       `json:"roles"`
}
```

**Step 2: Update InsertSymbols to include new columns**

Add `scip_symbol` and `roles` to the INSERT. Columns go from 10 to 12. Update `maxBatch = 65535 / 12`.

**Step 3: Update scan helpers**

`scanSymbols` must scan the two new columns from SELECT queries.

**Step 4: Run tests**

Run: `go test ./internal/storage/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/storage/symbols.go internal/storage/records_test.go
git commit -m "feat: add scip_symbol and roles to SymbolRecord"
```

---

### Task 4: Update storage layer — ChunkRecord with source_type

**Files:**
- Modify: `internal/storage/chunks.go`
- Modify: `internal/storage/records_test.go`

**Step 1: Update ChunkRecord**

```go
type ChunkRecord struct {
    ID         int64   `json:"id"`
    SymbolID   *int64  `json:"symbol_id,omitempty"` // nullable for doc chunks
    Content    string  `json:"content"`
    TokenCount int     `json:"token_count"`
    SourceType string  `json:"source_type"`
    SourceURI  *string `json:"source_uri,omitempty"`
}
```

**Step 2: Update UpsertChunk**

Change INSERT to include `source_type` and `source_uri`. The ON CONFLICT key for code chunks remains `symbol_id`, but doc chunks need a different conflict strategy. Add a new function `InsertDocChunk` for non-symbol chunks that uses `source_type + source_uri` as the dedup key.

**Step 3: Update SemanticSearch query in embeddings.go**

Add `c.source_type` to the SELECT and return it in `SemanticSearchResult`:
```go
type SemanticSearchResult struct {
    // ... existing fields ...
    SourceType  string  `json:"source_type"`
    SourceURI   *string `json:"source_uri,omitempty"`
}
```

**Step 4: Run tests**

Run: `go test ./internal/storage/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/storage/chunks.go internal/storage/embeddings.go internal/storage/records_test.go
git commit -m "feat: add source_type and source_uri to ChunkRecord"
```

---

### Task 5: Add storage functions for new tables

**Files:**
- Create: `internal/storage/datastore.go`
- Create: `internal/storage/documents.go`
- Create: `internal/storage/history.go`

**Step 1: Write datastore.go**

```go
// DatastoreTableRecord maps to a row in the datastore_tables table.
type DatastoreTableRecord struct { ... }

func (db *DB) UpsertDatastoreTable(ctx, rec) error
func (db *DB) GetDatastoreTableByName(ctx, name, engine) (*DatastoreTableRecord, error)
func (db *DB) ListDatastoreTables(ctx) ([]DatastoreTableRecord, error)
```

**Step 2: Write documents.go**

```go
// DocumentRecord maps to a row in the documents table.
type DocumentRecord struct { ... }

func (db *DB) UpsertDocument(ctx, rec) error
func (db *DB) GetDocumentByExternalID(ctx, sourceType, externalID) (*DocumentRecord, error)
func (db *DB) ListDocuments(ctx, sourceType) ([]DocumentRecord, error)
```

**Step 3: Write history.go**

```go
// SymbolHistoryRecord maps to a row in the symbol_history table.
type SymbolHistoryRecord struct { ... }

// FileHistoryRecord maps to a row in the file_history table.
type FileHistoryRecord struct { ... }

func (db *DB) InsertSymbolHistory(ctx, rec) error
func (db *DB) GetSymbolHistory(ctx, symbolID, limit) ([]SymbolHistoryRecord, error)
func (db *DB) InsertFileHistory(ctx, rec) error
func (db *DB) GetFileHistory(ctx, fileID, limit) ([]FileHistoryRecord, error)
func (db *DB) EvictOldHistory(ctx, maxPerEntity) error  // cap at 10
```

**Step 4: Update records_test.go**

Add struct instantiation tests for all new record types.

**Step 5: Run tests**

Run: `go test ./internal/storage/ -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/storage/datastore.go internal/storage/documents.go internal/storage/history.go internal/storage/records_test.go
git commit -m "feat: add storage layer for datastore_tables, documents, and history"
```

---

### Task 6: Update indexer — adapt to new storage types

The indexer currently constructs `EdgeRecord{SourceSymbolID, TargetSymbolID, EdgeType}`. Update to use new polymorphic fields.

**Files:**
- Modify: `internal/indexer/indexer.go`

**Step 1: Update edge construction in indexer**

Find all places where `storage.EdgeRecord` is created. Change from:
```go
storage.EdgeRecord{SourceSymbolID: srcID, TargetSymbolID: tgtID, EdgeType: "calls"}
```
To:
```go
storage.EdgeRecord{SourceType: "symbol", SourceID: srcID, TargetType: "symbol", TargetID: tgtID, EdgeType: "calls"}
```

**Step 2: Update symbol construction**

Add `ScipSymbol` and `Roles` fields. Generate SCIP symbol IDs in the format:
```
go . <package_path> . <SymbolName>()
```

Roles for now: `Definition = 1` for all parsed symbols.

**Step 3: Update chunk construction**

Set `SourceType: "code"` on all chunks. `SymbolID` becomes a pointer.

**Step 4: Run tests**

Run: `go test ./internal/indexer/ -v`
Expected: PASS

Run: `go test ./... -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/indexer/indexer.go
git commit -m "refactor: update indexer for polymorphic edges and SCIP symbols"
```

---

### Task 7: Update retrieval layer

**Files:**
- Modify: `internal/retrieval/lexical.go`
- Modify: `internal/retrieval/semantic.go`
- Modify: `internal/retrieval/graph.go`
- Modify: `internal/retrieval/router.go`

**Step 1: Update lexical.go**

Raw SQL queries in `LexicalSearch` reference `symbols` and `files` tables directly. These don't change structurally — symbols table still has `name`, `kind`, `package_name`, etc. No changes needed unless adding SCIP symbol to results.

Add `ScipSymbol` and `SourceType` to `SearchResult` struct.

**Step 2: Update semantic.go**

`SemanticSearchResult` now includes `SourceType`. Update `semanticResultToSearchResult` to pass it through.

**Step 3: Update graph.go**

`GetCallers`, `GetCallees`, `GetImplementors` are called via `storage.DB` — already updated in Task 2. The graph.go wrapper functions should work without changes.

**Step 4: Update router.go**

Add `SourceType` to deduplication logic in `deduplicateBySymbolID`. For doc results, dedup by `SourceURI` instead.

**Step 5: Run tests**

Run: `go test ./internal/retrieval/ -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/retrieval/
git commit -m "refactor: update retrieval layer for polymorphic schema"
```

---

### Task 8: Update MCP handlers and CLI

**Files:**
- Modify: `internal/mcpserver/handlers.go`
- Modify: `internal/mcpserver/tools.go`
- Modify: `cmd/projectlens/main.go`

**Step 1: Update MCP handlers**

- `find_symbol`: add `scip_symbol` to response JSON
- `get_symbol_context`: add `scip_symbol` to response
- `index_status`: show per-stage status from `index_runs.stage`
- Other handlers: minimal changes — they call retrieval layer which handles the mapping

**Step 2: Update MCP tool schemas**

Update `tools.go` to document new response fields.

**Step 3: Update CLI inspect commands**

`inspect-symbol`: display `scip_symbol` and `roles` in output.
`status`: show per-stage breakdown.

**Step 4: Run tests**

Run: `go test ./... -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/mcpserver/ cmd/projectlens/main.go
git commit -m "refactor: update MCP handlers and CLI for new schema"
```

---

### Task 9: End-to-end verification — re-index ingest

**Step 1: Reset database and apply all migrations**

```bash
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
  -f migrations/001_initial_schema.down.sql
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
  -f migrations/001_initial_schema.up.sql
psql "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable" \
  -f migrations/002_intelligence_platform.up.sql
```

**Step 2: Run bootstrap on ingest**

```bash
export $(grep -v '^#' .env | xargs) && go run ./cmd/projectlens/ bootstrap \
  --repo /Users/hamed.zohrehvand/source/example-org/ingest/master \
  --db "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"
```

Expected: completes without error.

**Step 3: Verify data**

```bash
psql "..." -c "SELECT relname, n_live_tup FROM pg_stat_user_tables ORDER BY n_live_tup DESC;"
```

Expected: files ~2913, symbols ~23K, chunks ~23K, edges ~21K+, embeddings ~23K, summaries ~70+

**Step 4: Verify queries work**

```bash
export $(grep -v '^#' .env | xargs)
go run ./cmd/projectlens/ query "how does supplier funding work" --db "..."
go run ./cmd/projectlens/ inspect-symbol CalculateFunding --db "..."
go run ./cmd/projectlens/ status --db "..."
```

**Step 5: Commit**

```bash
git commit --allow-empty -m "chore: verified end-to-end indexing on ingest monorepo with new schema"
```

---

## Phase 2: Datastore Indexing (Layer B)

### Task 10: Implement SQL migration parser

**Files:**
- Create: `internal/datastore/migration_parser.go`
- Create: `internal/datastore/migration_parser_test.go`

Parse SQL migration files to extract CREATE TABLE, ALTER TABLE statements. Extract table name, columns (name, type, nullable, constraints), and engine type.

**Step 1: Write tests**

Test cases:
- Parse `CREATE TABLE foo (id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL)` → table "foo" with 2 columns
- Parse `ALTER TABLE foo ADD COLUMN bar INTEGER` → column addition
- Parse multi-statement migration file
- Parse ClickHouse `CREATE TABLE ... ENGINE = MergeTree()` (future-proof)

**Step 2: Implement parser**

Use simple regex/string parsing — no need for a full SQL parser. Migration files follow predictable patterns.

**Step 3: Run tests, commit**

```bash
git commit -m "feat: implement SQL migration file parser for datastore indexing"
```

---

### Task 11: Implement Go SQL scanner

**Files:**
- Create: `internal/datastore/sql_scanner.go`
- Create: `internal/datastore/sql_scanner_test.go`

Scan Go source files for raw SQL strings in `db.Query()`, `db.Exec()`, `db.QueryRow()` calls. Extract table names and operation type (SELECT/INSERT/UPDATE/DELETE).

**Step 1: Write tests**

Test cases:
- `db.Query("SELECT * FROM users WHERE id = $1")` → reads "users"
- `db.Exec("INSERT INTO orders (user_id) VALUES ($1)")` → writes "orders"
- `db.QueryRow("UPDATE inventory SET qty = $1 WHERE sku = $2")` → writes "inventory"
- Multi-line SQL strings with backtick quoting
- Subqueries: `SELECT * FROM a JOIN b ON ...` → reads both "a" and "b"

**Step 2: Implement scanner**

Use `go/ast` to find string literals inside specific function calls. Parse the SQL string for table references using regex patterns for `FROM`, `JOIN`, `INTO`, `UPDATE`, `DELETE FROM`.

**Step 3: Run tests, commit**

```bash
git commit -m "feat: implement Go SQL string scanner for datastore indexing"
```

---

### Task 12: Implement `index datastore` CLI command

**Files:**
- Create: `internal/datastore/indexer.go`
- Modify: `cmd/projectlens/main.go`

Orchestrates migration parsing + SQL scanning. Reads symbols from DB, scans their source files, creates `datastore_tables` records and `reads_table`/`writes_table` edges.

**Step 1: Implement orchestrator**

```go
func IndexDatastore(ctx, db, repoPath, config) error {
    // 1. Find and parse migration files
    // 2. Upsert datastore_tables records
    // 3. Load symbols from DB
    // 4. Scan Go source files for SQL
    // 5. Create edges: symbol → reads_table/writes_table → datastore_table
}
```

**Step 2: Add CLI command**

```bash
projectlens index datastore --repo /path --db "..."
```

**Step 3: Test on ingest repo, commit**

```bash
git commit -m "feat: implement datastore indexing pipeline"
```

---

## Phase 3: Change History (Layer C)

### Task 13: Implement git history collector

**Files:**
- Create: `internal/history/collector.go`
- Create: `internal/history/collector_test.go`

Walk `git log`, extract per-file changes, map diff hunks to symbol line ranges.

**Step 1: Write tests**

Test cases:
- Parse `git log --name-status` output → list of (commit, file, change_type)
- Map diff hunk `@@ -10,5 +10,7 @@` to symbol at lines 8-15 → modified
- Cap at 10 entries per file/symbol

**Step 2: Implement collector**

```go
func CollectFileHistory(ctx, repoPath, since) ([]FileChange, error)
func MapToSymbolHistory(fileChanges, symbols) []SymbolChange
```

Uses `os/exec` to run `git log --name-status --since=<timestamp> --format=...`.

**Step 3: Run tests, commit**

```bash
git commit -m "feat: implement git history collector"
```

---

### Task 14: Implement co-change coupling detector

**Files:**
- Create: `internal/history/coupling.go`
- Create: `internal/history/coupling_test.go`

Compute co-change pairs from commit history. Files/symbols that appear in the same commit get a coupling score.

**Step 1: Write tests**

Test cases:
- Files A and B appear in 3 of 5 commits together → coupling 0.6
- File C always changes alone → no coupling edges
- Symbol-level: two symbols in same file but different hunks → not coupled

**Step 2: Implement detector**

```go
func ComputeCoupling(changes []CommitChanges) []CouplingPair
```

**Step 3: Run tests, commit**

```bash
git commit -m "feat: implement co-change coupling detection"
```

---

### Task 15: Implement `index history` CLI command

**Files:**
- Create: `internal/history/indexer.go`
- Modify: `cmd/projectlens/main.go`

Orchestrates: git log → file_history + symbol_history + co-change edges.

**Step 1: Implement orchestrator**

```go
func IndexHistory(ctx, db, repoPath, config) error {
    // 1. Get last index timestamp from DB
    // 2. Collect file history since last index
    // 3. Map to symbol history
    // 4. Compute coupling
    // 5. Store file_history, symbol_history records
    // 6. Store co-change edges
    // 7. Evict old history (cap at 10)
}
```

**Step 2: Add CLI command**

```bash
projectlens index history --repo /path --db "..."
```

**Step 3: Test on ingest repo, commit**

```bash
git commit -m "feat: implement history indexing pipeline"
```

---

## Phase 4: Doc Augmentation (Layer D)

### Task 16: Implement Confluence fetcher

**Files:**
- Create: `internal/docs/confluence.go`
- Create: `internal/docs/confluence_test.go`

Fetch pages from Confluence REST API v2. Extract title, body text, metadata.

**Step 1: Write tests with mock HTTP**

Test cases:
- Fetch page by ID → returns DocumentRecord
- Fetch pages by space → returns list
- Incremental: only fetch pages modified since last sync

**Step 2: Implement fetcher**

```go
type ConfluenceFetcher struct { baseURL, token string }
func (f *ConfluenceFetcher) FetchPage(ctx, pageID) (*DocumentRecord, error)
func (f *ConfluenceFetcher) FetchSpace(ctx, spaceKey, since) ([]DocumentRecord, error)
```

**Step 3: Run tests, commit**

```bash
git commit -m "feat: implement Confluence page fetcher"
```

---

### Task 17: Implement Jira fetcher

**Files:**
- Create: `internal/docs/jira.go`
- Create: `internal/docs/jira_test.go`

Fetch tickets from Jira REST API. Extract key, title, description, status, labels.

**Step 1: Write tests with mock HTTP**

**Step 2: Implement fetcher**

```go
type JiraFetcher struct { baseURL, token string }
func (f *JiraFetcher) FetchByJQL(ctx, jql, since) ([]DocumentRecord, error)
```

**Step 3: Run tests, commit**

```bash
git commit -m "feat: implement Jira ticket fetcher"
```

---

### Task 18: Implement commit-message ticket linker

**Files:**
- Create: `internal/docs/linker.go`
- Create: `internal/docs/linker_test.go`

Extract Jira ticket IDs from git commit messages and create edges to affected files/symbols.

**Step 1: Write tests**

Test cases:
- Commit message `FOR-1234: fix funding calc` → extracts `FOR-1234`
- Multiple tickets: `FOR-1234 FOR-5678: refactor` → extracts both
- No ticket: `fix typo` → no extraction
- Link ticket to files changed in that commit → edges

**Step 2: Implement linker**

```go
func ExtractTicketIDs(commitMsg string, pattern *regexp.Regexp) []string
func LinkTicketsToCode(ctx, db, repoPath, ticketPattern) error
```

**Step 3: Run tests, commit**

```bash
git commit -m "feat: implement commit-message ticket linker"
```

---

### Task 19: Implement `index docs` CLI command

**Files:**
- Create: `internal/docs/indexer.go`
- Modify: `cmd/projectlens/main.go`

Orchestrates: Confluence + Jira fetch → documents → chunks → ticket linking.

**Step 1: Implement orchestrator**

```go
func IndexDocs(ctx, db, repoPath, config) error {
    // 1. Fetch Confluence pages
    // 2. Fetch Jira tickets
    // 3. Upsert documents
    // 4. Chunk document body text
    // 5. Extract ticket IDs from git commits
    // 6. Create edges: document → mentions → symbol/file
}
```

**Step 2: Add CLI command**

```bash
projectlens index docs --repo /path --db "..."
```

**Step 3: Test, commit**

```bash
git commit -m "feat: implement docs indexing pipeline"
```

---

## Phase 5: Independent Embed & Summarize + New MCP Tools

### Task 20: Extract embed and summarize as independent commands

**Files:**
- Modify: `internal/indexer/indexer.go` — remove embed/summarize steps from code pipeline
- Create: `internal/embed/embedder.go` — standalone embed-all-missing-chunks
- Create: `internal/summarize/summarizer.go` — standalone summarize-all-missing
- Modify: `cmd/projectlens/main.go` — add `index embed`, `index summarize`, `index all`

**Step 1: Implement standalone embedder**

```go
func EmbedMissing(ctx, db, openaiClient) error {
    // SELECT chunks.id FROM chunks LEFT JOIN embeddings ON ... WHERE embeddings.id IS NULL
    // Batch embed, upsert
}
```

**Step 2: Implement standalone summarizer**

```go
func SummarizeMissing(ctx, db, openaiClient) error {
    // Find packages/tables/documents without summaries
    // Generate and upsert
}
```

**Step 3: Add CLI commands and `index all` orchestrator**

**Step 4: Run tests, commit**

```bash
git commit -m "feat: extract embed and summarize as independent pipeline stages"
```

---

### Task 21: Add new MCP tools

**Files:**
- Modify: `internal/mcpserver/handlers.go`
- Modify: `internal/mcpserver/tools.go`
- Modify: `internal/mcpserver/server.go`

**Step 1: Implement `get_table_context` handler**

Given a table name → columns, which symbols read/write it, migration source.

**Step 2: Implement `get_change_history` handler**

Given a symbol or file name → last 10 commits, authors, diffs.

**Step 3: Implement `get_coupling` handler**

Given a symbol or file → co-change partners ranked by coupling strength.

**Step 4: Update tool schemas in tools.go**

**Step 5: Register new tools in server.go**

**Step 6: Run tests, commit**

```bash
git commit -m "feat: add get_table_context, get_change_history, get_coupling MCP tools"
```

---

### Task 22: Update configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `configs/index.yaml`

**Step 1: Extend Config struct**

Add `Datastore`, `History`, `Docs`, `Embeddings` sections to Config.

**Step 2: Update index.yaml with new sections**

**Step 3: Run tests, commit**

```bash
git commit -m "feat: extend configuration for datastore, history, and docs settings"
```

---

## Task Summary

| Phase | Tasks | Description |
|-------|-------|-------------|
| 1 | 1-9 | Schema refactoring — get code indexing working on new schema |
| 2 | 10-12 | Datastore indexing — migrations + SQL scanning |
| 3 | 13-15 | Change history — git log + coupling detection |
| 4 | 16-19 | Doc augmentation — Confluence + Jira + ticket linking |
| 5 | 20-22 | Independent stages + new MCP tools + config |

Each phase leaves the system in a working state. Phase 1 is the critical path — everything else builds on it.
