package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hman-pro/projectlens/internal/storage"
)

type saveKnowledgeResponse struct {
	ID                int64    `json:"id"`
	Embedded          bool     `json:"embedded"`
	AnchorsResolved   int      `json:"anchors_resolved"`
	AnchorsUnresolved []string `json:"anchors_unresolved"`
}

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

	entry := &storage.KnowledgeEntry{
		Category: category, Title: title, Body: body,
		Tags: tags, Source: "claude", SessionID: sessPtr,
	}
	entryID, _, err := s.db.InsertKnowledgeEntry(ctx, entry)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: %v", err)), nil
	}

	resolutions, err := s.db.InsertKnowledgeAnchors(ctx, entryID, anchors)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save_knowledge: anchors: %v", err)), nil
	}

	resp := saveKnowledgeResponse{
		ID:       entryID,
		Embedded: false,
	}
	for _, r := range resolutions {
		if r.Resolved {
			resp.AnchorsResolved++
		} else {
			resp.AnchorsUnresolved = append(resp.AnchorsUnresolved,
				fmt.Sprintf("%s:%s", r.Anchor.Type, r.Anchor.Ref))
		}
	}
	out, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(out)), nil
}

type knowledgeHit struct {
	ID         int64    `json:"id"`
	Category   string   `json:"category"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Tags       []string `json:"tags,omitempty"`
	Score      float32  `json:"score,omitempty"`
	MatchedVia string   `json:"matched_via"` // "vector" | "anchor" | "both"
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

	byID := map[int64]*knowledgeHit{}

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
			byID[h.Entry.ID] = &knowledgeHit{
				ID: h.Entry.ID, Category: h.Entry.Category, Title: h.Entry.Title,
				Body: h.Entry.Body, Tags: h.Entry.Tags,
				Score: h.Score, MatchedVia: "vector",
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
			byID[e.ID] = &knowledgeHit{
				ID: e.ID, Category: e.Category, Title: e.Title,
				Body: e.Body, Tags: e.Tags,
				Score: 1.0, MatchedVia: "anchor",
			}
		}
	}

	out := make([]*knowledgeHit, 0, len(byID))
	for _, h := range byID {
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
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
	return mcp.NewToolResultText(b.String()), nil
}
