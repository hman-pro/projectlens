package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pgvector/pgvector-go"

	"github.com/hman-pro/projectlens/internal/storage"
)

// maxKnowledgeBodyChars caps the body length fed to the embedder. Matches the
// project-wide oversized-chunk truncation convention.
const maxKnowledgeBodyChars = 30000

// knowledgeEmbedModelVersion is the model_version tag stored alongside
// synchronously-written knowledge embeddings.
const knowledgeEmbedModelVersion = "embedding-model"

// knowledgeDedupWindow is the look-back used by save_knowledge to absorb
// duplicate retries. 60s is short enough that intentional re-saves (delete
// + recreate, content refinement minutes later) still create new entries,
// and long enough to swallow rapid retry storms from agents reacting to
// embed/anchor diagnostics.
const knowledgeDedupWindow = 60 * time.Second

func (s *Server) handleSaveKnowledge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	category, err := req.RequireString("category")
	if err != nil {
		return mcp.NewToolResultError("save_knowledge: category required"), nil
	}
	title, err := req.RequireString("title")
	if err != nil {
		return mcp.NewToolResultError("save_knowledge: title required"), nil
	}
	body, err := req.RequireString("body")
	if err != nil {
		return mcp.NewToolResultError("save_knowledge: body required"), nil
	}

	var tags []string
	if raw := req.GetArguments()["tags"]; raw != nil {
		if arr, ok := raw.([]any); ok {
			for _, v := range arr {
				if str, ok := v.(string); ok {
					tags = append(tags, str)
				}
			}
		}
	}

	var anchors []storage.AnchorRequest
	if raw := req.GetArguments()["anchors"]; raw != nil {
		if arr, ok := raw.([]any); ok {
			for _, v := range arr {
				m, ok := v.(map[string]any)
				if !ok {
					continue
				}
				t, _ := m["type"].(string)
				r, _ := m["ref"].(string)
				if t == "" || r == "" {
					continue
				}
				anchors = append(anchors, storage.AnchorRequest{Type: t, Ref: r})
			}
		}
	}

	sessionID := req.GetString("session_id", "")
	var sessPtr *string
	if sessionID != "" {
		sessPtr = &sessionID
	}

	source := req.GetString("source", "agent")

	// Validate before any DB work so a malformed retry (e.g. bad category
	// with otherwise identical fields) surfaces the validation error
	// instead of getting absorbed by the dedup short-circuit.
	entry := &storage.KnowledgeEntry{
		Category: category, Title: title, Body: body,
		Tags: tags, Source: source, SessionID: sessPtr,
	}
	if err := entry.Validate(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: %v", err)), nil
	}

	// Serialize the dedup check + insert against concurrent identical
	// retries via a Postgres session-scoped advisory lock keyed on a
	// 64-bit hash of (source, title, body, category). pg_advisory_lock is
	// process-global on the server, so two parallel save_knowledge calls
	// with the same payload run their dedup-then-insert critical sections
	// back-to-back: the second caller's dedup check sees the first
	// caller's row and short-circuits. Lock is held only across the
	// critical section — embedding (which can take seconds) runs after
	// the unlock.
	lockKey := dedupLockKey(source, title, body, category)
	conn, err := s.db.Pool.Acquire(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: acquire conn: %v", err)), nil
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: lock: %v", err)), nil
	}
	unlocked := false
	releaseLock := func() {
		if unlocked {
			return
		}
		unlocked = true
		// Use a fresh context so a cancelled request context still releases
		// the lock; the pool returns the conn shortly after, which would
		// also drop session locks, but unlocking explicitly is cleaner.
		if _, err := conn.Exec(context.WithoutCancel(ctx),
			"SELECT pg_advisory_unlock($1)", lockKey); err != nil {
			log.Printf("save_knowledge: unlock failed (key=%d): %v", lockKey, err)
		}
	}
	defer releaseLock()

	// Dedup fast path. Lookup failures fall through to insert so a
	// transient DB hiccup never silently drops a knowledge write.
	if existingID, err := s.db.FindRecentDuplicateKnowledge(
		ctx, source, title, body, category, knowledgeDedupWindow,
	); err != nil {
		log.Printf("save_knowledge: dedup lookup failed: %v", err)
	} else if existingID != 0 {
		result, err := s.respondDedup(ctx, existingID, anchors)
		releaseLock()
		return result, err
	}

	entryID, chunkID, err := s.db.InsertKnowledgeEntry(ctx, entry)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: %v", err)), nil
	}

	resolutions, err := s.db.InsertKnowledgeAnchors(ctx, entryID, anchors)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: anchors: %v", err)), nil
	}

	// Insert is committed and the row is visible to other readers; we can
	// drop the dedup lock before the (slow) embed call.
	releaseLock()

	// Embed synchronously so the entry is immediately query-searchable.
	// Failures here are non-fatal: the entry is still persisted and can be
	// embedded later by `index-embed`. Surface the state via Embedded.
	embedded := s.embedKnowledgeChunk(ctx, chunkID, title, body)

	payload := SaveKnowledgePayload{ID: entryID, Embedded: embedded}
	fillAnchorResults(&payload, resolutions)
	out, _ := json.Marshal(payload)
	return mcp.NewToolResultStructured(payload, string(out)), nil
}

