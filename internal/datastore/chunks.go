package datastore

import (
	"fmt"
	"sort"
	"strings"
)

// BuildTableChunk creates an LLM-optimized text representation of a database
// table, combining its schema with information about which code reads/writes it.
func BuildTableChunk(table TableDef, readers []SQLRef, writers []SQLRef) string {
	var b strings.Builder

	qualifiedName := tableKey(table.Schema, table.Name)

	// Header section.
	fmt.Fprintf(&b, "Table: %s\n", qualifiedName)
	if table.Schema != "" {
		fmt.Fprintf(&b, "Schema: %s\n", table.Schema)
	}
	fmt.Fprintf(&b, "Created by migration: %s\n", table.SourceFile)

	// DDL section.
	b.WriteString("\n")
	b.WriteString(buildDDL(table))
	b.WriteString("\n")

	// Foreign keys section.
	b.WriteString("\nForeign keys:\n")
	fks := collectForeignKeys(table)
	if len(fks) == 0 {
		b.WriteString("  (none)\n")
	} else {
		for _, fk := range fks {
			fmt.Fprintf(&b, "  %s → %s\n", fk.column, fk.target)
		}
	}

	// Readers section.
	b.WriteString("\nRead by:\n")
	writeRefList(&b, readers)

	// Writers section.
	b.WriteString("\nWritten by:\n")
	writeRefList(&b, writers)

	return b.String()
}

// buildDDL reconstructs a CREATE TABLE DDL statement from a TableDef.
func buildDDL(table TableDef) string {
	var b strings.Builder

	qualifiedName := tableKey(table.Schema, table.Name)
	fmt.Fprintf(&b, "CREATE TABLE %s (\n", qualifiedName)

	for i, col := range table.Columns {
		b.WriteString("  ")
		b.WriteString(col.Name)
		b.WriteString(" ")
		b.WriteString(col.Type)

		if !col.IsNullable {
			b.WriteString(" NOT NULL")
		}
		if col.IsPrimaryKey {
			b.WriteString(" PRIMARY KEY")
		}
		if col.Default != "" {
			fmt.Fprintf(&b, " DEFAULT %s", col.Default)
		}
		if col.ForeignKey != "" {
			fmt.Fprintf(&b, " REFERENCES %s", col.ForeignKey)
		}

		if i < len(table.Columns)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}

	b.WriteString(");")
	return b.String()
}

// foreignKey is a helper for collecting FK info.
type foreignKey struct {
	column string
	target string
}

// collectForeignKeys returns all foreign key relationships from the table columns.
func collectForeignKeys(table TableDef) []foreignKey {
	var fks []foreignKey
	for _, col := range table.Columns {
		if col.ForeignKey != "" {
			fks = append(fks, foreignKey{
				column: col.Name,
				target: col.ForeignKey,
			})
		}
	}
	return fks
}

// writeRefList writes a sorted list of SQL references to the builder.
// If no refs are present, writes "(none discovered)".
func writeRefList(b *strings.Builder, refs []SQLRef) {
	if len(refs) == 0 {
		b.WriteString("  (none discovered)\n")
		return
	}

	// Sort by function name for deterministic output.
	sorted := make([]SQLRef, len(refs))
	copy(sorted, refs)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].FuncName != sorted[j].FuncName {
			return sorted[i].FuncName < sorted[j].FuncName
		}
		return sorted[i].Operation < sorted[j].Operation
	})

	for _, ref := range sorted {
		fmt.Fprintf(b, "  - %s (%s) — %s\n", ref.FuncName, ref.FilePath, ref.Operation)
	}
}
