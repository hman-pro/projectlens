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
func AssertSchemaExists(ctx context.Context, conn *pgx.Conn, schema string) error {
	var exists bool
	err := conn.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`,
		schema,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("storage: check schema %q: %w", schema, err)
	}
	if !exists {
		return fmt.Errorf("storage: schema %q does not exist; run `projectlens migrate --project <slug>` first", schema)
	}
	return nil
}

// PinSearchPath sets the connection's search_path to "<schema>,public" using
// a sanitized identifier. Bind parameters are not valid for SET search_path,
// so we concatenate the quoted identifier; QuoteSchema enforces escaping.
func PinSearchPath(ctx context.Context, conn *pgx.Conn, schema string) error {
	sql := "SET search_path TO " + QuoteSchema(schema) + ",public"
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("storage: set search_path %q: %w", schema, err)
	}
	return nil
}
