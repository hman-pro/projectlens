# Multi-Project Isolation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add multi-project isolation to ProjectLens using one Postgres storage schema per project, one MCP process serving `/{slug}/mcp` endpoints, and explicit `--project` CLI flags — while keeping the existing single-project `--repo` path working against `public`.

**Architecture:** A new `internal/projects` package parses a YAML project registry, validates slugs/storage schemas, and resolves per-project runtimes (storage schema, repo path, project config, pgxpool with `search_path` pinned via `AfterConnect`). `storage` gains schema-aware connect + migrate primitives that quote identifiers with `pgx.Identifier.Sanitize()` and assert schema existence at connection setup. CLI subcommands accept `--project` and route through the registry; legacy `--repo`-only invocations behave as the implicit `public` project. The MCP HTTP server mounts a separate Streamable-HTTP MCP server (with its own session manager) under `/{slug}/mcp` for each registered project; unknown paths return HTTP 404.

**Tech Stack:** Go, pgxpool (`jackc/pgx/v5`), `mark3labs/mcp-go`, `spf13/cobra`, `gopkg.in/yaml.v3`, Postgres 16 with pgvector.

**Source-of-truth references:**
- Spec: `docs/superpowers/specs/2026-05-25-multi-project-isolation-design.md`
- Existing pool/migrate: `internal/storage/db.go`
- Existing MCP server: `internal/mcpserver/server.go`, `cmd/projectlens-mcp/main.go`
- Existing CLI config resolution: `cmd/projectlens/main.go:874` (`loadCmdConfig`)
- Existing lock wrappers: `cmd/projectlens/lock.go`
- Existing config: `internal/config/config.go`

**Conventions:**
- Each task ends with a commit using Conventional Commits prefix.
- Unit tests run with `make test`. Integration tests use `//go:build integration` and require `DATABASE_URL` (the project's `make test-integration` target).
- The pgxpool / migration tests REQUIRE a running Postgres reachable via `DATABASE_URL`. Skip cleanly when unset, matching the pattern in `internal/storage/inspect_integration_test.go:14`.
- Identifier quoting: any SQL that splices a storage schema must call `pgx.Identifier{storageSchema}.Sanitize()` AFTER passing the validation regex.

---

## File Structure

**New files:**
- `internal/projects/identifier.go` — slug + storage-schema regex validators, reserved-name checks.
- `internal/projects/identifier_test.go` — table-driven validation tests.
- `internal/projects/registry.go` — `Registry` type, YAML loader, cross-validation (unique slugs, unique schemas).
- `internal/projects/registry_test.go` — registry parse + validation tests.
- `internal/projects/runtime.go` — `Runtime` struct + `Resolve(ctx, registry, slug) (*Runtime, error)` (loads project config overlay, opens scoped pool, asserts schema).
- `internal/projects/runtime_integration_test.go` — pool isolation + reuse tests (`//go:build integration`).
- `internal/storage/schema.go` — `AssertSchemaExists`, `PinSearchPath`, helper used by `ConnectScoped`.
- `internal/storage/schema_test.go` — unit tests for helpers that do not need a DB.
- `cmd/projectlens/projects_cmd.go` — `projects list` / `projects validate` subcommands.
- `configs/projects.example.yaml` — example multi-project registry.

**Modified files:**
- `internal/storage/db.go` — add `ConnectScoped(ctx, url, storageSchema)` and `MigrateInSchema(ctx, dir, storageSchema)`. Keep `Connect` / `Migrate` working for legacy public path.
- `internal/storage/db_integration_test.go` (new file) — two-schema isolation test.
- `internal/config/config.go` — no schema changes; consumed unchanged by project config overlay.
- `cmd/projectlens/main.go` — add `--project`, `--projects` persistent flags; rewrite `loadCmdConfig` to optionally resolve via registry; register `projects` subcommand; reject `--project` + `--repo` conflict.
- `cmd/projectlens/lock.go` — switch `withWriteLock` / `withWriteLockAfterMigrate` to use scoped pool from runtime.
- `cmd/projectlens-mcp/main.go` — replace single-server `Start` with multi-mount HTTP server (`/{slug}/mcp` per project).
- `internal/mcpserver/server.go` — accept per-project runtime; expose `Handler(mcpServer)` for use by the multi-mount HTTP layer; keep single-project `Start` working for legacy use.
- `internal/logger/logger.go` (if present) or call sites — ensure log records include `project_slug` + `storage_schema` fields when invoked under a project runtime.
- `docs/operations.md`, `docs/architecture.md`, `docs/internals.md`, `docs/AGENT_SETUP.md`, `README.md`, `CLAUDE.md` — multi-project usage docs.

---

## Phase A: Schema-aware storage primitives

### Task 1: Identifier validators

**Files:**
- Create: `internal/projects/identifier.go`
- Test: `internal/projects/identifier_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/projects/identifier_test.go
package projects

import "testing"

func TestValidateSlug(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
	}{
		{"ingest", true},
		{"projectlens", true},
		{"a_b-c-9", true},
		{"", false},
		{"Ingest", false},
		{"ingest!", false},
		{"-leading", false},
	}
	for _, c := range cases {
		err := ValidateSlug(c.in)
		if (err == nil) != c.ok {
			t.Errorf("ValidateSlug(%q) ok=%v err=%v", c.in, c.ok, err)
		}
	}
}

func TestValidateStorageSchema(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"ingest", true},
		{"projectlens", true},
		{"a1_2", true},
		{"", false},
		{"public", false},
		{"pg_anything", false},
		{"1leading", false},
		{"with-dash", false},
		{"With_Upper", false},
	}
	for _, c := range cases {
		err := ValidateStorageSchema(c.in)
		if (err == nil) != c.ok {
			t.Errorf("ValidateStorageSchema(%q) ok=%v err=%v", c.in, c.ok, err)
		}
	}
}
```

- [ ] **Step 2: Run test, expect FAIL (functions undefined)**

Run: `go test ./internal/projects/...`
Expected: build errors `undefined: ValidateSlug` / `ValidateStorageSchema`.

- [ ] **Step 3: Write implementation**

```go
// internal/projects/identifier.go
package projects

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	slugPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	schemaPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// ValidateSlug enforces the project slug shape used in CLI flags and MCP URL paths.
func ValidateSlug(s string) error {
	if s == "" {
		return fmt.Errorf("slug is empty")
	}
	if !slugPattern.MatchString(s) {
		return fmt.Errorf("invalid slug %q: must match %s", s, slugPattern)
	}
	return nil
}

// ValidateStorageSchema enforces a safe Postgres identifier and rejects reserved names.
func ValidateStorageSchema(s string) error {
	if s == "" {
		return fmt.Errorf("storage_schema is empty")
	}
	if !schemaPattern.MatchString(s) {
		return fmt.Errorf("invalid storage_schema %q: must match %s", s, schemaPattern)
	}
	if s == "public" {
		return fmt.Errorf("storage_schema %q is reserved (used for legacy single-project installs)", s)
	}
	if strings.HasPrefix(s, "pg_") {
		return fmt.Errorf("storage_schema %q starts with reserved prefix pg_", s)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/projects/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/projects/identifier.go internal/projects/identifier_test.go
git commit -m "feat(projects): identifier validators for slug and storage_schema"
```

---

### Task 2: Schema assertion + search_path helper

**Files:**
- Create: `internal/storage/schema.go`
- Test: `internal/storage/schema_test.go`

- [ ] **Step 1: Write the unit test for `QuoteSchema`**

```go
// internal/storage/schema_test.go
package storage

import "testing"

func TestQuoteSchema(t *testing.T) {
	cases := map[string]string{
		"ingest":     `"ingest"`,
		"projectlens": `"projectlens"`,
	}
	for in, want := range cases {
		got := QuoteSchema(in)
		if got != want {
			t.Errorf("QuoteSchema(%q)=%q want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/storage/ -run TestQuoteSchema`
Expected: `undefined: QuoteSchema`.

- [ ] **Step 3: Implement helpers**

```go
// internal/storage/schema.go
package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// QuoteSchema returns an SQL-safe, double-quoted identifier for a storage schema.
// The input MUST already pass projects.ValidateStorageSchema; this is the
// last-line-of-defense identifier quoting using pgx's built-in escape.
func QuoteSchema(name string) string {
	return pgx.Identifier{name}.Sanitize()
}

// AssertSchemaExists returns an error if the given schema is not present in
// information_schema.schemata. The error message points users at the migrate
// command so missing-schema failures are actionable.
func AssertSchemaExists(ctx context.Context, conn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, schema string) error {
	var exists bool
	err := conn.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`,
		schema,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("storage: check schema %q: %w", schema, err)
	}
	if !exists {
		return fmt.Errorf("storage schema %q does not exist; run `projectlens migrate --project <slug>` first", schema)
	}
	return nil
}

// PinSearchPath sets the connection's search_path to "<schema>,public" using
// a sanitized identifier. Bind parameters are not valid for SET search_path,
// so we concatenate the quoted identifier; QuoteSchema enforces escaping.
func PinSearchPath(ctx context.Context, conn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconnCommandTag, error)
}, schema string) error {
	sql := "SET search_path TO " + QuoteSchema(schema) + ",public"
	_, err := conn.Exec(ctx, sql)
	if err != nil {
		return fmt.Errorf("storage: set search_path %q: %w", schema, err)
	}
	return nil
}

// pgconnCommandTag matches pgconn.CommandTag without importing pgconn directly
// at the interface boundary.
type pgconnCommandTag = interface{}
```

Note: the loose interface in `PinSearchPath` is for testability; the real call site passes a `*pgx.Conn` whose `Exec` returns `pgconn.CommandTag`. If Go's type inference complains, simplify to a concrete `*pgx.Conn` signature (drop the interface) and rely on integration tests.

- [ ] **Step 4: Run test**

Run: `go test ./internal/storage/ -run TestQuoteSchema`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/schema.go internal/storage/schema_test.go
git commit -m "feat(storage): schema identifier quoting and existence assertion helpers"
```

---

### Task 3: `ConnectScoped` — project-scoped pgxpool

**Files:**
- Modify: `internal/storage/db.go`
- Test: `internal/storage/db_scoped_integration_test.go` (new)

- [ ] **Step 1: Write the failing integration test**

```go
// internal/storage/db_scoped_integration_test.go
//go:build integration

package storage_test

import (
	"context"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/storage"
)

func TestConnectScopedSetsSearchPath(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()

	// Manually create schema "ri_scopetest" for this test.
	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect root: %v", err)
	}
	defer root.Close()
	if _, err := root.Pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS ri_scopetest`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_scopetest CASCADE`)
	})

	db, err := storage.ConnectScoped(ctx, url, "ri_scopetest")
	if err != nil {
		t.Fatalf("ConnectScoped: %v", err)
	}
	defer db.Close()

	var sp string
	if err := db.Pool.QueryRow(ctx, `SHOW search_path`).Scan(&sp); err != nil {
		t.Fatalf("show search_path: %v", err)
	}
	if sp != `"ri_scopetest", public` && sp != `"ri_scopetest",public` {
		t.Fatalf("search_path = %q; want ri_scopetest first then public", sp)
	}
}

func TestConnectScopedFailsOnMissingSchema(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	_, err := storage.ConnectScoped(ctx, url, "ri_does_not_exist_xyz")
	if err == nil {
		t.Fatal("ConnectScoped did not error on missing schema")
	}
}
```

- [ ] **Step 2: Run, expect FAIL**

Run: `go test -tags=integration ./internal/storage/ -run TestConnectScoped`
Expected: build error `undefined: ConnectScoped`.

- [ ] **Step 3: Implement `ConnectScoped`**

