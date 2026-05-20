package datastore

import (
	"regexp"
	"sort"
	"strings"
)

// TableDef represents a database table extracted from migrations.
type TableDef struct {
	Name       string      `json:"name"`
	Schema     string      `json:"schema"` // e.g., "plan", "approval", "" for public
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
	ForeignKey   string `json:"foreign_key,omitempty"` // e.g., "customers(id)"
}

// MigrationFile is a parsed migration.
type MigrationFile struct {
	Name string
	SQL  string
}

// tableKey returns "schema.name" for schema-qualified tables, or just "name" for public.
func tableKey(schema, name string) string {
	if schema == "" {
		return name
	}
	return schema + "." + name
}

// ParseMigrations processes migration files in sorted order and returns the
// current schema state. ALTER TABLE modifications are merged into table defs.
func ParseMigrations(files []MigrationFile) []TableDef {
	// Sort files by name so migrations apply in order.
	sorted := make([]MigrationFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	tables := make(map[string]*TableDef)

	for _, f := range sorted {
		parseCreateTables(f, tables)
		parseAlterTables(f, tables)
	}

	result := make([]TableDef, 0, len(tables))
	for _, t := range tables {
		result = append(result, *t)
	}
	sort.Slice(result, func(i, j int) bool {
		return tableKey(result[i].Schema, result[i].Name) < tableKey(result[j].Schema, result[j].Name)
	})
	return result
}

// createTableRe matches CREATE TABLE statements. It captures:
//   - optional schema and table name
//   - the column definition body inside parentheses
//
// We use a two-pass approach: first find the CREATE TABLE header, then
// manually extract the balanced parenthesized body.
var createTableHeaderRe = regexp.MustCompile(
	`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` +
		`(?:(\w+)\.)?(\w+)\s*\(`,
)

func parseCreateTables(f MigrationFile, tables map[string]*TableDef) {
	sql := f.SQL

	// Find all CREATE TABLE headers in this migration.
	for {
		loc := createTableHeaderRe.FindStringSubmatchIndex(sql)
		if loc == nil {
			break
		}

		schema := ""
		if loc[2] >= 0 {
			schema = sql[loc[2]:loc[3]]
		}
		name := sql[loc[4]:loc[5]]

		// loc[0] is start of match; the opening '(' is at loc[1]-1.
		bodyStart := loc[1] // position right after '('
		body := extractBalancedParens(sql[bodyStart:])

		key := tableKey(schema, name)
		if _, exists := tables[key]; !exists {
			cols := parseColumnDefs(body)
			tables[key] = &TableDef{
				Name:       name,
				Schema:     schema,
				Columns:    cols,
				SourceFile: f.Name,
			}
		}

		// Advance past this CREATE TABLE to find the next one.
		sql = sql[bodyStart+len(body):]
	}
}

// extractBalancedParens returns the content between the implicit opening '('
// (already consumed) and the matching ')'. It handles nested parentheses.
func extractBalancedParens(s string) string {
	depth := 1
	for i, ch := range s {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[:i]
			}
		}
	}
	// If unbalanced, return the whole string.
	return s
}

// parseColumnDefs splits the CREATE TABLE body into column definitions and
// parses each one.
func parseColumnDefs(body string) []ColumnDef {
	parts := splitColumns(body)
	var cols []ColumnDef
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Skip table-level constraints.
		upper := strings.ToUpper(part)
		if isTableConstraint(upper) {
			continue
		}
		col, ok := parseOneColumn(part)
		if ok {
			cols = append(cols, col)
		}
	}
	return cols
}

// isTableConstraint returns true if the line is a table-level constraint
// (not a column definition).
func isTableConstraint(upper string) bool {
	prefixes := []string{"CONSTRAINT ", "UNIQUE ", "UNIQUE(", "CHECK ", "CHECK(", "PRIMARY KEY ", "PRIMARY KEY(", "FOREIGN KEY ", "FOREIGN KEY(", "EXCLUDE "}
	for _, p := range prefixes {
		if strings.HasPrefix(upper, p) {
			return true
		}
	}
	return false
}

// splitColumns splits the column definition body by top-level commas,
// respecting nested parentheses.
func splitColumns(body string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, ch := range body {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, body[start:i])
				start = i + 1
			}
		}
	}
	if start < len(body) {
		parts = append(parts, body[start:])
	}
	return parts
}

// Multi-word SQL type prefixes that need special handling.
var multiWordTypes = []string{
	"DOUBLE PRECISION",
	"CHARACTER VARYING",
	"TIME WITH TIME ZONE",
	"TIMESTAMP WITH TIME ZONE",
	"TIMESTAMP WITHOUT TIME ZONE",
	"TIME WITHOUT TIME ZONE",
}

