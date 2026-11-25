package agentsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// porcelain returns `git status --porcelain`, listing every untracked file.
func porcelain(t *testing.T, root string) string {
	t.Helper()
	return git(t, root, "status", "--porcelain", "--untracked-files=all")
}

func runInstall(t *testing.T, tg Target, assets string, o InstallOpts) InstallResult {
	t.Helper()
	res, err := Install(tg, assets, o)
	if err != nil {
		t.Fatalf("Install(%+v): %v", o, err)
	}
	return res
}

func claudeOpts() InstallOpts { return InstallOpts{Agent: AgentClaude, URL: evalURL} }
func codexOpts() InstallOpts  { return InstallOpts{Agent: AgentCodex, URL: evalURL} }

func forced(o InstallOpts) InstallOpts { o.Force = true; return o }

func itemKey(kind Kind, id string) string { return string(kind) + ":" + id }

func resultFor(t *testing.T, res InstallResult, kind Kind, id string) ItemResult {
	t.Helper()
	for _, r := range res.Items {
		if r.Kind == kind && r.ID == id {
			return r
		}
	}
	t.Fatalf("no result for %s %s in %+v", kind, id, res.Items)
	return ItemResult{}
}

// wantActions checks every item's action; items not named must have def.
func wantActions(t *testing.T, res InstallResult, def Action, named map[string]Action) {
	t.Helper()
	for _, r := range res.Items {
		want, ok := named[itemKey(r.Kind, r.ID)]
		if !ok {
			want = def
		}
		if r.Action != want {
			t.Errorf("%s %s: action %q, want %q (%+v)", r.Kind, r.ID, r.Action, want, r)
		}
	}
}