// respondDedup builds the dedup short-circuit response. The original
// entry's true embedding state is reported (not optimistically true), so
// agents see embedded:false when the original save was not embedded and
// can still defer to `index-embed` for semantic availability. New anchors
// are resolved and inserted because the edges unique constraint makes the
// insert idempotent — this lets a retry with corrected anchors attach
// them to the original entry without producing a duplicate row.
func (s *Server) respondDedup(
	ctx context.Context, existingID int64, anchors []storage.AnchorRequest,
) (*mcp.CallToolResult, error) {
	embedded, err := s.db.IsKnowledgeEntryEmbedded(ctx, existingID)
	if err != nil {
		// Lookup failure is not fatal — fall back to false rather than
		// reporting a misleading true; agents treat false as "lexical /
		// anchor search work, semantic lags by one indexer pass".
		log.Printf("save_knowledge: dedup embedded check failed (id=%d): %v", existingID, err)
		embedded = false
	}
	resolutions, err := s.db.InsertKnowledgeAnchors(ctx, existingID, anchors)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: dedup anchors: %v", err)), nil
	}
	payload := SaveKnowledgePayload{ID: existingID, Embedded: embedded, Deduped: true}
	fillAnchorResults(&payload, resolutions)
	out, _ := json.Marshal(payload)
	return mcp.NewToolResultStructured(payload, string(out)), nil
}

// dedupLockKey hashes the dedup tuple to a Postgres advisory-lock key.
// FNV-64a is non-cryptographic but has acceptable distribution for this
// purpose — a hash collision only causes spurious blocking between unrelated
// concurrent save_knowledge calls, never an incorrect dedup result.
func dedupLockKey(source, title, body, category string) int64 {
	h := fnv.New64a()
	for _, s := range []string{source, title, body, category} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return int64(h.Sum64())
}

// fillAnchorResults projects storage anchor resolutions onto the response
// payload. Unresolved anchors carry their resolver-supplied reason so the
// agent can distinguish "not found" from "ambiguous, use SCIP id".
func fillAnchorResults(p *SaveKnowledgePayload, resolutions []storage.AnchorResolution) {
	for _, r := range resolutions {
		if r.Resolved {
			p.AnchorsResolved++
			continue
		}
		s := fmt.Sprintf("%s:%s", r.Anchor.Type, r.Anchor.Ref)
		if r.Reason != "" {
			s += " (" + r.Reason + ")"
		}
		p.AnchorsUnresolved = append(p.AnchorsUnresolved, s)
	}
}

