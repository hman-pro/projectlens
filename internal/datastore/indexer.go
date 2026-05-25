package datastore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/hman-pro/projectlens/internal/logger"
	"github.com/hman-pro/projectlens/internal/storage"
)

// Config controls datastore indexing paths.
type Config struct {
	Engines      []EngineConfig `yaml:"engines"`
	SQLScanPaths []string       `yaml:"sql_scan_paths"`
}

// EngineConfig defines migration paths for a database engine.
type EngineConfig struct {
	Name           string   `yaml:"name"`
	MigrationPaths []string `yaml:"migration_paths"`
}

// Stats holds the counters produced by a single IndexDatastore run.
type Stats struct {
	Migrations int
	Tables     int
	SQLFiles   int
	TableRefs  int
}

// IndexDatastore runs the full datastore indexing pipeline. Returns Stats
// describing the run.
func IndexDatastore(ctx context.Context, db *storage.DB, repoPath string, cfg Config) (Stats, error) {
	startTime := time.Now()
	logger.Step("Datastore indexing")

	// Step 1: Find and parse migration files.
	var allMigrations []MigrationFile
	for _, engine := range cfg.Engines {
		for _, pattern := range engine.MigrationPaths {
			fullPattern := filepath.Join(repoPath, pattern)
			matches, err := filepath.Glob(fullPattern)
			if err != nil {
				return Stats{}, fmt.Errorf("datastore: glob %s: %w", pattern, err)
			}
			sort.Strings(matches)
			for _, path := range matches {
				data, err := os.ReadFile(path)
				if err != nil {
					return Stats{}, fmt.Errorf("datastore: read %s: %w", path, err)
				}
				relPath, _ := filepath.Rel(repoPath, path)
				allMigrations = append(allMigrations, MigrationFile{
					Name: relPath,
					SQL:  string(data),
				})
			}
		}
	}

	tables := ParseMigrations(allMigrations)
	logger.Info("parsed migrations", "migration_files", len(allMigrations), "tables", len(tables))

	// Step 2: Store datastore_tables records.
	tableIDMap := make(map[string]int64) // "schema.name" → DB ID
	for _, t := range tables {
		columnsJSON, _ := json.Marshal(t.Columns)
		fullName := t.Name
		if t.Schema != "" {
			fullName = t.Schema + "." + t.Name
		}
		rec := &storage.DatastoreTableRecord{
			Name:    fullName,
			Engine:  "postgres", // for now
			Columns: columnsJSON,
		}
		if err := db.UpsertDatastoreTable(ctx, rec); err != nil {
			return Stats{}, fmt.Errorf("datastore: upsert table %s: %w", fullName, err)
		}
		// Get the ID back by looking it up.
		stored, err := db.GetDatastoreTableByName(ctx, fullName, "postgres")
		if err != nil || stored == nil {
			logger.Warn("could not retrieve stored table", "table", fullName)
			continue
		}
		tableIDMap[fullName] = stored.ID
	}
	logger.Info("stored datastore_table records", "count", len(tableIDMap))

	// Step 3: Scan Go source files for SQL references.
	var allRefs []SQLRef
	sqlFilesScanned := 0
	for _, pattern := range cfg.SQLScanPaths {
		fullPattern := filepath.Join(repoPath, pattern)
		matches, err := filepath.Glob(fullPattern)
		if err != nil {
			continue
		}
		for _, path := range matches {
			// Skip test files.
			if isTestFile(path) {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			relPath, _ := filepath.Rel(repoPath, path)
			refs := ScanGoFile(relPath, data)
			allRefs = append(allRefs, refs...)
			sqlFilesScanned++
		}
	}
	logger.Info("scanned Go files", "sql_references", len(allRefs))

	// Step 4: Build reads_table/writes_table edges.
	//   Match SQLRef.Table to datastore_tables IDs.
	//   Match SQLRef.FuncName to symbol IDs in the DB.
	var edges []storage.EdgeRecord
	for _, ref := range allRefs {
		tableID, ok := tableIDMap[ref.Table]
		if !ok {
			continue // table not in our migrations (might be dynamic)
		}

		// Look up the Go symbol by function name.
		symbols, err := db.GetSymbolByName(ctx, ref.FuncName)
		if err != nil || len(symbols) == 0 {
			continue
		}

		edgeType := "reads_table"
		if ref.Operation == "INSERT" || ref.Operation == "UPDATE" || ref.Operation == "DELETE" {
			edgeType = "writes_table"
		}

		edges = append(edges, storage.EdgeRecord{
			SourceType:      "symbol",
			SourceID:        symbols[0].ID,
			TargetType:      "datastore_table",
			TargetID:        tableID,
			EdgeType:        edgeType,
			Provenance:      "sql_scanner",
			ConfidenceClass: "extracted",
		})
	}

	if len(edges) > 0 {
		if err := db.InsertEdges(ctx, edges); err != nil {
			return Stats{}, fmt.Errorf("datastore: insert edges: %w", err)
		}
	}
	logger.Info("created reads_table/writes_table edges", "count", len(edges))

	// Step 5: Build and store table chunks for embedding.
	// Group refs by table.
	readersByTable := make(map[string][]SQLRef)
	writersByTable := make(map[string][]SQLRef)
	for _, ref := range allRefs {
		if ref.Operation == "SELECT" {
			readersByTable[ref.Table] = append(readersByTable[ref.Table], ref)
		} else {
			writersByTable[ref.Table] = append(writersByTable[ref.Table], ref)
		}
	}

	chunksCreated := 0
	for _, t := range tables {
		fullName := t.Name
		if t.Schema != "" {
			fullName = t.Schema + "." + t.Name
		}
		content := BuildTableChunk(t, readersByTable[fullName], writersByTable[fullName])
		sourceURI := fullName
		rec := &storage.ChunkRecord{
			Content:    content,
			TokenCount: len(content) / 4, // rough estimate
			SourceType: "migration",
			SourceURI:  &sourceURI,
		}
		_, err := db.InsertDocChunk(ctx, rec)
		if err != nil {
			logger.Warn("could not store chunk for table", "table", fullName, "err", err)
			continue
		}
		chunksCreated++
	}
	logger.Info("created table chunks", "count", chunksCreated)

	logger.Info("datastore indexing complete", "elapsed", time.Since(startTime).Round(time.Millisecond))
	return Stats{
		Migrations: len(allMigrations),
		Tables:     len(tableIDMap),
		SQLFiles:   sqlFilesScanned,
		TableRefs:  len(edges),
	}, nil
}

// isTestFile returns true if the path ends with _test.go.
func isTestFile(path string) bool {
	return len(path) > 8 && path[len(path)-8:] == "_test.go"
}