func loadManifest(t *testing.T, tg Target) *Manifest {
	t.Helper()
	m, err := LoadManifest(tg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func excludeBlock(t *testing.T, tg Target) []string {
	t.Helper()
	b, err := tg.ExcludeBlock()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func excludeBytes(t *testing.T, tg Target) string {
	t.Helper()
	b, err := os.ReadFile(tg.excludePath())
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// snapshot maps every path under root (except .git) to its content, mode, or link target.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == ".git" {
			return filepath.SkipDir
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out[rel] = "link:" + target
		case d.IsDir():
			out[rel] = "dir"
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			out[rel] = info.Mode().String() + ":" + string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func evaluateSaved(t *testing.T, tg Target, a Agent, cs []Canon) []ItemStatus {
	t.Helper()
	return evaluate(t, tg, a, cs, loadManifest(t, tg))
}

func deleteManifest(t *testing.T, tg Target) {
	t.Helper()
	if err := os.Remove(tg.Abs(ManifestPath)); err != nil {
		t.Fatal(err)
	}
}

var claudeBlock = []string{
	"/.claude/settings.local.json", "/.claude/skills/alpha/", "/.claude/skills/beta/",
	"/.mcp.json", "/.projectlens/agent-manifest.json",
}

var codexBlockPaths = []string{
	"/.agents/skills/alpha/", "/.agents/skills/beta/", "/.codex/config.toml",
	"/.projectlens/agent-manifest.json",
}

func TestInstall_CleanRepo_Claude(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	before := porcelain(t, root)
	assets := defaultFixture().dir(t)
	cs, err := LoadCanonical(assets, AgentClaude, evalURL)
	if err != nil {
		t.Fatal(err)
	}

	res := runInstall(t, tg, assets, claudeOpts())
	if len(res.Items) != len(cs) {
		t.Fatalf("%d results for %d items", len(res.Items), len(cs))
	}
	wantActions(t, res, ActionWritten, nil)
	for _, p := range []string{".mcp.json", ".claude/skills/alpha/SKILL.md", ".claude/skills/beta/SKILL.md", ".claude/settings.local.json"} {
		if !tg.Exists(p) {
			t.Errorf("%s not written", p)
		}
	}
	m := loadManifest(t, tg)
	if m.Agents[AgentClaude] == nil || m.Agents[AgentClaude].URL != evalURL {
		t.Fatalf("manifest agent record: %+v", m.Agents)
	}
	for _, c := range cs {
		rec, ok := m.Record(AgentClaude, c.Kind, c.ID)
		if !ok || rec.Hash != c.Hash || rec.Pending != nil || rec.Path != c.Path {
			t.Errorf("record for %s %s: %+v %v", c.Kind, c.ID, rec, ok)
		}
	}
	if !reflect.DeepEqual(m.CreatedDirs, []string{".claude", ".claude/skills"}) {
		t.Errorf("CreatedDirs = %v", m.CreatedDirs)
	}
	if after := porcelain(t, root); after != before || after != "" {
		t.Fatalf("porcelain changed:\nbefore %q\nafter  %q", before, after)
	}
	if !reflect.DeepEqual(res.ExcludePaths, claudeBlock) || !reflect.DeepEqual(excludeBlock(t, tg), claudeBlock) {
		t.Fatalf("ExcludePaths = %v, block = %v", res.ExcludePaths, excludeBlock(t, tg))
	}
	if res.WorktreeNotice {
		t.Error("WorktreeNotice in a main work tree")
	}
}

func TestInstall_CleanRepo_Codex(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	res := runInstall(t, tg, defaultFixture().dir(t), codexOpts())
	if len(res.Items) != 3 {
		t.Fatalf("want 3 codex items, got %+v", res.Items)
	}
	wantActions(t, res, ActionWritten, nil)
	for _, p := range []string{".codex/config.toml", ".agents/skills/alpha/SKILL.md", ".agents/skills/beta/SKILL.md"} {
		if !tg.Exists(p) {
			t.Errorf("%s not written", p)
		}
	}
	if tg.Exists(".claude") || tg.Exists(".mcp.json") {
		t.Error("codex install wrote Claude files")
	}
	if got := readFile(t, tg.Abs(".codex/config.toml")); got != codexBlock(evalURL) {
		t.Errorf("config.toml = %q", got)
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}
	if !reflect.DeepEqual(res.ExcludePaths, codexBlockPaths) {
		t.Fatalf("ExcludePaths = %v", res.ExcludePaths)
	}
}

func TestInstall_Idempotent(t *testing.T) {
	for _, o := range []InstallOpts{claudeOpts(), codexOpts()} {
		root := newRepo(t)
		tg := mustTarget(t, root)
		assets := defaultFixture().dir(t)
		first := runInstall(t, tg, assets, o)
		snap, excl := snapshot(t, root), excludeBytes(t, tg)
		second := runInstall(t, tg, assets, o)
		wantActions(t, second, ActionCurrent, nil)
		for _, r := range second.Items {
			if !r.Owned || r.Detail != "" {
				t.Errorf("%s: second run item %+v", o.Agent, r)
			}
		}
		if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
			t.Errorf("%s: second run changed bytes", o.Agent)
		}
		if !reflect.DeepEqual(first.ExcludePaths, second.ExcludePaths) {
			t.Errorf("%s: exclude paths differ: %v vs %v", o.Agent, first.ExcludePaths, second.ExcludePaths)
		}
	}
}

func TestInstall_DryRunWritesNothing(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	snap, excl := snapshot(t, root), excludeBytes(t, tg)

	o := claudeOpts()
	o.DryRun = true
	res := runInstall(t, tg, assets, o)
	wantActions(t, res, ActionWritten, nil)
	if !reflect.DeepEqual(res.ExcludePaths, claudeBlock) {
		t.Errorf("dry-run ExcludePaths = %v", res.ExcludePaths)
	}
	if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl || tg.Exists(".projectlens") {
		t.Fatal("dry run wrote something")
	}

	// A dry run over an installed tree with a stale skill and an orphan writes nothing either.
	runInstall(t, tg, assets, claudeOpts())
	f := defaultFixture()
	f.alpha, f.beta = "alpha v2", ""
	snap, excl = snapshot(t, root), excludeBytes(t, tg)
	res = runInstall(t, tg, f.dir(t), o)
	wantActions(t, res, ActionCurrent, map[string]Action{"skill:alpha": ActionUpdated, "skill:beta": ActionRemoved})
	want := []string{"/.claude/settings.local.json", "/.claude/skills/alpha/", "/.mcp.json", "/.projectlens/agent-manifest.json"}
	if !reflect.DeepEqual(res.ExcludePaths, want) {
		t.Errorf("dry-run ExcludePaths = %v", res.ExcludePaths)
	}
	if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
		t.Fatal("dry run wrote something")
	}
}

func TestInstall_PreexistingUntrackedStaysVisible(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	write(t, tg.Abs(".mcp.json"), `{"mcpServers": {"other": {"type": "http", "url": "http://other/mcp"}}, "x": 1}`)
	write(t, tg.Abs(".claude/settings.local.json"), `{"permissions": {"allow": ["Bash"]}}`)
	before := porcelain(t, root)
	for _, p := range []string{".mcp.json", ".claude/settings.local.json"} {
		if !strings.Contains(before, p) {
			t.Fatalf("setup: porcelain does not list %s: %q", p, before)
		}
	}

	res := runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	wantActions(t, res, ActionWritten, nil)
	if after := porcelain(t, root); after != before {
		t.Fatalf("porcelain changed:\nbefore %q\nafter  %q", before, after)
	}
	m := loadManifest(t, tg)
	for _, p := range []string{".mcp.json", ".claude/settings.local.json"} {
		for _, e := range res.ExcludePaths {
			if e == "/"+p {
				t.Errorf("%s excluded", p)
			}
		}
		if m.Created(p) {
			t.Errorf("%s in CreatedPaths", p)
		}
	}
	if m.Created(".claude") || len(m.CreatedDirs) != 1 || m.CreatedDirs[0] != ".claude/skills" {
		t.Errorf("CreatedDirs = %v", m.CreatedDirs)
	}

	var mcp struct {
		MCPServers map[string]map[string]string `json:"mcpServers"`
		X          int                          `json:"x"`
	}
	if err := json.Unmarshal([]byte(readFile(t, tg.Abs(".mcp.json"))), &mcp); err != nil {
		t.Fatal(err)
	}
	if mcp.X != 1 || mcp.MCPServers["other"]["url"] != "http://other/mcp" || mcp.MCPServers["projectlens"]["url"] != evalURL {
		t.Errorf(".mcp.json content: %+v", mcp)
	}
	var settings struct {
		Permissions struct{ Allow []string } `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(readFile(t, tg.Abs(".claude/settings.local.json"))), &settings); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(settings.Permissions.Allow, []string{"Bash"}) {
		t.Errorf("settings content lost: %+v", settings)
	}
}

func TestInstall_TrackedFileNeverWritten(t *testing.T) {
	cases := []struct {
		name    string
		opts    InstallOpts
		path    string
		content string
		kind    Kind
	}{
		{"mcp.json", claudeOpts(), ".mcp.json", "{\"mcpServers\": {}}\n", KindMCP},
		{"settings.local.json", claudeOpts(), ".claude/settings.local.json", "{\"permissions\": {}}\n", KindHook},
		{"codex config", codexOpts(), ".codex/config.toml", "model = \"x\"\n", KindMCP},
	}
	for _, tc := range cases {
		for _, force := range []bool{false, true} {
			t.Run(tc.name, func(t *testing.T) {
				root := newRepo(t)
				tg := mustTarget(t, root)
				write(t, tg.Abs(tc.path), tc.content)
				git(t, root, "add", "-f", "--", tc.path)
				git(t, root, "commit", "-q", "-m", "tracked")
				assets := defaultFixture().dir(t)
				cs, err := LoadCanonical(assets, tc.opts.Agent, evalURL)
				if err != nil {
					t.Fatal(err)
				}
				o := tc.opts
				o.Force = force
				res := runInstall(t, tg, assets, o)
				n := 0
				for _, r := range res.Items {
					if r.Kind != tc.kind {
						if r.Action != ActionWritten {
							t.Errorf("untracked item %+v", r)
						}
						continue
					}
					n++
					if r.Action != ActionManual || !r.Tracked {
						t.Errorf("tracked item %+v", r)
					}
					c := findCanon(t, cs, r.Kind, r.ID)
					switch {
					case tc.kind == KindHook:
						if !strings.Contains(r.Snippet, mustJSONString(t, c.Hook.Command)) || !json.Valid([]byte(r.Snippet)) {
							t.Errorf("hook snippet %q", r.Snippet)
						}
					case tc.opts.Agent == AgentCodex:
						if r.Snippet != codexBlock(evalURL) {
							t.Errorf("codex snippet %q", r.Snippet)
						}
					default:
						if !strings.Contains(r.Snippet, evalURL) || !json.Valid([]byte(r.Snippet)) {
							t.Errorf("mcp snippet %q", r.Snippet)
						}
					}
					if _, ok := loadManifest(t, tg).Record(tc.opts.Agent, r.Kind, r.ID); ok {
						t.Errorf("tracked item recorded: %+v", r)
					}
				}
				if n == 0 {
					t.Fatal("no tracked item reported")
				}
				if got := readFile(t, tg.Abs(tc.path)); got != tc.content {
					t.Fatalf("tracked file changed: %q", got)
				}
				if p := porcelain(t, root); p != "" {
					t.Fatalf("porcelain %q", p)
				}
			})
		}
	}
}

func mustJSONString(t *testing.T, s string) string {
	t.Helper()
	b, err := marshalNoEscape(s)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Trim(string(b), `"`)
}

func TestInstall_StaleIsUpdated(t *testing.T) {
	tg := mustTarget(t, newRepo(t))
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	f := defaultFixture()
	f.alpha = "alpha v2"
	assets := f.dir(t)
	res := runInstall(t, tg, assets, claudeOpts())
	wantActions(t, res, ActionCurrent, map[string]Action{"skill:alpha": ActionUpdated})
	if st := resultFor(t, res, KindSkill, "alpha"); st.State != StateStale {
		t.Errorf("alpha before install: %+v", st)
	}
	if got := readFile(t, tg.Abs(".claude/skills/alpha/SKILL.md")); got != "alpha v2" {
		t.Errorf("alpha = %q", got)
	}
	alpha := findCanon(t, f.canon(t, AgentClaude, evalURL), KindSkill, "alpha")
	if rec, _ := loadManifest(t, tg).Record(AgentClaude, KindSkill, "alpha"); rec.Hash != alpha.Hash || rec.Pending != nil {
		t.Errorf("record %+v", rec)
	}
}

func TestInstall_ModifiedIsSkippedThenForced(t *testing.T) {
	tg := mustTarget(t, newRepo(t))
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	cs := defaultFixture().canon(t, AgentClaude, evalURL)
	alpha := findCanon(t, cs, KindSkill, "alpha")
	editByHand(t, tg, alpha)
	notes := filepath.Join(tg.Abs(alpha.Path), "local-notes.md")

	res := runInstall(t, tg, assets, claudeOpts())
	wantActions(t, res, ActionCurrent, map[string]Action{"skill:alpha": ActionSkipped})
	if st := resultFor(t, res, KindSkill, "alpha"); st.State != StateModified {
		t.Errorf("alpha: %+v", st)
	}
	if _, err := os.Stat(notes); err != nil {
		t.Fatal("skipped skill was changed")
	}

	res = runInstall(t, tg, assets, forced(claudeOpts()))
	wantActions(t, res, ActionCurrent, map[string]Action{"skill:alpha": ActionUpdated})
	if _, err := os.Stat(notes); !os.IsNotExist(err) {
		t.Fatal("forced update kept the local file")
	}
	if rec, _ := loadManifest(t, tg).Record(AgentClaude, KindSkill, "alpha"); rec.Hash != alpha.Hash || rec.Pending != nil {
		t.Errorf("record %+v", rec)
	}
	if p := porcelain(t, tg.Root); p != "" {
		t.Fatalf("porcelain %q", p)
	}
}

func TestInstall_EditedHookNotDuplicated(t *testing.T) {
	tg := mustTarget(t, newRepo(t))
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	start := findCanon(t, defaultFixture().canon(t, AgentClaude, evalURL), KindHook, "start")
	editByHand(t, tg, start)

	res := runInstall(t, tg, assets, claudeOpts())
	wantActions(t, res, ActionCurrent, map[string]Action{"hook:start": ActionSkipped})
	hooks, err := ReadHooks(tg.Abs(start.Path))
	if err != nil {
		t.Fatal(err)
	}
	if h := hooks["start"]; h.Dup || !strings.HasSuffix(h.Command, "# tweaked") {
		t.Fatalf("start hook %+v", h)
	}
}

func TestInstall_HookMarkerRemovedNotReadded(t *testing.T) {
	tg := mustTarget(t, newRepo(t))
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	p := tg.Abs(".claude/settings.local.json")
	b := readFile(t, p)
	stripped := strings.Replace(b, "projectlens-hook=start; ", "", 1)
	if stripped == b {
		t.Fatal("marker not found")
	}
	write(t, p, stripped)

	res := runInstall(t, tg, assets, claudeOpts())
	wantActions(t, res, ActionCurrent, map[string]Action{"hook:start": ActionSkipped})
	if st := resultFor(t, res, KindHook, "start"); st.State != StateRemovedLocally {
		t.Errorf("start: %+v", st)
	}
	hooks, err := ReadHooks(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hooks["start"]; ok {
		t.Fatal("hook re-added")
	}
	if got := readFile(t, p); strings.Count(got, "echo start") != 1 {
		t.Fatalf("hook entries for start: %s", got)
	}

	res = runInstall(t, tg, assets, forced(claudeOpts()))
	wantActions(t, res, ActionCurrent, map[string]Action{"hook:start": ActionWritten})
	if hooks, err = ReadHooks(p); err != nil || hooks["start"].Dup || hooks["start"].Command != ": projectlens-hook=start; echo start" {
		t.Fatalf("forced: hooks %+v %v", hooks, err)
	}
}

func TestInstall_SymlinkedSkill(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "sentinel"), "keep me")
	if err := os.MkdirAll(tg.Abs(".claude/skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := tg.Abs(".claude/skills/alpha")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	assets := defaultFixture().dir(t)
	outsideSnap := snapshot(t, outside)

	before := porcelain(t, root)
	if before != "?? .claude/skills/alpha\n" {
		t.Fatalf("setup porcelain %q", before)
	}

	res := runInstall(t, tg, assets, claudeOpts())
	alpha := resultFor(t, res, KindSkill, "alpha")
	if alpha.Action != ActionSkipped || alpha.State != StateUnmanaged || alpha.Detail != "symlink" {
		t.Fatalf("alpha: %+v", alpha)
	}
	if p := porcelain(t, root); p != before {
		t.Fatalf("porcelain %q, want %q", p, before)
	}
	if !tg.IsSymlink(".claude/skills/alpha") {
		t.Fatal("link replaced without --force")
	}

	res = runInstall(t, tg, assets, forced(claudeOpts()))
	if a := resultFor(t, res, KindSkill, "alpha").Action; a != ActionUpdated {
		t.Fatalf("forced alpha: %s", a)
	}
	if fi, err := os.Lstat(link); err != nil || !fi.IsDir() {
		t.Fatalf("alpha is not a real directory: %v %v", fi, err)
	}
	if got := readFile(t, filepath.Join(link, "SKILL.md")); got != "alpha v1" {
		t.Fatalf("alpha content %q", got)
	}
	if !reflect.DeepEqual(snapshot(t, outside), outsideSnap) {
		t.Fatal("outside dir changed")
	}
	// The link was visible before install; the replaced directory was not
	// created by install, so it stays visible (never excluded).
	if p := porcelain(t, root); p != "?? .claude/skills/alpha/SKILL.md\n" {
		t.Fatalf("porcelain after force %q", p)
	}
}

func TestInstall_OrphanRemoved(t *testing.T) {
	noBeta := defaultFixture()
	noBeta.beta = ""

	t.Run("disk equals record", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
		res := runInstall(t, tg, noBeta.dir(t), claudeOpts())
		wantActions(t, res, ActionCurrent, map[string]Action{"skill:beta": ActionRemoved})
		if st := resultFor(t, res, KindSkill, "beta"); st.State != StateOrphaned {
			t.Errorf("beta: %+v", st)
		}
		if tg.Exists(".claude/skills/beta") {
			t.Fatal("orphan dir kept")
		}
		want := []string{"/.claude/settings.local.json", "/.claude/skills/alpha/", "/.mcp.json", "/.projectlens/agent-manifest.json"}
		if got := excludeBlock(t, tg); !reflect.DeepEqual(got, want) || !reflect.DeepEqual(res.ExcludePaths, want) {
			t.Fatalf("block %v, ExcludePaths %v", got, res.ExcludePaths)
		}
		m := loadManifest(t, tg)
		if _, ok := m.Record(AgentClaude, KindSkill, "beta"); ok || m.Created(".claude/skills/beta") {
			t.Fatal("orphan record or created path kept")
		}
	})

	t.Run("disk differs", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
		write(t, tg.Abs(".claude/skills/beta/mine.md"), "mine")
		assets := noBeta.dir(t)
		res := runInstall(t, tg, assets, claudeOpts())
		wantActions(t, res, ActionCurrent, map[string]Action{"skill:beta": ActionSkipped})
		if !tg.Exists(".claude/skills/beta/mine.md") {
			t.Fatal("edited orphan removed without --force")
		}
		if _, ok := loadManifest(t, tg).Record(AgentClaude, KindSkill, "beta"); !ok {
			t.Fatal("skipped orphan lost its record")
		}
		res = runInstall(t, tg, assets, forced(claudeOpts()))
		wantActions(t, res, ActionCurrent, map[string]Action{"skill:beta": ActionRemoved})
		if tg.Exists(".claude/skills/beta") {
			t.Fatal("forced orphan removal kept the dir")
		}
	})

	t.Run("interrupted removal", func(t *testing.T) {
		for _, failAt := range []int{1, 2} {
			tg := mustTarget(t, newRepo(t))
			runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
			assets := noBeta.dir(t)
			o := claudeOpts()
			o.failAt = failAt
			if _, err := Install(tg, assets, o); err == nil {
				t.Fatalf("failAt %d: no error", failAt)
			}
			if p := porcelain(t, tg.Root); p != "" {
				t.Fatalf("failAt %d: porcelain %q", failAt, p)
			}
			res := runInstall(t, tg, assets, claudeOpts())
			wantActions(t, res, ActionCurrent, map[string]Action{"skill:beta": ActionRemoved})
			if _, ok := loadManifest(t, tg).Record(AgentClaude, KindSkill, "beta"); ok || tg.Exists(".claude/skills/beta") {
				t.Fatalf("failAt %d: orphan not finished", failAt)
			}
		}
	})
}

func TestInstall_InvalidJSONAbortsBeforeAnyWrite(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	write(t, tg.Abs(".claude/settings.local.json"), "{bad")
	excl := excludeBytes(t, tg)
	_, err := Install(tg, defaultFixture().dir(t), claudeOpts())
	if !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("want ErrInvalidJSON, got %v", err)
	}
	if tg.Exists(".claude/skills") || tg.Exists(".mcp.json") || tg.Exists(".projectlens") {
		t.Fatal("something was written")
	}
	if excludeBytes(t, tg) != excl {
		t.Fatal("exclude file changed")
	}
}

func TestInstall_InterruptedRunIsCompleted(t *testing.T) {
	for _, base := range []InstallOpts{claudeOpts(), codexOpts()} {
		assets := defaultFixture().dir(t)
		cs, err := LoadCanonical(assets, base.Agent, evalURL)
		if err != nil {
			t.Fatal(err)
		}
		for failAt := 1; failAt <= 2*len(cs); failAt++ {
			root := newRepo(t)
			tg := mustTarget(t, root)
			before := porcelain(t, root)
			snap, excl := snapshot(t, root), excludeBytes(t, tg)
			o := base
			o.failAt = failAt
			if _, err := Install(tg, assets, o); err == nil {
				t.Fatalf("%s failAt %d: no error", base.Agent, failAt)
			}

			m := loadManifest(t, tg)
			for _, c := range cs {
				disk, err := installedHash(tg, c.Item)
				if err != nil {
					t.Fatal(err)
				}
				if !disk.present {
					continue
				}
				if _, ok := m.Record(base.Agent, c.Kind, c.ID); !ok {
					t.Errorf("%s failAt %d: %s %s on disk without a record", base.Agent, failAt, c.Kind, c.ID)
				}
				if !m.Created(c.Path) {
					t.Errorf("%s failAt %d: %s not in CreatedPaths %v", base.Agent, failAt, c.Path, m.CreatedPaths)
				}
			}
			for _, st := range evaluate(t, tg, base.Agent, cs, m) {
				switch st.State {
				case StateModified, StateUnmanaged, StateRemovedLocally:
					t.Errorf("%s failAt %d: %+v", base.Agent, failAt, st)
				}
			}
			if p := porcelain(t, root); p != before {
				t.Fatalf("%s failAt %d: porcelain %q", base.Agent, failAt, p)
			}

			// The rerun finishes without --force.
			res := runInstall(t, tg, assets, base)
			k := (failAt + 1) / 2 // the interrupted item, 1-based
			for i, r := range res.Items {
				want := ActionWritten
				if i+1 < k || (i+1 == k && failAt%2 == 0) {
					want = ActionCurrent
				}
				if r.Action != want {
					t.Errorf("%s failAt %d: item %d %s %s: %s, want %s", base.Agent, failAt, i+1, r.Kind, r.ID, r.Action, want)
				}
			}
			if p := porcelain(t, root); p != before {
				t.Fatalf("%s failAt %d: porcelain after rerun %q", base.Agent, failAt, p)
			}
			for _, st := range evaluateSaved(t, tg, base.Agent, cs) {
				if st.State != StateCurrent || !st.Owned {
					t.Errorf("%s failAt %d: after rerun %+v", base.Agent, failAt, st)
				}
			}
			for _, r := range loadManifest(t, tg).Agents[base.Agent].Items {
				if r.Pending != nil {
					t.Errorf("%s failAt %d: journal left %+v", base.Agent, failAt, r)
				}
			}

			runUninstall(t, tg, uninstallOf(base.Agent))
			if got := snapshot(t, root); !reflect.DeepEqual(got, snap) {
				t.Fatalf("%s failAt %d: after uninstall %v", base.Agent, failAt, got)
			}
			if excludeBytes(t, tg) != excl {
				t.Fatalf("%s failAt %d: exclude file after uninstall %q", base.Agent, failAt, excludeBytes(t, tg))
			}
		}
	}
}

func TestInstall_ManifestDeletedRecoversFromExcludeBlock(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	runInstall(t, tg, assets, codexOpts())
	block, before := excludeBlock(t, tg), porcelain(t, root)
	deleteManifest(t, tg)

	res := runInstall(t, tg, assets, claudeOpts())
	wantActions(t, res, ActionCurrent, nil)
	for _, st := range evaluateSaved(t, tg, AgentClaude, defaultFixture().canon(t, AgentClaude, evalURL)) {
		if st.State != StateCurrent || !st.Owned {
			t.Errorf("claude after recovery: %+v", st)
		}
	}
	if got := excludeBlock(t, tg); !reflect.DeepEqual(got, block) {
		t.Fatalf("block %v, want %v", got, block)
	}
	if p := porcelain(t, root); p != before {
		t.Fatalf("porcelain %q, want %q", p, before)
	}

	res = runInstall(t, tg, assets, codexOpts())
	wantActions(t, res, ActionCurrent, nil)
	for _, st := range evaluateSaved(t, tg, AgentCodex, defaultFixture().canon(t, AgentCodex, evalURL)) {
		if st.State != StateCurrent || !st.Owned {
			t.Errorf("codex after recovery: %+v", st)
		}
	}
	if got := excludeBlock(t, tg); !reflect.DeepEqual(got, block) {
		t.Fatalf("block %v, want %v", got, block)
	}

	runUninstall(t, tg, uninstallOf(AgentClaude, AgentCodex))
	// Known limitation: CreatedDirs is not recovered after manifest loss, so empty
	// parent directories (.claude/skills, .agents/skills, .codex, ...) may remain.
	wantGone(t, tg, ".mcp.json", ".claude/settings.local.json", ".claude/skills/alpha", ".claude/skills/beta",
		".agents/skills/alpha", ".agents/skills/beta", ".codex/config.toml", ".projectlens")
	if got := excludeBlock(t, tg); len(got) != 0 {
		t.Fatalf("block after uninstall %v", got)
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain after uninstall %q", p)
	}
}

func TestInstall_MatchingPreexistingIsNotOwned(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	cs := defaultFixture().canon(t, AgentClaude, evalURL)
	place(t, tg, findCanon(t, cs, KindSkill, "alpha"))
	place(t, tg, findCanon(t, cs, KindMCP, "projectlens"))
	paths := []string{".claude/skills/alpha/SKILL.md", ".mcp.json"}

	check := func(t *testing.T, res InstallResult, action Action, owned bool) {
		t.Helper()
		m := loadManifest(t, tg)
		for _, k := range []struct {
			kind Kind
			id   string
			path string
		}{{KindSkill, "alpha", ".claude/skills/alpha"}, {KindMCP, "projectlens", ".mcp.json"}} {
			r := resultFor(t, res, k.kind, k.id)
			if r.Action != action || r.State != StateCurrent || r.Owned {
				t.Errorf("%s: %+v", k.id, r)
			}
			if action == ActionCurrent && r.Detail != "pre-existing, not managed" {
				t.Errorf("%s detail %q", k.id, r.Detail)
			}
			if _, ok := m.Record(AgentClaude, k.kind, k.id); ok != owned {
				t.Errorf("%s recorded = %v", k.id, ok)
			}
			if m.Created(k.path) {
				t.Errorf("%s in CreatedPaths", k.path)
			}
			for _, e := range res.ExcludePaths {
				if strings.HasPrefix(e, "/"+k.path) {
					t.Errorf("%s excluded", k.path)
				}
			}
		}
		p := porcelain(t, root)
		for _, path := range paths {
			if !strings.Contains(p, path) {
				t.Errorf("porcelain does not list %s: %q", path, p)
			}
		}
	}

	alpha, mcp := findCanon(t, cs, KindSkill, "alpha"), findCanon(t, cs, KindMCP, "projectlens")
	mcpPresent := func() bool {
		t.Helper()
		_, present, err := ReadMCPJSON(tg.Abs(".mcp.json"))
		if err != nil {
			t.Fatal(err)
		}
		return present
	}

	res := runInstall(t, tg, assets, claudeOpts())
	check(t, res, ActionCurrent, false)

	// Uninstall leaves what it does not own.
	runUninstall(t, tg, uninstallOf(AgentClaude))
	if h, err := HashDir(tg.Abs(alpha.Path)); err != nil || h != alpha.Hash {
		t.Fatalf("unowned skill changed: %v", err)
	}
	if !mcpPresent() {
		t.Fatal("unowned MCP entry removed")
	}

	res = runInstall(t, tg, assets, forced(claudeOpts()))
	check(t, res, ActionAdopted, true)
	for _, st := range evaluateSaved(t, tg, AgentClaude, cs) {
		if st.State != StateCurrent || !st.Owned {
			t.Errorf("after adoption: %+v", st)
		}
	}

	// Adopted items are removed; the file install did not create stays.
	runUninstall(t, tg, uninstallOf(AgentClaude))
	if tg.Exists(alpha.Path) {
		t.Fatal("adopted skill kept")
	}
	if mcpPresent() {
		t.Fatal("adopted MCP entry kept")
	}
	if !tg.Exists(mcp.Path) {
		t.Fatal(".mcp.json deleted although install did not create it")
	}
}

func TestInstall_SymlinkedAncestorNeverWrittenThrough(t *testing.T) {
	for _, comp := range []string{".agents", ".agents/skills", ".claude", ".claude/skills", ".codex"} {
		for _, force := range []bool{false, true} {
			t.Run(comp, func(t *testing.T) {
				root := newRepo(t)
				tg := mustTarget(t, root)
				outside := t.TempDir()
				write(t, filepath.Join(outside, "sentinel"), "sentinel")
				outsideSnap := snapshot(t, outside)
				if err := os.MkdirAll(filepath.Dir(tg.Abs(comp)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, tg.Abs(comp)); err != nil {
					t.Fatal(err)
				}
				o := claudeOpts()
				if strings.HasPrefix(comp, ".agents") || comp == ".codex" {
					o = codexOpts()
				}
				o.Force = force
				res := runInstall(t, tg, defaultFixture().dir(t), o)
				m := loadManifest(t, tg)
				blocked := 0
				for _, r := range res.Items {
					if !strings.HasPrefix(r.Path, comp+"/") {
						if r.Action != ActionWritten {
							t.Errorf("unaffected item %+v", r)
						}
						continue
					}
					blocked++
					if r.Action != ActionBlocked || r.Snippet != comp+" is a symlink; lens will not write through it" {
						t.Errorf("affected item %+v", r)
					}
					if _, ok := m.Record(o.Agent, r.Kind, r.ID); ok {
						t.Errorf("blocked item recorded: %+v", r)
					}
				}
				if blocked == 0 {
					t.Fatal("no item blocked")
				}
				if !reflect.DeepEqual(snapshot(t, outside), outsideSnap) {
					t.Fatalf("outside dir changed: %v", snapshot(t, outside))
				}
			})
		}
	}
}

func TestInstall_SymlinkedManifestDirRefused(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "sentinel"), "sentinel")
	if err := os.Symlink(outside, tg.Abs(".projectlens")); err != nil {
		t.Fatal(err)
	}
	snap, outsideSnap, excl := snapshot(t, root), snapshot(t, outside), excludeBytes(t, tg)
	if _, err := Install(tg, defaultFixture().dir(t), claudeOpts()); !errors.Is(err, ErrEscapesTarget) {
		t.Fatalf("want ErrEscapesTarget, got %v", err)
	}
	if !reflect.DeepEqual(snapshot(t, root), snap) || !reflect.DeepEqual(snapshot(t, outside), outsideSnap) || excludeBytes(t, tg) != excl {
		t.Fatal("something was written")
	}
}