// embedKnowledgeChunk embeds the title+body via the router's embedder and
// upserts an embeddings row keyed by chunkID. Returns true only when both the
// embed call and the DB upsert succeed. The chunk content mirrors what
// InsertKnowledgeEntry wrote (title + "\n\n" + body) so the embedding matches
// what `index-embed` would produce on a later pass.
func (s *Server) embedKnowledgeChunk(ctx context.Context, chunkID int64, title, body string) bool {
	if s.router == nil {
		return false
	}
	content := title + "\n\n" + body
	if len(content) > maxKnowledgeBodyChars {
		content = content[:maxKnowledgeBodyChars]
	}
	vec, err := s.router.EmbedQuery(ctx, content)
	if err != nil {
		// Covers both "no embedder configured" and live embedder failures.
		log.Printf("save_knowledge: embed skipped (chunk=%d): %v", chunkID, err)
		return false
	}
	rec := &storage.EmbeddingRecord{
		ChunkID:      chunkID,
		ModelVersion: knowledgeEmbedModelVersion,
		Embedding:    pgvector.NewHalfVector(vec),
	}
	if err := s.db.UpsertEmbedding(ctx, rec); err != nil {
		log.Printf("save_knowledge: upsert embedding failed (chunk=%d): %v", chunkID, err)
		return false
	}
	return true
}

func (s *Server) handleSearchKnowledge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := req.GetString("query", "")
	category := req.GetString("category", "")
	anchorType := req.GetString("anchor_type", "")
	anchorRef := req.GetString("anchor_ref", "")
	limit := req.GetInt("limit", 10)
	if query == "" && anchorType == "" {
		return mcp.NewToolResultError("search_knowledge: provide query and/or anchor"), nil
	}

	byID := map[int64]*KnowledgeHit{}

	// Vector path
	if query != "" {
		vec, err := s.router.EmbedQuery(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search_knowledge: embed: %v", err)), nil
		}
		hits, err := s.db.SearchKnowledgeByVector(ctx, vec, category, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search_knowledge: vector: %v", err)), nil
		}
		for _, h := range hits {
			byID[h.Entry.ID] = &KnowledgeHit{
				ID: h.Entry.ID, Category: h.Entry.Category, Title: h.Entry.Title,
				Body: h.Entry.Body, Tags: h.Entry.Tags,
				Score: float64(h.Score), MatchedVia: "vector",
			}
		}
	}

	// Anchor path
	if anchorType != "" && anchorRef != "" {
		entries, err := s.db.KnowledgeForAnchor(ctx,
			storage.AnchorRequest{Type: anchorType, Ref: anchorRef}, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search_knowledge: anchor: %v", err)), nil
		}
		for _, e := range entries {
			if existing, ok := byID[e.ID]; ok {
				existing.MatchedVia = "both"
				existing.Score += 0.1
				continue
			}
			byID[e.ID] = &KnowledgeHit{
				ID: e.ID, Category: e.Category, Title: e.Title,
				Body: e.Body, Tags: e.Tags,
				Score: 1.0, MatchedVia: "anchor",
			}
		}
	}

	out := make([]*KnowledgeHit, 0, len(byID))
	for _, h := range byID {
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}

	payload := SearchKnowledgePayload{Query: query, Total: len(out)}
	payload.Entries = make([]KnowledgeHit, 0, len(out))
	for _, h := range out {
		payload.Entries = append(payload.Entries, *h)
	}

	var b strings.Builder
	if len(out) == 0 {
		b.WriteString("No matching knowledge entries.\n")
	} else {
		for _, h := range out {
			fmt.Fprintf(&b, "[%s] (#%d, %s, score=%.2f)\n%s\n%s\n\n",
				h.MatchedVia, h.ID, h.Category, h.Score, h.Title, h.Body)
		}
	}

	return mcp.NewToolResultStructured(payload, b.String()), nil
}
