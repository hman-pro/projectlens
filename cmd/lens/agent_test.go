package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/hman-pro/projectlens/internal/gitenv"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/hman-pro/projectlens/internal/agentsetup"
	"github.com/spf13/cobra"
)

// agentRepoRoot returns the projectlens checkout (the directory with go.mod).
func agentRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// gitCmd runs git in dir with no repository-local variables inherited from the
// caller and no global or system git config, so neither leaks into a test.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(gitenv.Scrub(os.Environ()), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// agentFixture points bakedCheckout at the checkout, then moves into a fresh
// git repository. It returns the repo root.
func agentFixture(t *testing.T) string {
	t.Helper()
	checkout := agentRepoRoot(t)
	old := bakedCheckout
	bakedCheckout = checkout
	t.Cleanup(func() { bakedCheckout = old })

	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "t@example.com"},
		{"config", "core.excludesFile", os.DevNull},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		gitCmd(t, repo, args...)
	}
	t.Setenv("HOME", t.TempDir())
	t.Chdir(repo)
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// runAgent runs `lens agent <args>` under a root carrying the real
// persistent flags and returns stdout, stderr, and the error.
func runAgent(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := &cobra.Command{Use: "lens"}
	root.PersistentFlags().String("config", "configs/index.yaml", "")
	root.PersistentFlags().String("db", "", "")
	root.PersistentFlags().String("repo", "", "")
	root.PersistentFlags().String("project", "", "")
	root.PersistentFlags().String("projects", "configs/projects.yaml", "")
	root.AddCommand(newAgentCmd())
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(append([]string{"agent"}, args...))
	err := root.Execute()
	return out.String(), errb.String(), err
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("want *exitError, got %v", err)
	}
	return ee.code
}

func TestAgentCmd_UnsupportedAgentExits2(t *testing.T) {
	agentFixture(t)
	_, _, err := runAgent(t, "install", "cursor")
	if exitCode(t, err) != 2 || !strings.Contains(err.Error(), "supported: claude, codex") {
		t.Fatalf("got %v", err)
	}
}

func TestAgentCmd_InstallMissingArgExits2(t *testing.T) {
	agentFixture(t)
	_, _, err := runAgent(t, "install")
	if exitCode(t, err) != 2 {
		t.Fatalf("got %v", err)
	}
}