func TestInstall_TrackedManifestRefused(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	content := "{\"version\": 1}\n"
	write(t, tg.Abs(ManifestPath), content)
	git(t, root, "add", "-f", "--", ManifestPath)
	git(t, root, "commit", "-q", "-m", "tracked manifest")
	excl := excludeBytes(t, tg)
	if _, err := Install(tg, defaultFixture().dir(t), claudeOpts()); !errors.Is(err, ErrManifestTracked) {
		t.Fatalf("want ErrManifestTracked, got %v", err)
	}
	if tg.Exists(".claude") || tg.Exists(".mcp.json") || excludeBytes(t, tg) != excl {
		t.Fatal("something was written")
	}
	if got := readFile(t, tg.Abs(ManifestPath)); got != content {
		t.Fatalf("manifest bytes changed: %q", got)
	}
}

func TestInstall_InterruptedStaleUpdate(t *testing.T) {
	v2 := defaultFixture()
	v2.alpha = "alpha v2"
	v2.startCmd = ": projectlens-hook=start; echo start v2"
	// Items in order: mcp (current), alpha (write: steps 1, 2), beta, edit (current), start (write: steps 3, 4).
	for _, tc := range []struct {
		kind   Kind
		id     string
		failAt int
	}{{KindSkill, "alpha", 1}, {KindHook, "start", 3}} {
		t.Run(tc.id, func(t *testing.T) {
			tg := mustTarget(t, newRepo(t))
			runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
			oldC := findCanon(t, defaultFixture().canon(t, AgentClaude, evalURL), tc.kind, tc.id)
			assets := v2.dir(t)
			cs, err := LoadCanonical(assets, AgentClaude, evalURL)
			if err != nil {
				t.Fatal(err)
			}
			newC := findCanon(t, cs, tc.kind, tc.id)

			o := claudeOpts()
			o.failAt = tc.failAt
			if _, err := Install(tg, assets, o); err == nil {
				t.Fatal("no error")
			}
			m := loadManifest(t, tg)
			rec, _ := m.Record(AgentClaude, tc.kind, tc.id)
			if rec.Pending == nil || rec.Pending.Op != "write" || rec.Pending.Hash != newC.Hash || rec.Hash != oldC.Hash {
				t.Fatalf("journal %+v", rec)
			}
			disk, err := installedHash(tg, newC.Item)
			if err != nil || disk.hash != oldC.Hash {
				t.Fatalf("disk changed before the write step: %+v %v", disk, err)
			}
			if st := findStatus(t, evaluate(t, tg, AgentClaude, cs, m), tc.kind, tc.id); st.State != StateStale {
				t.Fatalf("evaluate: %+v", st)
			}

			res := runInstall(t, tg, assets, claudeOpts())
			if a := resultFor(t, res, tc.kind, tc.id).Action; a != ActionUpdated {
				t.Fatalf("rerun: %s", a)
			}
			rec, _ = loadManifest(t, tg).Record(AgentClaude, tc.kind, tc.id)
			if rec.Hash != newC.Hash || rec.Pending != nil {
				t.Fatalf("record %+v", rec)
			}
		})
	}
}

