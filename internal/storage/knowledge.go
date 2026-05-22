package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
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

// FindRecentDuplicateKnowledge returns the id of an existing entry that
// matches (source, title, body, category) and was created within the given
// window, or 0 if none. Category is part of the key because re-classifying
// the same prose under a different category is a real edit, not a retry.
// Used by save_knowledge to absorb rapid retry storms when an agent
// re-submits the same content after seeing embed/anchor diagnostics on a
// previous attempt.
func (db *DB) FindRecentDuplicateKnowledge(
	ctx context.Context,
	source, title, body, category string,
	window time.Duration,
) (int64, error) {
	if window <= 0 {
		return 0, nil
	}
	const q = `
        SELECT id FROM knowledge_entries
        WHERE source = $1 AND title = $2 AND body = $3 AND category = $4
          AND created_at > NOW() - make_interval(secs => $5)
        ORDER BY created_at DESC
        LIMIT 1`
	var id int64
	err := db.Pool.QueryRow(ctx, q, source, title, body, category, window.Seconds()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("storage: knowledge: find recent duplicate: %w", err)
	}
	return id, nil
}

// IsKnowledgeEntryEmbedded reports whether the paired chunk for the given
// knowledge entry has an embeddings row. Used by save_knowledge dedup hits
// to report the original entry's true embedding state instead of optimistically
// claiming Embedded:true.
func (db *DB) IsKnowledgeEntryEmbedded(ctx context.Context, entryID int64) (bool, error) {
	const q = `
        SELECT EXISTS (
          SELECT 1
          FROM embeddings e
          JOIN chunks c ON c.id = e.chunk_id
          WHERE c.source_uri = $1
        )`
	var exists bool
	if err := db.Pool.QueryRow(ctx, q,
		fmt.Sprintf("knowledge:%d", entryID),
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("storage: knowledge: check embedded: %w", err)
	}
	return exists, nil
}

