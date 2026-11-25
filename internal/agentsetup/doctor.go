package agentsetup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// networkTimeout bounds each network operation doctor performs.
const networkTimeout = 5 * time.Second

// CheckResult is the outcome of one check.
type CheckResult string

const (
	CheckPass    CheckResult = "pass"
	CheckFail    CheckResult = "fail"
	CheckSkipped CheckResult = "skipped"
	CheckManual  CheckResult = "manual"
)

// Check names, in report order.
const (
	checkAssetsFound      = "assets-found"
	checkItemsCurrent     = "items-current"
	checkServerReachable  = "server-reachable"
	checkToolsMatch       = "tools-match"
	checkInstalledURL     = "installed-url-matches"
	checkNotShadowed      = "not-shadowed"
	checkNoDuplicateHooks = "no-duplicate-hooks"
	checkAgentWillLoad    = "agent-will-load"
	checkLensOnPath       = "lens-on-path"
	nextAssets            = "pass --assets <path-to-projectlens>/agent"
	nextStartServer       = "start the server: make mcp (or make docker-up)"
	nextToolsMismatch     = "rebuild and restart the server, or check the URL points at ProjectLens"
	defaultMCPPath        = "/mcp"
	nextDuplicateHooks    = "delete the ProjectLens hook entries from " + claudeSharedSettings + " (they are installed in .claude/settings.local.json)"
	claudeSharedSettings  = ".claude/settings.json"
	codexTrustedLevel     = "trusted"
	codexProjectsTableKey = "projects"
)

// Check is one diagnosis result.
type Check struct {
	Name   string      `json:"name"`
	Agent  Agent       `json:"agent"` // "" for checks that are not per agent
	Result CheckResult `json:"result"`
	Detail string      `json:"detail"`
	Next   string      `json:"next"` // required when Result is "fail" or "manual"
}

// DoctorDeps holds the injectable side effects of Doctor.
type DoctorDeps struct {
	ExpectedTools []string
	ListTools     func(ctx context.Context, url string) ([]string, error)       // nil -> MCPListTools
	HTTPGet       func(ctx context.Context, url string) (status int, err error) // nil -> default client, 5s timeout
	LookPath      func(string) (string, error)                                  // nil -> exec.LookPath
}

// DoctorOpts selects what Doctor inspects.
type DoctorOpts struct {
	Agents      []Agent
	URLOverride string // --url
	AssetsDir   string // "" when ResolveAssets failed
	AssetsErr   error
}

// MCPListTools connects to a streamable-HTTP MCP server and returns its tool names.
func MCPListTools(ctx context.Context, url string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, networkTimeout)
	defer cancel()
	c, err := client.NewStreamableHttpClient(url)
	if err != nil {
		return nil, fmt.Errorf("create MCP client: %w", err)
	}
	defer c.Close()
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("start MCP client: %w", err)
	}
	var init mcp.InitializeRequest
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "lens-agent-doctor", Version: "1.0.0"}
	if _, err := c.Initialize(ctx, init); err != nil {
		return nil, fmt.Errorf("initialize MCP session: %w", err)
	}
	res, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, t := range res.Tools {
		names = append(names, t.Name)
	}
	return names, nil
}