func TestInstall_InterruptedFirstHookWrite(t *testing.T) {
	// Fresh repo: mcp (1, 2), alpha (3, 4), beta (5, 6), edit (7, 8), start (9, 10).
	for failAt, id := range map[int]string{7: "edit", 9: "start"} {
		root := newRepo(t)
		tg := mustTarget(t, root)
		assets := defaultFixture().dir(t)
		cs := defaultFixture().canon(t, AgentClaude, evalURL)
		o := claudeOpts()
		o.failAt = failAt
		if _, err := Install(tg, assets, o); err == nil {
			t.Fatal("no error")
		}
		if st := findStatus(t, evaluateSaved(t, tg, AgentClaude, cs), KindHook, id); st.State != StateMissing {
			t.Fatalf("%s: %+v", id, st)
		}
		res := runInstall(t, tg, assets, claudeOpts())
		if a := resultFor(t, res, KindHook, id).Action; a != ActionWritten {
			t.Fatalf("%s rerun: %s", id, a)
		}
		hooks, err := ReadHooks(tg.Abs(".claude/settings.local.json"))
		if err != nil || len(hooks) != 2 {
			t.Fatalf("hooks %+v %v", hooks, err)
		}
		if p := porcelain(t, root); p != "" {
			t.Fatalf("porcelain %q", p)
		}
	}
}

