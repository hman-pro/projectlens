# ProjectLens Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a local, containerized Go code intelligence layer that gives Claude Code fast, ranked retrieval over symbols, packages, and dependency graphs from a large Go monorepo.

**Architecture:** Go indexer parses the target repo using `go/packages` + `go/callgraph`, stores symbols/chunks/edges/embeddings in Postgres + pgvector, and serves results to Claude Code via an MCP server over Streamable HTTP. OpenAI handles all embeddings and package summarization.

**Tech Stack:** Go 1.26, Postgres 16 + pgvector, `golang.org/x/tools/go/packages`, `golang.org/x/tools/go/callgraph`, `github.com/pgvector/pgvector-go`, `github.com/openai/openai-go`, `github.com/mark3labs/mcp-go` (or `github.com/modelcontextprotocol/go-sdk`), Docker Compose.

**Design document:** `docs/plans/2026-04-14-projectlens-design.md`

**Target repo:** `/Users/hamed.zohrehvand/source/example-org/ingest/master/` (module: `github.com/example-org/ingest`)

---

## Phase 0 — Project Foundation

### Task 1: Initialize Go Module and Project Skeleton

**Files:**
- Create: `go.mod`
- Create: `cmd/projectlens/main.go`
- Create: `cmd/projectlens-mcp/main.go`
- Create: `internal/census/census.go`
- Create: `internal/classifier/classifier.go`
- Create: `internal/storage/storage.go`
- Create: `internal/config/config.go`
- Create: `configs/index.yaml`

**Step 1: Initialize Go module**

```bash
cd /Users/hamed.zohrehvand/source/projectlens
go mod init github.com/hman-pro/projectlens
```

**Step 2: Create CLI entrypoint skeleton**

Create `cmd/projectlens/main.go` — a cobra-based CLI with subcommand stubs for: `census`, `bootstrap`, `reindex`, `status`, `inspect-symbol`, `inspect-package`, `query`.

```go
package main

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
)

func main() {
    root := &cobra.Command{
        Use:   "projectlens",
        Short: "Repository intelligence layer for Go codebases",
    }

    root.AddCommand(
        newCensusCmd(),
        newBootstrapCmd(),
        newReindexCmd(),
        newStatusCmd(),
        newInspectSymbolCmd(),
        newInspectPackageCmd(),
        newQueryCmd(),
    )

    root.PersistentFlags().String("config", "configs/index.yaml", "path to index config")
    root.PersistentFlags().String("db", "", "database URL (overrides config)")
    root.PersistentFlags().String("repo", "", "path to target repository (overrides config)")

    if err := root.Execute(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

func newCensusCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "census",
        Short: "Scan repository and classify files",
        RunE: func(cmd *cobra.Command, args []string) error {
            fmt.Println("census: not yet implemented")
            return nil
        },
    }
}

func newBootstrapCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "bootstrap",
        Short: "Run migrations and full index from scratch",
        RunE: func(cmd *cobra.Command, args []string) error {
            fmt.Println("bootstrap: not yet implemented")
            return nil
        },
    }
}

func newReindexCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "reindex",
        Short: "Incremental reindex of changed files",
        RunE: func(cmd *cobra.Command, args []string) error {
            fmt.Println("reindex: not yet implemented")
            return nil
        },
    }
    cmd.Flags().Bool("full", false, "force complete reindex")
    cmd.Flags().Bool("dry-run", false, "show what would change without writing")
    return cmd
}

func newStatusCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "status",
        Short: "Show index status and freshness",
        RunE: func(cmd *cobra.Command, args []string) error {
            fmt.Println("status: not yet implemented")
            return nil
        },
    }
}

func newInspectSymbolCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "inspect-symbol [name]",
        Short: "Look up a symbol and its relationships",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            fmt.Printf("inspect-symbol %s: not yet implemented\n", args[0])
            return nil
        },
    }
}

func newInspectPackageCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "inspect-package [name]",
        Short: "Show package summary and exported symbols",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            fmt.Printf("inspect-package %s: not yet implemented\n", args[0])
            return nil
        },
    }
}

func newQueryCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "query [text]",
        Short: "Run retrieval pipeline from terminal",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            fmt.Printf("query %q: not yet implemented\n", args[0])
            return nil
        },
    }
    cmd.Flags().String("mode", "auto", "retrieval mode: lexical|semantic|graph|auto")
    return cmd
}
```

**Step 3: Create MCP server entrypoint skeleton**

Create `cmd/projectlens-mcp/main.go`:

```go
package main

import (
    "fmt"
    "os"
)

func main() {
    fmt.Println("projectlens-mcp: not yet implemented")
    os.Exit(0)
}
```

**Step 4: Create config structure**

Create `internal/config/config.go`:

```go
package config

type Config struct {
    RepoPath    string       `yaml:"repo_path"`
    DatabaseURL string       `yaml:"database_url"`
    OpenAIKey   string       `yaml:"openai_api_key"`
    Index       IndexConfig  `yaml:"index"`
}

type IndexConfig struct {
    IncludePatterns []string `yaml:"include_patterns"`
    ExcludePatterns []string `yaml:"exclude_patterns"`
    GeneratedMarkers []string `yaml:"generated_markers"`
}
```

Create `configs/index.yaml`:

```yaml
repo_path: "/repo"
database_url: "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"

index:
  include_patterns:
    - "**/*.go"
  exclude_patterns:
    - "**/vendor/**"
    - "**/third_party/**"
    - "**/testdata/**"
    - "**/*_test.go"
    - "**/node_modules/**"
  generated_markers:
    - "Code generated"
    - "DO NOT EDIT"
    - "_generated.go"
    - "_gen.go"
    - ".pb.go"
    - "_grpc.go"
    - "_string.go"
    - "zz_generated"
```

**Step 5: Add dependencies and verify build**

```bash
go get github.com/spf13/cobra
go get gopkg.in/yaml.v3
go build ./cmd/projectlens/
go build ./cmd/projectlens-mcp/
```

**Step 6: Commit**

