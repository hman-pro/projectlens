package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

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
