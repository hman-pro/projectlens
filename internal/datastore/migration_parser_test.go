package datastore

import (
	"testing"
)

func TestCreateTableWithSchemaPrefix(t *testing.T) {
	files := []MigrationFile{
		{
			Name: "000001_create_sets.up.sql",
			SQL: `CREATE TABLE rounding.sets (
				id SERIAL PRIMARY KEY,
				project_id INTEGER NOT NULL,
				uuid UUID NOT NULL DEFAULT gen_random_uuid(),
				name TEXT NOT NULL,
				criteria JSONB,
				UNIQUE (project_id, name)
			);`,
		},
	}

	tables := ParseMigrations(files)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := tables[0]
	if tbl.Schema != "rounding" {
		t.Errorf("expected schema 'rounding', got %q", tbl.Schema)
	}
	if tbl.Name != "sets" {
		t.Errorf("expected name 'sets', got %q", tbl.Name)
	}
	if tbl.SourceFile != "000001_create_sets.up.sql" {
		t.Errorf("expected source file '000001_create_sets.up.sql', got %q", tbl.SourceFile)
	}
	if len(tbl.Columns) != 5 {
		t.Fatalf("expected 5 columns, got %d", len(tbl.Columns))
	}

	// id SERIAL PRIMARY KEY
	assertColumn(t, tbl.Columns[0], "id", "SERIAL", false, true, "", "")
	// project_id INTEGER NOT NULL
	assertColumn(t, tbl.Columns[1], "project_id", "INTEGER", false, false, "", "")
	// uuid UUID NOT NULL DEFAULT gen_random_uuid()
	assertColumn(t, tbl.Columns[2], "uuid", "UUID", false, false, "gen_random_uuid()", "")
	// name TEXT NOT NULL
	assertColumn(t, tbl.Columns[3], "name", "TEXT", false, false, "", "")
	// criteria JSONB (nullable)
	assertColumn(t, tbl.Columns[4], "criteria", "JSONB", true, false, "", "")
}

func TestAlterTableAddColumnMerge(t *testing.T) {
	files := []MigrationFile{
		{
			Name: "000001_create_requests.up.sql",
			SQL: `CREATE TABLE approval.requests (
				id SERIAL PRIMARY KEY,
				status TEXT NOT NULL
			);`,
		},
		{
			Name: "000002_add_deal_columns.up.sql",
			SQL:  `ALTER TABLE approval.requests ADD COLUMN deal_supplier_id text, ADD COLUMN deal_uuid uuid;`,
		},
	}

	tables := ParseMigrations(files)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := tables[0]
	if tbl.Schema != "approval" {
		t.Errorf("expected schema 'approval', got %q", tbl.Schema)
	}
	if tbl.Name != "requests" {
		t.Errorf("expected name 'requests', got %q", tbl.Name)
	}
	if len(tbl.Columns) != 4 {
		t.Fatalf("expected 4 columns, got %d: %+v", len(tbl.Columns), tbl.Columns)
	}

	assertColumn(t, tbl.Columns[0], "id", "SERIAL", false, true, "", "")
	assertColumn(t, tbl.Columns[1], "status", "TEXT", false, false, "", "")
	assertColumn(t, tbl.Columns[2], "deal_supplier_id", "text", true, false, "", "")
	assertColumn(t, tbl.Columns[3], "deal_uuid", "uuid", true, false, "", "")
}

