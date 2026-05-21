package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
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

	// Dedup fast path: agents have been observed to retry save_knowledge
	// after seeing embedded=false or unresolved anchors on the first
	// attempt, producing identical rows. Detect (source, title, body)
	// matches within knowledgeDedupWindow and short-circuit. The dedup
	// check is best-effort — a lookup failure logs and falls through to
	// the normal insert path so a transient DB hiccup never silently
	// drops a knowledge write.
	if existingID, err := s.db.FindRecentDuplicateKnowledge(
		ctx, source, title, body, knowledgeDedupWindow,
	); err != nil {
		log.Printf("save_knowledge: dedup lookup failed: %v", err)
	} else if existingID != 0 {
		return s.respondDedup(ctx, existingID, anchors)
	}

	entry := &storage.KnowledgeEntry{
		Category: category, Title: title, Body: body,
		Tags: tags, Source: source, SessionID: sessPtr,
	}
	entryID, chunkID, err := s.db.InsertKnowledgeEntry(ctx, entry)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: %v", err)), nil
	}

	resolutions, err := s.db.InsertKnowledgeAnchors(ctx, entryID, anchors)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: anchors: %v", err)), nil
	}

	// Embed synchronously so the entry is immediately query-searchable.
	// Failures here are non-fatal: the entry is still persisted and can be
	// embedded later by `index-embed`. Surface the state via Embedded.
	embedded := s.embedKnowledgeChunk(ctx, chunkID, title, body)

	payload := SaveKnowledgePayload{ID: entryID, Embedded: embedded}
	fillAnchorResults(&payload, resolutions)
	out, _ := json.Marshal(payload)
	return mcp.NewToolResultStructured(payload, string(out)), nil
}

// respondDedup builds the dedup short-circuit response. New anchors are
// still resolved and inserted because the edges unique constraint makes
// the insert idempotent — this lets a retry with corrected anchors attach
// them to the original entry without producing a duplicate row.
func (s *Server) respondDedup(
	ctx context.Context, existingID int64, anchors []storage.AnchorRequest,
) (*mcp.CallToolResult, error) {
	resolutions, err := s.db.InsertKnowledgeAnchors(ctx, existingID, anchors)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: dedup anchors: %v", err)), nil
	}
	payload := SaveKnowledgePayload{ID: existingID, Embedded: true, Deduped: true}
	fillAnchorResults(&payload, resolutions)
	out, _ := json.Marshal(payload)
	return mcp.NewToolResultStructured(payload, string(out)), nil
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
