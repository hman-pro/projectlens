package storage

import (
	"context"
	"fmt"

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