func TestAgentCmd_InstallThenStatusExits0(t *testing.T) {
	root := agentFixture(t)
	out, _, err := runAgent(t, "install", "claude", "--url", "http://localhost:1/mcp")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Target: " + root + "   URL: http://localhost:1/mcp",
		"  written   .mcp.json                          (mcpServers.projectlens)",
		"  written   .claude/skills/use-projectlens/",
		"  written   .claude/settings.local.json        (4 hooks)",
		"  written   .git/info/exclude",
		"Next: restart Claude Code in this repo and approve the \"projectlens\" server.",
		"      Then run: lens agent doctor claude",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("install output missing %q:\n%s", want, out)
		}
	}
	out, _, err = runAgent(t, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if strings.Contains(out, "stale") {
		t.Fatalf("status output:\n%s", out)
	}
	skills, err := os.ReadDir(filepath.Join(bakedCheckout, "agent", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	rows := []string{
		"  current         .mcp.json                          (mcpServers.projectlens)",
		"  current         .claude/settings.local.json        (4 hooks)",
	}
	for _, sk := range skills {
		if sk.IsDir() {
			rows = append(rows, "  current         .claude/skills/"+sk.Name()+"/")
		}
	}
	for _, want := range rows {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("status missing row %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "\n  "); n != len(rows) {
		t.Errorf("status has %d rows, want %d:\n%s", n, len(rows), out)
	}
}

func TestAgentCmd_StatusNothingInstalledExits1(t *testing.T) {
	agentFixture(t)
	out, _, err := runAgent(t, "status")
	if exitCode(t, err) != 1 {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(out, "nothing installed") || !strings.Contains(out, "lens agent install") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestAgentCmd_StatusStaleExits1(t *testing.T) {
	agentFixture(t)
	assets := filepath.Join(t.TempDir(), "agent")
	if err := agentsetup.CopyDir(filepath.Join(bakedCheckout, "agent"), assets); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runAgent(t, "install", "claude", "--url", "http://localhost:1/mcp", "--assets", assets); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(assets, "skills", "use-projectlens", "SKILL.md")
	f, err := os.OpenFile(skill, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nnewer\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out, _, err := runAgent(t, "status", "--assets", assets)
	if exitCode(t, err) != 1 || !strings.Contains(out, "stale") {
		t.Fatalf("err=%v out:\n%s", err, out)
	}
}

func TestAgentCmd_StatusListsUnmanagedCreatedPaths(t *testing.T) {
	root := agentFixture(t)
	if _, _, err := runAgent(t, "install", "claude", "--url", "http://localhost:1/mcp"); err != nil {
		t.Fatal(err)
	}
	mp := filepath.Join(root, agentsetup.ManifestPath)
	data, err := os.ReadFile(mp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["created_paths"] = append(m["created_paths"].([]any), ".claude/skills/leftover")
	data, _ = json.Marshal(m)
	if err := os.WriteFile(mp, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, _ := runAgent(t, "status")
	tail := out[strings.Index(out, "Excluded from git"):]
	for _, bad := range []string{".mcp.json", "settings.local.json", ".claude/skills/use-projectlens"} {
		if strings.Contains(tail, bad) {
			t.Errorf("managed path %q listed as unmanaged:\n%s", bad, tail)
		}
	}
	for _, want := range []string{
		"Excluded from git but not managed (left by an earlier install):",
		".claude/skills/leftover",
		"Re-own with: lens agent install <agent>, or delete the path.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestAgentCmd_StatusJSON(t *testing.T) {
	agentFixture(t)
	if _, _, err := runAgent(t, "install", "claude", "--url", "http://localhost:1/mcp"); err != nil {
		t.Fatal(err)
	}
	out, _, err := runAgent(t, "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, out)
	}
	if len(items) == 0 {
		t.Fatal("no items")
	}
	for _, it := range items {
		assertJSONKeys(t, it, "agent", "kind", "id", "path", "state", "owned", "tracked", "detail")
	}
}

// assertJSONKeys pins the --json contract: exactly these keys, so a renamed or
// untagged field fails here instead of in a user's script.
func assertJSONKeys(t *testing.T, obj map[string]any, want ...string) {
	t.Helper()
	got := make([]string, 0, len(obj))
	for k := range obj {
		got = append(got, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("JSON keys = %v, want %v", got, want)
	}
}

func TestAgentCmd_DryRunOutput(t *testing.T) {
	root := agentFixture(t)
	out, _, err := runAgent(t, "install", "claude", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Dry run. Nothing was written.") {
		t.Fatalf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatalf(".mcp.json exists after dry run: %v", err)
	}
}

func TestAgentCmd_UninstallAll(t *testing.T) {
	agentFixture(t)
	for _, a := range []string{"claude", "codex"} {
		if _, _, err := runAgent(t, "install", a, "--url", "http://localhost:1/mcp"); err != nil {
			t.Fatalf("install %s: %v", a, err)
		}
	}
	if out, _, err := runAgent(t, "uninstall", "all"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	out, _, err := runAgent(t, "status")
	if exitCode(t, err) != 1 || !strings.Contains(out, "nothing installed") {
		t.Fatalf("err=%v out:\n%s", err, out)
	}
}

func TestAgentCmd_UninstallNothingExits0(t *testing.T) {
	agentFixture(t)
	out, _, err := runAgent(t, "uninstall", "all")
	if err != nil || !strings.Contains(out, "nothing installed") {
		t.Fatalf("err=%v out:\n%s", err, out)
	}
}

func TestAgentCmd_InstallCodexPrintsSnippet(t *testing.T) {
	agentFixture(t)
	out, _, err := runAgent(t, "install", "codex", "--url", "http://localhost:1/mcp")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Next: open Codex in this repo, trust the project, then run: lens agent doctor codex",
		"Codex has no hooks. For always-on guidance, paste this into AGENTS.md:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	snippet, err := os.ReadFile(filepath.Join(bakedCheckout, "agent", "codex", "AGENTS.md.snippet"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, strings.TrimRight(string(snippet), "\n")+"\n") {
		t.Errorf("output does not end with the snippet:\n%s", out)
	}
}

func TestAgentCmd_DoesNotNeedIndexConfig(t *testing.T) {
	agentFixture(t)
	if _, err := os.Stat("configs"); !os.IsNotExist(err) {
		t.Fatal("fixture repo has configs/")
	}
	out, _, err := runAgent(t, "--config", "/nonexistent/index.yaml", "install", "claude", "--url", "http://localhost:1/mcp")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestAgentCmd_DoctorNothingInstalledExits1(t *testing.T) {
	agentFixture(t)
	out, _, err := runAgent(t, "doctor")
	if exitCode(t, err) != 1 || !strings.Contains(out, "nothing installed") {
		t.Fatalf("err=%v out:\n%s", err, out)
	}
}

func TestAgentCmd_DoctorJSONFailsWhenServerDown(t *testing.T) {
	agentFixture(t)
	if _, _, err := runAgent(t, "install", "claude", "--url", "http://127.0.0.1:1/mcp"); err != nil {
		t.Fatal(err)
	}
	out, _, err := runAgent(t, "doctor", "claude", "--json")
	if exitCode(t, err) != 1 {
		t.Fatalf("err=%v out:\n%s", err, out)
	}
	var checks []map[string]any
	if err := json.Unmarshal([]byte(out), &checks); err != nil || len(checks) == 0 {
		t.Fatalf("stdout is not a JSON array of checks: %v\n%s", err, out)
	}
	for _, c := range checks {
		assertJSONKeys(t, c, "name", "agent", "result", "detail", "next")
	}
}

func TestAgentCmd_UnknownSubcommandExits2(t *testing.T) {
	agentFixture(t)
	_, _, err := runAgent(t, "bogus")
	if exitCode(t, err) != 2 || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("got %v", err)
	}
	_, _, err = runAgent(t, "bogus", "claude")
	if exitCode(t, err) != 2 {
		t.Fatalf("got %v", err)
	}
}

func TestAgentCmd_BareAgentPrintsHelpExits0(t *testing.T) {
	agentFixture(t)
	out, _, err := runAgent(t)
	if err != nil || !strings.Contains(out, "install") {
		t.Fatalf("err=%v out:\n%s", err, out)
	}
}

func TestPrintRows_HooksGroupByActionAndDetail(t *testing.T) {
	hook := func(id string) agentsetup.Item {
		return agentsetup.Item{Agent: agentsetup.AgentClaude, Kind: agentsetup.KindHook, ID: id, Path: ".claude/settings.local.json"}
	}
	rows := []row{
		{action: "current", item: hook("a")},
		{action: "current", item: hook("b")},
		{action: "skipped", item: hook("c"), detail: "defined outside the managed block"},
		{action: "current", item: hook("d")},
	}
	var buf bytes.Buffer
	printRows(&buf, rows, 9)
	out := buf.String()
	if !strings.Contains(out, "  current   .claude/settings.local.json        (3 hooks)\n") ||
		!strings.Contains(out, "  skipped   .claude/settings.local.json        (1 hook; defined outside the managed block)\n") ||
		strings.Count(out, "\n") != 2 {
		t.Fatalf("output:\n%s", out)
	}
}

func TestAgentCmd_ManualItemPrintsSnippet(t *testing.T) {
	root := agentFixture(t)
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte("{\"mcpServers\":{}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, root, "add", ".mcp.json")
	gitCmd(t, root, "commit", "-q", "-m", "mcp")
	out, _, err := runAgent(t, "install", "claude", "--url", "http://localhost:1/mcp")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "  manual    .mcp.json") {
			if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "      ") || strings.TrimSpace(lines[i+1]) == "" {
				t.Fatalf("no indented snippet beneath manual row:\n%s", out)
			}
			return
		}
	}
	t.Fatalf("no manual .mcp.json row:\n%s", out)
}

func TestAgentCmd_WorktreeNotice(t *testing.T) {
	root := agentFixture(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitCmd(t, root, "worktree", "add", "-q", "-b", "wt", wt)
	t.Chdir(wt)
	out, _, err := runAgent(t, "install", "claude", "--url", "http://localhost:1/mcp")
	if err != nil {
		t.Fatal(err)
	}
	want := "Note: this is a linked worktree; the exclude entries above apply to every worktree of this repository."
	if !strings.Contains(out, want) {
		t.Fatalf("missing worktree notice:\n%s", out)
	}
}

func TestAgentCmd_CodexMissingSnippetFailsBeforeWriting(t *testing.T) {
	root := agentFixture(t)
	assets := filepath.Join(t.TempDir(), "agent")
	if err := agentsetup.CopyDir(filepath.Join(bakedCheckout, "agent"), assets); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(assets, "codex", "AGENTS.md.snippet")); err != nil {
		t.Fatal(err)
	}
	_, _, err := runAgent(t, "install", "codex", "--url", "http://localhost:1/mcp", "--assets", assets)
	if err == nil || !strings.Contains(err.Error(), "AGENTS.md snippet") {
		t.Fatalf("got %v", err)
	}
	if _, serr := os.Stat(filepath.Join(root, ".codex")); !os.IsNotExist(serr) {
		t.Fatalf("install wrote files before failing: %v", serr)
	}
}

// addOwnSettings adds a key of the developer's own to the installed
// .claude/settings.local.json, as Claude Code does on any "always allow".
func addOwnSettings(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(root, ".claude", "settings.local.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["permissions"] = map[string]any{"allow": []any{"Bash(ls:*)"}}
	data, _ = json.Marshal(doc)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readExclude(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestAgentCmd_CreatedPathOnlyLifecycle(t *testing.T) {
	root := agentFixture(t)
	excl := readExclude(t, root)
	if _, _, err := runAgent(t, "install", "claude", "--url", "http://localhost:1/mcp"); err != nil {
		t.Fatal(err)
	}
	addOwnSettings(t, root)
	if out, _, err := runAgent(t, "uninstall", "claude"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}

	// (a) The kept file is listed, with how to finish.
	unmanaged := []string{
		"Excluded from git but not managed (left by an earlier install):",
		"  .claude/settings.local.json",
		"Re-own with: lens agent install <agent>, or delete the path, then run: lens agent uninstall all",
	}
	out, _, err := runAgent(t, "status")
	if exitCode(t, err) != 1 {
		t.Fatalf("status: %v", err)
	}
	for _, want := range append([]string{"nothing installed"}, unmanaged...) {
		if !strings.Contains(out, want) {
			t.Errorf("status missing %q:\n%s", want, out)
		}
	}
	out, errOut, err := runAgent(t, "status", "--json")
	if exitCode(t, err) != 1 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("status --json: err=%v stdout:\n%s", err, out)
	}
	for _, want := range unmanaged {
		if !strings.Contains(errOut, want) {
			t.Errorf("status --json stderr missing %q:\n%s", want, errOut)
		}
	}
	out, _, err = runAgent(t, "uninstall", "all")
	if err != nil || !strings.Contains(out, "nothing installed") || !strings.Contains(out, unmanaged[1]) {
		t.Fatalf("uninstall all with the file present: err=%v\n%s", err, out)
	}

	// (b) Once the file is deleted, uninstall all leaves no trace.
	if err := os.Remove(filepath.Join(root, ".claude", "settings.local.json")); err != nil {
		t.Fatal(err)
	}
	if out, _, err := runAgent(t, "uninstall", "all"); err != nil {
		t.Fatalf("uninstall all: %v\n%s", err, out)
	}
	for _, p := range []string{agentsetup.ManifestPath, ".projectlens", ".claude"} {
		if _, err := os.Lstat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	if got := readExclude(t, root); got != excl {
		t.Errorf("exclude file not restored:\n%s", got)
	}
}

// (c) With the manifest deleted, live wiring is never reported as "nothing
// installed": every command names the install that recovers it.
func TestAgentCmd_ManifestDeletedPointsToRecovery(t *testing.T) {
	root := agentFixture(t)
	if _, _, err := runAgent(t, "install", "claude", "--url", "http://localhost:1/mcp"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, agentsetup.ManifestPath)); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status"}, {"doctor"}, {"uninstall", "all"}, {"uninstall", "claude"}} {
		out, _, err := runAgent(t, args...)
		wantCode := 1
		if args[0] == "uninstall" {
			wantCode = 0
		}
		if (wantCode == 0 && err != nil) || (wantCode != 0 && exitCode(t, err) != wantCode) {
			t.Errorf("%v: err=%v", args, err)
		}
		if strings.Contains(out, "nothing installed") || strings.Contains(out, "is not installed") ||
			!strings.Contains(out, "  .mcp.json") || !strings.Contains(out, "run: lens agent install claude") {
			t.Errorf("%v output:\n%s", args, out)
		}
	}
	out, errOut, err := runAgent(t, "status", "--json")
	if exitCode(t, err) != 1 || strings.TrimSpace(out) != "[]" || !strings.Contains(errOut, "run: lens agent install claude") {
		t.Fatalf("status --json: err=%v stdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
}

func TestAgentCmd_BadURLOrPortIsUsageErrorBeforeWriting(t *testing.T) {
	root := agentFixture(t)
	excl := readExclude(t, root)
	for _, args := range [][]string{
		{"install", "codex", "--url", `http://x/"y`},
		{"install", "claude", "--url", "ftp://x/mcp"},
		{"install", "claude", "--port", "-1"},
		{"install", "claude", "--port", "65536"},
	} {
		_, _, err := runAgent(t, args...)
		if exitCode(t, err) != 2 {
			t.Errorf("%v: %v", args, err)
		}
	}
	for _, p := range []string{".projectlens", ".codex", ".claude", ".mcp.json"} {
		if _, err := os.Lstat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Errorf("%s written: %v", p, err)
		}
	}
	if readExclude(t, root) != excl {
		t.Error("exclude file changed")
	}
}

// The restart-and-approve lines print only when the MCP entry is in place.
func TestAgentCmd_NextLinesWhenMCPManual(t *testing.T) {
	root := agentFixture(t)
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte("{\"mcpServers\":{}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, root, "add", ".mcp.json")
	gitCmd(t, root, "commit", "-q", "-m", "mcp")
	out, _, err := runAgent(t, "install", "claude", "--url", "http://localhost:1/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "restart Claude Code") || !strings.Contains(out, "Next: resolve the items above, then re-run: lens agent install claude") {
		t.Fatalf("manual MCP entry:\n%s", out)
	}
}

func TestAgentCmd_NextLinesWhenMCPInPlace(t *testing.T) {
	agentFixture(t)
	for _, run := range []string{"written", "current"} {
		out, _, err := runAgent(t, "install", "claude", "--url", "http://localhost:1/mcp")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "Next: restart Claude Code in this repo") || strings.Contains(out, "resolve the items above") {
			t.Fatalf("%s MCP entry:\n%s", run, out)
		}
	}
}

func TestAgentCmd_UninstallNamesAgentNotInstalled(t *testing.T) {
	agentFixture(t)
	if _, _, err := runAgent(t, "install", "claude", "--url", "http://localhost:1/mcp"); err != nil {
		t.Fatal(err)
	}
	out, _, err := runAgent(t, "uninstall", "codex")
	if err != nil || strings.TrimSpace(out) != "codex is not installed" {
		t.Fatalf("err=%v out:\n%s", err, out)
	}
}
