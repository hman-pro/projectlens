package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hman-pro/projectlens/internal/config"
)

type PG struct {
	pool     *pgxpool.Pool
	cfg      *config.Config
	repoPath string
}

func NewPG(pool *pgxpool.Pool, cfg *config.Config, repoPath string) *PG {
	return &PG{pool: pool, cfg: cfg, repoPath: repoPath}
}

// knownTables is the static list of tables that appear in the Storage view.
// Kept in sync with CLAUDE.md.
var knownTables = []string{
	"files", "symbols", "chunks", "embeddings", "summaries", "edges",
	"index_runs", "git_refs", "datastore_tables", "documents",
	"symbol_history", "file_history", "knowledge_entries", "schema_migrations",
}

func (s *PG) Health(ctx context.Context) (HealthSnapshot, error) {
	const q = `
		SELECT id, started_at, completed_at, commit_sha, stage, status,
		       files_processed, symbols_extracted, edges_created
		FROM index_runs ORDER BY id DESC LIMIT 1
	`
	var (
		id        int64
		started   time.Time
		completed *time.Time
		commit    string
		stage     string
		status    string
		files     int
		symbols   int
		edges     int
	)
	row := s.pool.QueryRow(ctx, q)
	if err := row.Scan(&id, &started, &completed, &commit, &stage, &status, &files, &symbols, &edges); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return HealthSnapshot{}, nil
		}
		return HealthSnapshot{}, fmt.Errorf("store: health: %w", err)
	}
	head := s.gitHead()
	return HealthSnapshot{
		StartedAt:        started,
		CompletedAt:      completed,
		CommitSHA:        commit,
		Stage:            stage,
		Status:           status,
		FilesProcessed:   files,
		SymbolsExtracted: symbols,
		EdgesCreated:     edges,
		HeadCommit:       head,
		Staleness:        time.Since(started),
	}, nil
}

// gitHead returns the short HEAD commit of the target repo, or "" if unavailable.
func (s *PG) gitHead() string {
	if s.repoPath == "" {
		return ""
	}
	cmd := exec.Command("git", "-C", s.repoPath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (s *PG) Pipeline(ctx context.Context) (PipelineSnapshot, error) {
	const q = `
		SELECT DISTINCT ON (stage) stage, started_at, completed_at, status, files_processed
		FROM index_runs ORDER BY stage, id DESC
	`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return PipelineSnapshot{}, fmt.Errorf("store: pipeline: %w", err)
	}
	defer rows.Close()
	var stages []StageStat
	for rows.Next() {
		var (
			name      string
			started   time.Time
			completed *time.Time
			status    string
			files     int
		)
		if err := rows.Scan(&name, &started, &completed, &status, &files); err != nil {
			return PipelineSnapshot{}, fmt.Errorf("store: pipeline scan: %w", err)
		}
		dur := time.Duration(0)
		if completed != nil {
			dur = completed.Sub(started)
		}
		stages = append(stages, StageStat{
			Name:             name,
			LastRunStartedAt: started,
			Status:           status,
			FilesProcessed:   files,
			Duration:         dur,
		})
	}
	if err := rows.Err(); err != nil {
		return PipelineSnapshot{}, fmt.Errorf("store: pipeline rows: %w", err)
	}
	return PipelineSnapshot{Stages: stages}, nil
}

func (s *PG) Storage(ctx context.Context) (StorageSnapshot, error) {
	const tableQ = `
		SELECT relname, n_live_tup, pg_total_relation_size(relid)
		FROM pg_stat_user_tables WHERE relname = ANY($1)
	`
	rows, err := s.pool.Query(ctx, tableQ, knownTables)
	if err != nil {
		return StorageSnapshot{}, fmt.Errorf("store: storage tables: %w", err)
	}
	tables := make([]TableStat, 0, len(knownTables))
	for rows.Next() {
		var t TableStat
		if err := rows.Scan(&t.Name, &t.EstRows, &t.Bytes); err != nil {
			rows.Close()
			return StorageSnapshot{}, fmt.Errorf("store: storage scan: %w", err)
		}
		tables = append(tables, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return StorageSnapshot{}, fmt.Errorf("store: storage rows: %w", err)
	}

	const chunkQ = `
		SELECT c.source_type, count(*) AS total, count(e.id) AS embedded
		FROM chunks c
		LEFT JOIN embeddings e ON e.chunk_id = c.id
		GROUP BY c.source_type
	`
	crows, err := s.pool.Query(ctx, chunkQ)
	if err != nil {
		return StorageSnapshot{}, fmt.Errorf("store: storage chunks: %w", err)
	}
	defer crows.Close()
	chunks := ChunkStats{ByType: map[string]int64{}}
	for crows.Next() {
		var srcType string
		var total, embedded int64
		if err := crows.Scan(&srcType, &total, &embedded); err != nil {
			return StorageSnapshot{}, fmt.Errorf("store: storage chunk scan: %w", err)
		}
		chunks.ByType[srcType] = total
		chunks.Total += total
		chunks.Embedded += embedded
	}
	if err := crows.Err(); err != nil {
		return StorageSnapshot{}, fmt.Errorf("store: storage chunk rows: %w", err)
	}
	return StorageSnapshot{Tables: tables, Chunks: chunks}, nil
}

func (s *PG) Runs(ctx context.Context, limit int) (RunsSnapshot, error) {
	if limit <= 0 || limit > RunsMaxRows {
		limit = RunsMaxRows
	}
	const q = `
		SELECT id, started_at, completed_at, commit_sha, stage, status,
		       files_processed, symbols_extracted, edges_created
		FROM index_runs ORDER BY id DESC LIMIT $1
	`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return RunsSnapshot{}, fmt.Errorf("store: runs: %w", err)
	}
	defer rows.Close()
	var runs []IndexRun
	for rows.Next() {
		var r IndexRun
		var completed sql.NullTime
		if err := rows.Scan(&r.ID, &r.StartedAt, &completed, &r.CommitSHA, &r.Stage, &r.Status,
			&r.FilesProcessed, &r.SymbolsExtracted, &r.EdgesCreated); err != nil {
			return RunsSnapshot{}, fmt.Errorf("store: runs scan: %w", err)
		}
		if completed.Valid {
			t := completed.Time
			r.CompletedAt = &t
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return RunsSnapshot{}, fmt.Errorf("store: runs rows: %w", err)
	}
	return RunsSnapshot{Runs: runs}, nil
}

func (s *PG) Config(_ context.Context) (ConfigSnapshot, error) {
	host, dbname := parseDSN(s.cfg.DatabaseURL)
	return ConfigSnapshot{
		EmbeddingProvider:     s.cfg.Embeddings.Provider,
		EmbeddingModel:        s.cfg.Embeddings.Model,
		EmbeddingDims:         s.cfg.Embeddings.Dimensions,
		EmbeddingEndpoint:     s.cfg.Embeddings.Endpoint,
		SummarizationProvider: s.cfg.Summarization.Provider,
		SummarizationModel:    s.cfg.Summarization.Model,
		DBHost:                host,
		DBName:                dbname,
	}, nil
}

func parseDSN(dsn string) (host, dbname string) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", ""
	}
	host = u.Host
	if strings.HasPrefix(u.Path, "/") {
		dbname = u.Path[1:]
	}
	return host, dbname
}