```bash
git add .
git commit -m "feat: initialize project skeleton with CLI stubs and config"
```

---

### Task 2: Docker Compose and Postgres + pgvector

**Files:**
- Create: `docker/Dockerfile`
- Create: `docker/docker-compose.yml`
- Create: `.env.example`
- Create: `migrations/001_initial_schema.up.sql`
- Create: `migrations/001_initial_schema.down.sql`

**Step 1: Create Dockerfile**

Create `docker/Dockerfile`:

```dockerfile
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /bin/projectlens ./cmd/projectlens/
RUN go build -o /bin/projectlens-mcp ./cmd/projectlens-mcp/

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/projectlens /bin/projectlens
COPY --from=builder /bin/projectlens-mcp /bin/projectlens-mcp
COPY migrations /migrations
COPY configs /configs

ENTRYPOINT ["/bin/projectlens"]
```

**Step 2: Create docker-compose.yml**

Create `docker/docker-compose.yml`:

```yaml
services:
  postgres:
    image: pgvector/pgvector:pg16
    volumes:
      - projectlens-data:/var/lib/postgresql/data
    ports:
      - "${PROJECTLENS_DB_PORT:-5433}:5432"
    environment:
      POSTGRES_DB: projectlens
      POSTGRES_USER: projectlens
      POSTGRES_PASSWORD: ${PROJECTLENS_DB_PASSWORD:-projectlens}
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U projectlens -d projectlens"]
      interval: 5s
      timeout: 5s
      retries: 5

  projectlens-mcp:
    build:
      context: ..
      dockerfile: docker/Dockerfile
    command: ["/bin/projectlens-mcp"]
    ports:
      - "${PROJECTLENS_MCP_PORT:-8484}:8484"
    volumes:
      - ${PROJECTLENS_REPO_PATH:?Set PROJECTLENS_REPO_PATH to target repo}:/repo:ro
    environment:
      DATABASE_URL: postgres://projectlens:${PROJECTLENS_DB_PASSWORD:-projectlens}@postgres:5432/projectlens?sslmode=disable
      OPENAI_API_KEY: ${OPENAI_API_KEY:?Set OPENAI_API_KEY}
      REPO_PATH: /repo
    depends_on:
      postgres:
        condition: service_healthy

  projectlens-indexer:
    build:
      context: ..
      dockerfile: docker/Dockerfile
    command: ["reindex"]
    profiles: ["index"]
    volumes:
      - ${PROJECTLENS_REPO_PATH:?Set PROJECTLENS_REPO_PATH to target repo}:/repo:ro
    environment:
      DATABASE_URL: postgres://projectlens:${PROJECTLENS_DB_PASSWORD:-projectlens}@postgres:5432/projectlens?sslmode=disable
      OPENAI_API_KEY: ${OPENAI_API_KEY:?Set OPENAI_API_KEY}
      REPO_PATH: /repo
    depends_on:
      postgres:
        condition: service_healthy

volumes:
  projectlens-data:
```

**Step 3: Create .env.example**

Create `.env.example`:

```bash
PROJECTLENS_REPO_PATH=/Users/you/source/your-repo
PROJECTLENS_DB_PASSWORD=projectlens
PROJECTLENS_DB_PORT=5433
PROJECTLENS_MCP_PORT=8484
OPENAI_API_KEY=sk-your-key-here
```

**Step 4: Create initial migration**

Create `migrations/001_initial_schema.up.sql`:

```sql
CREATE EXTENSION IF NOT EXISTS vector;

-- Files indexed from the target repository
CREATE TABLE files (
    id              BIGSERIAL PRIMARY KEY,
    path            TEXT NOT NULL,
    package_name    TEXT NOT NULL,
    checksum        TEXT NOT NULL,
    language        TEXT NOT NULL DEFAULT 'go',
    is_generated    BOOLEAN NOT NULL DEFAULT FALSE,
    is_test         BOOLEAN NOT NULL DEFAULT FALSE,
    line_count      INTEGER NOT NULL DEFAULT 0,
    heuristic_summary TEXT,
    commit_sha      TEXT NOT NULL,
    indexed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (path)
);

-- Symbols extracted from files
CREATE TABLE symbols (
    id              BIGSERIAL PRIMARY KEY,
    file_id         BIGINT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL,  -- func, method, struct, interface, const, var
    package_name    TEXT NOT NULL,
    receiver        TEXT,           -- for methods only
    signature       TEXT NOT NULL,
    doc_comment     TEXT,
    line_start      INTEGER NOT NULL,
    line_end        INTEGER NOT NULL,
    checksum        TEXT NOT NULL,
    indexed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_symbols_name ON symbols(name);
CREATE INDEX idx_symbols_package ON symbols(package_name);
CREATE INDEX idx_symbols_kind ON symbols(kind);
CREATE INDEX idx_symbols_file_id ON symbols(file_id);

-- Embeddable chunks (1:1 with symbols)
CREATE TABLE chunks (
    id              BIGSERIAL PRIMARY KEY,
    symbol_id       BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    content         TEXT NOT NULL,
    token_count     INTEGER NOT NULL DEFAULT 0,
    UNIQUE (symbol_id)
);

-- Vector embeddings via pgvector
CREATE TABLE embeddings (
    id              BIGSERIAL PRIMARY KEY,
    chunk_id        BIGINT NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    model_version   TEXT NOT NULL,
    embedding       vector(3072),
    UNIQUE (chunk_id, model_version)
);

-- HNSW index for fast cosine similarity
CREATE INDEX idx_embeddings_hnsw ON embeddings
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- LLM-generated package summaries
CREATE TABLE summaries (
    id              BIGSERIAL PRIMARY KEY,
    package_name    TEXT NOT NULL,
    summary_text    TEXT NOT NULL,
    model_version   TEXT NOT NULL,
    generated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (package_name)
);

-- Dependency graph edges between symbols
CREATE TABLE edges (
    id                  BIGSERIAL PRIMARY KEY,
    source_symbol_id    BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    target_symbol_id    BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    edge_type           TEXT NOT NULL,  -- calls, imports, implements, depends_on
    UNIQUE (source_symbol_id, target_symbol_id, edge_type)
);

CREATE INDEX idx_edges_source ON edges(source_symbol_id);
CREATE INDEX idx_edges_target ON edges(target_symbol_id);
CREATE INDEX idx_edges_type ON edges(edge_type);

-- Index run tracking for freshness
CREATE TABLE index_runs (
    id              BIGSERIAL PRIMARY KEY,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ,
    commit_sha      TEXT NOT NULL,
    files_processed INTEGER NOT NULL DEFAULT 0,
    symbols_extracted INTEGER NOT NULL DEFAULT 0,
    edges_created   INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'running'  -- running, completed, failed
);

-- Git ref tracking
CREATE TABLE git_refs (
    id              BIGSERIAL PRIMARY KEY,
    branch          TEXT NOT NULL,
    commit_sha      TEXT NOT NULL,
    indexed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (branch)
);
```

