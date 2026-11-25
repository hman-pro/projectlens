//go:build smoke

package smoketest

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hman-pro/projectlens/internal/mcpserver"
)

func TestFixtureRepo_ThreeCommits(t *testing.T) {
	dir := buildFixtureRepo(t)
	count, err := gitOutput(dir, "rev-list", "--count", "HEAD")
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if got := strings.TrimSpace(string(count)); got != "3" {
		t.Fatalf("commit count = %q, want 3", got)
	}
	status, err := gitOutput(dir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if s := strings.TrimSpace(string(status)); s != "" {
		t.Fatalf("work tree not clean: %q", s)
	}
}

// gitOutput runs git in dir with the scrubbed subprocess environment.
func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = subprocEnv()
	return cmd.Output()
}

func TestSmoke_FullLoop(t *testing.T) {
	ollama := NewFakeOllama(1024)
	defer ollama.Close()
	db := freshDatabase(t)
	repo := buildFixtureRepo(t)
	cfg := writeConfig(t, ollama.URL, repo)
	env := smokeEnv(ollama.URL, db, repo)

	runLens(t, env, "migrate", "--config", cfg, "--db", db)
	out := runLens(t, env, "index-all", "--config", cfg, "--db", db, "--repo", repo)
	if n := ollama.EmbedCalls(); n < 1 {
		t.Fatalf("fake Ollama served %d /api/embed calls during index-all, want >= 1 (is lens routing embeddings elsewhere?)", n)
	}
	for _, bad := range []string{"embedding failed", "datastore indexing failed", "history indexing failed"} {
		if strings.Contains(out, bad) {
			t.Errorf("index-all output contains %q:\n%s", bad, out)
		}
	}

	ctx := context.Background()
	c, err := client.NewStreamableHttpClient(startMCP(t, cfg, env))
	if err != nil {
		t.Fatalf("new mcp client: %v", err)
	}
	defer c.Close()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start mcp client: %v", err)
	}
	var initReq mcp.InitializeRequest
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "smoke-test", Version: "0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	wantTools := []string{"find_symbol", "get_change_history", "get_coupling", "get_package_summary",
		"get_symbol_context", "get_table_context", "index_status", "save_knowledge", "search_go_context", "search_knowledge"}
	if strings.Join(names, ",") != strings.Join(wantTools, ",") {
		t.Fatalf("tools = %v, want %v", names, wantTools)
	}

	st := callTool[statusPayload](t, c, "index_status", nil)
	for _, s := range []string{"code", "datastore", "history", "embed"} {
		if got := st.Stages[s].Status; got != "completed" {
			t.Errorf("stage %s status = %q, want completed (%+v)", s, got, st.Stages[s])
		}
	}
	if st.Git.Head == "" {
		t.Error("index_status git.head is empty")
	}
	var embedderOK, summarizerSeen bool
	for _, p := range st.Providers {
		if p.Role == "embedder" && p.State == "reachable" {
			embedderOK = true
		}
		if p.Role == "summarizer" {
			summarizerSeen = true
			if p.State != "disabled" {
				t.Errorf("summarizer state = %q, want disabled", p.State)
			}
		}
		if p.Error != "" {
			t.Errorf("provider %s/%s error: %s", p.Role, p.Provider, p.Error)
		}
	}

	if !embedderOK {
		t.Errorf("no reachable embedder provider in %+v", st.Providers)
	}
	if !summarizerSeen {
		t.Errorf("no summarizer provider in %+v", st.Providers)
	}

	fs := callTool[mcpserver.FindSymbolPayload](t, c, "find_symbol", map[string]any{"name": "NewWidget"})
	if fs.Total < 1 || len(fs.Hits) < 1 {
		t.Fatalf("find_symbol NewWidget: %+v", fs)
	}
	if got := fs.Hits[0].Evidence.FilePath; got != "widget/widget.go" {
		t.Errorf("find_symbol file = %q, want widget/widget.go", got)
	}

	sg := callTool[mcpserver.SearchGoContextPayload](t, c, "search_go_context", map[string]any{"query": "save a widget"})
	if sg.Degradation.Degraded {
		t.Errorf("search_go_context degraded: %+v", sg.Degradation)
	}
	if sg.Total < 1 {
		t.Errorf("search_go_context total = %d, want >= 1", sg.Total)
	}

	sc := callTool[mcpserver.SymbolContextPayload](t, c, "get_symbol_context", map[string]any{"name": "NewWidget"})
	if sc.NotFound {
		t.Fatalf("get_symbol_context NewWidget not found")
	}
	if !hasHit(sc.Callers, "main") {
		t.Errorf("NewWidget callers = %+v, want one named main", sc.Callers)
	}
	si := callTool[mcpserver.SymbolContextPayload](t, c, "get_symbol_context", map[string]any{"name": "Store"})
	if !hasHit(si.Implementors, "SQLStore") {
		t.Errorf("Store implementors = %+v, want SQLStore", si.Implementors)
	}

	ps := callTool[mcpserver.PackageSummaryPayload](t, c, "get_package_summary", map[string]any{"package_name": "widget"})
	if ps.PackageName != "widget" {
		t.Errorf("package_name = %q, want widget", ps.PackageName)
	}

	tc := callTool[mcpserver.TableContextPayload](t, c, "get_table_context", map[string]any{"table_name": "widgets"})
	if tc.NotFound {
		t.Fatalf("get_table_context widgets not found")
	}
	hasPK := false
	for _, col := range tc.Columns {
		if col.Name == "id" && col.IsPrimaryKey {
			hasPK = true
		}
	}
	if !hasPK {
		t.Errorf("widgets columns = %+v, want primary key id", tc.Columns)
	}
	if !hasEdge(tc.WrittenBy, "Save") {
		t.Errorf("widgets written_by = %+v, want Save", tc.WrittenBy)
	}
	if !hasEdge(tc.ReadBy, "Load") {
		t.Errorf("widgets read_by = %+v, want Load", tc.ReadBy)
	}

	ch := callTool[mcpserver.ChangeHistoryPayload](t, c, "get_change_history", map[string]any{"name": "widget/widget.go"})
	if len(ch.Records) != 3 {
		t.Errorf("change history records = %d, want 3 (%+v)", len(ch.Records), ch)
	}

	cp := callTool[mcpserver.CouplingPayload](t, c, "get_coupling", map[string]any{"name": "widget/widget.go"})
	coupled := false
	for _, e := range cp.Coupled {
		if e.FilePath == "main.go" {
			coupled = true
		}
	}
	if !coupled {
		t.Errorf("coupling = %+v, want main.go", cp)
	}

	sk := callTool[mcpserver.SaveKnowledgePayload](t, c, "save_knowledge", map[string]any{
		"category": "lesson", "title": "smoke: widgets", "body": "Widgets are saved via SQLStore.",
		"anchors": []map[string]string{{"type": "symbol", "ref": "NewWidget"}},
	})
	if sk.ID <= 0 || !sk.Embedded || sk.AnchorsResolved != 1 {
		t.Errorf("save_knowledge = %+v, want id>0, embedded, 1 anchor resolved", sk)
	}

	kq := callTool[mcpserver.SearchKnowledgePayload](t, c, "search_knowledge",
		map[string]any{"anchor_type": "symbol", "anchor_ref": "NewWidget"})
	found := false
	for _, e := range kq.Entries {
		if e.Title == "smoke: widgets" {
			found = true
		}
	}
	if !found {
		t.Errorf("search_knowledge entries = %+v, want smoke: widgets", kq.Entries)
	}
}

func hasHit(hits []mcpserver.SymbolHit, name string) bool {
	for _, h := range hits {
		if h.Name == name {
			return true
		}
	}
	return false
}

// hasEdge reports whether any hit's symbol name is name or ends in ".name".
func hasEdge(hits []mcpserver.TableEdgeHit, name string) bool {
	for _, h := range hits {
		if h.Name == name || strings.HasSuffix(h.Name, "."+name) {
			return true
		}
	}
	return false
}