func TestMultipleTablesFromMultipleFiles(t *testing.T) {
	files := []MigrationFile{
		{
			Name: "000001_create_plans.up.sql",
			SQL:  `CREATE TABLE plan.display_locations (id SERIAL PRIMARY KEY, name TEXT NOT NULL);`,
		},
		{
			Name: "000002_create_settings.up.sql",
			SQL:  `CREATE TABLE settings.preferences (id SERIAL PRIMARY KEY, key TEXT NOT NULL, value TEXT);`,
		},
		{
			Name: "000003_create_users.up.sql",
			SQL:  `CREATE TABLE users (id BIGINT PRIMARY KEY, email TEXT NOT NULL);`,
		},
	}

	tables := ParseMigrations(files)
	if len(tables) != 3 {
		t.Fatalf("expected 3 tables, got %d", len(tables))
	}

	// Tables should be sorted by schema.name.
	// plan.display_locations, settings.preferences, users
	if tables[0].Schema != "plan" || tables[0].Name != "display_locations" {
		t.Errorf("expected first table plan.display_locations, got %s.%s", tables[0].Schema, tables[0].Name)
	}
	if tables[1].Schema != "settings" || tables[1].Name != "preferences" {
		t.Errorf("expected second table settings.preferences, got %s.%s", tables[1].Schema, tables[1].Name)
	}
	if tables[2].Schema != "" || tables[2].Name != "users" {
		t.Errorf("expected third table users (public), got %q.%s", tables[2].Schema, tables[2].Name)
	}
	if len(tables[2].Columns) != 2 {
		t.Fatalf("expected 2 columns in users, got %d", len(tables[2].Columns))
	}
}

func TestIfNotExistsVariant(t *testing.T) {
	files := []MigrationFile{
		{
			Name: "000001_create_events.up.sql",
			SQL: `CREATE TABLE IF NOT EXISTS ai.events (
				id BIGINT PRIMARY KEY,
				payload JSONB NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT now()
			);`,
		},
	}

	tables := ParseMigrations(files)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := tables[0]
	if tbl.Schema != "ai" {
		t.Errorf("expected schema 'ai', got %q", tbl.Schema)
	}
	if tbl.Name != "events" {
		t.Errorf("expected name 'events', got %q", tbl.Name)
	}
	if len(tbl.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(tbl.Columns))
	}

	assertColumn(t, tbl.Columns[0], "id", "BIGINT", false, true, "", "")
	assertColumn(t, tbl.Columns[1], "payload", "JSONB", false, false, "", "")
	assertColumn(t, tbl.Columns[2], "created_at", "TIMESTAMPTZ", false, false, "now()", "")
}

func TestColumnWithReferences(t *testing.T) {
	files := []MigrationFile{
		{
			Name: "000001_create_orders.up.sql",
			SQL: `CREATE TABLE orders (
				id SERIAL PRIMARY KEY,
				customer_id INTEGER NOT NULL REFERENCES customers(id),
				product_id INTEGER REFERENCES products(id)
			);`,
		},
	}

	tables := ParseMigrations(files)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := tables[0]
	if len(tbl.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(tbl.Columns))
	}

	assertColumn(t, tbl.Columns[0], "id", "SERIAL", false, true, "", "")
	assertColumn(t, tbl.Columns[1], "customer_id", "INTEGER", false, false, "", "customers(id)")
	assertColumn(t, tbl.Columns[2], "product_id", "INTEGER", true, false, "", "products(id)")
}

func TestColumnWithDefaultExpression(t *testing.T) {
	files := []MigrationFile{
		{
			Name: "000001_create_tokens.up.sql",
			SQL: `CREATE TABLE dataread.tokens (
				id SERIAL PRIMARY KEY,
				token UUID NOT NULL DEFAULT gen_random_uuid(),
				is_active BOOLEAN NOT NULL DEFAULT true,
				created_at TIMESTAMPTZ DEFAULT now(),
				counter INTEGER NOT NULL DEFAULT 0
			);`,
		},
	}

	tables := ParseMigrations(files)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := tables[0]
	if len(tbl.Columns) != 5 {
		t.Fatalf("expected 5 columns, got %d", len(tbl.Columns))
	}

	assertColumn(t, tbl.Columns[0], "id", "SERIAL", false, true, "", "")
	assertColumn(t, tbl.Columns[1], "token", "UUID", false, false, "gen_random_uuid()", "")
	assertColumn(t, tbl.Columns[2], "is_active", "BOOLEAN", false, false, "true", "")
	assertColumn(t, tbl.Columns[3], "created_at", "TIMESTAMPTZ", true, false, "now()", "")
	assertColumn(t, tbl.Columns[4], "counter", "INTEGER", false, false, "0", "")
}