Create `migrations/001_initial_schema.down.sql`:

```sql
DROP TABLE IF EXISTS git_refs;
DROP TABLE IF EXISTS index_runs;
DROP TABLE IF EXISTS edges;
DROP TABLE IF EXISTS summaries;
DROP TABLE IF EXISTS embeddings;
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS symbols;
DROP TABLE IF EXISTS files;
DROP EXTENSION IF EXISTS vector;
```

**Step 5: Verify Postgres starts and migration runs**

```bash
cd /Users/hamed.zohrehvand/source/projectlens
cp .env.example .env  # edit with real values
cd docker && docker compose up -d postgres
# wait for healthy
docker compose exec postgres psql -U projectlens -d projectlens -f /dev/stdin < ../migrations/001_initial_schema.up.sql
docker compose exec postgres psql -U projectlens -d projectlens -c "\dt"
```

Expected: all 8 tables listed.

**Step 6: Commit**

```bash
git add docker/ migrations/ .env.example .gitignore
git commit -m "feat: add Docker Compose setup with Postgres + pgvector and initial schema"
```

---

### Task 3: Storage Layer (Postgres Client)

**Files:**
- Create: `internal/storage/db.go`
- Create: `internal/storage/files.go`
- Create: `internal/storage/symbols.go`
- Create: `internal/storage/chunks.go`
- Create: `internal/storage/embeddings.go`
- Create: `internal/storage/summaries.go`
- Create: `internal/storage/edges.go`
- Create: `internal/storage/indexruns.go`
- Test: `internal/storage/db_test.go`

**Step 1: Write integration test for database connection**

Create `internal/storage/db_test.go` that verifies connecting to a test database, running migrations, and basic CRUD for the `files` table.

```go
//go:build integration

package storage_test

import (
    "context"
    "testing"

    "github.com/hman-pro/projectlens/internal/storage"
)

func TestConnect(t *testing.T) {
    db, err := storage.Connect(context.Background(), testDatabaseURL())
    if err != nil {
        t.Fatalf("connect: %v", err)
    }
    defer db.Close()

    if err := db.Ping(context.Background()); err != nil {
        t.Fatalf("ping: %v", err)
    }
}
```

**Step 2: Run test to verify it fails**

```bash
go test -tags=integration ./internal/storage/ -run TestConnect -v
```

Expected: FAIL — `storage.Connect` not defined.

**Step 3: Implement storage.DB with pgx**

Create `internal/storage/db.go` using `github.com/jackc/pgx/v5/pgxpool`. Implement `Connect`, `Close`, `Ping`, and `Migrate` (reads and runs SQL files from migrations directory).

**Step 4: Implement table-specific CRUD**

Each file (`files.go`, `symbols.go`, etc.) implements:
- `Insert` / `UpsertBatch` for the table
- `GetByID`, `GetByName` (where applicable)
- `DeleteByFileID` (for cascading cleanup on reindex)

Key methods:
- `files.go`: `UpsertFile`, `GetFileByPath`, `ListFiles`, `DeleteStaleFiles`
- `symbols.go`: `InsertSymbols`, `GetSymbolByName`, `GetSymbolsByFileID`, `GetSymbolsByPackage`
- `chunks.go`: `UpsertChunk`, `GetChunkBySymbolID`
- `embeddings.go`: `UpsertEmbedding`, `SemanticSearch(vector, topK)`
- `summaries.go`: `UpsertSummary`, `GetSummaryByPackage`
- `edges.go`: `InsertEdges`, `GetCallers`, `GetCallees`, `GetImplementors`
- `indexruns.go`: `StartRun`, `CompleteRun`, `FailRun`, `GetLatestRun`

**Step 5: Run integration tests**

```bash
go test -tags=integration ./internal/storage/ -v
```

Expected: all PASS.

**Step 6: Commit**

```bash
git add internal/storage/
git commit -m "feat: implement storage layer with pgx for all tables"
```

---

## Phase 1 — Census and Classification

### Task 4: File Classifier

**Files:**
- Create: `internal/classifier/classifier.go`
- Test: `internal/classifier/classifier_test.go`

**Step 1: Write tests for classification rules**

Test cases:
- `service/web/handler.go` → handwritten, production
- `service/web/handler_test.go` → handwritten, test
- `pkg/models/user.pb.go` → generated
- `vendor/github.com/foo/bar.go` → excluded
- A file containing `// Code generated` → generated
- A file in `testdata/` → excluded