func TestInstall_ForcedOverwriteInterruptedCompletesWithoutForce(t *testing.T) {
	cs := defaultFixture().canon(t, AgentClaude, evalURL)
	alpha := findCanon(t, cs, KindSkill, "alpha")

	t.Run("unmanaged skill", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		assets := defaultFixture().dir(t)
		place(t, tg, alpha)
		editByHand(t, tg, alpha)
		disk, err := installedHash(tg, alpha.Item)
		if err != nil {
			t.Fatal(err)
		}
		o := forced(claudeOpts())
		o.failAt = 3 // mcp (1, 2), alpha (3, 4)
		if _, err := Install(tg, assets, o); err == nil {
			t.Fatal("no error")
		}
		m := loadManifest(t, tg)
		if rec, _ := m.Record(AgentClaude, KindSkill, "alpha"); rec.Hash != disk.hash || rec.Pending == nil {
			t.Fatalf("journal %+v, want Hash of the content on disk", rec)
		}
		if st := findStatus(t, evaluate(t, tg, AgentClaude, cs, m), KindSkill, "alpha"); st.State != StateStale {
			t.Fatalf("evaluate: %+v", st)
		}
		res := runInstall(t, tg, assets, claudeOpts())
		if a := resultFor(t, res, KindSkill, "alpha").Action; a != ActionUpdated {
			t.Fatalf("rerun without force: %s", a)
		}
	})

	t.Run("modified skill", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		assets := defaultFixture().dir(t)
		runInstall(t, tg, assets, claudeOpts())
		editByHand(t, tg, alpha)
		o := forced(claudeOpts())
		o.failAt = 1 // alpha is the first write
		if _, err := Install(tg, assets, o); err == nil {
			t.Fatal("no error")
		}
		if st := findStatus(t, evaluateSaved(t, tg, AgentClaude, cs), KindSkill, "alpha"); st.State != StateStale {
			t.Fatalf("evaluate: %+v", st)
		}
		res := runInstall(t, tg, assets, claudeOpts())
		if a := resultFor(t, res, KindSkill, "alpha").Action; a != ActionUpdated {
			t.Fatalf("rerun without force: %s", a)
		}
		if tg.Exists(".claude/skills/alpha/local-notes.md") {
			t.Fatal("local file kept")
		}
	})
}

func TestInstall_SkillTempLeftoverIsHarmless(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	tmp := ".claude/skills/.alpha.lens-tmp"

	// While a skill write is journaled, its temp sibling is excluded.
	o := claudeOpts()
	o.failAt = 3 // alpha's odd step
	if _, err := Install(tg, assets, o); err == nil {
		t.Fatal("no error")
	}
	found := false
	for _, e := range excludeBlock(t, tg) {
		found = found || e == "/"+tmp+"/"
	}
	if !found {
		t.Fatalf("temp sibling not excluded during the write: %v", excludeBlock(t, tg))
	}
	// A crash mid-copy leaves the temp sibling behind; it stays hidden.
	write(t, tg.Abs(tmp+"/partial"), "partial")
	if p := porcelain(t, root); p != "" {
		t.Fatalf("leftover visible: %q", p)
	}

	// The next run removes it and drops the pattern.
	res := runInstall(t, tg, assets, claudeOpts())
	if tg.Exists(tmp) {
		t.Fatal("leftover kept")
	}
	if !reflect.DeepEqual(res.ExcludePaths, claudeBlock) {
		t.Fatalf("ExcludePaths %v", res.ExcludePaths)
	}

	// A sibling that was not in the exclude block when the run started is not
	// lens's: it is left alone and stays visible.
	write(t, tg.Abs(tmp+"/partial"), "partial")
	before := porcelain(t, root)
	runInstall(t, tg, assets, claudeOpts())
	if !tg.Exists(tmp) || readFile(t, tg.Abs(tmp+"/partial")) != "partial" {
		t.Fatal("unlisted sibling removed or changed")
	}
	if p := porcelain(t, root); p != before || !strings.Contains(p, tmp) {
		t.Fatalf("porcelain %q, want %q", p, before)
	}
}

func TestInstall_ManifestDeleted_EditedAndStaleStayExcluded(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	editByHand(t, tg, findCanon(t, defaultFixture().canon(t, AgentClaude, evalURL), KindSkill, "alpha"))
	f := defaultFixture()
	f.beta = "beta v2"
	assets := f.dir(t)
	block, before := excludeBlock(t, tg), porcelain(t, root)
	deleteManifest(t, tg)

	res := runInstall(t, tg, assets, claudeOpts())
	if got := excludeBlock(t, tg); !reflect.DeepEqual(got, block) {
		t.Fatalf("block %v, want %v", got, block)
	}
	if p := porcelain(t, root); p != before {
		t.Fatalf("porcelain %q, want %q", p, before)
	}
	wantActions(t, res, ActionCurrent, map[string]Action{"skill:alpha": ActionSkipped, "skill:beta": ActionSkipped})
	for _, id := range []string{"alpha", "beta"} {
		r := resultFor(t, res, KindSkill, id)
		if r.State != StateUnmanaged || r.Detail != "recovered path; content differs from canonical" {
			t.Errorf("%s: %+v", id, r)
		}
	}
	cs := f.canon(t, AgentClaude, evalURL)
	for _, st := range evaluateSaved(t, tg, AgentClaude, cs) {
		if st.Kind != KindSkill && (st.State != StateCurrent || !st.Owned) {
			t.Errorf("untouched item %+v", st)
		}
	}

	res = runInstall(t, tg, assets, forced(claudeOpts()))
	wantActions(t, res, ActionCurrent, map[string]Action{"skill:alpha": ActionUpdated, "skill:beta": ActionUpdated})
	for _, st := range evaluateSaved(t, tg, AgentClaude, cs) {
		if st.State != StateCurrent || !st.Owned {
			t.Errorf("after force: %+v", st)
		}
	}
	if got := excludeBlock(t, tg); !reflect.DeepEqual(got, block) {
		t.Fatalf("block after force %v, want %v", got, block)
	}
	if p := porcelain(t, root); p != before {
		t.Fatalf("porcelain after force %q", p)
	}
}

func TestInstall_ManifestDeleted_OtherAgentLeftUnrecorded(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	runInstall(t, tg, assets, codexOpts())
	block := excludeBlock(t, tg)
	codexSnap := snapshot(t, tg.Abs(".agents"))
	codexConfig := readFile(t, tg.Abs(".codex/config.toml"))
	deleteManifest(t, tg)

	runInstall(t, tg, assets, claudeOpts())
	m := loadManifest(t, tg)
	if m.Agents[AgentCodex] != nil {
		t.Fatalf("codex re-recorded by a claude install: %+v", m.Agents[AgentCodex])
	}
	for _, p := range []string{".agents/skills/alpha", ".agents/skills/beta", ".codex/config.toml"} {
		if !m.Created(p) {
			t.Errorf("%s not in CreatedPaths %v", p, m.CreatedPaths)
		}
	}
	if got := excludeBlock(t, tg); !reflect.DeepEqual(got, block) {
		t.Fatalf("block %v, want %v", got, block)
	}
	if p := porcelain(t, root); strings.Contains(p, ".codex") || strings.Contains(p, ".agents") {
		t.Fatalf("codex paths visible: %q", p)
	}
	if !reflect.DeepEqual(snapshot(t, tg.Abs(".agents")), codexSnap) || readFile(t, tg.Abs(".codex/config.toml")) != codexConfig {
		t.Fatal("codex files changed")
	}

	// Uninstalling claude keeps the unrecorded codex paths owned and hidden.
	runUninstall(t, tg, uninstallOf(AgentClaude))
	m = loadManifest(t, tg)
	if len(m.Agents) != 0 {
		t.Fatalf("agent records left: %+v", m.Agents)
	}
	for _, p := range []string{".agents/skills/alpha", ".agents/skills/beta", ".codex/config.toml"} {
		if !m.Created(p) {
			t.Errorf("%s not in CreatedPaths %v", p, m.CreatedPaths)
		}
	}
	if got := excludeBlock(t, tg); !reflect.DeepEqual(got, codexBlockPaths) {
		t.Fatalf("block %v, want %v", got, codexBlockPaths)
	}
	if p := porcelain(t, root); strings.Contains(p, ".codex") || strings.Contains(p, ".agents") {
		t.Fatalf("codex paths visible: %q", p)
	}
	if !reflect.DeepEqual(snapshot(t, tg.Abs(".agents")), codexSnap) || readFile(t, tg.Abs(".codex/config.toml")) != codexConfig {
		t.Fatal("codex files changed by the claude uninstall")
	}

	runInstall(t, tg, assets, codexOpts())
	for _, st := range evaluateSaved(t, tg, AgentCodex, defaultFixture().canon(t, AgentCodex, evalURL)) {
		if st.State != StateCurrent || !st.Owned {
			t.Errorf("codex after reinstall: %+v", st)
		}
	}
	runUninstall(t, tg, uninstallOf(AgentCodex))
	wantGone(t, tg, ".agents/skills/alpha", ".agents/skills/beta", ".codex/config.toml", ManifestPath)
	if got := excludeBlock(t, tg); len(got) != 0 {
		t.Fatalf("block after uninstall %v", got)
	}
}