// parseOneColumn parses a single column definition string.
func parseOneColumn(s string) (ColumnDef, bool) {
	// Normalize whitespace.
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ColumnDef{}, false
	}

	// The first token is the column name.
	tokens := strings.Fields(s)
	if len(tokens) < 2 {
		return ColumnDef{}, false
	}

	colName := tokens[0]
	// If the "name" looks like a keyword, it's probably not a column.
	upperName := strings.ToUpper(colName)
	if upperName == "CONSTRAINT" || upperName == "UNIQUE" || upperName == "CHECK" || upperName == "PRIMARY" || upperName == "FOREIGN" || upperName == "EXCLUDE" {
		return ColumnDef{}, false
	}

	// Determine the column type. Check for multi-word types first.
	rest := s[len(colName)+1:]
	colType := ""
	restAfterType := ""

	upperRest := strings.ToUpper(rest)
	matched := false
	for _, mwt := range multiWordTypes {
		if strings.HasPrefix(upperRest, mwt) {
			colType = mwt
			restAfterType = strings.TrimSpace(rest[len(mwt):])
			matched = true
			break
		}
	}
	if !matched {
		// Single-word type, but it might have a parenthesized precision like VARCHAR(255).
		colType, restAfterType = extractType(rest)
	}

	col := ColumnDef{
		Name:       colName,
		Type:       colType,
		IsNullable: true, // default: nullable unless NOT NULL specified
	}

	// Parse the remaining tokens for constraints.
	parseConstraints(&col, restAfterType)

	return col, true
}

// extractType extracts the type token from the rest of the column definition.
// It handles types with parenthesized precision like VARCHAR(255) or NUMERIC(10,2).
func extractType(rest string) (string, string) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", ""
	}

	fields := strings.Fields(rest)
	baseType := fields[0]

	// Check if the base type ends with '(' or if the next token starts a paren group.
	if strings.Contains(baseType, "(") {
		// Type like "VARCHAR(255)" — might be complete or split.
		if strings.Contains(baseType, ")") {
			// Complete: "VARCHAR(255)"
			after := strings.TrimSpace(rest[len(baseType):])
			return baseType, after
		}
		// Incomplete: find the closing paren.
		idx := strings.Index(rest, ")")
		if idx >= 0 {
			typePart := rest[:idx+1]
			after := strings.TrimSpace(rest[idx+1:])
			// Remove internal spaces: "NUMERIC( 10, 2 )" -> "NUMERIC(10,2)"
			typePart = strings.ReplaceAll(typePart, " ", "")
			return typePart, after
		}
	}

	// Check if the next token starts with '(' — e.g., type is "NUMERIC" and next is "(10,2)".
	afterBase := strings.TrimSpace(rest[len(baseType):])
	if strings.HasPrefix(afterBase, "(") {
		idx := strings.Index(afterBase, ")")
		if idx >= 0 {
			precPart := afterBase[:idx+1]
			typePart := baseType + strings.ReplaceAll(precPart, " ", "")
			after := strings.TrimSpace(afterBase[idx+1:])
			return typePart, after
		}
	}

	// Simple type without precision.
	after := strings.TrimSpace(rest[len(baseType):])
	return baseType, after
}

// parseConstraints parses constraint keywords from the remaining column definition text.
func parseConstraints(col *ColumnDef, s string) {
	upper := strings.ToUpper(s)
	tokens := strings.Fields(s)
	upperTokens := strings.Fields(upper)

	for i := 0; i < len(upperTokens); i++ {
		switch upperTokens[i] {
		case "NOT":
			if i+1 < len(upperTokens) && upperTokens[i+1] == "NULL" {
				col.IsNullable = false
				i++ // skip "NULL"
			}
		case "PRIMARY":
			if i+1 < len(upperTokens) && upperTokens[i+1] == "KEY" {
				col.IsPrimaryKey = true
				col.IsNullable = false
				i++ // skip "KEY"
			}
		case "DEFAULT":
			col.Default = extractDefault(tokens, i+1)
			// Skip past the default value tokens.
			i = skipDefault(upperTokens, i+1)
		case "REFERENCES":
			if i+1 < len(tokens) {
				col.ForeignKey = extractReference(tokens, i+1)
				i = skipReference(upperTokens, i+1)
			}
		}
	}
}