Add to `internal/storage/db.go`:

```go
// ConnectScoped creates a pgxpool pinned to the given storage schema. Every
// borrowed connection has search_path = "<schema>",public set in AfterConnect,
// after asserting the schema exists. Identifier safety relies on the caller
// passing a value already vetted by projects.ValidateStorageSchema.
func ConnectScoped(ctx context.Context, databaseURL, storageSchema string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("storage: parse config: %w", err)
	}
	quoted := QuoteSchema(storageSchema)
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		var exists bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`,
			storageSchema,
		).Scan(&exists); err != nil {
			return fmt.Errorf("storage: check schema %q: %w", storageSchema, err)
		}
		if !exists {
			return fmt.Errorf("storage schema %q does not exist; run `projectlens migrate --project <slug>` first", storageSchema)
		}
		if _, err := conn.Exec(ctx, "SET search_path TO "+quoted+",public"); err != nil {
			return fmt.Errorf("storage: set search_path %q: %w", storageSchema, err)
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("storage: connect scoped: %w", err)
	}
	// Force at least one connection so AfterConnect runs and surfaces errors now.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: ping scoped pool: %w", err)
	}
	return &DB{Pool: pool}, nil
}
```

Add the imports `"github.com/jackc/pgx/v5"` to the file.

- [ ] **Step 4: Run integration tests**

Run: `make test-integration` (or `go test -tags=integration ./internal/storage/...`)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/db.go internal/storage/db_scoped_integration_test.go
git commit -m "feat(storage): ConnectScoped pgxpool with AfterConnect schema assert + search_path"
```

---

### Task 4: `MigrateInSchema` — schema-aware migration runner

**Files:**
- Modify: `internal/storage/db.go`
- Test: `internal/storage/db_migrate_integration_test.go` (new)

- [ ] **Step 1: Write the failing integration test**

```go
// internal/storage/db_migrate_integration_test.go
//go:build integration

package storage_test

import (
	"context"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/storage"
)

func TestMigrateInSchemaIsolatesTwoProjects(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()

	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect root: %v", err)
	}
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_proj_a CASCADE`)
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_proj_b CASCADE`)
	})

	dir := findMigrationsDirForTest(t)
	if err := root.MigrateInSchema(ctx, dir, "ri_proj_a"); err != nil {
		t.Fatalf("migrate proj_a: %v", err)
	}
	if err := root.MigrateInSchema(ctx, dir, "ri_proj_b"); err != nil {
		t.Fatalf("migrate proj_b: %v", err)
	}

	var nA, nB int
	if err := root.Pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema='ri_proj_a' AND table_name='files'`).Scan(&nA); err != nil {
		t.Fatalf("count A: %v", err)
	}
	if err := root.Pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema='ri_proj_b' AND table_name='files'`).Scan(&nB); err != nil {
		t.Fatalf("count B: %v", err)
	}
	if nA != 1 || nB != 1 {
		t.Fatalf("expected files in both schemas: nA=%d nB=%d", nA, nB)
	}

	// Bookkeeping is schema-local.
	var mA, mB int
	_ = root.Pool.QueryRow(ctx, `SELECT count(*) FROM ri_proj_a.schema_migrations`).Scan(&mA)
	_ = root.Pool.QueryRow(ctx, `SELECT count(*) FROM ri_proj_b.schema_migrations`).Scan(&mB)
	if mA == 0 || mB == 0 {
		t.Fatalf("expected schema_migrations rows in each schema, got A=%d B=%d", mA, mB)
	}
}

// findMigrationsDirForTest mirrors cmd/projectlens/main.go:findMigrationsDir
// but only resolves relative to the repo root; tests run from package dir.
func findMigrationsDirForTest(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"../../migrations", "../../../migrations"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatal("migrations dir not found")
	return ""
}
```

- [ ] **Step 2: Run, expect FAIL**

Run: `go test -tags=integration ./internal/storage/ -run TestMigrateInSchema`
Expected: build error `undefined: (*DB).MigrateInSchema`.

- [ ] **Step 3: Implement `MigrateInSchema`**

Add to `internal/storage/db.go`:

```go
// MigrateInSchema runs all .up.sql files in migrationsDir inside the given
// storage schema. The pgvector extension is database-global and is created
// in public; project tables live in the storage schema. Schema_migrations
// bookkeeping is created inside the storage schema.
//
// storageSchema MUST already be vetted by projects.ValidateStorageSchema.
func (db *DB) MigrateInSchema(ctx context.Context, migrationsDir, storageSchema string) error {
	if storageSchema == "" {
		return fmt.Errorf("storage: MigrateInSchema requires storage_schema")
	}
	quoted := QuoteSchema(storageSchema)

	// 1. Ensure pgvector exists at database scope (idempotent).
	if _, err := db.Pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return fmt.Errorf("storage: create extension vector: %w", err)
	}
	// 2. Create the storage schema if missing.
	if _, err := db.Pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+quoted); err != nil {
		return fmt.Errorf("storage: create schema %s: %w", quoted, err)
	}

	// Acquire one connection and pin its search_path for all subsequent statements.
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("storage: acquire conn: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET search_path TO "+quoted+",public"); err != nil {
		return fmt.Errorf("storage: set search_path: %w", err)
	}

	// 3. Bookkeeping table inside the storage schema.
	const createTracker = `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`
	if _, err := conn.Exec(ctx, createTracker); err != nil {
		return fmt.Errorf("storage: create migration tracker in %s: %w", quoted, err)
	}

	files, err := ReadMigrationFiles(migrationsDir)
	if err != nil {
		return fmt.Errorf("storage: migrate: %w", err)
	}

	for _, mf := range files {
		var exists bool
		if err := conn.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = $1)", mf.Name,
		).Scan(&exists); err != nil {
			return fmt.Errorf("storage: check migration %s in %s: %w", mf.Name, quoted, err)
		}
		if exists {
			continue
		}
		// Strip any leading "CREATE EXTENSION vector" lines — extension is global, already done.
		sql := mf.SQL
		if _, err := conn.Exec(ctx, sql); err != nil {
			return fmt.Errorf("storage: migrate %s in %s: %w", mf.Name, quoted, err)
		}
		if _, err := conn.Exec(ctx,
			"INSERT INTO schema_migrations (name) VALUES ($1)", mf.Name,
		); err != nil {
			return fmt.Errorf("storage: record migration %s in %s: %w", mf.Name, quoted, err)
		}
	}
	return nil
}
```

Note: `CREATE EXTENSION IF NOT EXISTS vector` is idempotent at the database level, so re-running migration 001 against a second schema is safe. The extension's types (`vector`, `halfvec`) live in `public` and resolve via the `,public` suffix in `search_path`.

- [ ] **Step 4: Run integration tests**

Run: `make test-integration`
Expected: PASS (both schemas get `files` table and own `schema_migrations`).

- [ ] **Step 5: Commit**

```bash
git add internal/storage/db.go internal/storage/db_migrate_integration_test.go
git commit -m "feat(storage): MigrateInSchema runs migrations inside per-project storage schema"
```

---

### Task 5: Cross-schema isolation integration test

**Files:**
- Create: `internal/storage/db_isolation_integration_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration

package storage_test

import (
	"context"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/storage"
)

func TestScopedPoolsDoNotSeeAcrossSchemas(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()

	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_iso_a CASCADE`)
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_iso_b CASCADE`)
	})

	dir := findMigrationsDirForTest(t)
	for _, s := range []string{"ri_iso_a", "ri_iso_b"} {
		if err := root.MigrateInSchema(ctx, dir, s); err != nil {
			t.Fatalf("migrate %s: %v", s, err)
		}
	}

	dbA, err := storage.ConnectScoped(ctx, url, "ri_iso_a")
	if err != nil {
		t.Fatal(err)
	}
	defer dbA.Close()
	dbB, err := storage.ConnectScoped(ctx, url, "ri_iso_b")
	if err != nil {
		t.Fatal(err)
	}
	defer dbB.Close()

	if _, err := dbA.Pool.Exec(ctx, `INSERT INTO files(path, package_name, checksum, commit_sha) VALUES ('a.go','pkg/a','xxx','c')`); err != nil {
		t.Fatalf("insert into A: %v", err)
	}

	var nB int
	if err := dbB.Pool.QueryRow(ctx, `SELECT count(*) FROM files`).Scan(&nB); err != nil {
		t.Fatalf("count from B: %v", err)
	}
	if nB != 0 {
		t.Fatalf("expected 0 files visible from B, got %d", nB)
	}
}

func TestConnectionReuseKeepsScope(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_reuse CASCADE`)
	})
	if err := root.MigrateInSchema(ctx, findMigrationsDirForTest(t), "ri_reuse"); err != nil {
		t.Fatal(err)
	}
	db, err := storage.ConnectScoped(ctx, url, "ri_reuse")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Borrow, release, re-borrow — same conn should still be scoped.
	for i := 0; i < 5; i++ {
		c, err := db.Pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var sp string
		if err := c.QueryRow(ctx, `SHOW search_path`).Scan(&sp); err != nil {
			c.Release()
			t.Fatal(err)
		}
		c.Release()
		if sp != `"ri_reuse", public` && sp != `"ri_reuse",public` {
			t.Fatalf("iter %d: search_path=%q lost scope", i, sp)
		}
	}
}
```

- [ ] **Step 2: Run**

Run: `go test -tags=integration ./internal/storage/ -run 'Isolation|Reuse'`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/db_isolation_integration_test.go
git commit -m "test(storage): cross-schema isolation + scoped pool connection reuse"
```

---

## Phase B: Project registry + runtime

### Task 6: `Registry` parsing and validation

**Files:**
- Create: `internal/projects/registry.go`
- Create: `internal/projects/registry_test.go`
- Create: `internal/projects/testdata/valid.yaml`
- Create: `internal/projects/testdata/dup_slug.yaml`
- Create: `internal/projects/testdata/dup_schema.yaml`
- Create: `internal/projects/testdata/missing_repo.yaml`

- [ ] **Step 1: Write the failing test**

```go
// internal/projects/registry_test.go
package projects

import (
	"strings"
	"testing"
)

func TestLoadRegistryValid(t *testing.T) {
	reg, err := LoadRegistry("testdata/valid.yaml")
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if reg.DatabaseURL == "" {
		t.Error("expected database_url")
	}
	if len(reg.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(reg.Projects))
	}
	if _, err := reg.Find("ingest"); err != nil {
		t.Errorf("Find(ingest): %v", err)
	}
	if _, err := reg.Find("nope"); err == nil {
		t.Error("Find(nope) should error")
	}
	if reg.DefaultProject != "ingest" {
		t.Errorf("default_project=%q", reg.DefaultProject)
	}
}

func TestLoadRegistryDuplicateSlug(t *testing.T) {
	_, err := LoadRegistry("testdata/dup_slug.yaml")
	if err == nil || !strings.Contains(err.Error(), "duplicate slug") {
		t.Fatalf("expected duplicate slug error, got %v", err)
	}
}

func TestLoadRegistryDuplicateSchema(t *testing.T) {
	_, err := LoadRegistry("testdata/dup_schema.yaml")
	if err == nil || !strings.Contains(err.Error(), "duplicate storage_schema") {
		t.Fatalf("expected duplicate storage_schema error, got %v", err)
	}
}

func TestLoadRegistryMissingRepoPath(t *testing.T) {
	_, err := LoadRegistry("testdata/missing_repo.yaml")
	if err == nil || !strings.Contains(err.Error(), "repo_path") {
		t.Fatalf("expected repo_path error, got %v", err)
	}
}
```

Test fixtures:

```yaml
# internal/projects/testdata/valid.yaml
database_url: postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable
default_project: ingest
projects:
  - slug: ingest
    storage_schema: ingest
    repo_path: /tmp/ingest
  - slug: projectlens
    storage_schema: projectlens
    repo_path: /tmp/projectlens
    config_path: configs/projectlens.yaml
```

```yaml
# internal/projects/testdata/dup_slug.yaml
database_url: postgres://x/y
projects:
  - slug: ingest
    storage_schema: ingest
    repo_path: /tmp/a
  - slug: ingest
    storage_schema: other
    repo_path: /tmp/b
```

```yaml
# internal/projects/testdata/dup_schema.yaml
database_url: postgres://x/y
projects:
  - slug: a
    storage_schema: shared
    repo_path: /tmp/a
  - slug: b
    storage_schema: shared
    repo_path: /tmp/b
```

```yaml
# internal/projects/testdata/missing_repo.yaml
database_url: postgres://x/y
projects:
  - slug: a
    storage_schema: a
```

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/projects/ -run Registry`
Expected: build error `undefined: LoadRegistry`.

- [ ] **Step 3: Implement registry**

```go
// internal/projects/registry.go
package projects

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Project is a single registry entry.
type Project struct {
	Slug          string `yaml:"slug"`
	StorageSchema string `yaml:"storage_schema"`
	RepoPath      string `yaml:"repo_path"`
	ConfigPath    string `yaml:"config_path,omitempty"`
}

// Registry is the parsed projects.yaml.
type Registry struct {
	DatabaseURL    string    `yaml:"database_url"`
	DefaultProject string    `yaml:"default_project,omitempty"`
	Projects       []Project `yaml:"projects"`
}

// LoadRegistry reads and validates a YAML project registry from path.
func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read registry %s: %w", path, err)
	}
	var reg Registry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse registry %s: %w", path, err)
	}
	if err := reg.Validate(); err != nil {
		return nil, fmt.Errorf("registry %s: %w", path, err)
	}
	return &reg, nil
}

// Validate runs all registry-level invariants.
func (r *Registry) Validate() error {
	if r.DatabaseURL == "" {
		return fmt.Errorf("database_url is required")
	}
	if len(r.Projects) == 0 {
		return fmt.Errorf("at least one project is required")
	}
	slugs := map[string]bool{}
	schemas := map[string]bool{}
	for i, p := range r.Projects {
		if err := ValidateSlug(p.Slug); err != nil {
			return fmt.Errorf("projects[%d]: %w", i, err)
		}
		if err := ValidateStorageSchema(p.StorageSchema); err != nil {
			return fmt.Errorf("projects[%d] (%s): %w", i, p.Slug, err)
		}
		if p.RepoPath == "" {
			return fmt.Errorf("projects[%d] (%s): repo_path is required", i, p.Slug)
		}
		if slugs[p.Slug] {
			return fmt.Errorf("duplicate slug %q", p.Slug)
		}
		if schemas[p.StorageSchema] {
			return fmt.Errorf("duplicate storage_schema %q", p.StorageSchema)
		}
		slugs[p.Slug] = true
		schemas[p.StorageSchema] = true
	}
	if r.DefaultProject != "" {
		if !slugs[r.DefaultProject] {
			return fmt.Errorf("default_project %q is not a configured project", r.DefaultProject)
		}
	}
	return nil
}

// Find returns the project with the given slug.
func (r *Registry) Find(slug string) (Project, error) {
	for _, p := range r.Projects {
		if p.Slug == slug {
			return p, nil
		}
	}
	return Project{}, fmt.Errorf("unknown project %q", slug)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/projects/ -run Registry`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/projects/registry.go internal/projects/registry_test.go internal/projects/testdata/
git commit -m "feat(projects): YAML registry loader with slug/schema validation"
```

---

### Task 7: `Runtime` resolver

**Files:**
- Create: `internal/projects/runtime.go`
- Create: `internal/projects/runtime_test.go`

- [ ] **Step 1: Write the failing unit test (config overlay only — no DB)**

```go
// internal/projects/runtime_test.go
package projects

import (
	"path/filepath"
	"testing"
)

func TestLoadProjectConfigOverlay(t *testing.T) {
	// Write a minimal per-project config to a temp dir.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "p.yaml")
	if err := writeFile(cfgPath, `
database_url: ignored-by-overlay
repo_path: should-be-overridden
embeddings:
  provider: ollama
  model: mxbai-embed-large
`); err != nil {
		t.Fatal(err)
	}

	p := Project{
		Slug:          "demo",
		StorageSchema: "demo",
		RepoPath:      "/canonical/path",
		ConfigPath:    cfgPath,
	}
	cfg, err := LoadProjectConfig(p, "ignored-db-url")
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	if cfg.RepoPath != "/canonical/path" {
		t.Errorf("RepoPath overlay: got %q want /canonical/path", cfg.RepoPath)
	}
	if cfg.Embeddings.Provider != "ollama" {
		t.Errorf("Embeddings.Provider: got %q", cfg.Embeddings.Provider)
	}
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}
```

Add `import "os"` to the test file.

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/projects/ -run LoadProjectConfig`
Expected: build error `undefined: LoadProjectConfig`.

- [ ] **Step 3: Implement `Runtime` + `LoadProjectConfig`**

```go
// internal/projects/runtime.go
package projects

import (
	"context"
	"fmt"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/storage"
)

// Runtime is everything a CLI command or MCP handler needs to operate on
// one project: the resolved config, the project-scoped DB pool, and the
// identity fields used for logs and surfaces.
type Runtime struct {
	Slug          string
	StorageSchema string
	RepoPath      string
	Config        *config.Config
	DB            *storage.DB
}

// Close releases the project's DB pool.
func (r *Runtime) Close() {
	if r != nil && r.DB != nil {
		r.DB.Close()
	}
}

// LoadProjectConfig loads the optional per-project config file and overlays
// the registry's RepoPath. It also injects the registry's databaseURL.
// Project config supplies indexing/provider settings; identity (repo_path,
// storage_schema) always comes from the registry.
func LoadProjectConfig(p Project, databaseURL string) (*config.Config, error) {
	var cfg *config.Config
	if p.ConfigPath != "" {
		c, err := config.Load(p.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("load project config %s: %w", p.ConfigPath, err)
		}
		cfg = c
	} else {
		cfg = &config.Config{}
	}
	cfg.RepoPath = p.RepoPath
	cfg.DatabaseURL = databaseURL
	return cfg, nil
}

// Resolve opens a project-scoped pool and returns a ready Runtime. The
// caller MUST call Runtime.Close.
func Resolve(ctx context.Context, reg *Registry, slug string) (*Runtime, error) {
	p, err := reg.Find(slug)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadProjectConfig(p, reg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	db, err := storage.ConnectScoped(ctx, reg.DatabaseURL, p.StorageSchema)
	if err != nil {
		return nil, fmt.Errorf("project %q: %w", slug, err)
	}
	return &Runtime{
		Slug:          p.Slug,
		StorageSchema: p.StorageSchema,
		RepoPath:      p.RepoPath,
		Config:        cfg,
		DB:            db,
	}, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/projects/ -run LoadProjectConfig`
Expected: PASS.

- [ ] **Step 5: Add integration test for `Resolve`**

```go
// internal/projects/runtime_integration_test.go
//go:build integration

package projects_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hman-pro/projectlens/internal/projects"
	"github.com/hman-pro/projectlens/internal/storage"
)

func TestResolveOpensScopedPool(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_rt_test CASCADE`)
	})

	migrationsDir := findMigrationsDirForTest(t)
	if err := root.MigrateInSchema(ctx, migrationsDir, "ri_rt_test"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	regPath := filepath.Join(dir, "projects.yaml")
	_ = os.WriteFile(regPath, []byte(`
database_url: `+url+`
projects:
  - slug: rt
    storage_schema: ri_rt_test
    repo_path: /tmp/x
`), 0o644)

	reg, err := projects.LoadRegistry(regPath)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := projects.Resolve(ctx, reg, "rt")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defer rt.Close()
	if rt.StorageSchema != "ri_rt_test" {
		t.Errorf("StorageSchema=%q", rt.StorageSchema)
	}
}

func findMigrationsDirForTest(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"../../migrations", "../../../migrations"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatal("migrations dir not found")
	return ""
}
```

- [ ] **Step 6: Run integration test**

Run: `go test -tags=integration ./internal/projects/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/projects/runtime.go internal/projects/runtime_test.go internal/projects/runtime_integration_test.go
git commit -m "feat(projects): Runtime resolver with scoped pool and config overlay"
```

---

## Phase C: CLI wiring

### Task 8: `--project` / `--projects` flags + runtime resolution in `loadCmdConfig`

**Files:**
- Modify: `cmd/projectlens/main.go`
- Modify: `cmd/projectlens/lock.go`

- [ ] **Step 1: Add persistent flags + new resolver**

Edit `cmd/projectlens/main.go` around line 39:

```go
rootCmd.PersistentFlags().String("config", "configs/index.yaml", "path to single-project config file (legacy)")
rootCmd.PersistentFlags().String("db", "", "database URL override")
rootCmd.PersistentFlags().String("repo", "", "repository path override (legacy single-project use)")
rootCmd.PersistentFlags().String("project", "", "project slug from the registry")
rootCmd.PersistentFlags().String("projects", "configs/projects.yaml", "path to project registry YAML")
```

Add to `cmd/projectlens/main.go`:

```go
// resolveProjectRuntime returns a ready *projects.Runtime when --project is
// passed. It enforces that --repo is NOT also passed (registry wins, conflict
// is loud per spec).
func resolveProjectRuntime(ctx context.Context, cmd *cobra.Command) (*projects.Runtime, error) {
	slug, _ := cmd.Flags().GetString("project")
	if slug == "" {
		return nil, nil
	}
	repoFlag, _ := cmd.Flags().GetString("repo")
	if repoFlag != "" {
		return nil, fmt.Errorf("--project and --repo are mutually exclusive; remove one")
	}
	regPath, _ := cmd.Flags().GetString("projects")
	reg, err := projects.LoadRegistry(regPath)
	if err != nil {
		return nil, err
	}
	if dbURL, _ := cmd.Flags().GetString("db"); dbURL != "" {
		reg.DatabaseURL = dbURL
	}
	return projects.Resolve(ctx, reg, slug)
}
```

Add `"github.com/hman-pro/projectlens/internal/projects"` to imports.

- [ ] **Step 2: Make `loadCmdConfig` optionally project-aware**

Replace `loadCmdConfig` at `cmd/projectlens/main.go:874`:

```go
// loadCmdConfig returns either a project runtime (when --project is set) or
// a legacy single-project (cfg, repoPath) pair. The runtime, when non-nil,
// owns its DB and must be Closed by the caller.
func loadCmdConfig(cmd *cobra.Command) (*config.Config, string, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, "", fmt.Errorf("loading config: %w", err)
	}
	if dbURL, _ := cmd.Flags().GetString("db"); dbURL != "" {
		cfg.DatabaseURL = dbURL
	}
	repoPath, _ := cmd.Flags().GetString("repo")
	if repoPath == "" {
		repoPath = cfg.RepoPath
	}
	if repoPath == "" {
		return nil, "", fmt.Errorf("repository path required: use --repo flag or set repo_path in config")
	}
	return cfg, repoPath, nil
}
```

(Behavior unchanged for the legacy path; new project path goes through `resolveProjectRuntime`.)

- [ ] **Step 3: Adapt `withWriteLock` + `withWriteLockAfterMigrate`**

Edit `cmd/projectlens/lock.go`:

```go
func withWriteLock(cmdName string, run LockedCmd) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		// Project path
		rt, err := resolveProjectRuntime(ctx, cmd)
		if err != nil {
			return err
		}
		if rt != nil {
			defer rt.Close()
			lock, err := acquireOrExit(ctx, rt.DB, cmdName)
			if err != nil {
				return err
			}
			defer func() { _ = lock.Release(context.Background()) }()
			return run(ctx, cmd, rt.DB, rt.Config, rt.RepoPath)
		}
		// Legacy path
		cfg, repoPath, err := loadCmdConfig(cmd)
		if err != nil {
			return err
		}
		db, err := storage.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer db.Close()
		lock, err := acquireOrExit(ctx, db, cmdName)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Release(context.Background()) }()
		return run(ctx, cmd, db, cfg, repoPath)
	}
}

func withWriteLockAfterMigrate(cmdName string, run LockedCmd) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		// Project path: migrations happen via explicit `migrate --project`.
		// bootstrap on project path still migrates the project schema before locking.
		rt, err := resolveProjectRuntime(ctx, cmd)
		if err != nil {
			// For project path, schema-missing is the expected first-run state. The migrate
			// step below should run BEFORE the scoped pool is opened. Handle that branch.
			if isMissingSchemaErr(err) {
				return runProjectBootstrap(ctx, cmd, cmdName, run)
			}
			return err
		}
		if rt != nil {
			defer rt.Close()
			// Project schema exists — migrate idempotently, then lock and run.
			if err := migrateProjectSchema(ctx, cmd); err != nil {
				return err
			}
			lock, err := acquireOrExit(ctx, rt.DB, cmdName)
			if err != nil {
				return err
			}
			defer func() { _ = lock.Release(context.Background()) }()
			return run(ctx, cmd, rt.DB, rt.Config, rt.RepoPath)
		}
		// Legacy path: unchanged.
		cfg, repoPath, err := loadCmdConfig(cmd)
		if err != nil {
			return err
		}
		db, err := storage.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer db.Close()
		if err := db.Migrate(ctx, findMigrationsDir()); err != nil {
			return fmt.Errorf("running migrations: %w", err)
		}
		lock, err := acquireOrExit(ctx, db, cmdName)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Release(context.Background()) }()
		return run(ctx, cmd, db, cfg, repoPath)
	}
}

func isMissingSchemaErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "storage schema") && strings.Contains(err.Error(), "does not exist")
}

func runProjectBootstrap(ctx context.Context, cmd *cobra.Command, cmdName string, run LockedCmd) error {
	if err := migrateProjectSchema(ctx, cmd); err != nil {
		return err
	}
	rt, err := resolveProjectRuntime(ctx, cmd)
	if err != nil {
		return err
	}
	defer rt.Close()
	lock, err := acquireOrExit(ctx, rt.DB, cmdName)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release(context.Background()) }()
	return run(ctx, cmd, rt.DB, rt.Config, rt.RepoPath)
}

// migrateProjectSchema runs MigrateInSchema for the --project's storage schema
// using a root (unscoped) pool — required because the schema may not exist yet.
func migrateProjectSchema(ctx context.Context, cmd *cobra.Command) error {
	regPath, _ := cmd.Flags().GetString("projects")
	slug, _ := cmd.Flags().GetString("project")
	reg, err := projects.LoadRegistry(regPath)
	if err != nil {
		return err
	}
	if dbURL, _ := cmd.Flags().GetString("db"); dbURL != "" {
		reg.DatabaseURL = dbURL
	}
	p, err := reg.Find(slug)
	if err != nil {
		return err
	}
	root, err := storage.Connect(ctx, reg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer root.Close()
	return root.MigrateInSchema(ctx, findMigrationsDir(), p.StorageSchema)
}
```

Add imports: `"strings"`, `"github.com/hman-pro/projectlens/internal/projects"`.

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/projectlens/main.go cmd/projectlens/lock.go
git commit -m "feat(cli): --project flag resolves through registry and scoped pool"
```

---

### Task 9: `migrate --project` and explicit project-mode migrate

**Files:**
- Modify: `cmd/projectlens/main.go` (`newMigrateCmd`)

- [ ] **Step 1: Rewrite `newMigrateCmd` to branch on `--project`**

Replace `newMigrateCmd` at `cmd/projectlens/main.go:174`:

```go
func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending SQL migrations (per project schema when --project is set)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			slug, _ := cmd.Flags().GetString("project")
			if slug == "" {
				// Legacy path — public schema.
				cfg, _, err := loadCmdConfig(cmd)
				if err != nil {
					return err
				}
				db, err := storage.Connect(ctx, cfg.DatabaseURL)
				if err != nil {
					return fmt.Errorf("connecting to database: %w", err)
				}
				defer db.Close()
				if err := db.Migrate(ctx, findMigrationsDir()); err != nil {
					return fmt.Errorf("running migrations: %w", err)
				}
				fmt.Println("migrations up to date (public schema)")
				return nil
			}
			if err := migrateProjectSchema(ctx, cmd); err != nil {
				return err
			}
			fmt.Printf("migrations up to date (project %s)\n", slug)
			return nil
		},
	}
}
```

- [ ] **Step 2: Smoke test by hand against a scratch DB**

```bash
DATABASE_URL=$DATABASE_URL go run ./cmd/projectlens migrate --projects /tmp/test-projects.yaml --project demo
```

Expected: `migrations up to date (project demo)`.

- [ ] **Step 3: Build + vet**

```bash
make vet
make build
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/projectlens/main.go
git commit -m "feat(cli): migrate respects --project and runs MigrateInSchema"
```

---

### Task 10: `projects list` and `projects validate` subcommands

**Files:**
- Create: `cmd/projectlens/projects_cmd.go`
- Modify: `cmd/projectlens/main.go` (register subcommand)

- [ ] **Step 1: Implement subcommand**

```go
// cmd/projectlens/projects_cmd.go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/projects"
)

func newProjectsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "Inspect the project registry",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List configured projects",
			RunE: func(cmd *cobra.Command, _ []string) error {
				regPath, _ := cmd.Flags().GetString("projects")
				reg, err := projects.LoadRegistry(regPath)
				if err != nil {
					return err
				}
				fmt.Printf("registry: %s\n", regPath)
				fmt.Printf("database: %s\n", reg.DatabaseURL)
				if reg.DefaultProject != "" {
					fmt.Printf("default:  %s\n", reg.DefaultProject)
				}
				fmt.Println()
				fmt.Printf("%-16s %-20s %s\n", "SLUG", "STORAGE_SCHEMA", "REPO_PATH")
				for _, p := range reg.Projects {
					fmt.Printf("%-16s %-20s %s\n", p.Slug, p.StorageSchema, p.RepoPath)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "validate",
			Short: "Validate the project registry",
			RunE: func(cmd *cobra.Command, _ []string) error {
				regPath, _ := cmd.Flags().GetString("projects")
				if _, err := projects.LoadRegistry(regPath); err != nil {
					return err
				}
				fmt.Printf("registry %s is valid\n", regPath)
				return nil
			},
		},
	)
	return cmd
}
```

Register in `cmd/projectlens/main.go` after `newMigrateCmd()` line:

```go
newMigrateCmd(),
newProjectsCmd(),
```

- [ ] **Step 2: Run `make build`**

Run: `make build`
Expected: PASS.

- [ ] **Step 3: Manual smoke**

```bash
./bin/projectlens projects validate --projects internal/projects/testdata/valid.yaml
./bin/projectlens projects list --projects internal/projects/testdata/valid.yaml
```

Expected: validate prints "is valid"; list prints the two rows.

- [ ] **Step 4: Commit**

```bash
git add cmd/projectlens/projects_cmd.go cmd/projectlens/main.go
git commit -m "feat(cli): projects list/validate subcommands"
```

---

### Task 11: Example project registry

**Files:**
- Create: `configs/projects.example.yaml`

- [ ] **Step 1: Write the example**

```yaml
# configs/projects.example.yaml
# Copy to configs/projects.yaml and edit. The MCP server and CLI default to
# configs/projects.yaml when --projects is not passed.
database_url: postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable

# Optional. Used by the TUI when no --project is given.
default_project: projectlens

projects:
  - slug: projectlens
    storage_schema: projectlens
    repo_path: /Users/you/source/projectlens
    # config_path is optional; supplies indexing/provider settings, not identity.
    config_path: configs/index.yaml

  # - slug: ingest
  #   storage_schema: ingest
  #   repo_path: /Users/you/source/example-org/ingest
  #   config_path: configs/ingest.yaml
```

- [ ] **Step 2: Commit**

```bash
git add configs/projects.example.yaml
git commit -m "docs(configs): example multi-project registry"
```

---

## Phase D: MCP routing

### Task 12: Per-project MCP server wiring

**Files:**
- Modify: `internal/mcpserver/server.go` (extract handler factory)
- Modify: `cmd/projectlens-mcp/main.go` (multi-mount)

- [ ] **Step 1: Add `Handler` method that returns the per-server HTTP handler**

Extend `internal/mcpserver/server.go`:

```go
import (
	// ... existing imports ...
	"net/http"
)