func TestInstall_HookPlacementRestored(t *testing.T) {
	tg := mustTarget(t, newRepo(t))
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	f := defaultFixture()
	f.editMatcher = "Write"
	res := runInstall(t, tg, f.dir(t), claudeOpts())
	wantActions(t, res, ActionCurrent, map[string]Action{"hook:edit": ActionUpdated})

	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(readFile(t, tg.Abs(".claude/settings.local.json"))), &doc); err != nil {
		t.Fatal(err)
	}
	var where []string
	for event, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if id, _ := HookID(h.Command); id == "edit" {
					where = append(where, event+"/"+g.Matcher)
				}
			}
		}
	}
	if !reflect.DeepEqual(where, []string{"PreToolUse/Write"}) {
		t.Fatalf("edit hook found at %v", where)
	}
}

func addWorktree(t *testing.T, root string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, root, "worktree", "add", "-q", "-b", "wt", wt)
	resolved, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestInstall_TwoWorktrees(t *testing.T) {
	root := newRepo(t)
	wt := addWorktree(t, root)
	main, linked := mustTarget(t, root), mustTarget(t, wt)
	assets := defaultFixture().dir(t)
	mainBefore, wtBefore := porcelain(t, root), porcelain(t, wt)

	runInstall(t, main, assets, claudeOpts())
	res := runInstall(t, linked, assets, claudeOpts())
	if !res.WorktreeNotice {
		t.Error("no WorktreeNotice for a linked worktree")
	}
	content := excludeBytes(t, main)
	if strings.Count(content, blockStartPrefix) != 2 ||
		!strings.Contains(content, blockStartPrefix+root+blockStartSuffix) ||
		!strings.Contains(content, blockStartPrefix+wt+blockStartSuffix) {
		t.Fatalf("exclude file:\n%s", content)
	}
	if !reflect.DeepEqual(excludeBlock(t, main), claudeBlock) || !reflect.DeepEqual(excludeBlock(t, linked), claudeBlock) {
		t.Fatal("per-worktree blocks differ from the expected paths")
	}
	if porcelain(t, root) != mainBefore || porcelain(t, wt) != wtBefore {
		t.Fatalf("porcelain changed: %q / %q", porcelain(t, root), porcelain(t, wt))
	}
}

func TestInstall_LinkedWorktree(t *testing.T) {
	root := newRepo(t)
	wt := addWorktree(t, root)
	tg := mustTarget(t, wt)
	mainSnap := snapshot(t, root)
	// Everything beside the worktree, in its parent directory.
	besideWorktree := func() map[string]string {
		s := snapshot(t, filepath.Dir(wt))
		for k := range s {
			if k == "wt" || strings.HasPrefix(k, "wt"+string(filepath.Separator)) {
				delete(s, k)
			}
		}
		return s
	}
	outsideSnap := besideWorktree()

	res := runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	wantActions(t, res, ActionWritten, nil)
	if !res.WorktreeNotice {
		t.Error("WorktreeNotice false")
	}
	for _, p := range []string{".mcp.json", ".claude/skills/alpha/SKILL.md", ManifestPath} {
		if _, err := os.Stat(filepath.Join(wt, p)); err != nil {
			t.Errorf("%s not in the worktree: %v", p, err)
		}
	}
	if !strings.Contains(excludeBytes(t, tg), blockStartPrefix+wt+blockStartSuffix) {
		t.Fatal("block not in the shared exclude file")
	}
	if tg.excludePath() != filepath.Join(root, ".git", "info", "exclude") {
		t.Fatalf("exclude path %s", tg.excludePath())
	}
	if !reflect.DeepEqual(snapshot(t, root), mainSnap) {
		t.Fatal("main work tree changed")
	}
	if after := besideWorktree(); !reflect.DeepEqual(after, outsideSnap) {
		t.Fatalf("created outside the worktree: %v", after)
	}
	if p := porcelain(t, wt); p != "" {
		t.Fatalf("porcelain %q", p)
	}
}

func TestInstall_CodexForeignDefinition(t *testing.T) {
	const blockedSnippet = "remove your existing mcp_servers.projectlens definition from .codex/config.toml, then re-run"
	for _, spelling := range []string{`[mcp_servers."projectlens"]`, `[mcp_servers . projectlens]`} {
		for _, force := range []bool{false, true} {
			t.Run(spelling, func(t *testing.T) {
				tg := mustTarget(t, newRepo(t))
				content := "model = \"x\"\n\n" + spelling + "\nurl = \"" + evalURLOther + "\"\n"
				write(t, tg.Abs(".codex/config.toml"), content)
				o := codexOpts()
				o.Force = force
				res := runInstall(t, tg, defaultFixture().dir(t), o)
				r := resultFor(t, res, KindMCP, "projectlens")
				if r.Action != ActionBlocked || r.Snippet != blockedSnippet || r.State != StateBlocked {
					t.Fatalf("got %+v", r)
				}
				got := readFile(t, tg.Abs(".codex/config.toml"))
				if got != content {
					t.Fatalf("bytes changed: %q", got)
				}
				var m map[string]any
				if err := toml.Unmarshal([]byte(got), &m); err != nil {
					t.Fatal(err)
				}
				if _, ok := loadManifest(t, tg).Record(AgentCodex, KindMCP, "projectlens"); ok {
					t.Fatal("blocked item recorded")
				}
			})
		}
	}

	t.Run("canonical url", func(t *testing.T) {
		for _, force := range []bool{false, true} {
			tg := mustTarget(t, newRepo(t))
			content := "[mcp_servers.projectlens]\nurl = \"" + evalURL + "\"\n"
			write(t, tg.Abs(".codex/config.toml"), content)
			o := codexOpts()
			o.Force = force
			res := runInstall(t, tg, defaultFixture().dir(t), o)
			r := resultFor(t, res, KindMCP, "projectlens")
			if r.Action != ActionCurrent || r.Owned || r.Detail != "pre-existing, not managed" {
				t.Fatalf("force=%v: %+v", force, r)
			}
			if readFile(t, tg.Abs(".codex/config.toml")) != content {
				t.Fatal("bytes changed")
			}
			if _, ok := loadManifest(t, tg).Record(AgentCodex, KindMCP, "projectlens"); ok {
				t.Fatalf("force=%v: foreign definition adopted", force)
			}
		}
	})

	t.Run("foreign definition beside the managed block", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		content := "[mcp_servers.projectlens]\nurl = \"" + evalURL + "\"\n\n" + codexBlock(evalURL)
		write(t, tg.Abs(".codex/config.toml"), content)
		snap, excl := snapshot(t, tg.Root), excludeBytes(t, tg)
		if _, err := Install(tg, defaultFixture().dir(t), forced(codexOpts())); !errors.Is(err, ErrInvalidTOML) {
			t.Fatalf("want ErrInvalidTOML, got %v", err)
		}
		if !reflect.DeepEqual(snapshot(t, tg.Root), snap) || excludeBytes(t, tg) != excl {
			t.Fatal("something was written")
		}
	})
}

func TestInstall_BothAgents(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	res := runInstall(t, tg, assets, codexOpts())
	want := []string{
		"/.agents/skills/alpha/", "/.agents/skills/beta/", "/.claude/settings.local.json",
		"/.claude/skills/alpha/", "/.claude/skills/beta/", "/.codex/config.toml", "/.mcp.json",
		"/.projectlens/agent-manifest.json",
	}
	if !reflect.DeepEqual(res.ExcludePaths, want) || !reflect.DeepEqual(excludeBlock(t, tg), want) {
		t.Fatalf("ExcludePaths %v", res.ExcludePaths)
	}
	m := loadManifest(t, tg)
	if got := m.InstalledAgents(); !reflect.DeepEqual(got, []Agent{AgentClaude, AgentCodex}) {
		t.Fatalf("agents %v", got)
	}
	if len(m.Agents[AgentClaude].Items) != 5 || len(m.Agents[AgentCodex].Items) != 3 {
		t.Fatalf("records %+v", m.Agents)
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}
}

