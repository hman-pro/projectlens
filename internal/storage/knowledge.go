package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

var validKnowledgeCategories = map[string]struct{}{
	"lesson":           {},
	"best_practice":    {},
	"convention":       {},
	"domain_knowledge": {},
	"how_to":           {},
	"decision":         {},
}

type KnowledgeEntry struct {
	ID        int64    `json:"id"`
	Category  string   `json:"category"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Tags      []string `json:"tags,omitempty"`
	Source    string   `json:"source,omitempty"`
	SessionID *string  `json:"session_id,omitempty"`
}

func (e *KnowledgeEntry) Validate() error {
	if e.Title == "" {
		return fmt.Errorf("title required")
	}
	if e.Body == "" {
		return fmt.Errorf("body required")
	}
	if _, ok := validKnowledgeCategories[e.Category]; !ok {
		return fmt.Errorf("category %q not in allowed set", e.Category)
	}
	return nil
}

// InsertKnowledgeEntry inserts the entry and a paired knowledge-typed chunk
// in a single transaction. Returns the new entry ID and chunk ID.
func (db *DB) InsertKnowledgeEntry(ctx context.Context, e *KnowledgeEntry) (entryID, chunkID int64, err error) {
	if err := e.Validate(); err != nil {
		return 0, 0, fmt.Errorf("storage: knowledge: %w", err)
	}
	if e.Source == "" {
		e.Source = "claude"
	}

	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("storage: knowledge: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	const insertEntry = `
        INSERT INTO knowledge_entries (category, title, body, tags, source, session_id)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id`
	if err = tx.QueryRow(ctx, insertEntry,
		e.Category, e.Title, e.Body, e.Tags, e.Source, e.SessionID,
	).Scan(&entryID); err != nil {
		return 0, 0, fmt.Errorf("storage: knowledge: insert entry: %w", err)
	}

	sourceURI := fmt.Sprintf("knowledge:%d", entryID)
	content := e.Title + "\n\n" + e.Body
	const insertChunk = `
        INSERT INTO chunks (symbol_id, content, token_count, source_type, source_uri)
        VALUES (NULL, $1, $2, 'knowledge', $3)
        RETURNING id`
	// token_count: rough estimate, 1 token ≈ 4 chars; embedder retruncates anyway.
	if err = tx.QueryRow(ctx, insertChunk,
		content, len(content)/4, sourceURI,
	).Scan(&chunkID); err != nil {
		return 0, 0, fmt.Errorf("storage: knowledge: insert chunk: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("storage: knowledge: commit: %w", err)
	}
	e.ID = entryID
	return entryID, chunkID, nil
}

type KnowledgeListFilters struct {
	Category string
	Tag      string
	Limit    int
}

func (f *KnowledgeListFilters) Validate() error {
	if f.Category != "" {
		if _, ok := validKnowledgeCategories[f.Category]; !ok {
			return fmt.Errorf("category %q not in allowed set", f.Category)
		}
	}
	if f.Limit < 0 {
		return fmt.Errorf("limit must be non-negative")
	}
	return nil
}

func (db *DB) GetKnowledgeEntry(ctx context.Context, id int64) (*KnowledgeEntry, error) {
	const q = `
        SELECT id, category, title, body, tags, source, session_id
        FROM knowledge_entries
        WHERE id = $1`
	var e KnowledgeEntry
	err := db.Pool.QueryRow(ctx, q, id).Scan(
		&e.ID, &e.Category, &e.Title, &e.Body, &e.Tags, &e.Source, &e.SessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: knowledge: get %d: %w", id, err)
	}
	return &e, nil
}

// DeleteKnowledgeEntry removes the entry, its chunk, and any anchor edges.
// Single transaction. Returns the number of entry rows deleted (0 or 1).
func (db *DB) DeleteKnowledgeEntry(ctx context.Context, id int64) (int, error) {
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("storage: knowledge: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sourceURI := fmt.Sprintf("knowledge:%d", id)

	if _, err = tx.Exec(ctx, `
        DELETE FROM embeddings
        WHERE chunk_id IN (SELECT id FROM chunks WHERE source_uri = $1)`, sourceURI); err != nil {
		return 0, fmt.Errorf("storage: knowledge: delete embeddings: %w", err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM chunks WHERE source_uri = $1`, sourceURI); err != nil {
		return 0, fmt.Errorf("storage: knowledge: delete chunk: %w", err)
	}
	if _, err = tx.Exec(ctx, `
        DELETE FROM edges
        WHERE source_type = 'knowledge' AND source_id = $1`, id); err != nil {
		return 0, fmt.Errorf("storage: knowledge: delete edges: %w", err)
	}

	res, err := tx.Exec(ctx, `DELETE FROM knowledge_entries WHERE id = $1`, id)
	if err != nil {
		return 0, fmt.Errorf("storage: knowledge: delete entry: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("storage: knowledge: commit: %w", err)
	}
	return int(res.RowsAffected()), nil
}

func (db *DB) ListKnowledgeEntries(ctx context.Context, f KnowledgeListFilters) ([]KnowledgeEntry, error) {
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("storage: knowledge: %w", err)
	}
	limit := f.Limit
	if limit == 0 {
		limit = 100
	}

	args := []any{}
	where := []string{}
	if f.Category != "" {
		args = append(args, f.Category)
		where = append(where, fmt.Sprintf("category = $%d", len(args)))
	}
	if f.Tag != "" {
		args = append(args, f.Tag)
		where = append(where, fmt.Sprintf("$%d = ANY(tags)", len(args)))
	}
	args = append(args, limit)

	q := `SELECT id, category, title, body, tags, source, session_id FROM knowledge_entries`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	rows, err := db.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: knowledge: list: %w", err)
	}
	defer rows.Close()

	var out []KnowledgeEntry
	for rows.Next() {
		var e KnowledgeEntry
		if err := rows.Scan(&e.ID, &e.Category, &e.Title, &e.Body, &e.Tags, &e.Source, &e.SessionID); err != nil {
			return nil, fmt.Errorf("storage: knowledge: scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
