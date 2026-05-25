package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

// RunProviders identifies the embedding and summarization providers used
// by a run. Either or both fields may be empty when a stage does not
// touch that provider role. Strings come from a client's role-specific
// Identity method, not from config — config is the intent, the client
// is the truth (see docs/2026-05-23-run-observability-design.md).
type RunProviders struct {
	Embed     string
	Summarize string
}

// IndexRunRecord maps to a row in the index_runs table.
type IndexRunRecord struct {
	ID                 int64          `json:"id"`
	StartedAt          time.Time      `json:"started_at"`
	CompletedAt        *time.Time     `json:"completed_at,omitempty"`
	CommitSHA          string         `json:"commit_sha"`
	FilesProcessed     int            `json:"files_processed"`
	SymbolsExtracted   int            `json:"symbols_extracted"`
	EdgesCreated       int            `json:"edges_created"`
	Status             string         `json:"status"`
	Stage              string         `json:"stage"`
	ErrorText          string         `json:"error_text,omitempty"`
	ProviderEmbed      string         `json:"provider_embed,omitempty"`
	ProviderSummarize  string         `json:"provider_summarize,omitempty"`
	Metrics            map[string]any `json:"metrics,omitempty"`
}

// GitRefRecord maps to a row in the git_refs table.
type GitRefRecord struct {
	ID        int64     `json:"id"`
	Branch    string    `json:"branch"`
	CommitSHA string    `json:"commit_sha"`
	IndexedAt time.Time `json:"indexed_at"`
}

// maxErrorTextBytes caps stored error_text so a pathological wrapped
// error chain doesn't bloat the row.
const maxErrorTextBytes = 4096

// sanitizeErrText redacts known secret patterns and truncates to
// maxErrorTextBytes. Best-effort hygiene, not a security boundary.
func sanitizeErrText(s string) string {
	if s == "" {
		return ""
	}
	for _, p := range errRedactors {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	if len(s) > maxErrorTextBytes {
		s = s[:maxErrorTextBytes]
	}
	return s
}

var errRedactors = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]+`), "Bearer [REDACTED]"},
	{regexp.MustCompile(`(?i)authorization:\s*[^\s,]+`), "Authorization: [REDACTED]"},
	{regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]+`), "sk-ant-[REDACTED]"},
	{regexp.MustCompile(`sk-[A-Za-z0-9]{16,}`), "sk-[REDACTED]"},
	{regexp.MustCompile(`(postgres(?:ql)?://[^:]+):[^@]+@`), "$1:[REDACTED]@"},
}

func metricsJSON(m map[string]any) ([]byte, error) {
	if m == nil {
		return []byte(`{}`), nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("storage: marshal metrics: %w", err)
	}
	return b, nil
}

// StartRun inserts a new index run with status "running" and returns its id.
// providers is recorded immediately so a run that crashes before
// CompleteRun still shows what it was configured with.
func (db *DB) StartRun(ctx context.Context, commitSHA string, providers RunProviders) (int64, error) {
	const query = `
		INSERT INTO index_runs (commit_sha, status, provider_embed, provider_summarize)
		VALUES ($1, 'running', NULLIF($2, ''), NULLIF($3, ''))
		RETURNING id
	`
	var id int64
	err := db.Pool.QueryRow(ctx, query, commitSHA, providers.Embed, providers.Summarize).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("storage: start run: %w", err)
	}
	return id, nil
}