func TestMigrationsSortedByName(t *testing.T) {
	// Provide files in reverse order to verify sorting.
	files := []MigrationFile{
		{
			Name: "000002_alter.up.sql",
			SQL:  `ALTER TABLE plan.items ADD COLUMN weight NUMERIC;`,
		},
		{
			Name: "000001_create.up.sql",
			SQL:  `CREATE TABLE plan.items (id SERIAL PRIMARY KEY, name TEXT NOT NULL);`,
		},
	}

	tables := ParseMigrations(files)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := tables[0]
	if len(tbl.Columns) != 3 {
		t.Fatalf("expected 3 columns (after alter merge), got %d", len(tbl.Columns))
	}
	assertColumn(t, tbl.Columns[2], "weight", "NUMERIC", true, false, "", "")
}

func TestIgnoresNonDDLStatements(t *testing.T) {
	files := []MigrationFile{
		{
			Name: "000001_mixed.up.sql",
			SQL: `
				CREATE INDEX idx_name ON plan.items(name);
				GRANT SELECT ON plan.items TO readonly;
				ALTER TABLE plan.items OWNER TO admin;
				ALTER TABLE plan.items ENABLE ROW LEVEL SECURITY;
				CREATE POLICY item_policy ON plan.items USING (true);
				CREATE TABLE plan.items (id SERIAL PRIMARY KEY, name TEXT NOT NULL);
			`,
		},
	}

	tables := ParseMigrations(files)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	if len(tables[0].Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(tables[0].Columns))
	}
}

func TestTypeWithPrecision(t *testing.T) {
	files := []MigrationFile{
		{
			Name: "000001_precision.up.sql",
			SQL:  `CREATE TABLE metrics (id SERIAL PRIMARY KEY, score NUMERIC(10,2) NOT NULL, label VARCHAR(255));`,
		},
	}

	tables := ParseMigrations(files)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := tables[0]
	if len(tbl.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(tbl.Columns))
	}
	assertColumn(t, tbl.Columns[1], "score", "NUMERIC(10,2)", false, false, "", "")
	assertColumn(t, tbl.Columns[2], "label", "VARCHAR(255)", true, false, "", "")
}

func TestPublicSchemaTable(t *testing.T) {
	files := []MigrationFile{
		{
			Name: "000001_public.up.sql",
			SQL:  `CREATE TABLE sessions (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), user_id INTEGER NOT NULL);`,
		},
	}

	tables := ParseMigrations(files)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := tables[0]
	if tbl.Schema != "" {
		t.Errorf("expected empty schema for public table, got %q", tbl.Schema)
	}
	if tbl.Name != "sessions" {
		t.Errorf("expected name 'sessions', got %q", tbl.Name)
	}
	assertColumn(t, tbl.Columns[0], "id", "UUID", false, true, "gen_random_uuid()", "")
	assertColumn(t, tbl.Columns[1], "user_id", "INTEGER", false, false, "", "")
}

// assertColumn is a test helper that validates a ColumnDef against expected values.
func assertColumn(t *testing.T, col ColumnDef, name, colType string, nullable, pk bool, dflt, fk string) {
	t.Helper()
	if col.Name != name {
		t.Errorf("column name: expected %q, got %q", name, col.Name)
	}
	if col.Type != colType {
		t.Errorf("column %q type: expected %q, got %q", name, colType, col.Type)
	}
	if col.IsNullable != nullable {
		t.Errorf("column %q nullable: expected %v, got %v", name, nullable, col.IsNullable)
	}
	if col.IsPrimaryKey != pk {
		t.Errorf("column %q primary key: expected %v, got %v", name, pk, col.IsPrimaryKey)
	}
	if col.Default != dflt {
		t.Errorf("column %q default: expected %q, got %q", name, dflt, col.Default)
	}
	if col.ForeignKey != fk {
		t.Errorf("column %q foreign key: expected %q, got %q", name, fk, col.ForeignKey)
	}
}
