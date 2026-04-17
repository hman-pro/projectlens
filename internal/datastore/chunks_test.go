package datastore

import (
	"strings"
	"testing"
)

func TestBuildTableChunk_MultipleColumnsReadersWriters(t *testing.T) {
	table := TableDef{
		Name:       "sets",
		Schema:     "rounding",
		SourceFile: "000217_rounding_sets.up.sql",
		Columns: []ColumnDef{
			{Name: "id", Type: "SERIAL", IsPrimaryKey: true},
			{Name: "project_id", Type: "INTEGER"},
			{Name: "uuid", Type: "UUID", Default: "gen_random_uuid()"},
			{Name: "name", Type: "TEXT"},
			{Name: "criteria", Type: "JSONB", IsNullable: true},
		},
	}

	readers := []SQLRef{
		{Table: "rounding.sets", Operation: "SELECT", FuncName: "ListSets", FilePath: "core/rounding/pgstore/store.go"},
		{Table: "rounding.sets", Operation: "SELECT", FuncName: "GetSetByID", FilePath: "core/rounding/pgstore/store.go"},
	}

	writers := []SQLRef{
		{Table: "rounding.sets", Operation: "INSERT", FuncName: "CreateSet", FilePath: "core/rounding/pgstore/store.go"},
		{Table: "rounding.sets", Operation: "UPDATE", FuncName: "UpdateSet", FilePath: "core/rounding/pgstore/store.go"},
		{Table: "rounding.sets", Operation: "DELETE", FuncName: "DeleteSet", FilePath: "core/rounding/pgstore/store.go"},
	}

	got := BuildTableChunk(table, readers, writers)

	// Verify header.
	mustContain(t, got, "Table: rounding.sets")
	mustContain(t, got, "Schema: rounding")
	mustContain(t, got, "Created by migration: 000217_rounding_sets.up.sql")

	// Verify DDL.
	mustContain(t, got, "CREATE TABLE rounding.sets (")
	mustContain(t, got, "id SERIAL NOT NULL PRIMARY KEY")
	mustContain(t, got, "project_id INTEGER NOT NULL")
	mustContain(t, got, "uuid UUID NOT NULL DEFAULT gen_random_uuid()")
	mustContain(t, got, "name TEXT NOT NULL")
	mustContain(t, got, "criteria JSONB")

	// Verify FK section says none.
	mustContain(t, got, "Foreign keys:")
	mustContain(t, got, "(none)")

	// Verify readers.
	mustContain(t, got, "Read by:")
	mustContain(t, got, "GetSetByID (core/rounding/pgstore/store.go)")
	mustContain(t, got, "ListSets (core/rounding/pgstore/store.go)")

	// Verify writers.
	mustContain(t, got, "Written by:")
	mustContain(t, got, "CreateSet (core/rounding/pgstore/store.go) — INSERT")
	mustContain(t, got, "UpdateSet (core/rounding/pgstore/store.go) — UPDATE")
	mustContain(t, got, "DeleteSet (core/rounding/pgstore/store.go) — DELETE")
}

func TestBuildTableChunk_NoReadersNoWriters(t *testing.T) {
	table := TableDef{
		Name:       "audit_log",
		Schema:     "core",
		SourceFile: "000050_audit_log.up.sql",
		Columns: []ColumnDef{
			{Name: "id", Type: "BIGSERIAL", IsPrimaryKey: true},
			{Name: "event", Type: "TEXT"},
		},
	}

	got := BuildTableChunk(table, nil, nil)

	mustContain(t, got, "Read by:")
	mustContain(t, got, "(none discovered)")
	mustContain(t, got, "Written by:")

	// Ensure "(none discovered)" appears twice (once for readers, once for writers).
	count := strings.Count(got, "(none discovered)")
	if count != 2 {
		t.Errorf("expected '(none discovered)' to appear 2 times, got %d\nchunk:\n%s", count, got)
	}
}

func TestBuildTableChunk_WithForeignKeys(t *testing.T) {
	table := TableDef{
		Name:       "order_items",
		Schema:     "sales",
		SourceFile: "000100_order_items.up.sql",
		Columns: []ColumnDef{
			{Name: "id", Type: "SERIAL", IsPrimaryKey: true},
			{Name: "order_id", Type: "INTEGER", ForeignKey: "sales.orders(id)"},
			{Name: "product_id", Type: "INTEGER", ForeignKey: "catalog.products(id)"},
			{Name: "quantity", Type: "INTEGER"},
		},
	}

	got := BuildTableChunk(table, nil, nil)

	mustContain(t, got, "Foreign keys:")
	mustContain(t, got, "order_id → sales.orders(id)")
	mustContain(t, got, "product_id → catalog.products(id)")

	// Should NOT contain "(none)" in the FK section.
	fkIdx := strings.Index(got, "Foreign keys:")
	readIdx := strings.Index(got, "Read by:")
	fkSection := got[fkIdx:readIdx]
	if strings.Contains(fkSection, "(none)") {
		t.Errorf("FK section should not contain '(none)' when foreign keys exist\nsection:\n%s", fkSection)
	}
}

func TestBuildTableChunk_EmptySchema(t *testing.T) {
	table := TableDef{
		Name:       "migrations",
		Schema:     "",
		SourceFile: "000001_init.up.sql",
		Columns: []ColumnDef{
			{Name: "id", Type: "SERIAL", IsPrimaryKey: true},
			{Name: "version", Type: "TEXT"},
		},
	}

	got := BuildTableChunk(table, nil, nil)

	// Table name should NOT be schema-qualified.
	mustContain(t, got, "Table: migrations")
	mustContain(t, got, "CREATE TABLE migrations (")

	// Should NOT contain "Schema:" line.
	if strings.Contains(got, "Schema:") {
		t.Errorf("expected no 'Schema:' line for public schema table\nchunk:\n%s", got)
	}
}

func TestBuildTableChunk_ColumnAllAttributes(t *testing.T) {
	table := TableDef{
		Name:       "tokens",
		Schema:     "auth",
		SourceFile: "000010_tokens.up.sql",
		Columns: []ColumnDef{
			{
				Name:         "id",
				Type:         "UUID",
				IsPrimaryKey: true,
				IsNullable:   false,
				Default:      "gen_random_uuid()",
				ForeignKey:   "auth.users(id)",
			},
		},
	}

	got := BuildTableChunk(table, nil, nil)

	// Verify the column line includes all attributes.
	mustContain(t, got, "id UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid() REFERENCES auth.users(id)")

	// Verify FK section.
	mustContain(t, got, "id → auth.users(id)")
}

// mustContain is a test helper that fails if substr is not found in s.
func mustContain(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected output to contain %q\ngot:\n%s", substr, s)
	}
}