func defaultHTTPGet(ctx context.Context, u string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, networkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// Doctor diagnoses why the installed agents cannot reach or use the MCP
// server. It never writes any file.
func Doctor(ctx context.Context, env Env, t Target, m *Manifest, o DoctorOpts, d DoctorDeps) []Check {
	if d.ListTools == nil {
		d.ListTools = MCPListTools
	}
	if d.HTTPGet == nil {
		d.HTTPGet = defaultHTTPGet
	}
	if d.LookPath == nil {
		d.LookPath = exec.LookPath
	}

	var out []Check
	if o.AssetsErr != nil {
		out = append(out, Check{Name: checkAssetsFound, Result: CheckFail, Detail: o.AssetsErr.Error(), Next: nextAssets})
	} else {
		out = append(out, Check{Name: checkAssetsFound, Result: CheckPass, Detail: o.AssetsDir})
	}

	if len(o.Agents) > 0 {
		recorded := func(a Agent) string {
			if m != nil && m.Agents[a] != nil && m.Agents[a].URL != "" {
				return m.Agents[a].URL
			}
			return o.URLOverride
		}
		probeURL := func(a Agent) string {
			if o.URLOverride != "" {
				return o.URLOverride
			}
			return recorded(a)
		}

		for _, a := range o.Agents {
			out = append(out, itemsCurrent(t, m, o, a, recorded(a)))
		}

		var urls []string
		seen := map[string]bool{}
		for _, a := range o.Agents {
			if u := probeURL(a); u != "" && !seen[u] {
				seen[u] = true
				urls = append(urls, u)
			}
		}
		reachable := map[string]bool{}
		for _, u := range urls {
			c := serverReachable(ctx, d, u)
			reachable[u] = c.Result == CheckPass
			out = append(out, c)
		}
		for _, u := range urls {
			out = append(out, toolsMatch(ctx, d, u, reachable[u]))
		}

		for _, a := range o.Agents {
			out = append(out, installedURLMatches(t, a, recorded(a)))
		}
		for _, a := range o.Agents {
			out = append(out, notShadowed(env, a, recorded(a)))
		}
		for _, a := range o.Agents {
			if a == AgentClaude {
				out = append(out, noDuplicateHooks(t, o))
			}
		}
		for _, a := range o.Agents {
			out = append(out, agentWillLoad(env, t, a))
		}
	}

	out = append(out, lensOnPath(d))
	return out
}

func itemsCurrent(t Target, m *Manifest, o DoctorOpts, a Agent, url string) Check {
	c := Check{Name: checkItemsCurrent, Agent: a}
	if o.AssetsErr != nil || o.AssetsDir == "" {
		c.Result = CheckSkipped
		c.Detail = "assets not found"
		return c
	}
	installNext := "run: lens agent install " + string(a)
	canon, err := LoadCanonical(o.AssetsDir, a, url)
	var sts []ItemStatus
	if err == nil {
		sts, err = Evaluate(t, a, canon, m)
	}
	if err != nil {
		c.Result, c.Detail, c.Next = CheckFail, err.Error(), installNext
		return c
	}
	var bad []string
	var manualPaths []string
	needInstall := false
	for _, s := range sts {
		if s.State == StateCurrent {
			continue
		}
		bad = append(bad, fmt.Sprintf("%s %s: %s", s.Kind, s.ID, s.State))
		if s.State == StateManual {
			manualPaths = append(manualPaths, s.Path)
		} else {
			needInstall = true
		}
	}
	if len(bad) == 0 {
		c.Result = CheckPass
		return c
	}
	c.Result = CheckFail
	c.Detail = strings.Join(bad, "; ")
	var nexts []string
	if needInstall {
		nexts = append(nexts, installNext)
	}
	if len(manualPaths) > 0 {
		nexts = append(nexts, "add the printed entry to "+strings.Join(manualPaths, ", "))
	}
	c.Next = strings.Join(nexts, "; ")
	return c
}

// urlSlug returns the first path segment of u.
func urlSlug(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	seg, _, _ := strings.Cut(strings.TrimPrefix(p.Path, "/"), "/")
	return seg
}

func serverReachable(ctx context.Context, d DoctorDeps, u string) Check {
	c := Check{Name: checkServerReachable, Detail: u}
	status, err := d.HTTPGet(ctx, u)
	if err != nil {
		c.Result, c.Detail, c.Next = CheckFail, u+": "+err.Error(), nextStartServer
		return c
	}
	p, _ := url.Parse(u)
	switch {
	case status == http.StatusNotFound && (p == nil || strings.TrimRight(p.Path, "/") != defaultMCPPath):
		c.Result, c.Detail = CheckFail, fmt.Sprintf("%s: HTTP 404", u)
		c.Next = urlSlug(u) + " is not registered in configs/projects.yaml"
	case status == http.StatusServiceUnavailable:
		c.Result, c.Detail = CheckFail, fmt.Sprintf("%s: HTTP 503", u)
		c.Next = "run: lens migrate --project " + urlSlug(u)
		if p != nil && strings.TrimRight(p.Path, "/") == defaultMCPPath { // single-project mode
			c.Next = "run: lens migrate"
		}
	default:
		c.Result, c.Detail = CheckPass, fmt.Sprintf("%s: HTTP %d", u, status)
	}
	return c
}

func toolsMatch(ctx context.Context, d DoctorDeps, u string, reachable bool) Check {
	c := Check{Name: checkToolsMatch}
	if !reachable {
		c.Result, c.Detail = CheckSkipped, u+": server not reachable"
		return c
	}
	got, err := d.ListTools(ctx, u)
	if err != nil {
		c.Result, c.Detail, c.Next = CheckFail, u+": "+err.Error(), nextToolsMismatch
		return c
	}
	have := toSet(got)
	want := toSet(d.ExpectedTools)
	var missing, extra []string
	for n := range want {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	for n := range have {
		if !want[n] {
			extra = append(extra, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) == 0 && len(extra) == 0 {
		c.Result, c.Detail = CheckPass, fmt.Sprintf("%s: %d tools", u, len(got))
		return c
	}
	c.Result = CheckFail
	c.Detail = fmt.Sprintf("%s: missing: %s; extra: %s", u, strings.Join(missing, ", "), strings.Join(extra, ", "))
	c.Next = nextToolsMismatch
	return c
}

func toSet(names []string) map[string]bool {
	s := make(map[string]bool, len(names))
	for _, n := range names {
		s[n] = true
	}
	return s
}

// jsonServerURL reads mcpServers.projectlens.url from a JSON file. present is
// false when the file is missing, unparseable, or has no such entry.
func jsonServerURL(path string) (u string, present bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return "", false
	}
	raw, ok := doc.MCPServers[mcpServerName]
	if !ok {
		return "", false
	}
	var e struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(raw, &e) // an entry without a string url has url ""
	return e.URL, true
}

// tomlTable parses a TOML file; nil when missing or unparseable.
func tomlTable(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if toml.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}

func codexServerURL(path string) (u string, present bool) {
	servers, _ := tomlTable(path)["mcp_servers"].(map[string]any)
	entry, ok := servers[mcpServerName]
	if !ok {
		return "", false
	}
	t, _ := entry.(map[string]any)
	u, _ = t["url"].(string)
	return u, true
}

func installedURLMatches(t Target, a Agent, want string) Check {
	c := Check{Name: checkInstalledURL, Agent: a}
	pl, err := PlacementFor(a, ScopePersonal)
	if err != nil {
		c.Result, c.Detail, c.Next = CheckFail, err.Error(), "run: lens agent install "+string(a)
		return c
	}
	var got string
	var present bool
	if a == AgentCodex {
		got, present = codexServerURL(t.Abs(pl.MCPFile))
	} else {
		got, present = jsonServerURL(t.Abs(pl.MCPFile))
	}
	switch {
	case !present:
		c.Result, c.Detail = CheckSkipped, "no installed entry in "+pl.MCPFile
	case got == want:
		c.Result, c.Detail = CheckPass, got
	default:
		c.Result = CheckFail
		c.Detail = fmt.Sprintf("%s has %q, recorded %q", pl.MCPFile, got, want)
		c.Next = "run: lens agent install " + string(a)
	}
	return c
}

func notShadowed(env Env, a Agent, want string) Check {
	c := Check{Name: checkNotShadowed, Agent: a}
	var file, got string
	var present bool
	switch a {
	case AgentCodex:
		file = filepath.Join(env.Home, ".codex", "config.toml")
		got, present = codexServerURL(file)
	default:
		file = filepath.Join(env.Home, ".claude.json")
		got, present = jsonServerURL(file)
	}
	if present && got != want {
		c.Result = CheckFail
		c.Detail = fmt.Sprintf("%s defines projectlens at %q, not %q", file, got, want)
		c.Next = "remove or update the projectlens entry in " + file
		return c
	}
	c.Result = CheckPass
	return c
}

// noDuplicateHooks reports ProjectLens hooks in .claude/settings.json, as the old
// manual instructions placed them: beside the installed ones in
// settings.local.json they would fire twice. A command matches a canonical one
// once the ID marker is stripped from both, so the old unprefixed text and a
// prefixed copy both count. It only reads; a missing or unparseable file passes.
func noDuplicateHooks(t Target, o DoctorOpts) Check {
	c := Check{Name: checkNoDuplicateHooks, Agent: AgentClaude}
	if o.AssetsErr != nil || o.AssetsDir == "" {
		c.Result, c.Detail = CheckSkipped, "assets not found"
		return c
	}
	hooks, err := loadClaudeHooks(filepath.Join(o.AssetsDir, "claude", "settings-snippet.json"))
	if err != nil {
		c.Result, c.Detail = CheckSkipped, err.Error()
		return c
	}
	canon := make(map[string]string, len(hooks))
	for _, h := range hooks {
		canon[stripHookID(h.Command)] = h.ID
	}
	file := t.Abs(claudeSharedSettings)
	_, events, _, err := loadHooksDoc(file)
	if err != nil {
		c.Result, c.Detail = CheckPass, claudeSharedSettings+" not readable as settings; not checked"
		return c
	}
	names := make([]string, 0, len(events))
	for ev := range events {
		names = append(names, ev)
	}
	sort.Strings(names)
	var found []string
	for _, ev := range names {
		for _, g := range events[ev] {
			entries, err := groupEntries(file, g)
			if err != nil {
				continue
			}
			for _, raw := range entries {
				_, v := entryID(raw)
				if id, ok := canon[stripHookID(v.Command)]; ok && v.Command != "" {
					found = append(found, ev+" "+id)
				}
			}
		}
	}
	if len(found) == 0 {
		c.Result = CheckPass
		return c
	}
	c.Result = CheckFail
	c.Detail = claudeSharedSettings + " also has ProjectLens hooks (they would fire twice): " + strings.Join(found, ", ")
	c.Next = nextDuplicateHooks
	return c
}

// stripHookID removes a leading ": projectlens-hook=<id>; " marker.
func stripHookID(command string) string {
	if loc := hookIDRe.FindStringIndex(command); loc != nil {
		return command[loc[1]:]
	}
	return command
}

func agentWillLoad(env Env, t Target, a Agent) Check {
	c := Check{Name: checkAgentWillLoad, Agent: a}
	if a == AgentCodex {
		projects, _ := tomlTable(filepath.Join(env.Home, ".codex", "config.toml"))[codexProjectsTableKey].(map[string]any)
		entry, _ := projects[t.Root].(map[string]any)
		if lvl, _ := entry["trust_level"].(string); lvl == codexTrustedLevel {
			c.Result, c.Detail = CheckPass, t.Root+" is trusted"
			return c
		}
		c.Result = CheckManual
		c.Detail = "cannot confirm Codex trusts " + t.Root
		c.Next = "open Codex in " + t.Root + " and trust the project"
		return c
	}
	c.Result = CheckManual
	c.Detail = "Claude Code asks for approval of project MCP servers"
	c.Next = `run /mcp in Claude Code in ` + t.Root + ` and approve "projectlens"`
	return c
}

func lensOnPath(d DoctorDeps) Check {
	c := Check{Name: checkLensOnPath}
	p, err := d.LookPath("lens")
	if err == nil {
		c.Result, c.Detail = CheckPass, p
		return c
	}
	dir := "the directory of the lens binary"
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	c.Result, c.Detail = CheckFail, err.Error()
	c.Next = "add " + dir + " to PATH, or run: make install"
	return c
}