// extractDefault extracts the DEFAULT value expression from the token list
// starting at position start. It handles function calls like gen_random_uuid().
func extractDefault(tokens []string, start int) string {
	if start >= len(tokens) {
		return ""
	}

	// Collect tokens until we hit a constraint keyword or end.
	var parts []string
	for i := start; i < len(tokens); i++ {
		upper := strings.ToUpper(tokens[i])
		if upper == "NOT" || upper == "NULL" || upper == "PRIMARY" ||
			upper == "REFERENCES" || upper == "UNIQUE" || upper == "CHECK" ||
			upper == "CONSTRAINT" {
			break
		}
		parts = append(parts, tokens[i])
	}
	return strings.Join(parts, " ")
}

// skipDefault returns the index past the default value tokens.
func skipDefault(upperTokens []string, start int) int {
	i := start
	for i < len(upperTokens) {
		if upperTokens[i] == "NOT" || upperTokens[i] == "NULL" || upperTokens[i] == "PRIMARY" ||
			upperTokens[i] == "REFERENCES" || upperTokens[i] == "UNIQUE" || upperTokens[i] == "CHECK" ||
			upperTokens[i] == "CONSTRAINT" {
			return i - 1
		}
		i++
	}
	return i - 1
}

// extractReference extracts the REFERENCES target, e.g., "customers(id)".
func extractReference(tokens []string, start int) string {
	if start >= len(tokens) {
		return ""
	}
	ref := tokens[start]
	// If the reference doesn't contain parens but the next token does, append it.
	if !strings.Contains(ref, "(") && start+1 < len(tokens) && strings.HasPrefix(tokens[start+1], "(") {
		ref += tokens[start+1]
	}
	return ref
}

// skipReference returns the index past the reference tokens.
func skipReference(upperTokens []string, start int) int {
	if start >= len(upperTokens) {
		return start - 1
	}
	i := start
	// Skip the table reference (might be one or two tokens).
	if !strings.Contains(upperTokens[i], "(") && i+1 < len(upperTokens) && strings.HasPrefix(upperTokens[i+1], "(") {
		return i + 1
	}
	return i
}

// alterTableAddColumnRe matches ALTER TABLE ... ADD COLUMN statements.
var alterTableAddColumnRe = regexp.MustCompile(
	`(?i)ALTER\s+TABLE\s+(?:ONLY\s+)?(?:IF\s+EXISTS\s+)?(?:(\w+)\.)?(\w+)\s+ADD\s+COLUMN\s+(?:IF\s+NOT\s+EXISTS\s+)?`,
)

func parseAlterTables(f MigrationFile, tables map[string]*TableDef) {
	sql := f.SQL

	// Find all ALTER TABLE ... ADD COLUMN in this migration.
	// A single ALTER TABLE can have multiple ADD COLUMN clauses separated by commas.
	for {
		loc := alterTableAddColumnRe.FindStringSubmatchIndex(sql)
		if loc == nil {
			break
		}

		schema := ""
		if loc[2] >= 0 {
			schema = sql[loc[2]:loc[3]]
		}
		tableName := sql[loc[4]:loc[5]]
		key := tableKey(schema, tableName)

		// Get the rest of the statement after "ADD COLUMN [IF NOT EXISTS] ".
		rest := sql[loc[1]:]

		// Find the end of this statement (;).
		semiIdx := strings.Index(rest, ";")
		var stmtRest string
		if semiIdx >= 0 {
			stmtRest = rest[:semiIdx]
		} else {
			stmtRest = rest
		}

		// Parse columns from this ALTER TABLE statement.
		cols := parseAlterAddColumns(stmtRest)

		if td, exists := tables[key]; exists {
			td.Columns = append(td.Columns, cols...)
		} else {
			// Table not yet seen (could be from a different migration set).
			tables[key] = &TableDef{
				Name:       tableName,
				Schema:     schema,
				Columns:    cols,
				SourceFile: f.Name,
			}
		}

		// Advance past this ALTER statement.
		if semiIdx >= 0 {
			sql = rest[semiIdx+1:]
		} else {
			break
		}
	}
}

// addColumnSplitRe splits on ", ADD COLUMN" to handle multiple ADD COLUMN in one ALTER.
var addColumnSplitRe = regexp.MustCompile(`(?i),\s*ADD\s+COLUMN\s+(?:IF\s+NOT\s+EXISTS\s+)?`)

func parseAlterAddColumns(stmtRest string) []ColumnDef {
	// Split on "ADD COLUMN" boundaries for multiple columns.
	parts := addColumnSplitRe.Split(stmtRest, -1)

	var cols []ColumnDef
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		col, ok := parseOneColumn(part)
		if ok {
			cols = append(cols, col)
		}
	}
	return cols
}
