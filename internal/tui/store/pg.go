package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
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

func (s *PG) Config(ctx context.Context) (ConfigSnapshot, error) {
	host, dbname := parseDSN(s.cfg.DatabaseURL)
	mcpURL := resolveMCPURL()
	mcpStatus, mcpLatency, mcpErr := probeMCP(ctx, mcpURL)
	return ConfigSnapshot{
		EmbeddingProvider:     s.cfg.Embeddings.Provider,
		EmbeddingModel:        s.cfg.Embeddings.Model,
		EmbeddingDims:         s.cfg.Embeddings.Dimensions,
		EmbeddingEndpoint:     s.cfg.Embeddings.Endpoint,
		SummarizationProvider: s.cfg.Summarization.Provider,
		SummarizationModel:    s.cfg.Summarization.Model,
		DBHost:                host,
		DBName:                dbname,
		MCPURL:                mcpURL,
		MCPStatus:             mcpStatus,
		MCPLatency:            mcpLatency,
		MCPError:              mcpErr,
	}, nil
}

// resolveMCPURL builds the MCP server URL from env (PROJECTLENS_MCP_URL > MCP_PORT >
// PROJECTLENS_MCP_PORT > default 8484). Path is /mcp to match the streamable-http
// transport configured in claude/mcp-config.json.
func resolveMCPURL() string {
	if v := os.Getenv("PROJECTLENS_MCP_URL"); v != "" {
		return v
	}
	port := "8484"
	if v := os.Getenv("MCP_PORT"); v != "" {
		port = v
	} else if v := os.Getenv("PROJECTLENS_MCP_PORT"); v != "" {
		port = v
	}
	return fmt.Sprintf("http://localhost:%s/mcp", port)
}

// probeMCP issues a short HTTP GET against the MCP endpoint to determine
// reachability. Streamable-http accepts POST for JSON-RPC, so a GET typically
// returns 405 — that still proves the daemon is up. Connection refused or
// timeout means down. Returns ("up"|"down", latency, errMsg).
func probeMCP(ctx context.Context, rawURL string) (string, time.Duration, string) {
	probeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "down", 0, err.Error()
	}
	client := &http.Client{Timeout: 750 * time.Millisecond}
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return "down", latency, errMsg(err)
	}
	resp.Body.Close()
	return "up", latency, ""
}

func errMsg(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && i < len(msg)-2 {
		return msg[i+2:]
	}
	return msg
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

// EmbedPending counts chunks with no embedding row yet.
func (s *PG) EmbedPending(ctx context.Context) (int, error) {
	const q = `
		SELECT COUNT(*) FROM chunks c
		WHERE NOT EXISTS (SELECT 1 FROM embeddings e WHERE e.chunk_id = c.id)
	`
	var n int
	if err := s.pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: embed pending: %w", err)
	}
	return n, nil
}

// SummarizePending counts packages without a summary row. Uses
// summaries.package_name and files.package_name (the actual columns;
// see migrations/001).
func (s *PG) SummarizePending(ctx context.Context) (int, error) {
	const q = `
		SELECT COUNT(DISTINCT f.package_name)
		FROM files f
		WHERE f.package_name <> ''
		  AND NOT EXISTS (
		      SELECT 1 FROM summaries s WHERE s.package_name = f.package_name
		  )
	`
	var n int
	if err := s.pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: summarize pending: %w", err)
	}
	return n, nil
}

// HistoryNewCommits estimates how many commits would be ingested by
// the next index-history run. Uses file_history.committed_at as the
// reference timestamp; falls back to 0 when repoPath is empty.
func (s *PG) HistoryNewCommits(ctx context.Context) (int, error) {
	if s.repoPath == "" {
		return 0, nil
	}
	var since time.Time
	const q = `SELECT COALESCE(MAX(committed_at), '1970-01-01'::timestamptz) FROM file_history`
	if err := s.pool.QueryRow(ctx, q).Scan(&since); err != nil {
		return 0, fmt.Errorf("store: latest file_history: %w", err)
	}
	args := []string{"-C", s.repoPath, "rev-list", "--count",
		"--since=" + since.Add(-5*time.Minute).Format(time.RFC3339), "HEAD"}
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return 0, fmt.Errorf("store: git rev-list: %w", err)
	}
	n := 0
	for _, c := range strings.TrimSpace(string(out)) {
		if c < '0' || c > '9' {
			continue
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// ChangedFilesSinceLastRun returns the number of files whose persisted
// index timestamp is older than the most recent successful index run.
// Uses files.indexed_at (the actual column; see migrations/001:14).
func (s *PG) ChangedFilesSinceLastRun(ctx context.Context) (int, error) {
	const refQ = `SELECT COALESCE(MAX(completed_at), '1970-01-01'::timestamptz) FROM index_runs WHERE status = 'completed'`
	var ref time.Time
	if err := s.pool.QueryRow(ctx, refQ).Scan(&ref); err != nil {
		return 0, fmt.Errorf("store: last run: %w", err)
	}
	const q = `SELECT COUNT(*) FROM files WHERE indexed_at IS NULL OR indexed_at < $1`
	var n int
	if err := s.pool.QueryRow(ctx, q, ref).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: changed files: %w", err)
	}
	return n, nil
}