// CompleteRun marks a run as completed and records its statistics.
// filesProcessed/symbolsExtracted/edgesCreated are the code-stage legacy
// counters; metrics carries the full per-stage detail. Pass nil for
// metrics when not applicable.
func (db *DB) CompleteRun(ctx context.Context, runID int64,
	filesProcessed, symbolsExtracted, edgesCreated int,
	metrics map[string]any) error {
	mb, err := metricsJSON(metrics)
	if err != nil {
		return err
	}
	const query = `
		UPDATE index_runs SET
			completed_at      = NOW(),
			status            = 'completed',
			files_processed   = $2,
			symbols_extracted = $3,
			edges_created     = $4,
			metrics           = $5::jsonb
		WHERE id = $1
	`
	tag, err := db.Pool.Exec(ctx, query, runID, filesProcessed, symbolsExtracted, edgesCreated, mb)
	if err != nil {
		return fmt.Errorf("storage: complete run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("storage: complete run: run %d not found", runID)
	}
	return nil
}

// RecordStageRun inserts an already-finished run row for one stage of
// the pipeline (summarize, embed, history, datastore, etc.). Used when
// a stage runs to completion without going through Start/Complete —
// e.g., sub-stages of indexer.Run, or short standalone commands that
// don't need a "running" row visible while they execute.
//
// filesProcessed is the legacy compatibility shim — each stage writes a
// representative "items processed" count here so the TUI pipeline/list
// views keep rendering correct numbers; the full detail goes in metrics.
func (db *DB) RecordStageRun(ctx context.Context,
	commitSHA, stage, status string,
	started, completed time.Time,
	filesProcessed int,
	providers RunProviders,
	metrics map[string]any,
	errText string) error {
	mb, err := metricsJSON(metrics)
	if err != nil {
		return err
	}
	const query = `
		INSERT INTO index_runs
			(commit_sha, started_at, completed_at, stage, status,
			 files_processed, provider_embed, provider_summarize, metrics, error_text)
		VALUES ($1, $2, $3, $4, $5, $6,
		        NULLIF($7, ''), NULLIF($8, ''), $9::jsonb, NULLIF($10, ''))
	`
	if _, err := db.Pool.Exec(ctx, query,
		commitSHA, started, completed, stage, status,
		filesProcessed,
		providers.Embed, providers.Summarize, mb,
		sanitizeErrText(errText),
	); err != nil {
		return fmt.Errorf("storage: record stage run: %w", err)
	}
	return nil
}

// FailRun marks a run as failed and stores the (sanitized, truncated)
// error message. errText may be empty if no usable message is available.
func (db *DB) FailRun(ctx context.Context, runID int64, errText string) error {
	const query = `
		UPDATE index_runs SET
			completed_at = NOW(),
			status       = 'failed',
			error_text   = NULLIF($2, '')
		WHERE id = $1
	`
	tag, err := db.Pool.Exec(ctx, query, runID, sanitizeErrText(errText))
	if err != nil {
		return fmt.Errorf("storage: fail run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("storage: fail run: run %d not found", runID)
	}
	return nil
}

const indexRunSelectCols = `id, started_at, completed_at, commit_sha,
	       files_processed, symbols_extracted, edges_created, status, stage,
	       COALESCE(error_text, ''),
	       COALESCE(provider_embed, ''),
	       COALESCE(provider_summarize, ''),
	       metrics`

func scanIndexRun(scanner interface {
	Scan(dest ...any) error
}, r *IndexRunRecord) error {
	var mb []byte
	if err := scanner.Scan(
		&r.ID, &r.StartedAt, &r.CompletedAt, &r.CommitSHA,
		&r.FilesProcessed, &r.SymbolsExtracted, &r.EdgesCreated, &r.Status, &r.Stage,
		&r.ErrorText, &r.ProviderEmbed, &r.ProviderSummarize, &mb,
	); err != nil {
		return err
	}
	if len(mb) > 0 {
		m := map[string]any{}
		if err := json.Unmarshal(mb, &m); err == nil && len(m) > 0 {
			r.Metrics = m
		}
	}
	return nil
}

// GetLatestRun returns the most recent index run.
// Returns nil, nil if no runs exist.
func (db *DB) GetLatestRun(ctx context.Context) (*IndexRunRecord, error) {
	query := `SELECT ` + indexRunSelectCols + ` FROM index_runs ORDER BY id DESC LIMIT 1`
	r := &IndexRunRecord{}
	if err := scanIndexRun(db.Pool.QueryRow(ctx, query), r); err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get latest run: %w", err)
	}
	return r, nil
}

// GetLatestRunsByStage returns the most recent index_runs row for each
// stage that has ever run. Stages currently emitted by the indexer:
// 'code', 'summarize', 'embed', 'history', 'datastore'. Stages with no
// rows are absent from the map. Used by index_status so agents see
// per-stage freshness instead of a single "latest run of any kind"
// timestamp.
func (db *DB) GetLatestRunsByStage(ctx context.Context) (map[string]IndexRunRecord, error) {
	query := `SELECT DISTINCT ON (stage) ` + indexRunSelectCols + `
		FROM index_runs ORDER BY stage, id DESC`
	rows, err := db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("storage: latest runs by stage: %w", err)
	}
	defer rows.Close()

	out := map[string]IndexRunRecord{}
	for rows.Next() {
		var r IndexRunRecord
		if err := scanIndexRun(rows, &r); err != nil {
			return nil, fmt.Errorf("storage: latest runs by stage: scan: %w", err)
		}
		out[r.Stage] = r
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: latest runs by stage: rows: %w", err)
	}
	return out, nil
}

// UpsertGitRef inserts or updates a git ref keyed by branch.
func (db *DB) UpsertGitRef(ctx context.Context, branch, commitSHA string) error {
	const query = `
		INSERT INTO git_refs (branch, commit_sha)
		VALUES ($1, $2)
		ON CONFLICT (branch) DO UPDATE SET
			commit_sha = EXCLUDED.commit_sha,
			indexed_at = NOW()
	`
	_, err := db.Pool.Exec(ctx, query, branch, commitSHA)
	if err != nil {
		return fmt.Errorf("storage: upsert git ref: %w", err)
	}
	return nil
}

// GetGitRef retrieves a git ref by branch name.
// Returns nil, nil if no row is found.
func (db *DB) GetGitRef(ctx context.Context, branch string) (*GitRefRecord, error) {
	const query = `
		SELECT id, branch, commit_sha, indexed_at
		FROM git_refs WHERE branch = $1
	`
	r := &GitRefRecord{}
	err := db.Pool.QueryRow(ctx, query, branch).Scan(
		&r.ID, &r.Branch, &r.CommitSHA, &r.IndexedAt,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: get git ref: %w", err)
	}
	return r, nil
}