// Handler returns an http.Handler that serves this Server's MCP tools over
// Streamable HTTP at the caller-supplied mount point. Each invocation creates
// a fresh MCPServer + session manager so multiple projects can be mounted
// independently in one process.
func (s *Server) Handler() http.Handler {
	mcpServer := server.NewMCPServer("projectlens", "1.0.0",
		server.WithHooks(s.loggingHooks()),
	)
	for _, r := range s.toolRegistry() {
		mcpServer.AddTool(r.tool, withTimeout(handlerTimeout, r.handler))
	}
	return server.NewStreamableHTTPServer(mcpServer)
}
```

(Existing `Start` should be kept for legacy single-server use, but it can stay as-is.)

- [ ] **Step 2: Rewrite `cmd/projectlens-mcp/main.go` for multi-mount**

Replace the `run()` body in `cmd/projectlens-mcp/main.go` to:

1. Detect `--projects` flag; default `configs/projects.yaml`.
2. If file exists → multi-project mode. Otherwise → legacy single-project mode using `configs/index.yaml`.
3. In multi-project mode:
   - Load the registry.
   - For each project, resolve runtime (open scoped pool + load config).
   - Build per-project `*mcpserver.Server` and register its handler under `/{slug}/mcp`.
   - 404 fallthrough for unknown paths.

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/mcpserver"
	"github.com/hman-pro/projectlens/internal/projects"
	"github.com/hman-pro/projectlens/internal/providers/anthropic"
	"github.com/hman-pro/projectlens/internal/providers/ollama"
	"github.com/hman-pro/projectlens/internal/providers/openai"
	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
	_ "github.com/joho/godotenv/autoload"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	projectsPath := envOr("PROJECTS_PATH", "configs/projects.yaml")
	port := portFromEnv()

	if _, err := os.Stat(projectsPath); err == nil {
		return runMultiProject(ctx, projectsPath, port)
	}
	return runLegacySingle(ctx, port)
}

func runMultiProject(ctx context.Context, projectsPath string, port int) error {
	reg, err := projects.LoadRegistry(projectsPath)
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}
	mux := http.NewServeMux()
	cleanups := []func(){}

	for _, p := range reg.Projects {
		rt, err := projects.Resolve(ctx, reg, p.Slug)
		if err != nil {
			// Spec: one broken project must not prevent other endpoints from serving.
			fmt.Fprintf(os.Stderr, "warn: project %q not ready: %v\n", p.Slug, err)
			continue
		}
		cleanups = append(cleanups, rt.Close)

		srv, err := buildProjectServer(rt, port)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: project %q server init failed: %v\n", p.Slug, err)
			rt.Close()
			continue
		}
		mount := "/" + p.Slug + "/mcp"
		handler := srv.Handler()
		mux.Handle(mount, http.StripPrefix(mount, handler))
		mux.Handle(mount+"/", http.StripPrefix(mount, handler))
		fmt.Printf("mounted %s -> storage_schema=%s repo=%s\n", mount, p.StorageSchema, p.RepoPath)
	}

	defer func() {
		for _, c := range cleanups {
			c()
		}
	}()

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("projectlens MCP server listening on %s\n", addr)
	httpSrv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func buildProjectServer(rt *projects.Runtime, port int) (*mcpserver.Server, error) {
	cfg := rt.Config
	var embedder retrieval.QueryEmbedder
	switch cfg.Embeddings.Provider {
	case "ollama":
		embedder = ollama.NewClient(cfg.Embeddings.Endpoint, cfg.Embeddings.Model)
	case "openai":
		if cfg.OpenAIKey != "" {
			if cfg.Embeddings.Dimensions > 0 {
				embedder = openai.NewClientWithDims(cfg.OpenAIKey, cfg.Embeddings.Dimensions)
			} else {
				embedder = openai.NewClient(cfg.OpenAIKey)
			}
		}
	}
	router := retrieval.NewRouter(rt.DB, embedder)
	srv := mcpserver.New(rt.DB, router, port, rt.RepoPath).
		WithSummarizer(newSummarizerProber(cfg))
	return srv, nil
}

func runLegacySingle(ctx context.Context, port int) error {
	cfgPath := envOr("CONFIG_PATH", "configs/index.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required (set via env or config)")
	}
	db, err := storage.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		return fmt.Errorf("pinging database: %w", err)
	}

	var embedder retrieval.QueryEmbedder
	switch cfg.Embeddings.Provider {
	case "ollama":
		embedder = ollama.NewClient(cfg.Embeddings.Endpoint, cfg.Embeddings.Model)
	case "openai":
		if cfg.OpenAIKey != "" {
			if cfg.Embeddings.Dimensions > 0 {
				embedder = openai.NewClientWithDims(cfg.OpenAIKey, cfg.Embeddings.Dimensions)
			} else {
				embedder = openai.NewClient(cfg.OpenAIKey)
			}
		}
	}
	router := retrieval.NewRouter(db, embedder)
	srv := mcpserver.New(db, router, port, cfg.RepoPath).
		WithSummarizer(newSummarizerProber(cfg))
	return srv.Start(ctx)
}

func portFromEnv() int {
	port := 8484
	if v := os.Getenv("MCP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			port = p
		}
	}
	return port
}

func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// keep summarizerProberFunc and newSummarizerProber as in the previous version
// (they don't change).
```

Re-import the existing `summarizerProberFunc` and `newSummarizerProber` definitions from the prior file (do not delete them; this rewrite is a refactor of `run()` only).

Also: `strings` import is added in case future code needs it; remove if Go vet flags unused.

- [ ] **Step 3: Build**

```bash
make build
```

Expected: PASS.

- [ ] **Step 4: Smoke test multi-mount manually**

Create `configs/projects.yaml` with two projects (migrated). Start the server:

```bash
./bin/projectlens-mcp
```

Then `curl -i http://localhost:8484/unknown/mcp` should return `404`.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/server.go cmd/projectlens-mcp/main.go
git commit -m "feat(mcp): multi-project HTTP mounts at /{slug}/mcp with per-project session managers"
```

---

### Task 13: MCP unknown-project 404 + broken-project tolerance integration test

**Files:**
- Create: `internal/mcpserver/multi_project_integration_test.go`

- [ ] **Step 1: Write the failing integration test**

```go
//go:build integration

package mcpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/mcpserver"
	"github.com/hman-pro/projectlens/internal/projects"
	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
)

func TestUnknownProjectReturns404(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_mcp_a CASCADE`)
	})
	dir := findMigrationsDirForTest(t)
	if err := root.MigrateInSchema(ctx, dir, "ri_mcp_a"); err != nil {
		t.Fatal(err)
	}
	rt, err := projects.Resolve(ctx, &projects.Registry{
		DatabaseURL: url,
		Projects:    []projects.Project{{Slug: "a", StorageSchema: "ri_mcp_a", RepoPath: "/tmp/x"}},
	}, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	srv := mcpserver.New(rt.DB, retrieval.NewRouter(rt.DB, nil), 0, rt.RepoPath)
	mux := http.NewServeMux()
	mount := "/a/mcp"
	mux.Handle(mount, http.StripPrefix(mount, srv.Handler()))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/nope/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown project: status=%d want 404", resp.StatusCode)
	}
}

func findMigrationsDirForTest(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"../../migrations", "../../../migrations"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatal("migrations dir not found")
	return ""
}
```

- [ ] **Step 2: Run integration**

Run: `go test -tags=integration ./internal/mcpserver/ -run UnknownProject`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/mcpserver/multi_project_integration_test.go
git commit -m "test(mcp): unknown-project endpoint returns 404"
```

---

## Phase E: Logs + docs

### Task 14: Per-project log fields

**Files:**
- Modify: `internal/logger/logger.go` (whichever exposes the structured logger)
- Modify: a representative call site per indexer + MCP handler — wire `WithProject(slug, schema)` into the request/job context.

> Implementation note: the exact API depends on the existing logger package
> (`internal/logger`). Find the structured-fields helper (likely `logger.With(k, v)`
> or a context-based logger). Add a single convenience:
>
> ```go
> // WithProject returns a logger that carries project_slug + storage_schema fields.
> func WithProject(slug, schema string) *Logger {
>     return baseLogger.With("project_slug", slug, "storage_schema", schema)
> }
> ```
>
> Then thread `logger.WithProject(rt.Slug, rt.StorageSchema)` into:
> - the MCP server's `loggingHooks` (look up runtime per request, log before/after)
> - the CLI lock wrappers, after a runtime is resolved
> - the per-project indexer stages launched via `withWriteLock`

- [ ] **Step 1: Add `WithProject` helper to `internal/logger`**

Read `internal/logger/logger.go` first, then add the helper above. If the package already exposes structured logging through `slog`, the change is one wrapper. If it does not, defer this task and create a follow-up issue — but the spec requires the fields so prefer to add a thin wrapper now.

- [ ] **Step 2: Thread through CLI lock wrappers and MCP server**

In `cmd/projectlens/lock.go`, after `rt` is resolved and before `run(...)`:

```go
logger.WithProject(rt.Slug, rt.StorageSchema).Info("project runtime ready",
    "storage_schema", rt.StorageSchema, "repo", rt.RepoPath)
```

In `internal/mcpserver/server.go`, augment `loggingHooks` to read project identity from the server struct (add `slug string` field on `Server`, set it in `New`, surface in log messages).

- [ ] **Step 3: Run unit tests**

Run: `make test`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/logger/ internal/mcpserver/server.go cmd/projectlens/lock.go
git commit -m "feat(observability): project_slug + storage_schema log fields"
```

---

### Task 15: Update `status`, `report`, `export graph`, `index_status` to show project identity

**Files:**
- Modify: `cmd/projectlens/report.go`, `cmd/projectlens/export.go`, `cmd/projectlens/main.go` (status command), `internal/mcpserver/handlers.go` (index_status)

- [ ] **Step 1: Audit which surfaces print results**

```bash
grep -n "Println\|Printf" cmd/projectlens/report.go cmd/projectlens/export.go | head -20
grep -n "index_status\|IndexStatus" internal/mcpserver/handlers.go | head -10
```

- [ ] **Step 2: Add project identity to outputs**

For each surface, when running under a project runtime, prepend a line / add a field:

```
project: ingest (storage_schema=ingest)
```

For `index_status` (MCP), add fields `project_slug` and `storage_schema` to the response struct in `internal/mcpserver/types.go`. Populate them from the server's `slug`/`storageSchema` fields.

- [ ] **Step 3: Run tests**

Run: `make test`
Expected: PASS (update any golden test files that assert exact output).

- [ ] **Step 4: Commit**

```bash
git add cmd/projectlens/report.go cmd/projectlens/export.go cmd/projectlens/main.go internal/mcpserver/handlers.go internal/mcpserver/types.go
git commit -m "feat(observability): report/export/status/index_status surface project identity"
```

---

### Task 16: Documentation refresh

**Files:**
- Modify: `README.md`, `docs/architecture.md`, `docs/operations.md`, `docs/internals.md`, `docs/AGENT_SETUP.md`, `CLAUDE.md`

- [ ] **Step 1: Update `docs/operations.md`**

Add a "Projects" section that documents:
- the YAML registry shape (link to `configs/projects.example.yaml`),
- `projectlens projects list` / `validate`,
- `projectlens migrate --project <slug>`,
- `projectlens index-all --project <slug>`,
- legacy `--repo`/`public` path still working,
- MCP URL shape `/{slug}/mcp` and 404 behavior,
- local-only/no-auth caveat.

- [ ] **Step 2: Update `docs/architecture.md`**

Add storage-schema-per-project to the data layer section. Diagram: one DB, N schemas, N MCP mounts, one process.

- [ ] **Step 3: Update `docs/internals.md`**

Document `ConnectScoped` and `MigrateInSchema` semantics: `AfterConnect`, `search_path` pinning, pgvector global.

- [ ] **Step 4: Update `docs/AGENT_SETUP.md`**

Show updated MCP config snippets: per-project URL (`http://localhost:8484/<slug>/mcp`). Keep legacy single-project snippet with a "legacy" callout.

- [ ] **Step 5: Update `README.md`**

Quickstart: add the multi-project case under "Configuration".

- [ ] **Step 6: Update `CLAUDE.md`**

Add a row to the "Source-Of-Truth Files" table:

```
| Project registry | configs/projects.yaml + internal/projects/ |
```

Add a maintainer note under "Code Conventions":

> Storage schemas are quoted via `pgx.Identifier{}.Sanitize()` after passing
> `projects.ValidateStorageSchema`. Never splice an unvetted schema name into SQL.

- [ ] **Step 7: Commit**

```bash
git add README.md docs/ CLAUDE.md
git commit -m "docs: multi-project isolation usage, architecture, and conventions"
```

---

## Self-Review Checklist (run after writing all tasks)

**1. Spec coverage:**
- Architecture (schema-per-project) → Tasks 1–5.
- Registry validation + identifier safety → Tasks 1, 6.
- CLI `--project` everywhere → Task 8 (wrappers) + Task 9 (migrate) + Task 10 (subcommand).
- MCP `/{slug}/mcp` routing + per-project session manager + 404 + broken-project tolerance → Tasks 12, 13.
- Legacy public-schema fallback → Task 8 (lock wrappers branch) + Task 12 (legacy `runLegacySingle`).
- Connection-reuse leak proof → Task 5.
- pgvector global, table-local migration → Task 4.
- Project log fields → Task 14.
- `status` / `report` / `export graph` / `index_status` project identity → Task 15.
- Docs → Task 16.
- `--repo`/`--project` conflict fails loudly → Task 8 (`resolveProjectRuntime`).
- Example registry → Task 11.

**2. Placeholders:** none — every code step has a code block.

**3. Type consistency:** `Runtime` shape used identically in Tasks 7, 8, 12, 14. `MigrateInSchema(ctx, dir, schema)` signature used consistently in Tasks 4, 8, 9.

**Gaps to call out before execution:**
- TUI (`cmd/projectlens-tui`) integration is NOT included in this plan — spec says TUI should use the same resolution path but it's a follow-up. Add a final task once base CLI/MCP work lands.
- Removing the legacy `--repo` path is a Phase-F item out of scope per spec rollout step 6.

---

## Revisions (2026-05-25, addressing review at `2026-05-25-multi-project-isolation-review.md`)

Findings 1–5 in the review file require plan changes. Tasks below SUPERSEDE the
matching tasks above. Execute the revised tasks; ignore the originals where
explicitly superseded.

### Task 7.5 (NEW, REVISED to address review finding #6): Central `openCmdStorage` helper + `resolveProjectRuntime` together

`resolveProjectRuntime` MUST live in this task (not Task 8) so `cmd/projectlens/projectctx.go`
compiles standalone. Persistent flag definitions stay in Task 8 — `resolveProjectRuntime`
reads flags via `cmd.Flags().GetString(...)` which works regardless of where flags are
registered, but the test in this task must register the flags it reads via a local cobra
parent (no dependency on `rootCmd`).

Resolves review finding #1 (universal storage routing), finding #2 (mutex check
centralized), and finding #6 (Task 7.5 ordering / compile gap).

