package datastore

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

// SQLRef represents a SQL table reference found in Go source code.
type SQLRef struct {
	Table     string `json:"table"`      // e.g., "plan.display_locations"
	Operation string `json:"operation"`  // SELECT, INSERT, UPDATE, DELETE
	FuncName  string `json:"func_name"` // enclosing Go function name
	FilePath  string `json:"file_path"`
	Line      int    `json:"line"`
}

// Regex patterns for extracting table names from SQL.
var (
	fromRe       = regexp.MustCompile(`(?i)\bFROM\s+([a-zA-Z_][a-zA-Z0-9_.]+)`)
	joinRe       = regexp.MustCompile(`(?i)\bJOIN\s+([a-zA-Z_][a-zA-Z0-9_.]+)`)
	intoRe       = regexp.MustCompile(`(?i)\bINTO\s+([a-zA-Z_][a-zA-Z0-9_.]+)`)
	updateRe     = regexp.MustCompile(`(?i)\bUPDATE\s+([a-zA-Z_][a-zA-Z0-9_.]+)`)
	deleteFromRe = regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+([a-zA-Z_][a-zA-Z0-9_.]+)`)
)

// SQL keywords used to detect whether a string is SQL.
var sqlKeywords = []string{"SELECT", "INSERT", "UPDATE", "DELETE"}

// Table-indicating keywords that confirm a SQL context.
var sqlTableKeywords = []string{"FROM", "INTO", "UPDATE", "JOIN"}

// ScanGoFile parses a Go source file and extracts SQL table references
// from string literals. Returns deduplicated refs per function.
func ScanGoFile(filename string, src []byte) []SQLRef {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil
	}

	var refs []SQLRef
	seen := make(map[string]bool)

	// Collect function ranges so we can map positions to enclosing functions.
	type funcRange struct {
		name  string
		start token.Pos
		end   token.Pos
	}
	var funcs []funcRange

	// First pass: collect all function declarations.
	ast.Inspect(f, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok {
			funcs = append(funcs, funcRange{
				name:  fn.Name.Name,
				start: fn.Pos(),
				end:   fn.End(),
			})
		}
		return true
	})

	// findFunc returns the name of the enclosing function for a given position.
	findFunc := func(pos token.Pos) string {
		for _, fr := range funcs {
			if pos >= fr.start && pos <= fr.end {
				return fr.name
			}
		}
		return ""
	}

	// Second pass: find all string literals.
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}

		val, err := strconv.Unquote(lit.Value)
		if err != nil {
			// For backtick strings, strconv.Unquote handles them.
			// If it fails, try trimming backticks manually.
			if strings.HasPrefix(lit.Value, "`") && strings.HasSuffix(lit.Value, "`") {
				val = lit.Value[1 : len(lit.Value)-1]
			} else {
				return true
			}
		}

		if !looksLikeSQL(val) {
			return true
		}

		// Skip Go template SQL.
		if strings.Contains(val, "{{") {
			return true
		}

		op := detectOperation(val)
		tables := extractTables(val)
		funcName := findFunc(lit.Pos())
		line := fset.Position(lit.Pos()).Line

		for _, table := range tables {
			key := fmt.Sprintf("%s|%s|%s", funcName, table, op)
			if seen[key] {
				continue
			}
			seen[key] = true
			refs = append(refs, SQLRef{
				Table:     table,
				Operation: op,
				FuncName:  funcName,
				FilePath:  filename,
				Line:      line,
			})
		}

		return true
	})

	return refs
}

// looksLikeSQL checks whether a string value appears to contain SQL.
// It requires both a SQL keyword (SELECT, INSERT, UPDATE, DELETE) and
// a table-indicating keyword (FROM, INTO, UPDATE, JOIN).
func looksLikeSQL(s string) bool {
	upper := strings.ToUpper(s)
	hasKeyword := false
	for _, kw := range sqlKeywords {
		if strings.Contains(upper, kw) {
			hasKeyword = true
			break
		}
	}
	if !hasKeyword {
		return false
	}
	for _, kw := range sqlTableKeywords {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

// detectOperation returns the first SQL operation keyword found in the string.
func detectOperation(s string) string {
	upper := strings.ToUpper(s)
	// Check in priority order: the first keyword that appears.
	ops := []string{"INSERT", "SELECT", "UPDATE", "DELETE"}
	earliest := len(upper)
	result := ""
	for _, op := range ops {
		idx := strings.Index(upper, op)
		if idx >= 0 && idx < earliest {
			earliest = idx
			result = op
		}
	}
	return result
}

// extractTables finds all table references in a SQL string.
func extractTables(s string) []string {
	var tables []string
	seen := make(map[string]bool)

	// Normalize whitespace for matching.
	normalized := normalizeWhitespace(s)

	patterns := []*regexp.Regexp{
		deleteFromRe,
		fromRe,
		joinRe,
		intoRe,
		updateRe,
	}

	for _, re := range patterns {
		matches := re.FindAllStringSubmatch(normalized, -1)
		for _, m := range matches {
			table := m[1]
			// Filter out SQL keywords that regex might accidentally match.
			if isSQLKeyword(table) {
				continue
			}
			if !seen[table] {
				seen[table] = true
				tables = append(tables, table)
			}
		}
	}

	return tables
}

// normalizeWhitespace collapses all whitespace runs into a single space.
func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// isSQLKeyword returns true if the given word is a SQL keyword that should
// not be treated as a table name.
func isSQLKeyword(s string) bool {
	keywords := map[string]bool{
		"SELECT": true, "FROM": true, "WHERE": true, "JOIN": true,
		"INNER": true, "LEFT": true, "RIGHT": true, "OUTER": true,
		"CROSS": true, "ON": true, "AND": true, "OR": true,
		"INSERT": true, "INTO": true, "VALUES": true, "UPDATE": true,
		"SET": true, "DELETE": true, "ORDER": true, "BY": true,
		"GROUP": true, "HAVING": true, "LIMIT": true, "OFFSET": true,
		"UNION": true, "ALL": true, "DISTINCT": true, "AS": true,
		"NOT": true, "NULL": true, "IS": true, "IN": true,
		"EXISTS": true, "BETWEEN": true, "LIKE": true, "CASE": true,
		"WHEN": true, "THEN": true, "ELSE": true, "END": true,
		"RETURNING": true, "CREATE": true, "ALTER": true, "DROP": true,
		"TABLE": true, "INDEX": true, "CONSTRAINT": true, "PRIMARY": true,
		"FOREIGN": true, "KEY": true, "REFERENCES": true, "CASCADE": true,
		"DEFAULT": true, "CHECK": true, "UNIQUE": true, "WITH": true,
	}
	return keywords[strings.ToUpper(s)]
}