func TestInstall_TamperedManifestNeverDeletesOutside(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())

	sibling, err := os.MkdirTemp(filepath.Dir(root), "sibling")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(sibling, "sentinel"), "sentinel")

	for name, path := range map[string]string{
		"relative":       "../" + filepath.Base(sibling),
		"through skills": ".claude/skills/../../../" + filepath.Base(sibling),
		"absolute":       sibling,
	} {
		t.Run(name, func(t *testing.T) {
			m := loadManifest(t, tg)
			ar := m.Agents[AgentClaude]
			ar.Items = append(ar.Items, Record{Kind: KindSkill, ID: "gamma", Path: path, Hash: "h"})
			b, err := json.MarshalIndent(m, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			original := readFile(t, tg.Abs(ManifestPath))
			write(t, tg.Abs(ManifestPath), string(b))
			t.Cleanup(func() { write(t, tg.Abs(ManifestPath), original) })

			snap, outsideSnap, excl := snapshot(t, root), snapshot(t, sibling), excludeBytes(t, tg)
			for _, force := range []bool{false, true} {
				o := claudeOpts()
				o.Force = force
				if _, err := Install(tg, assets, o); !errors.Is(err, ErrInvalidManifest) {
					t.Fatalf("force=%v: want ErrInvalidManifest, got %v", force, err)
				}
			}
			if !reflect.DeepEqual(snapshot(t, sibling), outsideSnap) {
				t.Fatal("outside directory changed")
			}
			if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
				t.Fatal("work tree or exclude file changed")
			}
		})
	}

	// CreatedPaths and CreatedDirs entries must be shaped like what install
	// records: a placement path, or a proper ancestor of one. Otherwise an entry
	// would hide a developer's path from git or delete their empty directories.
	write(t, filepath.Join(root, "src", "keep.go"), "package src\n")
	git(t, root, "add", "src")
	git(t, root, "commit", "-q", "-m", "src")
	for name, edit := range map[string]func(m *Manifest){
		"created path src":     func(m *Manifest) { m.CreatedPaths = append(m.CreatedPaths, "src") },
		"created dir emptydir": func(m *Manifest) { m.CreatedDirs = append(m.CreatedDirs, "emptydir/sub", "emptydir") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Join(root, "emptydir", "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			m := loadManifest(t, tg)
			edit(m)
			b, err := json.MarshalIndent(m, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			original := readFile(t, tg.Abs(ManifestPath))
			write(t, tg.Abs(ManifestPath), string(b))
			t.Cleanup(func() { write(t, tg.Abs(ManifestPath), original) })

			snap, excl := snapshot(t, root), excludeBytes(t, tg)
			if _, err := Install(tg, assets, claudeOpts()); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("install: want ErrInvalidManifest, got %v", err)
			}
			if _, err := Uninstall(tg, uninstallOf(AgentClaude, AgentCodex)); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("uninstall: want ErrInvalidManifest, got %v", err)
			}
			if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
				t.Fatal("work tree or exclude file changed")
			}
			write(t, filepath.Join(root, "src", "new.go"), "package src\n")
			if p := porcelain(t, root); !strings.Contains(p, "src/new.go") {
				t.Fatalf("src/new.go hidden from git status: %q", p)
			}
			if err := os.Remove(filepath.Join(root, "src", "new.go")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstall_InterruptedForcedOrphanRemoval(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	write(t, tg.Abs(".claude/skills/beta/mine.md"), "mine") // disk now differs from the record
	noBeta := defaultFixture()
	noBeta.beta = ""
	assets := noBeta.dir(t)

	o := forced(claudeOpts())
	o.failAt = 1 // beta's removal is the only journaled operation
	if _, err := Install(tg, assets, o); err == nil {
		t.Fatal("no error")
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}
	res := runInstall(t, tg, assets, claudeOpts())
	wantActions(t, res, ActionCurrent, map[string]Action{"skill:beta": ActionRemoved})
	if tg.Exists(".claude/skills/beta") {
		t.Fatal("orphan kept")
	}
	if _, ok := loadManifest(t, tg).Record(AgentClaude, KindSkill, "beta"); ok {
		t.Fatal("orphan record kept")
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}
}

func TestInstall_InterruptedForcedDedupe(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	edit := findCanon(t, defaultFixture().canon(t, AgentClaude, evalURL), KindHook, "edit")
	// A second, different entry for the same ID under another event.
	dup := edit.Hook
	dup.Event, dup.Matcher, dup.Command = "Stop", "", edit.Hook.Command+" # copy"
	p := tg.Abs(edit.Path)
	var doc map[string]any
	if err := json.Unmarshal([]byte(readFile(t, p)), &doc); err != nil {
		t.Fatal(err)
	}
	doc["hooks"].(map[string]any)["Stop"] = []any{map[string]any{"matcher": "", "hooks": []any{
		map[string]any{"type": dup.Type, "command": dup.Command}}}}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	write(t, p, string(b))

	res := runInstall(t, tg, assets, claudeOpts())
	if r := resultFor(t, res, KindHook, "edit"); r.Action != ActionSkipped || r.Detail != "duplicate entries" {
		t.Fatalf("edit: %+v", r)
	}
	o := forced(claudeOpts())
	o.failAt = 1 // edit is the only write
	if _, err := Install(tg, assets, o); err == nil {
		t.Fatal("no error")
	}
	res = runInstall(t, tg, assets, claudeOpts())
	if a := resultFor(t, res, KindHook, "edit").Action; a != ActionUpdated {
		t.Fatalf("rerun without force: %s", a)
	}
	hooks, err := ReadHooks(p)
	if err != nil || hooks["edit"].Dup || hooks["edit"].Hash() != edit.Hash {
		t.Fatalf("hooks %+v %v", hooks, err)
	}
}

// The real path of a skill being replaced is only ever whole-old, absent, or
// whole-new; the old copy is moved aside first. Each crash state is simulated here.
func TestInstall_SkillReplaceIntermediateStates(t *testing.T) {
	v2 := defaultFixture()
	v2.alpha = "alpha v2"
	const alphaPath, aside = ".claude/skills/alpha", ".claude/skills/.alpha.lens-old"

	interrupted := func(t *testing.T) (Target, string) {
		t.Helper()
		root := newRepo(t)
		tg := mustTarget(t, root)
		runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
		assets := v2.dir(t)
		o := claudeOpts()
		o.failAt = 1 // alpha's update is journaled, not yet done
		if _, err := Install(tg, assets, o); err == nil {
			t.Fatal("no error")
		}
		if err := os.Rename(tg.Abs(alphaPath), tg.Abs(aside)); err != nil {
			t.Fatal(err)
		}
		return tg, assets
	}
	finish := func(t *testing.T, tg Target, assets string, want Action) {
		t.Helper()
		if p := porcelain(t, tg.Root); p != "" {
			t.Fatalf("crash state visible: %q", p)
		}
		res := runInstall(t, tg, assets, claudeOpts())
		if a := resultFor(t, res, KindSkill, "alpha").Action; a != want {
			t.Fatalf("rerun: %s, want %s", a, want)
		}
		if tg.Exists(aside) || readFile(t, tg.Abs(alphaPath+"/SKILL.md")) != "alpha v2" {
			t.Fatal("not finished")
		}
		if p := porcelain(t, tg.Root); p != "" {
			t.Fatalf("porcelain %q", p)
		}
		if !reflect.DeepEqual(excludeBlock(t, tg), claudeBlock) {
			t.Fatalf("block %v", excludeBlock(t, tg))
		}
	}

	t.Run("aside present, real absent", func(t *testing.T) {
		tg, assets := interrupted(t)
		write(t, tg.Abs(".claude/skills/.alpha.lens-tmp/SKILL.md"), "alpha v2") // copy done too
		finish(t, tg, assets, ActionWritten)
	})
	t.Run("aside present, real new", func(t *testing.T) {
		tg, assets := interrupted(t)
		if err := CopyDir(filepath.Join(assets, "skills", "alpha"), tg.Abs(alphaPath)); err != nil {
			t.Fatal(err)
		}
		finish(t, tg, assets, ActionCurrent)
	})
	t.Run("orphan: aside present, real absent", func(t *testing.T) {
		root := newRepo(t)
		tg := mustTarget(t, root)
		runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
		noBeta := defaultFixture()
		noBeta.beta = ""
		assets := noBeta.dir(t)
		o := claudeOpts()
		o.failAt = 1
		if _, err := Install(tg, assets, o); err == nil {
			t.Fatal("no error")
		}
		if err := os.Rename(tg.Abs(".claude/skills/beta"), tg.Abs(".claude/skills/.beta.lens-old")); err != nil {
			t.Fatal(err)
		}
		if p := porcelain(t, root); p != "" {
			t.Fatalf("crash state visible: %q", p)
		}
		res := runInstall(t, tg, assets, claudeOpts())
		wantActions(t, res, ActionCurrent, map[string]Action{"skill:beta": ActionRemoved})
		if tg.Exists(".claude/skills/.beta.lens-old") || tg.Exists(".claude/skills/beta") {
			t.Fatal("not finished")
		}
		if p := porcelain(t, root); p != "" {
			t.Fatalf("porcelain %q", p)
		}
	})
}

func TestInstall_OtherAgentTempLeftoverCleaned(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	o := claudeOpts()
	o.failAt = 3 // claude alpha's odd step: its temp and aside siblings are excluded
	if _, err := Install(tg, assets, o); err == nil {
		t.Fatal("no error")
	}
	leftovers := []string{".claude/skills/.alpha.lens-tmp", ".claude/skills/.alpha.lens-old"}
	for _, l := range leftovers {
		write(t, tg.Abs(l+"/partial"), "partial")
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("leftovers visible: %q", p)
	}
	runInstall(t, tg, assets, codexOpts())
	for _, l := range leftovers {
		if tg.Exists(l) {
			t.Errorf("%s kept by a codex install", l)
		}
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}
}

func TestInstall_PlanMatchesStatusesByItem(t *testing.T) {
	tg := mustTarget(t, newRepo(t))
	cs := defaultFixture().canon(t, AgentClaude, evalURL)
	m := manifestWith(AgentClaude)
	sts := evaluate(t, tg, AgentClaude, cs, m)
	for i, j := 0, len(sts)-1; i < j; i, j = i+1, j-1 {
		sts[i], sts[j] = sts[j], sts[i]
	}
	ops, err := planInstall(tg, claudeOpts(), cs, sts, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.canon.Item != op.Item {
			t.Errorf("status %+v planned with canonical %+v", op.Item, op.canon.Item)
		}
	}
	stray := ItemStatus{Item: Item{Agent: AgentClaude, Kind: KindSkill, ID: "gamma", Path: ".claude/skills/gamma"}, State: StateMissing}
	if _, err := planInstall(tg, claudeOpts(), cs, append(sts, stray), m, nil); err == nil {
		t.Fatal("a non-orphan status without a canonical item must be an error")
	}
}

func TestInstall_SymlinkInsideSkill(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	outside := t.TempDir()
	write(t, filepath.Join(outside, "sentinel"), "sentinel")
	outsideSnap := snapshot(t, outside)
	if err := os.Symlink(outside, tg.Abs(".claude/skills/alpha/link")); err != nil {
		t.Fatal(err)
	}
	before := porcelain(t, root)

	res := runInstall(t, tg, assets, claudeOpts())
	r := resultFor(t, res, KindSkill, "alpha")
	if r.Action != ActionSkipped || r.State != StateModified || r.Detail != "contains a symlink" {
		t.Fatalf("alpha: %+v", r)
	}
	if !tg.IsSymlink(".claude/skills/alpha/link") {
		t.Fatal("link removed without --force")
	}

	res = runInstall(t, tg, assets, forced(claudeOpts()))
	if a := resultFor(t, res, KindSkill, "alpha").Action; a != ActionUpdated {
		t.Fatalf("forced: %s", a)
	}
	if tg.Exists(".claude/skills/alpha/link") || tg.IsSymlink(".claude/skills/alpha") {
		t.Fatal("not replaced with a real directory")
	}
	if h, err := HashDir(tg.Abs(".claude/skills/alpha")); err != nil || h != findCanon(t, defaultFixture().canon(t, AgentClaude, evalURL), KindSkill, "alpha").Hash {
		t.Fatalf("alpha content: %v", err)
	}
	if !reflect.DeepEqual(snapshot(t, outside), outsideSnap) {
		t.Fatal("outside dir changed")
	}
	if p := porcelain(t, root); p != before {
		t.Fatalf("porcelain %q, want %q", p, before)
	}
}

func TestInstall_OrphanBlockedAndManual(t *testing.T) {
	noBeta := defaultFixture()
	noBeta.beta = ""

	t.Run("blocked", func(t *testing.T) {
		for _, force := range []bool{false, true} {
			tg := mustTarget(t, newRepo(t))
			runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
			outside := t.TempDir()
			if err := os.Rename(tg.Abs(".claude/skills/beta"), filepath.Join(outside, "beta")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "beta"), tg.Abs(".claude/skills/beta")); err != nil {
				t.Fatal(err)
			}
			outsideSnap := snapshot(t, outside)
			o := claudeOpts()
			o.Force = force
			res := runInstall(t, tg, noBeta.dir(t), o)
			r := resultFor(t, res, KindSkill, "beta")
			if r.Action != ActionBlocked || r.Snippet != ".claude/skills/beta is a symlink; lens will not write through it" {
				t.Fatalf("force=%v: %+v", force, r)
			}
			if !reflect.DeepEqual(snapshot(t, outside), outsideSnap) || !tg.IsSymlink(".claude/skills/beta") {
				t.Fatalf("force=%v: link or its target changed", force)
			}
			if _, ok := loadManifest(t, tg).Record(AgentClaude, KindSkill, "beta"); !ok {
				t.Fatalf("force=%v: record dropped", force)
			}
		}
	})

	// A tracked orphan whose entry is already gone (here an unstaged deletion):
	// the record is dropped and nothing is written.
	t.Run("tracked and absent", func(t *testing.T) {
		root := newRepo(t)
		tg := mustTarget(t, root)
		runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
		git(t, root, "add", "-f", "--", ".claude/skills/beta/SKILL.md")
		git(t, root, "commit", "-q", "-m", "tracked")
		if err := os.RemoveAll(tg.Abs(".claude/skills/beta")); err != nil {
			t.Fatal(err)
		}
		before := porcelain(t, root)
		res := runInstall(t, tg, noBeta.dir(t), claudeOpts())
		r := resultFor(t, res, KindSkill, "beta")
		if r.Action != ActionRemoved || !r.Tracked || r.Snippet != "" {
			t.Fatalf("beta %+v", r)
		}
		if _, ok := loadManifest(t, tg).Record(AgentClaude, KindSkill, "beta"); ok {
			t.Fatal("record kept")
		}
		if tg.Exists(".claude/skills/beta") || porcelain(t, root) != before {
			t.Fatalf("something written: %q", porcelain(t, root))
		}
	})

	t.Run("manual", func(t *testing.T) {
		for _, force := range []bool{false, true} {
			root := newRepo(t)
			tg := mustTarget(t, root)
			runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
			git(t, root, "add", "-f", "--", ".claude/skills/beta/SKILL.md")
			git(t, root, "commit", "-q", "-m", "tracked")
			o := claudeOpts()
			o.Force = force
			res := runInstall(t, tg, noBeta.dir(t), o)
			r := resultFor(t, res, KindSkill, "beta")
			if r.Action != ActionManual || !r.Tracked || !strings.Contains(r.Snippet, ".claude/skills/beta") {
				t.Fatalf("force=%v: %+v", force, r)
			}
			if readFile(t, tg.Abs(".claude/skills/beta/SKILL.md")) != "beta v1" {
				t.Fatalf("force=%v: tracked file changed", force)
			}
			if _, ok := loadManifest(t, tg).Record(AgentClaude, KindSkill, "beta"); !ok {
				t.Fatalf("force=%v: record dropped", force)
			}
		}
	})
}

// After manifest loss, a lens sibling left by a crash beside another agent's
// skill is still in the block: it is cleaned before any save drops its pattern.
func TestInstall_ManifestLossSiblingLeftoverCleaned(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	o := claudeOpts()
	o.failAt = 3 // claude alpha's odd step: its siblings are listed in the block
	if _, err := Install(tg, assets, o); err == nil {
		t.Fatal("no error")
	}
	leftovers := []string{".claude/skills/.alpha.lens-tmp", ".claude/skills/.alpha.lens-old"}
	for _, l := range leftovers {
		write(t, tg.Abs(l+"/partial"), "partial")
	}
	deleteManifest(t, tg)
	if p := porcelain(t, root); p != "" {
		t.Fatalf("setup porcelain %q", p)
	}

	co := codexOpts()
	co.onSave = func(stage string) {
		if p := porcelain(t, root); p != "" {
			t.Errorf("%s: porcelain %q", stage, p)
		}
	}
	runInstall(t, tg, assets, co)
	for _, l := range leftovers {
		if tg.Exists(l) {
			t.Errorf("%s kept", l)
		}
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}
}

func TestInstall_TrackedCodexForeignNotRerecorded(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%v", force), func(t *testing.T) {
			root := newRepo(t)
			tg := mustTarget(t, root)
			assets := defaultFixture().dir(t)
			cs := defaultFixture().canon(t, AgentCodex, evalURL)
			mcp := findCanon(t, cs, KindMCP, "projectlens")
			body := "[mcp_servers.projectlens]\nurl = \"" + evalURL + "\"\n"
			write(t, tg.Abs(mcp.Path), body)
			git(t, root, "add", "-f", "--", mcp.Path)
			git(t, root, "commit", "-q", "-m", "tracked")
			seed := manifestWith(AgentCodex, recordFor(mcp, MCPHash(evalURLOther)))
			if err := seed.Save(tg); err != nil {
				t.Fatal(err)
			}

			o := codexOpts()
			o.Force = force
			res := runInstall(t, tg, assets, o)
			if r := resultFor(t, res, KindMCP, "projectlens"); r.Action != ActionCurrent {
				t.Fatalf("action %q, want %q", r.Action, ActionCurrent)
			}
			rec, ok := loadManifest(t, tg).Record(AgentCodex, KindMCP, "projectlens")
			if !ok || rec.Hash != MCPHash(evalURLOther) {
				t.Fatalf("record re-recorded: %+v ok=%v", rec, ok)
			}
			got, err := os.ReadFile(tg.Abs(mcp.Path))
			if err != nil || string(got) != body {
				t.Fatalf("config.toml changed: %q err=%v", got, err)
			}
		})
	}
}

func TestBlockedSnippet_UsesFields(t *testing.T) {
	if got := blockedSnippet(ItemStatus{symlinkAt: "x", Detail: "unrelated text"}); got != symlinkSnippet("x") {
		t.Errorf("symlinkAt: %q", got)
	}
	if got := blockedSnippet(ItemStatus{foreign: true}); got != snippetForeign {
		t.Errorf("foreign: %q", got)
	}
	if got := blockedSnippet(ItemStatus{Detail: "symlinked parent x"}); got != "" {
		t.Errorf("detail text must not be parsed: %q", got)
	}
}