**Files:**
- Create: `cmd/projectlens/projectctx.go`
- Test:   `cmd/projectlens/projectctx_test.go`

- [ ] **Step 1: Write the failing test**

```go
// cmd/projectlens/projectctx_test.go
package main

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
)

func TestOpenCmdStorageRejectsProjectAndRepo(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.PersistentFlags().String("project", "", "")
	cmd.PersistentFlags().String("projects", "configs/projects.yaml", "")
	cmd.PersistentFlags().String("repo", "", "")
	cmd.PersistentFlags().String("config", "configs/index.yaml", "")
	cmd.PersistentFlags().String("db", "", "")
	_ = cmd.ParseFlags([]string{"--project", "foo", "--repo", "/tmp"})
	_, err := validateMutex(cmd)
	if err == nil {
		t.Fatal("expected mutex error")
	}
}

func TestValidateMutexAllowsRepoAlone(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.PersistentFlags().String("project", "", "")
	cmd.PersistentFlags().String("projects", "configs/projects.yaml", "")
	cmd.PersistentFlags().String("repo", "", "")
	_ = cmd.ParseFlags([]string{"--repo", "/tmp"})
	if _, err := validateMutex(cmd); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

// integration-style cover with stubbed registry path is in projectctx_integration_test.go.
var _ = context.Background
```

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./cmd/projectlens/ -run validateMutex -count=1`
Expected: `undefined: validateMutex`.

- [ ] **Step 3: Implement the helper**

```go
// cmd/projectlens/projectctx.go
package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/projects"
	"github.com/hman-pro/projectlens/internal/storage"
)

// CmdStorage is the resolved storage handle for one CLI command invocation.
// Exactly one of Runtime (project path) or LegacyDB (public path) is non-nil.
type CmdStorage struct {
	Runtime *projects.Runtime

	LegacyDB     *storage.DB
	LegacyCfg    *config.Config
	LegacyRepo   string

	cleanup func()
}

// DB returns the active DB regardless of project/legacy path.
func (c *CmdStorage) DB() *storage.DB {
	if c.Runtime != nil {
		return c.Runtime.DB
	}
	return c.LegacyDB
}

// Config returns the active config regardless of project/legacy path.
func (c *CmdStorage) Config() *config.Config {
	if c.Runtime != nil {
		return c.Runtime.Config
	}
	return c.LegacyCfg
}

// RepoPath returns the active repo path regardless of project/legacy path.
func (c *CmdStorage) RepoPath() string {
	if c.Runtime != nil {
		return c.Runtime.RepoPath
	}
	return c.LegacyRepo
}

// Slug returns the active project slug, or empty when on the legacy path.
func (c *CmdStorage) Slug() string {
	if c.Runtime != nil {
		return c.Runtime.Slug
	}
	return ""
}

// StorageSchema returns the active storage schema. Legacy path returns "public".
func (c *CmdStorage) StorageSchema() string {
	if c.Runtime != nil {
		return c.Runtime.StorageSchema
	}
	return "public"
}

// Close releases resources.
func (c *CmdStorage) Close() {
	if c == nil {
		return
	}
	if c.cleanup != nil {
		c.cleanup()
		return
	}
	if c.Runtime != nil {
		c.Runtime.Close()
		return
	}
	if c.LegacyDB != nil {
		c.LegacyDB.Close()
	}
}

// validateMutex enforces that --project and --repo cannot both be set.
// Returns true when --project was passed (project mode).
func validateMutex(cmd *cobra.Command) (bool, error) {
	slug, _ := cmd.Flags().GetString("project")
	repo, _ := cmd.Flags().GetString("repo")
	if slug != "" && repo != "" {
		return false, fmt.Errorf("--project and --repo are mutually exclusive; remove one")
	}
	return slug != "", nil
}

// openCmdStorage is the SINGLE entry point every storage-opening CLI command
// MUST use to obtain a DB handle. It enforces the --project/--repo mutex,
// resolves project runtime via the registry, or falls back to the legacy
// single-project path.
func openCmdStorage(ctx context.Context, cmd *cobra.Command) (*CmdStorage, error) {
	projectMode, err := validateMutex(cmd)
	if err != nil {
		return nil, err
	}
	if projectMode {
		rt, err := resolveProjectRuntime(ctx, cmd)
		if err != nil {
			return nil, err
		}
		return &CmdStorage{Runtime: rt}, nil
	}
	cfg, repoPath, err := loadCmdConfig(cmd)
	if err != nil {
		return nil, err
	}
	db, err := storage.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	return &CmdStorage{LegacyDB: db, LegacyCfg: cfg, LegacyRepo: repoPath}, nil
}

// resolveProjectRuntime opens a scoped runtime for the --project. Callers
// must route through openCmdStorage or migrateProjectSchemaFromFlags so
// validateMutex runs first.
func resolveProjectRuntime(ctx context.Context, cmd *cobra.Command) (*projects.Runtime, error) {
	slug, _ := cmd.Flags().GetString("project")
	if slug == "" {
		return nil, fmt.Errorf("internal: resolveProjectRuntime called without --project")
	}
	regPath, _ := cmd.Flags().GetString("projects")
	reg, err := projects.LoadRegistry(regPath)
	if err != nil {
		return nil, err
	}
	if dbURL, _ := cmd.Flags().GetString("db"); dbURL != "" {
		reg.DatabaseURL = dbURL
	}
	return projects.Resolve(ctx, reg, slug)
}

// migrateProjectSchemaFromFlags runs MigrateInSchema for --project. Callers
// must run validateMutex first; this function trusts that gate.
func migrateProjectSchemaFromFlags(ctx context.Context, cmd *cobra.Command) error {
	if _, err := validateMutex(cmd); err != nil {
		return err
	}
	regPath, _ := cmd.Flags().GetString("projects")
	slug, _ := cmd.Flags().GetString("project")
	reg, err := projects.LoadRegistry(regPath)
	if err != nil {
		return err
	}
	if dbURL, _ := cmd.Flags().GetString("db"); dbURL != "" {
		reg.DatabaseURL = dbURL
	}
	p, err := reg.Find(slug)
	if err != nil {
		return err
	}
	root, err := storage.Connect(ctx, reg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer root.Close()
	return root.MigrateInSchema(ctx, findMigrationsDir(), p.StorageSchema)
}
```

Note: `findMigrationsDir` is defined in `cmd/projectlens/main.go` (same package) so the reference resolves at build time. The test in this task only exercises `validateMutex`, so the package will compile and tests pass without any flag-registered cobra root.

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/projectlens/ -run validateMutex`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/projectlens/projectctx.go cmd/projectlens/projectctx_test.go
git commit -m "feat(cli): openCmdStorage helper unifies project + legacy storage entry points"
```

---

### Task 8 (REVISED, SUPERSEDES original Task 8): Wire `--project` flags + use `openCmdStorage` in lock wrappers

`resolveProjectRuntime` + `migrateProjectSchemaFromFlags` already added in
Task 7.5. This task only adds the persistent flag definitions and switches
lock wrappers to `openCmdStorage`.

**Files:**
- Modify: `cmd/projectlens/main.go` (persistent flags)
- Modify: `cmd/projectlens/lock.go` (use `openCmdStorage`)

- [ ] **Step 1: Add persistent flags**

In `cmd/projectlens/main.go` after existing persistent flags:

```go
rootCmd.PersistentFlags().String("project", "", "project slug from the registry")
rootCmd.PersistentFlags().String("projects", "configs/projects.yaml", "path to project registry YAML")
```

- [ ] **Step 2: Rewrite lock wrappers to use `openCmdStorage`**

```go
// cmd/projectlens/lock.go
func withWriteLock(cmdName string, run LockedCmd) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		cs, err := openCmdStorage(ctx, cmd)
		if err != nil {
			return err
		}
		defer cs.Close()
		lock, err := acquireOrExit(ctx, cs.DB(), cmdName)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Release(context.Background()) }()
		return run(ctx, cmd, cs.DB(), cs.Config(), cs.RepoPath())
	}
}

func withWriteLockAfterMigrate(cmdName string, run LockedCmd) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		// Validate mutex BEFORE any I/O.
		projectMode, err := validateMutex(cmd)
		if err != nil {
			return err
		}
		if projectMode {
			if err := migrateProjectSchemaFromFlags(ctx, cmd); err != nil {
				return err
			}
		}
		cs, err := openCmdStorage(ctx, cmd)
		if err != nil {
			return err
		}
		defer cs.Close()
		if !projectMode {
			if err := cs.DB().Migrate(ctx, findMigrationsDir()); err != nil {
				return fmt.Errorf("running migrations: %w", err)
			}
		}
		lock, err := acquireOrExit(ctx, cs.DB(), cmdName)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Release(context.Background()) }()
		return run(ctx, cmd, cs.DB(), cs.Config(), cs.RepoPath())
	}
}
```

- [ ] **Step 3: Build + vet**

```bash
make vet
make build
```

- [ ] **Step 4: Commit**

```bash
git add cmd/projectlens/main.go cmd/projectlens/lock.go
git commit -m "feat(cli): centralize project/legacy storage resolution via openCmdStorage"
```

---

### Task 8.5 (NEW): Migrate every direct `storage.Connect` call site to `openCmdStorage`

Resolves review finding #1 — read/report/knowledge/maintenance paths must not stay on `public`.

**Files (all in `cmd/projectlens/`):**
- `main.go`: `status` (line 208), `inspect-symbol` (line 261), `inspect-package` (line 361), `query` (line 420)
- `report.go` (line 38)
- `export.go` (line 44)
- `knowledge.go` (lines 33, 77, 113, 150)
- `lock.go` `newUnlockCmd` (line 115)

- [ ] **Step 1: For each call site, replace the pattern**

Before:

```go
cfg, _, err := loadCmdConfig(cmd)
if err != nil { return err }
db, err := storage.Connect(ctx, cfg.DatabaseURL)
if err != nil { return fmt.Errorf("connecting to database: %w", err) }
defer db.Close()
```

After:

```go
cs, err := openCmdStorage(ctx, cmd)
if err != nil { return err }
defer cs.Close()
db := cs.DB()
cfg := cs.Config() // only if the body still needs cfg
```

For commands that need `repoPath` (e.g., report, export, status), use `cs.RepoPath()`.

- [ ] **Step 2: Per-command sanity test**

For each command family, add a unit test under `cmd/projectlens/` that exercises
the flag parsing path (use a fake registry under a temp dir, verify that
`--project x --repo /y` returns the mutex error before any DB call):

```go
// cmd/projectlens/cli_mutex_test.go
package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func runWithFlags(t *testing.T, factory func() *cobra.Command, flags ...string) error {
	t.Helper()
	c := factory()
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("project", "", "")
	root.PersistentFlags().String("projects", "configs/projects.yaml", "")
	root.PersistentFlags().String("repo", "", "")
	root.PersistentFlags().String("config", "configs/index.yaml", "")
	root.PersistentFlags().String("db", "", "")
	root.AddCommand(c)
	root.SetArgs(append([]string{c.Use}, flags...))
	return root.Execute()
}

