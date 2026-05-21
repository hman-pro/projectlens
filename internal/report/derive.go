package report

import (
	"fmt"

	"github.com/hman-pro/projectlens/internal/indexstate"
)

var stageOrder = []string{"code", "summarize", "embed", "history", "datastore"}

var stageMissingAction = map[string]string{
	"code":      "run projectlens reindex",
	"summarize": "run projectlens index-summarize",
	"embed":     "run projectlens index-embed",
	"history":   "run projectlens index-history",
	"datastore": "run projectlens index-datastore",
}

func deriveDegradation(stages map[string]indexstate.StageFreshness, providers []indexstate.ProviderHealth) []StageDegradation {
	var out []StageDegradation
	for _, s := range stageOrder {
		st, ok := stages[s]
		if !ok {
			out = append(out, StageDegradation{
				Stage:           s,
				Reason:          "stage has never been indexed",
				SuggestedAction: stageMissingAction[s],
			})
			continue
		}
		if st.Status == "completed" && st.AgeMinutes > 24*60 {
			out = append(out, StageDegradation{
				Stage:           s,
				Reason:          fmt.Sprintf("stage age %.0fm exceeds 24h", st.AgeMinutes),
				SuggestedAction: "run projectlens reindex",
			})
		}
	}
	for _, p := range providers {
		if p.State == "error" || p.State == "not_configured" {
			reason := p.Error
			if reason == "" {
				reason = p.State
			}
			out = append(out, StageDegradation{
				Stage:           "provider:" + p.Role,
				Reason:          reason,
				SuggestedAction: "check provider credentials",
			})
		}
	}
	return out
}

func stageHealthy(stages map[string]indexstate.StageFreshness, stage string) bool {
	st, ok := stages[stage]
	if !ok {
		return false
	}
	return st.Status == "completed" && st.AgeMinutes <= 24*60
}

func deriveSuggestions(r *Report) []AgentQuestion {
	var out []AgentQuestion
	if stageHealthy(r.Stages, "datastore") && len(r.TopTables) > 0 {
		t := r.TopTables[0]
		name := t.Name
		if t.Schema != "" {
			name = t.Schema + "." + t.Name
		}
		out = append(out, AgentQuestion{
			Topic:         "Who reads " + name + "?",
			SuggestedTool: "get_table_context",
			Example:       "get_table_context " + name,
		})
	}
	if stageHealthy(r.Stages, "history") && len(r.HighCoupling) > 0 {
		c := r.HighCoupling[0]
		out = append(out, AgentQuestion{
			Topic:         "Which files change with " + c.FileA + "?",
			SuggestedTool: "get_coupling",
			Example:       "get_coupling " + c.FileA,
		})
	}
	if stageHealthy(r.Stages, "code") && len(r.TopPackages) > 0 {
		p := r.TopPackages[0]
		out = append(out, AgentQuestion{
			Topic:         "What does " + p.ImportPath + " do?",
			SuggestedTool: "get_package_summary",
			Example:       "get_package_summary " + p.ImportPath,
		})
	}
	if r.Knowledge.TotalEntries > 0 {
		out = append(out, AgentQuestion{
			Topic:         "Have we captured anything about this code?",
			SuggestedTool: "search_knowledge",
			Example:       "search_knowledge <topic>",
		})
	}
	return out
}