```go
package classifier_test

import (
    "testing"

    "github.com/hman-pro/projectlens/internal/classifier"
)

func TestClassifyPath(t *testing.T) {
    cfg := classifier.DefaultConfig()

    tests := []struct {
        path     string
        want     classifier.Classification
    }{
        {"service/web/handler.go", classifier.Classification{Language: "go", IsGenerated: false, IsTest: false, Excluded: false}},
        {"service/web/handler_test.go", classifier.Classification{Language: "go", IsGenerated: false, IsTest: true, Excluded: false}},
        {"pkg/models/user.pb.go", classifier.Classification{Language: "go", IsGenerated: true, IsTest: false, Excluded: false}},
        {"vendor/github.com/foo/bar.go", classifier.Classification{Language: "go", IsGenerated: false, IsTest: false, Excluded: true}},
    }

    for _, tt := range tests {
        t.Run(tt.path, func(t *testing.T) {
            got := classifier.ClassifyPath(tt.path, cfg)
            if got != tt.want {
                t.Errorf("ClassifyPath(%q) = %+v, want %+v", tt.path, got, tt.want)
            }
        })
    }
}

func TestClassifyContent(t *testing.T) {
    cfg := classifier.DefaultConfig()

    content := "// Code generated by protoc-gen-go. DO NOT EDIT.\npackage models\n"
    got := classifier.ClassifyContent(content, cfg)
    if !got.IsGenerated {
        t.Error("expected generated=true for content with Code generated marker")
    }
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/classifier/ -v
```

**Step 3: Implement classifier**

```go
package classifier

import (
    "path/filepath"
    "strings"
)

type Classification struct {
    Language    string
    IsGenerated bool
    IsTest      bool
    Excluded    bool
}

type Config struct {
    ExcludePatterns  []string
    GeneratedMarkers []string
}

func DefaultConfig() Config {
    return Config{
        ExcludePatterns: []string{"vendor/", "third_party/", "testdata/", "node_modules/"},
        GeneratedMarkers: []string{
            "Code generated", "DO NOT EDIT",
            "_generated.go", "_gen.go", ".pb.go",
            "_grpc.go", "_string.go", "zz_generated",
        },
    }
}

func ClassifyPath(path string, cfg Config) Classification {
    c := Classification{Language: languageFromExt(filepath.Ext(path))}

    for _, pat := range cfg.ExcludePatterns {
        if strings.Contains(path, pat) {
            c.Excluded = true
            return c
        }
    }

    if strings.HasSuffix(path, "_test.go") {
        c.IsTest = true
    }

    for _, marker := range cfg.GeneratedMarkers {
        if strings.Contains(path, marker) {
            c.IsGenerated = true
            break
        }
    }

    return c
}

func ClassifyContent(content string, cfg Config) Classification {
    c := Classification{Language: "go"}
    firstLines := content
    if len(firstLines) > 512 {
        firstLines = firstLines[:512]
    }
    for _, marker := range cfg.GeneratedMarkers {
        if strings.Contains(firstLines, marker) {
            c.IsGenerated = true
            break
        }
    }
    return c
}

func languageFromExt(ext string) string {
    switch ext {
    case ".go":
        return "go"
    default:
        return "unknown"
    }
}
```

**Step 4: Run tests**

```bash
go test ./internal/classifier/ -v
```

Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/classifier/
git commit -m "feat: implement file classifier with path and content rules"
```

---

### Task 5: Census Command

**Files:**
- Create: `internal/census/census.go`
- Test: `internal/census/census_test.go`
- Modify: `cmd/projectlens/main.go` — wire up census command

**Step 1: Write test for census walker**

```go
package census_test

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/hman-pro/projectlens/internal/census"
    "github.com/hman-pro/projectlens/internal/classifier"
)

