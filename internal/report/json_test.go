package report

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/storage"
)

func fixtureReport() *Report {
	return &Report{
		GeneratedAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		RepoPath:    "/tmp/repo",
		Git:         indexstate.GitState{Head: "abc123", Dirty: true},
		Stages: map[string]indexstate.StageFreshness{
			"code": {Stage: "code", Status: "completed", AgeMinutes: 5},
		},
		Providers:    []indexstate.ProviderHealth{{Role: "embedder", Provider: "ollama", State: "reachable"}},
		TopPackages:  []storage.PackageStat{{ImportPath: "pkg/a", SymbolCount: 3, FileCount: 2}},
		TopTables:    []storage.TableStat{{Schema: "public", Name: "orders", Engine: "postgres", ReadRefs: 2, WriteRefs: 1, SourceFileCount: 3}},
		HighCoupling: []storage.CouplingPair{{FileA: "a.go", FileB: "b.go", CoChangeCount: 3}},
		Knowledge: KnowledgeInventory{
			TotalEntries:     2,
			CountsByCategory: map[string]int{"lesson": 2},
			RecentEntries:    []storage.KnowledgeSummary{{ID: 1, Title: "t", Category: "lesson", Source: "test", CreatedAt: time.Date(2026, 5, 21, 11, 0, 0, 0, time.UTC)}},
		},
		Degraded:    []StageDegradation{{Stage: "embed", Reason: "missing", SuggestedAction: "run projectlens index-embed"}},
		Suggestions: []AgentQuestion{{Topic: "x", SuggestedTool: "find_symbol", Example: "find_symbol X"}},
	}
}

func TestJSONRenderer_RoundTrip(t *testing.T) {
	r := fixtureReport()
	var buf bytes.Buffer
	if err := (JSONRenderer{}).Render(&buf, r); err != nil {
		t.Fatalf("render: %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if decoded.RepoPath != "/tmp/repo" {
		t.Errorf("repo path: got %s", decoded.RepoPath)
	}
	if len(decoded.TopPackages) != 1 || decoded.TopPackages[0].ImportPath != "pkg/a" {
		t.Errorf("top packages: %+v", decoded.TopPackages)
	}
	if decoded.Knowledge.CountsByCategory["lesson"] != 2 {
		t.Errorf("knowledge counts: %+v", decoded.Knowledge.CountsByCategory)
	}
}