// InsertKnowledgeEntry inserts the entry and a paired knowledge-typed chunk
// in a single transaction. Returns the new entry ID and chunk ID.
func (db *DB) InsertKnowledgeEntry(ctx context.Context, e *KnowledgeEntry) (entryID, chunkID int64, err error) {
	if err := e.Validate(); err != nil {
		return 0, 0, fmt.Errorf("storage: knowledge: %w", err)
	}
	if e.Source == "" {
		e.Source = "agent"
	}
	// tags column is NOT NULL DEFAULT '{}'; pgx maps a nil slice to SQL NULL,
	// which would violate the constraint instead of taking the default.
	if e.Tags == nil {
		e.Tags = []string{}
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

// GetKnowledgeEntry returns the entry by id, or (nil, nil) if not found.
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
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
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

type AnchorRequest struct {
	Type string // "symbol" | "file" | "package" | "table"
	Ref  string // scip_symbol | path | package_name | table_name
}

type AnchorResolution struct {
	Anchor   AnchorRequest
	TargetID int64 // 0 if unresolved
	Resolved bool
	// Reason is "" when Resolved is true. When Resolved is false it carries
	// a short diagnostic — "not found" or "ambiguous: N matches — use SCIP id".
	// Surfaced to agents so they can pick a canonical ref instead of retrying
	// blindly with the same short name.
	Reason string
}

// InsertKnowledgeAnchors resolves each anchor to an existing target and writes
// edges (knowledge → target). Unresolved anchors are returned in the result; not an error.
func (db *DB) InsertKnowledgeAnchors(ctx context.Context, knowledgeID int64, anchors []AnchorRequest) ([]AnchorResolution, error) {
	out := make([]AnchorResolution, 0, len(anchors))
	edges := make([]EdgeRecord, 0, len(anchors))

	for _, a := range anchors {
		targetID, ok, reason, err := db.resolveAnchor(ctx, a)
		if err != nil {
			return nil, fmt.Errorf("storage: knowledge: resolve anchor %s:%s: %w", a.Type, a.Ref, err)
		}
		out = append(out, AnchorResolution{Anchor: a, TargetID: targetID, Resolved: ok, Reason: reason})
		if !ok {
			continue
		}
		conf := float32(1.0)
		edges = append(edges, EdgeRecord{
			SourceType:      "knowledge",
			SourceID:        knowledgeID,
			TargetType:      a.Type,
			TargetID:        targetID,
			EdgeType:        "knowledge_about",
			Confidence:      &conf,
			Provenance:      "knowledge",
			ConfidenceClass: "extracted",
		})
	}

	if len(edges) > 0 {
		if err := db.InsertEdges(ctx, edges); err != nil {
			return nil, fmt.Errorf("storage: knowledge: insert anchor edges: %w", err)
		}
	}
	return out, nil
}

type KnowledgeSearchHit struct {
	Entry KnowledgeEntry
	Score float32
}

// SearchKnowledgeByVector returns top-k knowledge entries ordered by cosine
// similarity to queryVec. Optional category filter.
func (db *DB) SearchKnowledgeByVector(
	ctx context.Context,
	queryVec []float32,
	category string,
	limit int,
) ([]KnowledgeSearchHit, error) {
	if limit <= 0 {
		limit = 10
	}
	args := []any{pgvector.NewHalfVector(queryVec)}
	where := "c.source_type = 'knowledge'"
	if category != "" {
		if _, ok := validKnowledgeCategories[category]; !ok {
			return nil, fmt.Errorf("storage: knowledge: bad category %q", category)
		}
		args = append(args, category)
		where += fmt.Sprintf(" AND k.category = $%d", len(args))
	}
	args = append(args, limit)

	q := fmt.Sprintf(`
        SELECT k.id, k.category, k.title, k.body, k.tags, k.source, k.session_id,
               1 - (e.embedding <=> $1) AS score
        FROM embeddings e
        JOIN chunks c             ON c.id = e.chunk_id
        JOIN knowledge_entries k  ON ('knowledge:' || k.id) = c.source_uri
        WHERE %s
        ORDER BY e.embedding <=> $1
        LIMIT $%d`, where, len(args))

	rows, err := db.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: knowledge: vector search: %w", err)
	}
	defer rows.Close()

	var out []KnowledgeSearchHit
	for rows.Next() {
		var h KnowledgeSearchHit
		if err := rows.Scan(
			&h.Entry.ID, &h.Entry.Category, &h.Entry.Title, &h.Entry.Body,
			&h.Entry.Tags, &h.Entry.Source, &h.Entry.SessionID, &h.Score,
		); err != nil {
			return nil, fmt.Errorf("storage: knowledge: scan: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// KnowledgeForAnchor finds knowledge entries anchored to a single target.
// For type="package", looks up by the package name (resolves through files).
func (db *DB) KnowledgeForAnchor(ctx context.Context, a AnchorRequest, limit int) ([]KnowledgeEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	targetID, ok, _, err := db.resolveAnchor(ctx, a)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	var q string
	switch a.Type {
	case "package":
		// All edges with target_type='package' that point at any file in this package.
		q = `
          SELECT k.id, k.category, k.title, k.body, k.tags, k.source, k.session_id
          FROM edges e
          JOIN files f             ON f.id = e.target_id
          JOIN knowledge_entries k ON k.id = e.source_id
          WHERE e.source_type = 'knowledge'
            AND e.edge_type = 'knowledge_about'
            AND e.target_type = 'package'
            AND f.package_name = (SELECT package_name FROM files WHERE id = $1)
          ORDER BY k.created_at DESC
          LIMIT $2`
	default:
		q = `
          SELECT k.id, k.category, k.title, k.body, k.tags, k.source, k.session_id
          FROM edges e
          JOIN knowledge_entries k ON k.id = e.source_id
          WHERE e.source_type = 'knowledge'
            AND e.edge_type = 'knowledge_about'
            AND e.target_type = $1
            AND e.target_id = $2
          ORDER BY k.created_at DESC
          LIMIT $3`
	}

	var rows pgx.Rows
	if a.Type == "package" {
		rows, err = db.Pool.Query(ctx, q, targetID, limit)
	} else {
		rows, err = db.Pool.Query(ctx, q, a.Type, targetID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("storage: knowledge: anchor search: %w", err)
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

// KnowledgeForSymbolWithPackage returns knowledge anchored either directly
// to symbolID, or to the symbol's enclosing package. Deduped by entry id.
func (db *DB) KnowledgeForSymbolWithPackage(ctx context.Context, symbolID int64, limit int) ([]KnowledgeEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	// DISTINCT ON requires its leading column to lead ORDER BY, so dedup happens
	// in an inner query and the outer ORDER BY restores newest-first ordering.
	const q = `
      WITH sym_pkg AS (
        SELECT s.id AS sid, f.package_name AS pkg
        FROM symbols s JOIN files f ON f.id = s.file_id
        WHERE s.id = $1
      )
      SELECT id, category, title, body, tags, source, session_id
      FROM (
        SELECT DISTINCT ON (k.id)
          k.id, k.category, k.title, k.body, k.tags, k.source, k.session_id, k.created_at
        FROM edges e
        JOIN knowledge_entries k ON k.id = e.source_id
        LEFT JOIN files pf       ON pf.id = e.target_id
        WHERE e.source_type = 'knowledge'
          AND e.edge_type = 'knowledge_about'
          AND (
            (e.target_type = 'symbol'  AND e.target_id = (SELECT sid FROM sym_pkg))
            OR
            (e.target_type = 'package' AND pf.package_name = (SELECT pkg FROM sym_pkg))
          )
        ORDER BY k.id, k.created_at DESC
      ) deduped
      ORDER BY created_at DESC
      LIMIT $2`

	rows, err := db.Pool.Query(ctx, q, symbolID, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: knowledge: symbol+package search: %w", err)
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

// resolveAnchor maps an AnchorRequest to the target row id with multi-strategy
// resolution per type. The strategies are deterministic — no fuzzy / suffix
// matching — and an ambiguous short-name lookup is rejected with a reason
// rather than silently picking the first match.
//
// Returns (id, resolved, reason, err). reason is "" when resolved; otherwise
// "not found" or "ambiguous: N matches — use SCIP id".
//
// Resolution per type:
//
//   - symbol:  1) exact scip_symbol; 2) unambiguous symbols.name fallback so
//     agents can anchor by short name when there is exactly one match.
//   - file:    exact files.path.
//   - package: 1) exact files.package_name; 2) last-path-segment fallback
//     ("core/funding" → "funding") so agents can pass import-path style refs.
//   - table:   exact datastore_tables.name.
//
// Package anchors are stored against the smallest file.id in that package;
// retrieval (KnowledgeForAnchor) joins via files.package_name to traverse.
func (db *DB) resolveAnchor(ctx context.Context, a AnchorRequest) (int64, bool, string, error) {
	switch a.Type {
	case "symbol":
		return db.resolveSymbolAnchor(ctx, a.Ref)
	case "file":
		return db.resolveSingleRow(ctx,
			`SELECT id FROM files WHERE path = $1 LIMIT 1`, a.Ref)
	case "package":
		id, ok, reason, err := db.resolvePackageAnchor(ctx, a.Ref)
		if err != nil || ok {
			return id, ok, reason, err
		}
		if i := strings.LastIndexByte(a.Ref, '/'); i >= 0 && i < len(a.Ref)-1 {
			return db.resolvePackageAnchor(ctx, a.Ref[i+1:])
		}
		return 0, false, "not found", nil
	case "table":
		return db.resolveSingleRow(ctx,
			`SELECT id FROM datastore_tables WHERE name = $1 LIMIT 1`, a.Ref)
	default:
		return 0, false, "", fmt.Errorf("unknown anchor type %q", a.Type)
	}
}

func (db *DB) resolveSymbolAnchor(ctx context.Context, ref string) (int64, bool, string, error) {
	var id int64
	err := db.Pool.QueryRow(ctx,
		`SELECT id FROM symbols WHERE scip_symbol = $1 LIMIT 1`, ref).Scan(&id)
	if err == nil {
		return id, true, "", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, "", err
	}

	// LIMIT 2 is enough to distinguish unambiguous from ambiguous; COUNT(*)
	// runs only on the ambiguous path so the unambiguous fast path stays cheap.
	rows, err := db.Pool.Query(ctx,
		`SELECT id FROM symbols WHERE name = $1 LIMIT 2`, ref)
	if err != nil {
		return 0, false, "", err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var rid int64
		if err := rows.Scan(&rid); err != nil {
			return 0, false, "", err
		}
		ids = append(ids, rid)
	}
	if err := rows.Err(); err != nil {
		return 0, false, "", err
	}
	if len(ids) == 0 {
		return 0, false, "not found", nil
	}
	if len(ids) == 1 {
		return ids[0], true, "", nil
	}
	var n int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM symbols WHERE name = $1`, ref).Scan(&n); err != nil {
		return 0, false, "", err
	}
	return 0, false, fmt.Sprintf("ambiguous: %d matches — use SCIP id", n), nil
}

func (db *DB) resolvePackageAnchor(ctx context.Context, name string) (int64, bool, string, error) {
	var id int64
	err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(MIN(id), 0) FROM files WHERE package_name = $1`, name).Scan(&id)
	if err != nil {
		return 0, false, "", err
	}
	if id == 0 {
		return 0, false, "not found", nil
	}
	return id, true, "", nil
}

func (db *DB) resolveSingleRow(ctx context.Context, query, ref string) (int64, bool, string, error) {
	var id int64
	err := db.Pool.QueryRow(ctx, query, ref).Scan(&id)
	if err == nil {
		return id, true, "", nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, "not found", nil
	}
	return 0, false, "", err
}
