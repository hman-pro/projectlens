# Datastore Indexing (Phase 2) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Index PostgreSQL schemas and SQL queries from the ingest monorepo so an LLM can understand data flows — which code reads/writes which tables, and what the schema looks like.

**Architecture:** Two independent parsers (migration parser + Go SQL scanner) feed into the existing polymorphic graph via `datastore_tables` records and `reads_table`/`writes_table` edges. A table-level chunk gets embedded into the same vector space as code, so semantic search surfaces tables alongside symbols. An `index datastore` CLI command orchestrates everything independently from code indexing.

**Tech Stack:** Go 1.26, regex-based SQL parsing (no external SQL parser dependency), existing Postgres + pgvector storage.

**Target repo data:**
- 123 PostgreSQL migrations in `db/postgresql/metastore/migrations/`
- 28 CREATE TABLE statements across 7 schemas (ai, dataread, panel, plan, rounding, settings, useraccess)
- ~1,283 raw SQL statements across 328 Go files (patterns: `s.db.Get`, `s.db.Exec`, `s.db.Query`, heredoc SQL strings)

---

### Task 1: Implement PostgreSQL migration parser

**Files:**
- Create: `internal/datastore/migration_parser.go`
- Create: `internal/datastore/migration_parser_test.go`

**What it does:** Parse `.up.sql` migration files to extract the **current schema state** — table name, schema, columns with types/constraints, foreign keys. Applies migrations in order so ALTER TABLE modifications are merged into the table definition.

**Step 1: Write tests**

Test cases using real patterns from the ingest repo:

```go
func TestParseMigrations_CreateTable(t *testing.T) {
    sql := `CREATE TABLE rounding.sets (
        id SERIAL PRIMARY KEY,
        project_id INTEGER NOT NULL,
        uuid UUID NOT NULL DEFAULT gen_random_uuid(),
        name TEXT NOT NULL,
        criteria JSONB,
        UNIQUE (project_id, name)
    );`
    tables := ParseMigrations([]MigrationFile{{Name: "001.up.sql", SQL: sql}})
    require.Len(t, tables, 1)
    assert.Equal(t, "sets", tables[0].Name)
    assert.Equal(t, "rounding", tables[0].Schema)
    assert.Len(t, tables[0].Columns, 5)
    assert.Equal(t, "id", tables[0].Columns[0].Name)
    assert.Equal(t, "SERIAL", tables[0].Columns[0].Type)
    assert.True(t, tables[0].Columns[0].IsPrimaryKey)
}

func TestParseMigrations_AlterTable(t *testing.T) {
    sqls := []MigrationFile{
        {Name: "001.up.sql", SQL: `CREATE TABLE approval.requests (id SERIAL PRIMARY KEY, type TEXT NOT NULL);`},
        {Name: "002.up.sql", SQL: `ALTER TABLE approval.requests ADD COLUMN deal_uuid UUID;`},
    }
    tables := ParseMigrations(sqls)
    require.Len(t, tables, 1)
    assert.Len(t, tables[0].Columns, 3) // id, type, deal_uuid
}

func TestParseMigrations_MultipleTables(t *testing.T) {
    // Test with multiple CREATE TABLEs in different schemas
}

func TestParseMigrations_IfNotExists(t *testing.T) {
    // CREATE TABLE IF NOT EXISTS should work
}
```

