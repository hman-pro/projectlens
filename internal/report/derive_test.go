package report

import (
	"testing"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/storage"
)

func TestDeriveDegradation_MissingStagesUseRealCommands(t *testing.T) {
	stages := map[string]indexstate.StageFreshness{
		"code": {Stage: "code", Status: "completed", AgeMinutes: 5},
	}
	got := deriveDegradation(stages, nil)
	wantSuggested := map[string]string{
		"summarize": "run projectlens index-summarize",
		"embed":     "run projectlens index-embed",
		"history":   "run projectlens index-history",
		"datastore": "run projectlens index-datastore",
	}
	for stage, want := range wantSuggested {
		var found bool
		for _, d := range got {
			if d.Stage == stage && d.SuggestedAction == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("stage %s: missing or wrong suggestion in %+v", stage, got)
		}
	}
}

func TestDeriveDegradation_MissingCodeUsesReindex(t *testing.T) {
	got := deriveDegradation(map[string]indexstate.StageFreshness{}, nil)
	for _, d := range got {
		if d.Stage == "code" {
			if d.SuggestedAction != "run projectlens reindex" {
				t.Errorf("code action: got %q want %q", d.SuggestedAction, "run projectlens reindex")
			}
			return
		}
	}
	t.Errorf("code degradation not emitted: %+v", got)
}

func TestDeriveDegradation_ProviderErrorAndNotConfigured(t *testing.T) {
	stages := map[string]indexstate.StageFreshness{
		"code":      {Stage: "code", Status: "completed"},
		"summarize": {Stage: "summarize", Status: "completed"},
		"embed":     {Stage: "embed", Status: "completed"},
		"history":   {Stage: "history", Status: "completed"},
		"datastore": {Stage: "datastore", Status: "completed"},
	}
	providers := []indexstate.ProviderHealth{
		{Role: "embedder", Provider: "ollama", State: "reachable"},
		{Role: "summarizer", Provider: "anthropic", State: "error", Error: "rate limited"},
		{Role: "extra", Provider: "x", State: "not_configured", Error: "creds missing"},
	}
	got := deriveDegradation(stages, providers)
	var sawErr, sawNC bool
	for _, d := range got {
		if d.Reason == "rate limited" {
			sawErr = true
		}
		if d.Reason == "creds missing" {
			sawNC = true
		}
	}
	if !sawErr {
		t.Errorf("missing error degradation: %+v", got)
	}
	if !sawNC {
		t.Errorf("missing not_configured degradation: %+v", got)
	}
}

func TestDeriveDegradation_StageOlderThan24h(t *testing.T) {
	stages := map[string]indexstate.StageFreshness{
		"code":      {Stage: "code", Status: "completed", AgeMinutes: 25 * 60},
		"summarize": {Stage: "summarize", Status: "completed", AgeMinutes: 10},
		"embed":     {Stage: "embed", Status: "completed", AgeMinutes: 10},
		"history":   {Stage: "history", Status: "completed", AgeMinutes: 10},
		"datastore": {Stage: "datastore", Status: "completed", AgeMinutes: 10},
	}
	got := deriveDegradation(stages, nil)
	for _, d := range got {
		if d.Stage == "code" && d.SuggestedAction == "run projectlens reindex" {
			return
		}
	}
	t.Errorf("stale-stage degradation missing: %+v", got)
}

func TestDeriveSuggestions_OnlyForHealthyStages(t *testing.T) {
	r := &Report{
		Stages: map[string]indexstate.StageFreshness{
			"datastore": {Stage: "datastore", Status: "completed", AgeMinutes: 5},
			"history":   {Stage: "history", Status: "completed", AgeMinutes: 5},
			"code":      {Stage: "code", Status: "completed", AgeMinutes: 5},
		},
		TopTables:    []storage.TableStat{{Schema: "public", Name: "orders"}},
		HighCoupling: []storage.CouplingPair{{FileA: "a.go", FileB: "b.go", CoChangeCount: 5}},
		TopPackages:  []storage.PackageStat{{ImportPath: "pkg/a", SymbolCount: 3, FileCount: 2}},
		Knowledge:    KnowledgeInventory{TotalEntries: 1},
	}
	got := deriveSuggestions(r)
	if len(got) < 4 {
		t.Fatalf("want at least 4 suggestions, got %d: %+v", len(got), got)
	}
}
