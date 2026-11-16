package agentsetup

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// newMCPServer starts a streamable-HTTP MCP server with one no-op tool per
// name and returns its /mcp URL.
func newMCPServer(t *testing.T, names ...string) string {
	t.Helper()
	s := server.NewMCPServer("projectlens", "1.0.0")
	for _, n := range names {
		s.AddTool(mcp.NewTool(n), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		})
	}
	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts.URL + "/mcp"
}

type doctorEnv struct {
	env    Env
	tg     Target
	m      *Manifest
	assets string
}

// doctorSetup installs agent a against url in a fresh repo.
func doctorSetup(t *testing.T, a Agent, url string) doctorEnv {
	t.Helper()
	tg, err := OpenTarget(newRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, InstallOpts{Agent: a, URL: url})
	m, err := LoadManifest(tg)
	if err != nil {
		t.Fatal(err)
	}
	return doctorEnv{env: Env{Cwd: tg.Root, Home: t.TempDir()}, tg: tg, m: m, assets: assets}
}

func okLookPath(string) (string, error) { return "/usr/local/bin/lens", nil }

func (e doctorEnv) run(t *testing.T, agents []Agent, o DoctorOpts, d DoctorDeps) []Check {
	t.Helper()
	o.Agents = agents
	if o.AssetsDir == "" && o.AssetsErr == nil {
		o.AssetsDir = e.assets
	}
	if d.LookPath == nil {
		d.LookPath = okLookPath
	}
	return Doctor(context.Background(), e.env, e.tg, e.m, o, d)
}

func checkNamed(t *testing.T, cs []Check, name string, a Agent) Check {
	t.Helper()
	for _, c := range cs {
		if c.Name == name && c.Agent == a {
			return c
		}
	}
	t.Fatalf("no check %s/%q in %+v", name, a, cs)
	return Check{}
}

func hasCheck(cs []Check, name string) bool {
	for _, c := range cs {
		if c.Name == name {
			return true
		}
	}
	return false
}

func TestDoctor_Healthy(t *testing.T) {
	url := newMCPServer(t, "a", "b")
	e := doctorSetup(t, AgentClaude, url)
	cs := e.run(t, []Agent{AgentClaude}, DoctorOpts{}, DoctorDeps{ExpectedTools: []string{"a", "b"}})
	wantOrder := []string{"assets-found", "items-current", "server-reachable", "tools-match", "installed-url-matches", "not-shadowed", "agent-will-load", "lens-on-path"}
	if len(cs) != len(wantOrder) {
		t.Fatalf("got %d checks, want %d: %+v", len(cs), len(wantOrder), cs)
	}
	for i, c := range cs {
		if c.Name != wantOrder[i] {
			t.Errorf("check %d = %s, want %s", i, c.Name, wantOrder[i])
		}
		want := CheckResult("pass")
		if c.Name == "agent-will-load" {
			want = "manual"
		}
		if c.Result != want {
			t.Errorf("%s = %s (%s), want %s", c.Name, c.Result, c.Detail, want)
		}
	}
}

func TestDoctor_ConnectionRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String() + "/mcp"
	l.Close()
	e := doctorSetup(t, AgentClaude, url)
	cs := e.run(t, []Agent{AgentClaude}, DoctorOpts{}, DoctorDeps{ExpectedTools: []string{"a"}})
	r := checkNamed(t, cs, "server-reachable", "")
	if r.Result != "fail" || !strings.Contains(r.Next, "start the server") {
		t.Errorf("server-reachable = %+v", r)
	}
	if got := checkNamed(t, cs, "tools-match", "").Result; got != "skipped" {
		t.Errorf("tools-match = %s, want skipped", got)
	}
}

func stubGet(status int) func(context.Context, string) (int, error) {
	return func(context.Context, string) (int, error) { return status, nil }
}

func TestDoctor_SlugNotRegistered(t *testing.T) {
	e := doctorSetup(t, AgentClaude, "http://localhost:8080/shop/mcp")
	cs := e.run(t, []Agent{AgentClaude}, DoctorOpts{}, DoctorDeps{HTTPGet: stubGet(404)})
	r := checkNamed(t, cs, "server-reachable", "")
	if r.Result != "fail" || !strings.Contains(r.Next, "not registered") || !strings.Contains(r.Next, "shop") {
		t.Errorf("got %+v", r)
	}
}