Run: `go test ./internal/datastore/ -v -run TestParseMigrations`
Expected: FAIL (package doesn't exist yet)

**Step 2: Implement migration parser**

Core types:
```go
package datastore

// TableDef represents a database table extracted from migrations.
type TableDef struct {
    Name       string      `json:"name"`
    Schema     string      `json:"schema"`
    Columns    []ColumnDef `json:"columns"`
    SourceFile string      `json:"source_file"` // migration that created it
}

// ColumnDef represents a column in a table.
type ColumnDef struct {
    Name         string `json:"name"`
    Type         string `json:"type"`
    IsNullable   bool   `json:"is_nullable"`
    IsPrimaryKey bool   `json:"is_primary_key"`
    Default      string `json:"default,omitempty"`
    ForeignKey   string `json:"foreign_key,omitempty"` // e.g., "customers.id"
}

// MigrationFile is a parsed migration.
type MigrationFile struct {
    Name string
    SQL  string
}

// ParseMigrations processes migration files in order and returns the current
// schema state. ALTER TABLE modifications are merged into existing table defs.
func ParseMigrations(files []MigrationFile) []TableDef
```

Implementation approach:
- Regex to find `CREATE TABLE [IF NOT EXISTS] [schema.]name (...)` blocks
- Parse column definitions inside the parens (split by comma, extract name + type + constraints)
- Regex to find `ALTER TABLE [schema.]name ADD COLUMN ...` and merge into existing table
- Process files in sorted order (migrations are numbered)

**Step 3: Run tests, verify pass**

Run: `go test ./internal/datastore/ -v`

**Step 4: Commit**

```bash
git commit -m "feat: implement PostgreSQL migration parser for datastore indexing"
```

---

### Task 2: Implement Go SQL scanner

**Files:**
- Create: `internal/datastore/sql_scanner.go`
- Create: `internal/datastore/sql_scanner_test.go`

**What it does:** Scan Go source files for raw SQL strings, extract table references and operation types. Produces structured records: "symbol X performs WRITE on table plan.display_locations (columns: project_id, created_by, ...)".

**Step 1: Write tests**

Test cases using real patterns from the ingest repo:

```go
func TestScanSQL_Insert(t *testing.T) {
    src := `package store
func (s *Store) Insert(ctx context.Context) error {
    _, err := s.db.Exec(ctx, ` + "`" + `
INSERT INTO plan.display_locations (project_id, created_by, display_id)
VALUES ($1, $2, $3)` + "`" + `, pid, uid, did)
    return err
}`
    refs := ScanGoFile("store.go", []byte(src))
    require.Len(t, refs, 1)
    assert.Equal(t, "plan.display_locations", refs[0].Table)
    assert.Equal(t, "INSERT", refs[0].Operation)
    assert.Equal(t, "Insert", refs[0].FuncName)
}

func TestScanSQL_Select(t *testing.T) {
    src := `package store
func (s *Store) List(ctx context.Context) error {
    rows, err := s.db.Query(ctx, "SELECT id, name FROM rounding.sets WHERE project_id = $1", pid)
    return err
}`
    refs := ScanGoFile("store.go", []byte(src))
    require.Len(t, refs, 1)
    assert.Equal(t, "rounding.sets", refs[0].Table)
    assert.Equal(t, "SELECT", refs[0].Operation)
}

func TestScanSQL_MultipleTablesJoin(t *testing.T) {
    // SELECT ... FROM table_a JOIN table_b ON ...
    // Should produce refs for both tables
}

func TestScanSQL_HeredocSQL(t *testing.T) {
    // Backtick-quoted multi-line SQL strings
}

func TestScanSQL_NoSQL(t *testing.T) {
    // Go file with no SQL → empty results
}
```

**Step 2: Implement scanner**

Core types:
```go
// SQLRef represents a SQL table reference found in Go source code.
type SQLRef struct {
    Table     string `json:"table"`      // e.g., "plan.display_locations"
    Operation string `json:"operation"`  // SELECT, INSERT, UPDATE, DELETE
    FuncName  string `json:"func_name"`  // enclosing Go function name
    FilePath  string `json:"file_path"`
    Line      int    `json:"line"`
}

// ScanGoFile parses a Go source file and extracts SQL table references
// from string literals.
func ScanGoFile(filename string, src []byte) []SQLRef
```

Implementation approach:
- Use `go/parser` + `go/ast` to walk the AST
- Find string literals (basic and backtick) inside function bodies
- For each string that looks like SQL (contains SELECT/INSERT/UPDATE/DELETE + FROM/INTO keywords):
  - Extract operation type from first keyword
  - Extract table names using regex: `FROM\s+(\w+\.?\w+)`, `INTO\s+(\w+\.?\w+)`, `UPDATE\s+(\w+\.?\w+)`, `JOIN\s+(\w+\.?\w+)`
  - Record the enclosing function name from the AST
- Return deduplicated refs per function

**Step 3: Run tests, verify pass**

**Step 4: Commit**

```bash
git commit -m "feat: implement Go SQL scanner for datastore indexing"
```

---

### Task 3: Implement table chunk builder

**Files:**
- Create: `internal/datastore/chunks.go`
- Create: `internal/datastore/chunks_test.go`

**What it does:** Build an LLM-optimized text chunk for each table, combining schema DDL, column metadata, FK relationships, and which Go symbols read/write it. This chunk gets embedded for semantic search.

**Step 1: Write tests**

```go
func TestBuildTableChunk(t *testing.T) {
    table := TableDef{
        Name:   "sets",
        Schema: "rounding",
        Columns: []ColumnDef{
            {Name: "id", Type: "SERIAL", IsPrimaryKey: true},
            {Name: "project_id", Type: "INTEGER", IsNullable: false},
            {Name: "name", Type: "TEXT", IsNullable: false},
        },
        SourceFile: "000217_rounding_sets.up.sql",
    }
    readers := []SQLRef{{Table: "rounding.sets", Operation: "SELECT", FuncName: "ListSets", FilePath: "core/rounding/store.go"}}
    writers := []SQLRef{{Table: "rounding.sets", Operation: "INSERT", FuncName: "CreateSet", FilePath: "core/rounding/store.go"}}

    chunk := BuildTableChunk(table, readers, writers)

    assert.Contains(t, chunk, "Table: rounding.sets")
    assert.Contains(t, chunk, "CREATE TABLE rounding.sets")
    assert.Contains(t, chunk, "id SERIAL PRIMARY KEY")
    assert.Contains(t, chunk, "Read by:")
    assert.Contains(t, chunk, "ListSets")
    assert.Contains(t, chunk, "Written by:")
    assert.Contains(t, chunk, "CreateSet")
}
```

**Step 2: Implement chunk builder**

Output format (optimized for LLM consumption):
```
Table: rounding.sets
Schema: rounding
Created by migration: 000217_rounding_sets.up.sql

CREATE TABLE rounding.sets (
  id SERIAL PRIMARY KEY,
  project_id INTEGER NOT NULL,
  name TEXT NOT NULL
);

Read by:
  - ListSets (core/rounding/store.go) — SELECT
  - GetSetByID (core/rounding/store.go) — SELECT

Written by:
  - CreateSet (core/rounding/store.go) — INSERT
  - UpdateSet (core/rounding/store.go) — UPDATE
```

**Step 3: Run tests, commit**

```bash
git commit -m "feat: implement LLM-optimized table chunk builder"
```

---

### Task 4: Implement `index datastore` orchestrator and CLI command

**Files:**
- Create: `internal/datastore/indexer.go`
- Modify: `cmd/projectlens/main.go`
- Modify: `configs/index.yaml`

**What it does:** Orchestrates the full datastore indexing pipeline: find migrations → parse → scan Go files → build table records → create edges → build chunks. Runs independently from code indexing (reads symbols from DB).

**Step 1: Implement orchestrator**

```go
// IndexDatastore runs the full datastore indexing pipeline.
func IndexDatastore(ctx context.Context, db *storage.DB, repoPath string, cfg DatastoreConfig) error {
    // 1. Find migration files matching config paths
    // 2. Parse migrations → []TableDef (current schema state)
    // 3. Upsert datastore_tables records
    // 4. Scan Go source files for SQL → []SQLRef
    // 5. Match SQLRef.Table to datastore_tables → create edges (reads_table/writes_table)
    //    Edge: source_type="symbol", source_id=symbolID, target_type="datastore_table", target_id=tableID
    //    To find symbolID: look up SQLRef.FuncName in symbols table
    // 6. Build table chunks → upsert into chunks (source_type="migration")
    // 7. Log results
}

// DatastoreConfig controls datastore indexing paths.
type DatastoreConfig struct {
    MigrationPaths []string // glob patterns, e.g., "db/postgresql/metastore/migrations/**/*.up.sql"
    SQLScanPaths   []string // glob patterns for Go files to scan, e.g., "core/**", "service/**"
}
```

**Step 2: Add CLI command**

```go
// In cmd/projectlens/main.go
func newIndexDatastoreCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "index-datastore",
        Short: "Index database schemas and SQL queries",
        RunE: func(cmd *cobra.Command, args []string) error {
            // Load config, connect to DB, call IndexDatastore
        },
    }
}
```

Register in rootCmd.AddCommand.

**Step 3: Add config section**

Add to `configs/index.yaml`:
```yaml
datastore:
  engines:
    - name: postgres
      migration_paths:
        - "db/postgresql/metastore/migrations/*.up.sql"
  sql_scan_paths:
    - "core/**/*.go"
    - "service/**/*.go"
    - "pkg/**/*.go"
    - "pipelines/**/*.go"
```

Add to `internal/config/config.go`:
```go
type DatastoreConfig struct {
    Engines      []EngineConfig `yaml:"engines"`
    SQLScanPaths []string       `yaml:"sql_scan_paths"`
}

type EngineConfig struct {
    Name           string   `yaml:"name"`
    MigrationPaths []string `yaml:"migration_paths"`
}
```

**Step 4: Test on ingest repo**

```bash
export $(grep -v '^#' .env | xargs) && go run ./cmd/projectlens/ index-datastore \
  --repo /Users/hamed.zohrehvand/source/example-org/ingest/master \
  --db "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"
```

Expected output:
```
── Datastore indexing ──
Parsed 123 migration files → 28 tables across 7 schemas
Scanned 328 Go files → 1283 SQL references
Created 28 datastore_table records
Created N reads_table/writes_table edges
Created 28 table chunks
```

**Step 5: Commit**

```bash
git commit -m "feat: implement index-datastore CLI command and orchestrator"
```

---

### Task 5: Add `get_table_context` MCP tool

**Files:**
- Modify: `internal/mcpserver/handlers.go`
- Modify: `internal/mcpserver/tools.go`
- Modify: `internal/mcpserver/server.go`

**What it does:** New MCP tool that returns a table's schema, columns, which Go symbols read/write it, and the migration that created it.

**Step 1: Add tool schema**

```go
// In tools.go
{
    Name:        "get_table_context",
    Description: "Get database table schema, columns, and which Go code reads/writes it",
    InputSchema: mcp.ToolInputSchema{
        Type: "object",
        Properties: map[string]map[string]interface{}{
            "table_name": {"type": "string", "description": "Table name (e.g., 'rounding.sets' or 'sets')"},
        },
        Required: []string{"table_name"},
    },
}
```

**Step 2: Implement handler**

```go
func (s *Server) handleGetTableContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    tableName := req.GetString("table_name", "")
    // 1. Look up in datastore_tables (try with and without schema prefix)
    // 2. Get edges where target_type="datastore_table" AND target_id=tableID
    // 3. Resolve source symbols for reads/writes
    // 4. Format response with DDL, columns, readers, writers
}
```

**Step 3: Register tool, run tests, commit**

```bash
git commit -m "feat: add get_table_context MCP tool"
```

---

## Task Summary

| Task | What | Output |
|------|------|--------|
| 1 | Migration parser | Parse 123 migrations → 28 table definitions |
| 2 | Go SQL scanner | Scan 328 Go files → ~1,283 SQL references |
| 3 | Table chunk builder | LLM-optimized text chunks per table |
| 4 | Orchestrator + CLI | `index-datastore` command, config, end-to-end |
| 5 | MCP tool | `get_table_context` for querying table info |
