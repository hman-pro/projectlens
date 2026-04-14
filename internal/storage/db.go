package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgxpool.Pool to provide application-level database operations.
type DB struct {
	Pool *pgxpool.Pool
}

// Connect creates a new connection pool to the given database URL and returns a DB.
func Connect(ctx context.Context, databaseURL string) (*DB, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("storage: connect: %w", err)
	}
	return &DB{Pool: pool}, nil
}

// Close shuts down the connection pool.
func (db *DB) Close() {
	db.Pool.Close()
}

// Ping verifies the database connection is alive.
func (db *DB) Ping(ctx context.Context) error {
	return db.Pool.Ping(ctx)
}

// Migrate reads .up.sql files from migrationsDir in sorted order and executes
// each one inside the database. It is meant for simple forward-only migration.
func (db *DB) Migrate(ctx context.Context, migrationsDir string) error {
	files, err := ReadMigrationFiles(migrationsDir)
	if err != nil {
		return fmt.Errorf("storage: migrate: %w", err)
	}

	for _, mf := range files {
		if _, err := db.Pool.Exec(ctx, mf.SQL); err != nil {
			return fmt.Errorf("storage: migrate %s: %w", mf.Name, err)
		}
	}
	return nil
}

// MigrationFile holds a single parsed migration.
type MigrationFile struct {
	Name string
	SQL  string
}

// ReadMigrationFiles reads all *.up.sql files from dir, sorted by name.
func ReadMigrationFiles(dir string) ([]MigrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	var files []MigrationFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		files = append(files, MigrationFile{Name: e.Name()})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})

	for i := range files {
		data, err := os.ReadFile(filepath.Join(dir, files[i].Name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", files[i].Name, err)
		}
		files[i].SQL = string(data)
	}

	return files, nil
}