func TestDoctor_SchemaNotMigrated(t *testing.T) {
	e := doctorSetup(t, AgentClaude, "http://localhost:8080/shop/mcp")
	cs := e.run(t, []Agent{AgentClaude}, DoctorOpts{}, DoctorDeps{HTTPGet: stubGet(503)})
	r := checkNamed(t, cs, "server-reachable", "")
	if r.Result != "fail" || !strings.Contains(r.Next, "lens migrate --project shop") {
		t.Errorf("got %+v", r)
	}
}

func TestDoctor_ToolMismatch(t *testing.T) {
	url := newMCPServer(t, "a", "b")
	e := doctorSetup(t, AgentClaude, url)
	cs := e.run(t, []Agent{AgentClaude}, DoctorOpts{}, DoctorDeps{ExpectedTools: []string{"a", "c"}})
	r := checkNamed(t, cs, "tools-match", "")
	if r.Result != "fail" || !strings.Contains(r.Detail, "missing: c") || !strings.Contains(r.Detail, "extra: b") || r.Next == "" {
		t.Errorf("got %+v", r)
	}
}

func TestDoctor_InstalledURLDrift(t *testing.T) {
	url := newMCPServer(t, "a")
	e := doctorSetup(t, AgentClaude, url)
	p := e.tg.Abs(".mcp.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), url, "http://localhost:1/mcp", 1)
	if edited == string(data) {
		t.Fatal("url not found in .mcp.json")
	}
	if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	cs := e.run(t, []Agent{AgentClaude}, DoctorOpts{}, DoctorDeps{ExpectedTools: []string{"a"}})
	r := checkNamed(t, cs, "installed-url-matches", AgentClaude)
	if r.Result != "fail" || r.Next != "run: lens agent install claude" {
		t.Errorf("got %+v", r)
	}
}

func TestDoctor_Shadowed(t *testing.T) {
	url := newMCPServer(t, "a")
	e := doctorSetup(t, AgentClaude, url)
	f := filepath.Join(e.env.Home, ".claude.json")
	body := `{"mcpServers":{"projectlens":{"type":"http","url":"http://localhost:1/mcp"}}}`
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cs := e.run(t, []Agent{AgentClaude}, DoctorOpts{}, DoctorDeps{ExpectedTools: []string{"a"}})
	r := checkNamed(t, cs, "not-shadowed", AgentClaude)
	if r.Result != "fail" || !strings.Contains(r.Next, f) {
		t.Errorf("got %+v", r)
	}
	after, _ := os.ReadFile(f)
	if string(after) != body {
		t.Errorf("file changed: %s", after)
	}
}

func TestDoctor_CodexTrusted(t *testing.T) {
	url := newMCPServer(t, "a")
	e := doctorSetup(t, AgentCodex, url)
	deps := DoctorDeps{ExpectedTools: []string{"a"}}
	cs := e.run(t, []Agent{AgentCodex}, DoctorOpts{}, deps)
	r := checkNamed(t, cs, "agent-will-load", AgentCodex)
	if r.Result != "manual" || !strings.Contains(r.Next, "trust the project") {
		t.Errorf("untrusted: %+v", r)
	}
	cfg := filepath.Join(e.env.Home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[projects.\"" + e.tg.Root + "\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cs = e.run(t, []Agent{AgentCodex}, DoctorOpts{}, deps)
	if r := checkNamed(t, cs, "agent-will-load", AgentCodex); r.Result != "pass" {
		t.Errorf("trusted: %+v", r)
	}
	if r := checkNamed(t, cs, "not-shadowed", AgentCodex); r.Result != "pass" {
		t.Errorf("not-shadowed: %+v", r)
	}
	if r := checkNamed(t, cs, "installed-url-matches", AgentCodex); r.Result != "pass" {
		t.Errorf("installed-url-matches: %+v", r)
	}
}

func TestDoctor_CodexShadowed(t *testing.T) {
	e := doctorSetup(t, AgentCodex, "http://localhost:8080/mcp")
	cfg := filepath.Join(e.env.Home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[mcp_servers.projectlens]\nurl = \"http://localhost:1/mcp\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cs := e.run(t, []Agent{AgentCodex}, DoctorOpts{}, DoctorDeps{HTTPGet: stubGet(200)})
	if r := checkNamed(t, cs, "not-shadowed", AgentCodex); r.Result != "fail" || r.Next == "" {
		t.Errorf("got %+v", r)
	}
}

func TestDoctor_NotInstalledIsNotAnItemFailure(t *testing.T) {
	e := doctorSetup(t, AgentClaude, "http://localhost:8080/mcp")
	cs := e.run(t, nil, DoctorOpts{}, DoctorDeps{})
	if hasCheck(cs, "items-current") {
		t.Errorf("items-current present: %+v", cs)
	}
	if len(cs) != 2 || cs[0].Name != "assets-found" || cs[1].Name != "lens-on-path" {
		t.Errorf("got %+v", cs)
	}
}

func TestDoctor_URLOverride(t *testing.T) {
	e := doctorSetup(t, AgentClaude, "http://localhost:8080/mcp")
	var probed []string
	d := DoctorDeps{
		ExpectedTools: []string{"a"},
		HTTPGet: func(_ context.Context, u string) (int, error) {
			probed = append(probed, u)
			return 200, nil
		},
		ListTools: func(_ context.Context, u string) ([]string, error) { return []string{"a"}, nil },
	}
	e.run(t, []Agent{AgentClaude}, DoctorOpts{URLOverride: "http://localhost:9/other/mcp"}, d)
	if len(probed) != 1 || probed[0] != "http://localhost:9/other/mcp" {
		t.Errorf("probed %v", probed)
	}
}

func TestDoctor_AssetsMissing(t *testing.T) {
	e := doctorSetup(t, AgentClaude, "http://localhost:8080/mcp")
	cs := e.run(t, []Agent{AgentClaude}, DoctorOpts{AssetsErr: errors.New("not found")}, DoctorDeps{HTTPGet: stubGet(200)})
	if r := checkNamed(t, cs, "assets-found", ""); r.Result != "fail" || !strings.Contains(r.Next, "--assets") {
		t.Errorf("assets-found: %+v", r)
	}
	if r := checkNamed(t, cs, "items-current", AgentClaude); r.Result != "skipped" {
		t.Errorf("items-current: %+v", r)
	}
}

func TestDoctor_ItemsNotCurrent(t *testing.T) {
	e := doctorSetup(t, AgentClaude, "http://localhost:8080/mcp")
	if err := os.RemoveAll(e.tg.Abs(".claude/skills/alpha")); err != nil {
		t.Fatal(err)
	}
	cs := e.run(t, []Agent{AgentClaude}, DoctorOpts{}, DoctorDeps{HTTPGet: stubGet(200)})
	r := checkNamed(t, cs, "items-current", AgentClaude)
	if r.Result != "fail" || r.Next != "run: lens agent install claude" {
		t.Errorf("got %+v", r)
	}
}

func TestDoctor_EveryFailureHasNext(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + l.Addr().String() + "/mcp"
	l.Close()
	var all []Check
	e := doctorSetup(t, AgentClaude, closed)
	all = append(all, e.run(t, []Agent{AgentClaude}, DoctorOpts{}, DoctorDeps{})...)
	all = append(all, e.run(t, []Agent{AgentClaude}, DoctorOpts{AssetsErr: errors.New("x")}, DoctorDeps{LookPath: func(string) (string, error) { return "", errors.New("no") }})...)
	e2 := doctorSetup(t, AgentClaude, "http://localhost:8080/shop/mcp")
	all = append(all, e2.run(t, []Agent{AgentClaude}, DoctorOpts{}, DoctorDeps{HTTPGet: stubGet(404)})...)
	all = append(all, e2.run(t, []Agent{AgentClaude}, DoctorOpts{}, DoctorDeps{HTTPGet: stubGet(503)})...)
	e3 := doctorSetup(t, AgentCodex, "http://localhost:8080/mcp")
	all = append(all, e3.run(t, []Agent{AgentCodex}, DoctorOpts{}, DoctorDeps{HTTPGet: stubGet(200), ExpectedTools: []string{"a"},
		ListTools: func(context.Context, string) ([]string, error) { return []string{"z"}, nil }})...)
	sawFail := false
	for _, c := range all {
		if c.Result == "fail" || c.Result == "manual" {
			sawFail = true
			if c.Next == "" {
				t.Errorf("%s/%s is %s with empty Next", c.Name, c.Agent, c.Result)
			}
		}
	}
	if !sawFail {
		t.Fatal("no failing fixtures")
	}
}