func TestStatusRejectsProjectAndRepo(t *testing.T) {
	err := runWithFlags(t, newStatusCmd, "--project", "foo", "--repo", "/tmp")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("got %v", err)
	}
}

func TestReportRejectsProjectAndRepo(t *testing.T) {
	err := runWithFlags(t, newReportCmd, "--project", "foo", "--repo", "/tmp")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("got %v", err)
	}
}
// repeat for: newExportCmd, newKnowledgeCmd subcommands, newQueryCmd,
// newInspectSymbolCmd, newInspectPackageCmd, newUnlockCmd, newMigrateCmd.
```

- [ ] **Step 3: Run**

Run: `go test ./cmd/projectlens/ -run RejectsProjectAndRepo`
Expected: PASS — proves every command routes through `validateMutex`.

- [ ] **Step 4: Commit**

```bash
git add cmd/projectlens/*.go
git commit -m "refactor(cli): route every storage-opening command through openCmdStorage"
```

---

### Task 9 (REVISED): `migrate --project` calls central mutex check

Supersedes the original Task 9 body. Replace `newMigrateCmd` with:

```go
func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending SQL migrations (per project schema when --project is set)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			projectMode, err := validateMutex(cmd)
			if err != nil {
				return err
			}
			if projectMode {
				if err := migrateProjectSchemaFromFlags(ctx, cmd); err != nil {
					return err
				}
				slug, _ := cmd.Flags().GetString("project")
				fmt.Printf("migrations up to date (project %s)\n", slug)
				return nil
			}
			// Legacy path.
			cfg, _, err := loadCmdConfig(cmd)
			if err != nil {
				return err
			}
			db, err := storage.Connect(ctx, cfg.DatabaseURL)
			if err != nil {
				return fmt.Errorf("connecting to database: %w", err)
			}
			defer db.Close()
			if err := db.Migrate(ctx, findMigrationsDir()); err != nil {
				return fmt.Errorf("running migrations: %w", err)
			}
			fmt.Println("migrations up to date (public schema)")
			return nil
		},
	}
}
```

Add a test:

```go
func TestMigrateRejectsProjectAndRepo(t *testing.T) {
	err := runWithFlags(t, newMigrateCmd, "--project", "foo", "--repo", "/tmp")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("got %v", err)
	}
}
```

Commit: `feat(cli): migrate --project enforces --project/--repo mutex centrally`.

---

### Task 13 (REVISED): Configured-but-broken project returns 503 with actionable error, NOT 404

Resolves review finding #3. Two distinct behaviors:

- **Unknown** project slug (`/wat/mcp`) → HTTP 404.
- **Known** project whose runtime failed to open (e.g., schema missing) → mount a fallback handler that responds with HTTP 503 + JSON body pointing to `projectlens migrate --project <slug>`.

**Files:**
- Modify: `cmd/projectlens-mcp/main.go` (`runMultiProject`)
- Modify: `internal/mcpserver/multi_project_integration_test.go` (add broken-project test)

- [ ] **Step 1: Replace broken-project skip with a readiness fallback handler**

In `runMultiProject`, replace the `continue` branch with:

```go
for _, p := range reg.Projects {
	rt, err := projects.Resolve(ctx, reg, p.Slug)
	mount := "/" + p.Slug + "/mcp"
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: project %q not ready: %v\n", p.Slug, err)
		stub := mcpserver.NotReadyHandler(p.Slug, err)
		mux.Handle(mount, stub)
		mux.Handle(mount+"/", stub)
		continue
	}
	cleanups = append(cleanups, rt.Close)
	srv, ierr := buildProjectServer(rt, port)
	if ierr != nil {
		fmt.Fprintf(os.Stderr, "warn: project %q server init failed: %v\n", p.Slug, ierr)
		rt.Close()
		stub := mcpserver.NotReadyHandler(p.Slug, ierr)
		mux.Handle(mount, stub)
		mux.Handle(mount+"/", stub)
		continue
	}
	handler := srv.Handler()
	mux.Handle(mount, http.StripPrefix(mount, handler))
	mux.Handle(mount+"/", http.StripPrefix(mount, handler))
	fmt.Printf("mounted %s -> storage_schema=%s repo=%s\n", mount, p.StorageSchema, p.RepoPath)
}
```

- [ ] **Step 2: Add `NotReadyHandler` to mcpserver**

```go
// internal/mcpserver/not_ready.go
package mcpserver

import (
	"encoding/json"
	"net/http"
	"strings"
)

// NotReadyHandler returns an http.Handler that responds with 503 and a JSON
// body explaining how to bring the project online. Used when a configured
// project's runtime cannot be opened (e.g., storage schema missing).
func NotReadyHandler(slug string, cause error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		hint := "projectlens migrate --project " + slug
		body := map[string]string{
			"error":   "project not ready",
			"project": slug,
			"cause":   strings.TrimSpace(cause.Error()),
			"hint":    hint,
		}
		_ = json.NewEncoder(w).Encode(body)
	})
}
```

- [ ] **Step 3: Add the integration test**

```go
// internal/mcpserver/multi_project_integration_test.go (append)
func TestKnownButBrokenProjectReturns503(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	// No schema created — Resolve will fail at ConnectScoped's existence check.
	reg := &projects.Registry{
		DatabaseURL: url,
		Projects:    []projects.Project{{Slug: "b", StorageSchema: "ri_b_not_exist", RepoPath: "/tmp/x"}},
	}
	_, err := projects.Resolve(ctx, reg, "b")
	if err == nil {
		t.Fatal("expected resolve error")
	}
	stub := mcpserver.NotReadyHandler("b", err)
	mux := http.NewServeMux()
	mux.Handle("/b/mcp", stub)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	resp, _ := http.Get(ts.URL + "/b/mcp")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("broken project: status=%d want 503", resp.StatusCode)
	}
}
```

- [ ] **Step 4: Per-project session manager separation test**

```go
func TestPerProjectSessionManagerSeparation(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" { t.Skip("DATABASE_URL not set") }
	ctx := context.Background()
	root, _ := storage.Connect(ctx, url)
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_sess_a CASCADE`)
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_sess_b CASCADE`)
	})
	dir := findMigrationsDirForTest(t)
	_ = root.MigrateInSchema(ctx, dir, "ri_sess_a")
	_ = root.MigrateInSchema(ctx, dir, "ri_sess_b")
	rtA, _ := projects.Resolve(ctx, &projects.Registry{DatabaseURL: url,
		Projects: []projects.Project{{Slug: "a", StorageSchema: "ri_sess_a", RepoPath: "/tmp/x"}}}, "a")
	rtB, _ := projects.Resolve(ctx, &projects.Registry{DatabaseURL: url,
		Projects: []projects.Project{{Slug: "b", StorageSchema: "ri_sess_b", RepoPath: "/tmp/x"}}}, "b")
	defer rtA.Close(); defer rtB.Close()

	srvA := mcpserver.New(rtA.DB, retrieval.NewRouter(rtA.DB, nil), 0, rtA.RepoPath)
	srvB := mcpserver.New(rtB.DB, retrieval.NewRouter(rtB.DB, nil), 0, rtB.RepoPath)
	mux := http.NewServeMux()
	mux.Handle("/a/mcp", http.StripPrefix("/a/mcp", srvA.Handler()))
	mux.Handle("/b/mcp", http.StripPrefix("/b/mcp", srvB.Handler()))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// initialize request body shape per Streamable HTTP MCP.
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	for _, path := range []string{"/a/mcp", "/b/mcp"} {
		resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(initBody))
		if err != nil { t.Fatalf("%s: %v", path, err) }
		if resp.StatusCode >= 400 {
			t.Fatalf("%s: status=%d", path, resp.StatusCode)
		}
	}
}
```

- [ ] **Step 5: Commits**

```bash
git add internal/mcpserver/not_ready.go cmd/projectlens-mcp/main.go internal/mcpserver/multi_project_integration_test.go
git commit -m "feat(mcp): configured-but-broken project returns 503 with migrate hint"
```

---

### Task 14 (REVISED to address review findings #5 and #7): Project-scoped logger reaches every indexer/job log line

Two helpers — `WithProject` for callers that hold a logger reference, plus
`Bind` for the CLI's package-level callers (`logger.Stage`, `logger.Info`,
`recordStageRun`, indexer stage callbacks). `Bind` rebinds the package
global `L` for the duration of one CLI command, so every package-level
`logger.X(...)` call inside that command inherits `project_slug` +
`storage_schema`.

The CLI is single-shot per process, so swapping `L` during one command is
safe (no other command runs concurrently in the same process). The MCP
server NEVER calls `logger.Bind`; it routes through per-request scoped
loggers in `loggingHooks` instead.

**Files:**
- Modify: `internal/logger/logger.go`
- Modify: `cmd/projectlens/lock.go`
- Modify: `internal/mcpserver/server.go`
- Modify: `cmd/projectlens-mcp/main.go`

- [ ] **Step 1: Add `WithProject` + `Bind` to logger**

Rewrite `internal/logger/logger.go` top to keep one consistent name for the
charmbracelet import (rename existing `"github.com/charmbracelet/log"` to
`charmlog "github.com/charmbracelet/log"` everywhere in this file):

```go
// internal/logger/logger.go (append below existing definitions)

// WithProject returns a derived logger carrying project_slug + storage_schema.
// Pass empty strings to get the current L unchanged. Use when the caller has
// a logger reference and wants per-call scope.
func WithProject(slug, schema string) *charmlog.Logger {
	if slug == "" && schema == "" {
		return L
	}
	return L.With("project_slug", slug, "storage_schema", schema)
}

// Bind rebinds the package-level L to a derived logger that carries
// project_slug + storage_schema. Returns a restore function that the
// caller MUST defer. Bind is intended for one-shot CLI commands so
// package-level helpers (Info/Warn/Error/Stage/Step/Progress) and indexer
// stage callbacks (e.g. recordStageRun) inherit the project fields without
// each call site needing a logger handle.
//
// Bind is NOT safe to call from a concurrent server context — the MCP server
// must instead pass a scoped logger explicitly through its handlers. CLI
// processes run one command per process, so the rebind window is the whole
// process lifetime and there is no contention.
func Bind(slug, schema string) (restore func()) {
	prev := L
	L = WithProject(slug, schema)
	return func() { L = prev }
}
```

- [ ] **Step 2: Call `Bind` in CLI lock wrappers**

After `cs` is obtained in `withWriteLock` AND `withWriteLockAfterMigrate`,
before calling `run(...)`:

```go
restore := logger.Bind(cs.Slug(), cs.StorageSchema())
defer restore()
logger.Info("storage ready", "repo", cs.RepoPath())
```

Every downstream package-level log call (`logger.Stage`, `logger.Info`,
`logger.Warn`, `recordStageRun`'s warns at `cmd/projectlens/main.go:541`,
`logger.Stage` calls at `cmd/projectlens/main.go:734`+, indexer goroutine
logs) now inherits `project_slug` and `storage_schema`. No call-site changes
needed.

For commands that do not use the lock wrappers (read-only paths after Task
8.5 routes them through `openCmdStorage`), also call `logger.Bind` once
after `openCmdStorage` and defer the restore. Recommend adding it inside
`openCmdStorage` itself so every CLI command body benefits without per-call
boilerplate:

```go
// In openCmdStorage, before returning *CmdStorage, bind once. Stash the
// restore function on CmdStorage so Close() runs it.
restore := logger.Bind(cs.Slug(), cs.StorageSchema())
cs.cleanup = func() {
	restore()
	if cs.Runtime != nil { cs.Runtime.Close() }
	if cs.LegacyDB != nil { cs.LegacyDB.Close() }
}
```

(This makes `Close()` call `cleanup` instead of the per-branch close logic
already drafted in Task 7.5. Adjust the `Close()` body accordingly.)

- [ ] **Step 3: Use it in MCP server logging hooks**

Add field to `mcpserver.Server`:

```go
type Server struct {
	// ... existing fields ...
	slug          string
	storageSchema string
}
```

Set via a new constructor option:

```go
func (s *Server) WithProjectIdentity(slug, schema string) *Server {
	s.slug = slug
	s.storageSchema = schema
	return s
}
```

In `loggingHooks` (existing in `internal/mcpserver/server.go`), replace
`log.Printf("tool call …")` with logger.WithProject-derived calls:

```go
import "github.com/hman-pro/projectlens/internal/logger"

projLog := logger.WithProject(s.slug, s.storageSchema)
hooks.AddBeforeCallTool(func(_ context.Context, id any, msg *mcp.CallToolRequest) {
	starts.Store(id, time.Now())
	args, _ := json.Marshal(msg.Params.Arguments)
	projLog.Info("tool call", "name", msg.Params.Name, "args", string(args))
})
// AfterCallTool + OnError similarly.
```

Drop the legacy stdlib `log` import once all sites converted.

In `cmd/projectlens-mcp/main.go::buildProjectServer`, set identity:

```go
srv := mcpserver.New(rt.DB, router, port, rt.RepoPath).
	WithSummarizer(newSummarizerProber(cfg)).
	WithProjectIdentity(rt.Slug, rt.StorageSchema)
```

- [ ] **Step 4: Build + vet + test**

```bash
make vet && make build && make test
```

- [ ] **Step 5: Commit**

```bash
git add internal/logger/logger.go cmd/projectlens/ internal/mcpserver/server.go cmd/projectlens-mcp/main.go
git commit -m "feat(observability): WithProject logger threads project_slug + storage_schema into CLI and MCP"
```

---

### Task 5 (EXPANDED): Cover `symbols`, `index_runs`, `knowledge_entries` in isolation tests

Resolves review test gap. Add to `db_isolation_integration_test.go`:

```go
func TestSymbolsIndexRunsKnowledgeDoNotCrossSchemas(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	root, _ := storage.Connect(ctx, url)
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_iso2_a CASCADE`)
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_iso2_b CASCADE`)
	})
	dir := findMigrationsDirForTest(t)
	for _, s := range []string{"ri_iso2_a", "ri_iso2_b"} {
		if err := root.MigrateInSchema(ctx, dir, s); err != nil {
			t.Fatal(err)
		}
	}
	dbA, _ := storage.ConnectScoped(ctx, url, "ri_iso2_a")
	defer dbA.Close()
	dbB, _ := storage.ConnectScoped(ctx, url, "ri_iso2_b")
	defer dbB.Close()

	// Insert a file in A (required for symbol FK), then a symbol.
	var fileID int64
	_ = dbA.Pool.QueryRow(ctx,
		`INSERT INTO files(path, package_name, checksum, commit_sha) VALUES ('s.go','pkg/a','x','c') RETURNING id`,
	).Scan(&fileID)
	_, _ = dbA.Pool.Exec(ctx,
		`INSERT INTO symbols(file_id, name, kind, line_start, line_end, signature) VALUES ($1,'F','func',1,1,'')`,
		fileID)
	_, _ = dbA.Pool.Exec(ctx,
		`INSERT INTO index_runs(commit_sha, status, started_at, stage) VALUES ('c','running', NOW(), 'parse')`)
	_, _ = dbA.Pool.Exec(ctx,
		`INSERT INTO knowledge_entries(category, title, body) VALUES ('cat','t','b')`)

	checks := []struct {
		name string
		tbl  string
	}{
		{"symbols", "symbols"},
		{"index_runs", "index_runs"},
		{"knowledge_entries", "knowledge_entries"},
	}
	for _, c := range checks {
		var n int
		if err := dbB.Pool.QueryRow(ctx, "SELECT count(*) FROM "+c.tbl).Scan(&n); err != nil {
			t.Fatalf("count %s from B: %v", c.tbl, err)
		}
		if n != 0 {
			t.Errorf("expected 0 %s visible from B, got %d", c.tbl, n)
		}
	}
}
```

Commit with: `test(storage): isolation across symbols/index_runs/knowledge_entries`.

If exact column names differ from the migrations in this repo, adjust to match
`migrations/001_initial_schema.up.sql`, `migrations/002_intelligence_platform.up.sql`,
and `migrations/004_knowledge_layer.up.sql`.

---

### Task 17 (NEW): TUI project resolution

Resolves review finding #4. Spec requires TUI to use the same resolution path
as the CLI (`docs/superpowers/specs/2026-05-25-multi-project-isolation-design.md:353`).

**Files:**
- Modify: `cmd/projectlens-tui/main.go`
- Modify: `internal/tui/jobs/runner.go` (if `RunnerTarget` needs `ProjectSlug`)

- [ ] **Step 1: Detect registry; resolve active project**

Resolution rules in `run()`:

1. Parse `--project` from CLI args (cobra-style or env var `PROJECT`).
2. Look for `configs/projects.yaml` (or `--projects` override). If absent → legacy single-project path (current behavior).
3. If registry present:
   - If `--project` passed → use it.
   - Else if `reg.DefaultProject` set → use it.
   - Else if exactly one project in registry → use it.
   - Else → fail with: "multiple projects configured; pass --project or set default_project in projects.yaml".
4. After resolution: open `projects.Runtime` and build `RunnerTarget` with the project's repo path + a new field `ProjectSlug` so launched `projectlens` jobs inherit `--project <slug>`.

- [ ] **Step 2: Code**

```go
// cmd/projectlens-tui/main.go (rewrite of run())
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	projectsPath := getEnvOr("PROJECTS_PATH", "configs/projects.yaml")
	slugFlag := os.Getenv("PROJECT")
	// (Optional: parse explicit --project flag from os.Args if you keep TUI flag-less today.)

	var (
		pool        *pgxpool.Pool
		cfg         *config.Config
		repoPath    string
		dbURL       string
		projectSlug string
		runtimeDone func()
	)

	if _, err := os.Stat(projectsPath); err == nil {
		reg, rerr := projects.LoadRegistry(projectsPath)
		if rerr != nil {
			return fmt.Errorf("load registry: %w", rerr)
		}
		slug, serr := pickActiveProject(reg, slugFlag)
		if serr != nil {
			return serr
		}
		rt, rerr := projects.Resolve(ctx, reg, slug)
		if rerr != nil {
			return rerr
		}
		runtimeDone = rt.Close
		pool = rt.DB.Pool
		cfg = rt.Config
		repoPath = rt.RepoPath
		dbURL = reg.DatabaseURL
		projectSlug = rt.Slug
	} else {
		// Legacy single-project path.
		cfgPath := getEnvOr("CONFIG_PATH", "configs/index.yaml")
		legacyCfg, lerr := config.Load(cfgPath)
		if lerr != nil {
			return fmt.Errorf("load config: %w", lerr)
		}
		if legacyCfg.DatabaseURL == "" {
			return fmt.Errorf("DATABASE_URL is required (set in .env or config)")
		}
		p, perr := pgxpool.New(ctx, legacyCfg.DatabaseURL)
		if perr != nil {
			return fmt.Errorf("connect db: %w", perr)
		}
		runtimeDone = p.Close
		pool = p
		cfg = legacyCfg
		repoPath = legacyCfg.RepoPath
		dbURL = legacyCfg.DatabaseURL
	}
	defer runtimeDone()

	s := store.NewPG(pool, cfg, repoPath)
	// ... unchanged section setup ...

	binPath, berr := jobs.ResolveBinary()
	if berr != nil {
		log.Printf("projectlens binary not resolvable: %v", berr)
	}
	target := jobs.RunnerTarget{
		BinaryPath:  binPath,
		ConfigPath:  getEnvOr("CONFIG_PATH", "configs/index.yaml"),
		DatabaseURL: dbURL,
		RepoPath:    repoPath,
		ProjectSlug: projectSlug, // NEW field
	}
	// ... unchanged ...
}

func pickActiveProject(reg *projects.Registry, explicit string) (string, error) {
	if explicit != "" {
		if _, err := reg.Find(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	if reg.DefaultProject != "" {
		return reg.DefaultProject, nil
	}
	if len(reg.Projects) == 1 {
		return reg.Projects[0].Slug, nil
	}
	return "", fmt.Errorf("multiple projects configured; pass --project or set default_project in projects.yaml")
}
```

- [ ] **Step 3: Plumb `ProjectSlug` into job invocations**

In `internal/tui/jobs/runner.go`, when constructing the `exec.Cmd` for the
`projectlens` binary, append `--project <slug>` (and `--projects <path>` if you
ship the registry path explicitly) BEFORE the subcommand arguments when
`target.ProjectSlug != ""`.

- [ ] **Step 4: Manual smoke**

```bash
PROJECTS_PATH=configs/projects.yaml ./bin/projectlens-tui
# fail if no default_project and >1 projects
PROJECTS_PATH=configs/projects.yaml PROJECT=ingest ./bin/projectlens-tui
# opens with ingest scope
```

- [ ] **Step 5: Commit**

```bash
git add cmd/projectlens-tui/main.go internal/tui/jobs/
git commit -m "feat(tui): resolve active project via registry and pass --project to jobs"
```

---

## Revised Execution Order

Run tasks in this final order:

1. Phase A: 1 → 2 → 3 → 4 → 5 (+5 expansion)
2. Phase B: 6 → 7
3. **7.5 (new)** → 8 (revised) → **8.5 (new)** → 9 (revised) → 10 → 11
4. Phase D: 12 → 13 (revised)
5. Phase E: 14 (revised) → 15 → **17 (new TUI)** → 16

## Updated Self-Review

- Review finding #1 (universal storage routing) → Tasks 7.5, 8.5.
- Review finding #2 (mutex bypass in migrate) → Task 8 + 9 revisions reuse `validateMutex`.
- Review finding #3 (broken-project 503 vs 404) → Task 13 revision + new test.
- Review finding #4 (TUI) → Task 17.
- Review finding #5 (logger) → Task 14 revision with concrete charmbracelet API.
- Review finding #6 (Task 7.5 compile gap) → Task 7.5 now defines `resolveProjectRuntime` + `migrateProjectSchemaFromFlags` in the same file as `openCmdStorage`; revised Task 8 only adds flags + lock-wrapper rewrites.
- Review finding #7 (indexer logs unscoped) → Task 14 adds `logger.Bind` that rebinds package-level `L` for one CLI command; integrated into `openCmdStorage.Close()` so every command body — including `logger.Stage`, `recordStageRun`, indexer stage callbacks — inherits project fields without per-call-site edits.
- Test gaps: Task 5 expansion + Task 13 step 4 (per-project session manager separation).

## Execution Handoff

Plan saved to `docs/superpowers/plans/2026-05-25-multi-project-isolation.md`. Two execution options:

**1. Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration on the 18-task plan.

**2. Inline Execution** — execute the tasks in this session via `superpowers:executing-plans`, batched with checkpoints at the end of each phase (A → B → C → D → E).

Which approach?