func TestWalk(t *testing.T) {
    // Create a temp dir with some Go files
    dir := t.TempDir()
    os.MkdirAll(filepath.Join(dir, "pkg", "foo"), 0o755)
    os.WriteFile(filepath.Join(dir, "pkg", "foo", "bar.go"), []byte("package foo\n\nfunc Bar() {}\n"), 0o644)
    os.WriteFile(filepath.Join(dir, "pkg", "foo", "bar_test.go"), []byte("package foo\n"), 0o644)
    os.WriteFile(filepath.Join(dir, "pkg", "foo", "gen.pb.go"), []byte("// Code generated\npackage foo\n"), 0o644)

    result, err := census.Walk(dir, classifier.DefaultConfig())
    if err != nil {
        t.Fatal(err)
    }

    if result.Total != 3 {
        t.Errorf("total = %d, want 3", result.Total)
    }
    if result.Handwritten != 1 {
        t.Errorf("handwritten = %d, want 1", result.Handwritten)
    }
    if result.Test != 1 {
        t.Errorf("test = %d, want 1", result.Test)
    }
    if result.Generated != 1 {
        t.Errorf("generated = %d, want 1", result.Generated)
    }
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/census/ -v
```

**Step 3: Implement census walker**

```go
package census

import (
    "crypto/sha256"
    "encoding/hex"
    "io/fs"
    "os"
    "path/filepath"
    "strings"

    "github.com/hman-pro/projectlens/internal/classifier"
)

type FileEntry struct {
    Path           string
    RelPath        string
    PackageName    string
    Checksum       string
    Classification classifier.Classification
    LineCount      int
}

type Result struct {
    Total       int
    Handwritten int
    Test        int
    Generated   int
    Excluded    int
    Files       []FileEntry
}

func Walk(repoPath string, cfg classifier.Config) (*Result, error) {
    var result Result

    err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return err
        }
        if d.IsDir() {
            return nil
        }
        if filepath.Ext(path) != ".go" {
            return nil
        }

        relPath, _ := filepath.Rel(repoPath, path)
        cls := classifier.ClassifyPath(relPath, cfg)

        if cls.Excluded {
            result.Excluded++
            result.Total++
            return nil
        }

        content, err := os.ReadFile(path)
        if err != nil {
            return err
        }

        contentCls := classifier.ClassifyContent(string(content), cfg)
        if contentCls.IsGenerated {
            cls.IsGenerated = true
        }

        hash := sha256.Sum256(content)
        lineCount := strings.Count(string(content), "\n")

        entry := FileEntry{
            Path:           path,
            RelPath:        relPath,
            Checksum:       hex.EncodeToString(hash[:]),
            Classification: cls,
            LineCount:      lineCount,
        }

        result.Files = append(result.Files, entry)
        result.Total++

        switch {
        case cls.IsGenerated:
            result.Generated++
        case cls.IsTest:
            result.Test++
        default:
            result.Handwritten++
        }

        return nil
    })

    return &result, err
}
```

**Step 4: Run tests**

```bash
go test ./internal/census/ -v
```

Expected: PASS.

**Step 5: Wire census command into CLI**

Update `cmd/projectlens/main.go` to call `census.Walk` and print a summary table.

**Step 6: Test against real repo**

```bash
go run ./cmd/projectlens/ census --repo /Users/hamed.zohrehvand/source/example-org/ingest/master/
```

Expected: prints classification counts (~2960 handwritten, ~1190 test, ~27 generated).

**Step 7: Commit**

```bash
git add internal/census/ cmd/projectlens/
git commit -m "feat: implement census command with file walker and classification"
```

---

## Phase 2 — Go Parsing and Symbol Extraction

### Task 6: Go Parser (go/packages Wrapper)

**Files:**
- Create: `internal/parser/parser.go`
- Create: `internal/parser/parser_test.go`

**Step 1: Write test for parsing a simple Go package**

Create a test that uses a temp directory with a small Go module containing a function, struct, and interface. Verify that `parser.Parse` returns the expected symbols.

Test cases:
- Function with doc comment → extracted with name, signature, line range, doc
- Method on a struct → extracted with receiver
- Struct definition → extracted
- Interface definition → extracted
- Unexported function → still extracted (we index everything, rank later)

**Step 2: Run test to verify it fails**

```bash
go test ./internal/parser/ -v
```

**Step 3: Implement parser**

The parser wraps `golang.org/x/tools/go/packages` with `packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps` load mode.

Walk the AST of each file, extract:
- `*ast.FuncDecl` → function or method (check `Recv` for methods)
- `*ast.GenDecl` with `token.TYPE` → struct or interface
- `*ast.GenDecl` with `token.CONST` or `token.VAR` → constants, variables

For each symbol, capture: name, kind, package, receiver, signature (printed from AST), doc comment, line start/end, and the source body text.

```bash
go get golang.org/x/tools/go/packages
```

**Step 4: Run tests**

```bash
go test ./internal/parser/ -v
```

**Step 5: Commit**

```bash
git add internal/parser/
git commit -m "feat: implement Go parser with symbol extraction via go/packages"
```

---

### Task 7: Call Graph and Edge Construction

**Files:**
- Create: `internal/graph/graph.go`
- Create: `internal/graph/graph_test.go`

**Step 1: Write test for call graph extraction**

Create a temp Go module where `funcA` calls `funcB`, `StructX` implements `InterfaceY`. Verify that `graph.Build` returns the correct `calls` and `implements` edges.

**Step 2: Run test to verify it fails**

```bash
go test ./internal/graph/ -v
```

**Step 3: Implement graph builder**

```bash
go get golang.org/x/tools/go/callgraph
go get golang.org/x/tools/go/callgraph/cha
go get golang.org/x/tools/go/ssa
go get golang.org/x/tools/go/ssa/ssautil
```

Build SSA from loaded packages, run CHA algorithm. Convert callgraph edges to our internal `Edge` type. Additionally, walk `types.Info` to find interface satisfaction (`types.Implements`).

Extract:
- `calls` edges from callgraph
- `implements` edges from type checker
- `imports` edges from package import paths
- `depends_on` edges from transitive package dependencies

**Step 4: Run tests**

```bash
go test ./internal/graph/ -v
```

**Step 5: Test on a small subset of the real repo**

```bash
go test ./internal/graph/ -v -run TestRealRepo -tags=integration
```

Verify it can handle a single real package from the target repo without crashing.

**Step 6: Commit**

```bash
git add internal/graph/
git commit -m "feat: implement call graph and edge construction via CHA"
```

---

### Task 8: Chunking

**Files:**
- Create: `internal/chunks/chunks.go`
- Create: `internal/chunks/chunks_test.go`

**Step 1: Write test for chunk generation**

Given a parsed symbol (function with doc comment, body, package context), verify that `chunks.Create` produces a chunk with the expected content format:

```
// Package foo provides utilities for bar.
//
// FuncName does something specific.
func FuncName(x int) (string, error) {
    // body here
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/chunks/ -v
```

**Step 3: Implement chunking**

Each chunk = package doc (first line) + symbol doc comment + symbol signature + symbol body. Include line range metadata. Count tokens (approximate: split on whitespace).

**Step 4: Run tests**

```bash
go test ./internal/chunks/ -v
```

**Step 5: Commit**

```bash
git add internal/chunks/
git commit -m "feat: implement symbol-based chunking"
```

---

## Phase 3 — Summaries and Embeddings

### Task 9: Heuristic File Summaries

**Files:**
- Create: `internal/summaries/heuristic.go`
- Create: `internal/summaries/heuristic_test.go`

**Step 1: Write test**

Given a file's AST (package doc, exported function signatures, doc comments), verify that `summaries.HeuristicFileSummary` produces a concise text block.

**Step 2: Implement**

Extract: package-level doc comment, all exported symbol signatures with their doc comments. Concatenate with clear structure. Cap at ~500 tokens.

**Step 3: Run tests and commit**

```bash
go test ./internal/summaries/ -v
git add internal/summaries/
git commit -m "feat: implement heuristic file summaries"
```

---

### Task 10: LLM Package Summaries (OpenAI)

**Files:**
- Create: `internal/openai/client.go`
- Create: `internal/summaries/package_summary.go`
- Test: `internal/summaries/package_summary_test.go`

**Step 1: Create OpenAI client wrapper**

Create `internal/openai/client.go` that wraps `github.com/openai/openai-go` with:
- `GeneratePackageSummary(packageName string, exportedSymbols []string) (string, error)`
- `EmbedBatch(texts []string) ([][]float32, error)`

```bash
go get github.com/openai/openai-go
```

**Step 2: Write test for package summary generation**

Create an integration test (gated behind `//go:build integration`) that:
- Calls `GeneratePackageSummary` with a small set of symbol signatures
- Verifies the response is non-empty and mentions the package's purpose

**Step 3: Implement package summary prompt**

The prompt template for `gpt-4o-mini`:

```
You are a Go package documentation expert. Given the following exported symbols from a Go package, write a 2-4 sentence summary of what this package does, when a developer would use it, and its main responsibilities.

Package: {package_name}

Exported symbols:
{symbol_signatures}

Write a concise summary focused on purpose and usage, not implementation details.
```

**Step 4: Run integration test**

```bash
OPENAI_API_KEY=... go test -tags=integration ./internal/summaries/ -run TestPackageSummary -v
```

**Step 5: Commit**

```bash
git add internal/openai/ internal/summaries/
git commit -m "feat: implement OpenAI client and LLM package summaries"
```

---

### Task 11: Embedding Pipeline

**Files:**
- Modify: `internal/openai/client.go` — add `EmbedBatch`
- Create: `internal/embeddings/embeddings.go`
- Test: `internal/embeddings/embeddings_test.go`

**Step 1: Write integration test for embedding**

Verify that `EmbedBatch(["func Foo() string"])` returns a vector of dimension 3072.

**Step 2: Implement EmbedBatch in OpenAI client**

Use `text-embedding-3-large`, batch up to 100 texts per API call. Return `[][]float32`.

**Step 3: Write embedding pipeline**

`internal/embeddings/embeddings.go`: given a list of chunks, embed them in batches, return chunk-id to vector mapping.

**Step 4: Run integration test**

```bash
OPENAI_API_KEY=... go test -tags=integration ./internal/embeddings/ -v
```

**Step 5: Commit**

```bash
git add internal/openai/ internal/embeddings/
git commit -m "feat: implement embedding pipeline with OpenAI text-embedding-3-large"
```

---

## Phase 4 — Indexer Orchestration

### Task 12: Full Indexer Pipeline

**Files:**
- Create: `internal/indexer/indexer.go`
- Test: `internal/indexer/indexer_test.go`
- Modify: `cmd/projectlens/main.go` — wire `bootstrap` and `reindex` commands

**Step 1: Write integration test**

Test the full pipeline on a small temp Go module:
1. Census → finds files
2. Parse → extracts symbols
3. Chunk → creates chunks
4. Graph → builds edges
5. Summarize → generates heuristic file summaries
6. Embed → creates embeddings (mock or real OpenAI)
7. Store → persists everything to Postgres

Verify: database has expected rows in all tables.

**Step 2: Implement indexer orchestrator**

`internal/indexer/indexer.go`:

```go
type Indexer struct {
    db       *storage.DB
    openai   *openai.Client
    config   *config.Config
}

func (idx *Indexer) Run(ctx context.Context, full bool) error {
    // 1. Start index run
    // 2. Census: walk repo, classify, compute work list
    // 3. Parse: go/packages on work list
    // 4. Chunk: create chunks from symbols
    // 5. Graph: build edges
    // 6. Summarize: heuristic for files, LLM for packages
    // 7. Embed: batch embed chunks
    // 8. Store: persist all to database
    // 9. Complete index run
}
```

For incremental mode: compare checksums with stored files, only reprocess changed files. Clean up deleted files.

**Step 3: Wire bootstrap and reindex commands**

`bootstrap` = run migrations + full index.
`reindex` = incremental by default, `--full` for complete.

**Step 4: Test against real repo**

```bash
go run ./cmd/projectlens/ bootstrap --repo /Users/hamed.zohrehvand/source/example-org/ingest/master/ --db "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"
```

Monitor: should process ~2960 handwritten Go files, extract symbols, build edges, generate ~100 package summaries, embed all chunks. Log progress.

**Step 5: Verify data**

```bash
go run ./cmd/projectlens/ status --db "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"
```

Expected: shows file count, symbol count, edge count, last index time.

**Step 6: Commit**

```bash
git add internal/indexer/ cmd/projectlens/
git commit -m "feat: implement full indexer pipeline with bootstrap and reindex commands"
```

---

## Phase 5 — Retrieval Pipeline

### Task 13: Lexical Retrieval

**Files:**
- Create: `internal/retrieval/lexical.go`
- Test: `internal/retrieval/lexical_test.go`

**Step 1: Write integration test**

After indexing, search for a known symbol name. Verify it returns the correct file and line range.

**Step 2: Implement**

Lexical search queries:
- Exact match on `symbols.name` (case-insensitive)
- Prefix match on `symbols.name`
- Exact match on `files.path` containing the search term
- Exact match on `symbols.package_name`

Return ranked results with exact matches first, then prefix matches.

**Step 3: Test and commit**

```bash
go test -tags=integration ./internal/retrieval/ -run TestLexical -v
git add internal/retrieval/
git commit -m "feat: implement lexical retrieval for symbols and packages"
```

---

### Task 14: Semantic Retrieval

**Files:**
- Create: `internal/retrieval/semantic.go`
- Test: `internal/retrieval/semantic_test.go`

**Step 1: Write integration test**

Embed a natural language query, run cosine similarity search, verify relevant symbols appear in top-k.

**Step 2: Implement**

1. Embed the query text via OpenAI
2. Run pgvector cosine similarity: `SELECT ... ORDER BY embedding <=> $1 LIMIT $2`
3. Join with symbols and files to return full context

**Step 3: Test and commit**

```bash
OPENAI_API_KEY=... go test -tags=integration ./internal/retrieval/ -run TestSemantic -v
git add internal/retrieval/
git commit -m "feat: implement semantic retrieval via pgvector cosine similarity"
```

---

### Task 15: Graph Retrieval

**Files:**
- Create: `internal/retrieval/graph.go`
- Test: `internal/retrieval/graph_test.go`

**Step 1: Write integration test**

Given a known symbol, retrieve its callers and callees. Verify the graph traversal returns expected neighbors.

**Step 2: Implement**

BFS from seed symbol over `edges` table:
- `GetCallers(symbolID, maxDepth)` — follow `calls` edges in reverse
- `GetCallees(symbolID, maxDepth)` — follow `calls` edges forward
- `GetImplementors(symbolID)` — follow `implements` edges
- `GetPackageDeps(packageName)` — follow `depends_on` edges

Return results with hop distance for ranking.

**Step 3: Test and commit**

```bash
go test -tags=integration ./internal/retrieval/ -run TestGraph -v
git add internal/retrieval/
git commit -m "feat: implement graph retrieval with BFS traversal"
```

---

### Task 16: Query Router and Ranking

**Files:**
- Create: `internal/retrieval/router.go`
- Create: `internal/rerank/rerank.go`
- Test: `internal/retrieval/router_test.go`
- Test: `internal/rerank/rerank_test.go`

**Step 1: Write tests for query classification**

Test that:
- `"ReserveInventory"` → `exact_symbol`
- `"how does inventory reservation work"` → `implementation_search`
- `"what does pkg/temporal do"` → `package_overview`
- `"what calls ProcessPayment"` → `dependency_trace`

**Step 2: Implement query classifier**

Heuristic rules:
- Single word matching Go identifier pattern (CamelCase) → `exact_symbol`
- Contains "what calls" / "callers of" / "depends on" → `dependency_trace`
- Contains "package" or matches a known package path → `package_overview`
- Everything else → `implementation_search`

**Step 3: Implement ranking**

`internal/rerank/rerank.go`:

```go
type ScoredResult struct {
    Symbol      storage.Symbol
    File        storage.File
    Score       float64
    Source      string  // "lexical", "semantic", "graph"
    Relationship string // "caller", "callee", "implements", etc.
}

func Rank(results []ScoredResult) []ScoredResult {
    // Apply scoring factors:
    // - exact name match: +10.0
    // - same package: +2.0
    // - handwritten production: +0.0 (baseline)
    // - test file: -3.0 (unless test query)
    // - generated: -5.0
    // - semantic score: +score * 5.0
    // - graph distance 1: +3.0, distance 2: +1.0
    // Sort descending by total score
}
```

**Step 4: Implement router**

`internal/retrieval/router.go`: orchestrates classification → parallel retrieval → merge → rank → return top-k.

**Step 5: Run tests and commit**

```bash
go test ./internal/retrieval/ ./internal/rerank/ -v
git add internal/retrieval/ internal/rerank/
git commit -m "feat: implement query router with classification and ranking"
```

---

## Phase 6 — MCP Server

### Task 17: MCP Server with 5 Tools

**Files:**
- Create: `internal/mcpserver/server.go`
- Create: `internal/mcpserver/tools.go`
- Create: `internal/mcpserver/handlers.go`
- Test: `internal/mcpserver/server_test.go`
- Modify: `cmd/projectlens-mcp/main.go` — wire server

**Step 1: Add MCP dependency**

```bash
go get github.com/mark3labs/mcp-go
```

**Step 2: Write test for MCP tool registration**

Verify that the server registers 5 tools with correct names and schemas.

**Step 3: Implement MCP server**

`internal/mcpserver/server.go`: create an MCP server using `mcp-go` with Streamable HTTP transport on port 8484.

`internal/mcpserver/tools.go`: define tool schemas:

```go
var FindSymbolTool = mcp.Tool{
    Name:        "find_symbol",
    Description: "Find a Go symbol by name. Returns matching symbols with file path, line range, signature, and package.",
    InputSchema: mcp.ToolInputSchema{
        Type: "object",
        Properties: map[string]map[string]interface{}{
            "name":  {"type": "string", "description": "Symbol name (exact or fuzzy)"},
            "kind":  {"type": "string", "description": "Optional filter: func, method, struct, interface, const, var", "enum": []string{"func", "method", "struct", "interface", "const", "var"}},
        },
        Required: []string{"name"},
    },
}
// ... similar for other 4 tools
```

`internal/mcpserver/handlers.go`: implement handlers that call the retrieval pipeline:

- `handleFindSymbol` → lexical search, semantic fallback
- `handleSearchGoContext` → semantic + lexical, merged
- `handleGetSymbolContext` → graph traversal from symbol
- `handleGetPackageSummary` → direct lookup
- `handleIndexStatus` → latest index run

**Step 4: Wire MCP server entrypoint**

Update `cmd/projectlens-mcp/main.go` to load config, connect to DB, start MCP server.

**Step 5: Integration test**

Start the server, send MCP tool calls via HTTP, verify responses.

```bash
go test -tags=integration ./internal/mcpserver/ -v
```

**Step 6: Manual test with curl**

```bash
# Start server
go run ./cmd/projectlens-mcp/ &

# Test find_symbol
curl -X POST http://localhost:8484/mcp \
  -H "Content-Type: application/json" \
  -d '{"method": "tools/call", "params": {"name": "find_symbol", "arguments": {"name": "ReserveInventory"}}}'
```

**Step 7: Commit**

```bash
git add internal/mcpserver/ cmd/projectlens-mcp/
git commit -m "feat: implement MCP server with 5 tools over Streamable HTTP"
```

---

## Phase 7 — CLI Completion

### Task 18: Remaining CLI Commands

**Files:**
- Modify: `cmd/projectlens/main.go` — wire remaining commands

**Step 1: Implement `status` command**

Query `index_runs` and `git_refs`, display: last index time, commit SHA, file/symbol/edge counts, staleness warning.

**Step 2: Implement `inspect-symbol` command**

Call `GetSymbolByName`, display symbol details + callers + callees + implementors.

**Step 3: Implement `inspect-package` command**

Call `GetSummaryByPackage` + `GetSymbolsByPackage`, display summary + exported symbols + deps.

**Step 4: Implement `query` command**

Call the retrieval router, display ranked results with file paths and line ranges.

**Step 5: Test all commands against indexed data**

```bash
go run ./cmd/projectlens/ status
go run ./cmd/projectlens/ inspect-symbol ReserveInventory
go run ./cmd/projectlens/ inspect-package "service/web"
go run ./cmd/projectlens/ query "how does inventory reservation work"
```

**Step 6: Commit**

```bash
git add cmd/projectlens/
git commit -m "feat: implement status, inspect-symbol, inspect-package, and query CLI commands"
```

---

## Phase 8 — Claude Code Integration

### Task 19: Claude Code MCP Configuration

**Files:**
- Create: `claude/mcp-config.json`

**Step 1: Create MCP config for the target repo**

```json
{
  "mcpServers": {
    "projectlens": {
      "type": "streamable-http",
      "url": "http://localhost:8484/mcp"
    }
  }
}
```

**Step 2: Test connection**

Start Docker Compose, verify Claude Code discovers the tools.

**Step 3: Commit**

```bash
git add claude/
git commit -m "feat: add Claude Code MCP configuration"
```

---

### Task 20: CLAUDE.md ProjectLens Section

**Files:**
- Create: `claude/CLAUDE.md.snippet`

**Step 1: Write the CLAUDE.md section**

This is a snippet to be added to the target repo's CLAUDE.md:

```markdown
## ProjectLens — Repository Intelligence Layer

This repo has a local intelligence layer called ProjectLens running at localhost:8484.

### When to use ProjectLens
Use ProjectLens BEFORE opening files to answer implementation questions:
- "Where is X implemented?" → use `find_symbol`
- "How does X work?" → use `search_go_context`
- "What calls X? What does X depend on?" → use `get_symbol_context`
- "What does package Y do?" → use `get_package_summary`

### Preferred workflow
1. Call ProjectLens first to locate relevant code
2. Review the returned symbols, summaries, and relationships
3. Only open files to verify specific details — do not explore broadly
4. If results seem stale, check `index_status`

### What NOT to use ProjectLens for
- Reading file contents (use Read tool)
- Editing code (use Edit tool)
- Running commands (use Bash tool)
```

**Step 2: Commit**

```bash
git add claude/
git commit -m "feat: add CLAUDE.md snippet for ProjectLens guidance"
```

---

### Task 21: Claude Code Skills

**Files:**
- Create: `claude/skills/trace-go-flow/SKILL.md`
- Create: `claude/skills/debug-go-test/SKILL.md`
- Create: `claude/skills/explain-go-impact/SKILL.md`

**Step 1: Create trace-go-flow skill**

```markdown
---
name: trace-go-flow
description: Locate the implementation path for a behavior or symbol in the Go codebase
---

## Steps

1. Use `find_symbol` to locate the target symbol by name
   - If no exact match, use `search_go_context` with a natural language description
2. Use `get_symbol_context` on the found symbol to get callers, callees, and interface implementations
3. Use `get_package_summary` for the symbol's package to understand its role
4. Open only the top 1-2 files to verify the implementation
5. Summarize the implementation flow: entry point → key steps → exit point
```

**Step 2: Create debug-go-test skill**

```markdown
---
name: debug-go-test
description: Investigate failing or relevant tests and map them to production code
---

## Steps

1. Use `search_go_context` with the test name or behavior description
2. Use `get_symbol_context` on the production symbol under test to understand dependencies
3. Use `get_package_summary` for the test's package
4. Open the test file and the production code file
5. Explain: what the test expects, what the production code does, where they diverge
```

**Step 3: Create explain-go-impact skill**

```markdown
---
name: explain-go-impact
description: Estimate what breaks if a symbol or interface is changed
---

## Steps

1. Use `find_symbol` to locate the target symbol
2. Use `get_symbol_context` to get all callers and interface implementors
3. Use `get_package_summary` for each affected package (up to 5)
4. Summarize:
   - Direct callers that would break
   - Interface implementors that would need updating
   - Packages that depend on this symbol's package
   - Confidence level (high/medium/low based on graph completeness)
```

**Step 4: Commit**

```bash
git add claude/skills/
git commit -m "feat: add three Claude Code skills for ProjectLens workflows"
```

---

## Phase 9 — End-to-End Validation

### Task 22: End-to-End Test

**Step 1: Start everything**

```bash
cd /Users/hamed.zohrehvand/source/projectlens/docker
docker compose up -d
```

**Step 2: Run bootstrap**

```bash
docker compose --profile index run projectlens-indexer bootstrap
```

**Step 3: Verify index status**

```bash
go run ./cmd/projectlens/ status
```

Expected: ~2960 files, thousands of symbols, edges populated.

**Step 4: Test each MCP tool via CLI**

```bash
go run ./cmd/projectlens/ query "ReserveInventory"
go run ./cmd/projectlens/ query "how does order fulfillment work"
go run ./cmd/projectlens/ inspect-symbol ProcessPayment
go run ./cmd/projectlens/ inspect-package "service/graphql"
```

**Step 5: Test via Claude Code**

Open Claude Code in the target repo with ProjectLens MCP configured. Ask:
- "Where is inventory reservation implemented?"
- "What calls ProcessPayment?"
- "What does the pkg/temporal package do?"

Verify Claude uses the MCP tools and answers without broad file exploration.

**Step 6: Document any issues found and create follow-up tasks**

**Step 7: Commit any fixes**

```bash
git add .
git commit -m "fix: address issues found during end-to-end validation"
```

---

## Summary

| Phase | Tasks | What it delivers |
|-------|-------|-----------------|
| 0 — Foundation | 1-3 | Go project, Docker Compose, Postgres schema, storage layer |
| 1 — Census | 4-5 | File classifier, census command |
| 2 — Parsing | 6-8 | Symbol extraction, call graph, chunking |
| 3 — Summaries | 9-11 | Heuristic file summaries, LLM package summaries, embeddings |
| 4 — Indexer | 12 | Full indexer pipeline with bootstrap/reindex |
| 5 — Retrieval | 13-16 | Lexical, semantic, graph retrieval + ranking |
| 6 — MCP | 17 | MCP server with 5 tools |
| 7 — CLI | 18 | All 7 CLI commands working |
| 8 — Claude | 19-21 | MCP config, CLAUDE.md, 3 skills |
| 9 — Validation | 22 | End-to-end test on real repo |

**Total: 22 tasks across 10 phases.**

Dependencies: Tasks are sequential within phases. Phases 0-1 must complete before Phase 2. Phase 4 requires Phases 2-3. Phase 5 requires Phase 4. Phase 6 requires Phase 5. Phase 8 requires Phase 6.
