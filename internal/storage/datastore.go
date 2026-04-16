package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// DatastoreTableRecord maps to a row in the datastore_tables table.
type DatastoreTableRecord struct {
	ID           int64           `json:"id"`
	Name         string          `json:"name"`
	Engine       string          `json:"engine"`
	SchemaName   *string         `json:"schema_name,omitempty"`
	Columns      json.RawMessage `json:"columns,omitempty"`
	SourceFileID *int64          `json:"source_file_id,omitempty"`
	IndexedAt    time.Time       `json:"indexed_at"`
}

// UpsertDatastoreTable inserts or updates a datastore table record keyed by (name, engine).
func (db *DB) UpsertDatastoreTable(ctx context.Context, r *DatastoreTableRecord) error {
	const query = `
		INSERT INTO datastore_tables (name, engine, schema_name, columns, source_file_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (name, engine) DO UPDATE SET
			schema_name = EXCLUDED.schema_name,
			columns = EXCLUDED.columns,
			source_file_id = EXCLUDED.source_file_id,
			indexed_at = NOW()
	`
	_, err := db.Pool.Exec(ctx, query, r.Name, r.Engine, r.SchemaName, r.Columns, r.SourceFileID)
	if err != nil {
		return fmt.Errorf("storage: upsert datastore table: %w", err)
	}
	return nil
}

// GetDatastoreTableByName returns a datastore table by name and engine.
// Returns nil, nil if no row is found.
func (db *DB) GetDatastoreTableByName(ctx context.Context, name, engine string) (*DatastoreTableRecord, error) {
	const query = `
		SELECT id, name, engine, schema_name, columns, source_file_id, indexed_at
		FROM datastore_tables WHERE name = $1 AND engine = $2
	`
	r := &DatastoreTableRecord{}
	err := db.Pool.QueryRow(ctx, query, name, engine).Scan(
		&r.ID, &r.Name, &r.Engine, &r.SchemaName, &r.Columns, &r.SourceFileID, &r.IndexedAt,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get datastore table: %w", err)
	}
	return r, nil
}

// ListDatastoreTables returns all datastore table records.
func (db *DB) ListDatastoreTables(ctx context.Context) ([]DatastoreTableRecord, error) {
	const query = `
		SELECT id, name, engine, schema_name, columns, source_file_id, indexed_at
		FROM datastore_tables ORDER BY name
	`
	rows, err := db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("storage: list datastore tables: %w", err)
	}
	defer rows.Close()

	var results []DatastoreTableRecord
	for rows.Next() {
		var r DatastoreTableRecord
		if err := rows.Scan(&r.ID, &r.Name, &r.Engine, &r.SchemaName, &r.Columns, &r.SourceFileID, &r.IndexedAt); err != nil {
			return nil, fmt.Errorf("storage: scan datastore table: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
