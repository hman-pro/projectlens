package agentsetup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func runUninstall(t *testing.T, tg Target, o UninstallOpts) UninstallResult {
	t.Helper()
	res, err := Uninstall(tg, o)
	if err != nil {
		t.Fatalf("Uninstall(%+v): %v", o, err)
	}
	return res
}

func uninstallOf(agents ...Agent) UninstallOpts { return UninstallOpts{Agents: agents} }

func forcedUninstall(o UninstallOpts) UninstallOpts { o.Force = true; return o }

// wantUninstall checks every item's action; items not named must have def.
func wantUninstall(t *testing.T, res UninstallResult, def Action, named map[string]Action) {
	t.Helper()
	wantActions(t, InstallResult{Items: res.Items}, def, named)
}

func uresultFor(t *testing.T, res UninstallResult, kind Kind, id string) ItemResult {
	t.Helper()
	return resultFor(t, InstallResult{Items: res.Items}, kind, id)
}

func wantGone(t *testing.T, tg Target, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if tg.Exists(p) {
			t.Errorf("%s still exists", p)
		}
	}
}

func TestUninstall_RestoresCleanRepoExactly(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	snap, excl := snapshot(t, root), excludeBytes(t, tg)
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	runInstall(t, tg, assets, codexOpts())

	res := runUninstall(t, tg, uninstallOf(AgentClaude, AgentCodex))
	if res.Nothing || len(res.Items) != 8 {
		t.Fatalf("result %+v", res)
	}
	for _, r := range res.Items {
		if r.Action != ActionRemoved || !r.Owned || r.State != StateCurrent || r.Tracked {
			t.Errorf("item %+v", r)
		}
	}
	if got := snapshot(t, root); !reflect.DeepEqual(got, snap) {
		t.Fatalf("work tree differs:\n got %v\nwant %v", got, snap)
	}
	if got := excludeBytes(t, tg); got != excl {
		t.Fatalf("exclude file differs:\n got %q\nwant %q", got, excl)
	}
	wantGone(t, tg, ".claude", ".agents", ".codex", ".projectlens", ".mcp.json")
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}
}

func TestUninstall_KeepsPreexistingFilesAndContent(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	const mcpOrig = `{"mcpServers": {"other": {"type": "http", "url": "http://other/mcp"}}, "x": 1}`
	const tomlOrig = "model = \"x\"\n\n[profiles.fast]\nmodel = \"y\"\n"
	write(t, tg.Abs(".mcp.json"), mcpOrig)
	write(t, tg.Abs(".codex/config.toml"), tomlOrig)
	before, excl := porcelain(t, root), excludeBytes(t, tg)
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	runInstall(t, tg, assets, codexOpts())

	res := runUninstall(t, tg, uninstallOf(AgentClaude, AgentCodex))
	wantUninstall(t, res, ActionRemoved, nil)
	var got, want any
	if err := json.Unmarshal([]byte(readFile(t, tg.Abs(".mcp.json"))), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(mcpOrig), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf(".mcp.json = %v, want %v", got, want)
	}
	if b := readFile(t, tg.Abs(".codex/config.toml")); b != tomlOrig {
		t.Errorf("config.toml = %q, want %q", b, tomlOrig)
	}
	if p := porcelain(t, root); p != before {
		t.Errorf("porcelain %q, want %q", p, before)
	}
	if excludeBytes(t, tg) != excl {
		t.Error("exclude file differs")
	}
	wantGone(t, tg, ".claude", ".agents", ".projectlens")
}

func TestUninstall_OneAgentLeavesOtherWorking(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	runInstall(t, tg, assets, codexOpts())

	res := runUninstall(t, tg, uninstallOf(AgentClaude))
	if len(res.Items) != 5 {
		t.Fatalf("items %+v", res.Items)
	}
	for _, r := range res.Items {
		if r.Agent != AgentClaude || r.Action != ActionRemoved {
			t.Errorf("item %+v", r)
		}
	}
	for _, st := range evaluateSaved(t, tg, AgentCodex, defaultFixture().canon(t, AgentCodex, evalURL)) {
		if st.State != StateCurrent || !st.Owned {
			t.Errorf("codex %+v", st)
		}
	}
	if got := excludeBlock(t, tg); !reflect.DeepEqual(got, codexBlockPaths) {
		t.Fatalf("block %v, want %v", got, codexBlockPaths)
	}
	if got := loadManifest(t, tg).InstalledAgents(); !reflect.DeepEqual(got, []Agent{AgentCodex}) {
		t.Fatalf("agents %v", got)
	}
	wantGone(t, tg, ".claude", ".mcp.json")
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}
}

func TestUninstall_ModifiedSkippedThenForced(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	snap, excl := snapshot(t, root), excludeBytes(t, tg)
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	alpha := findCanon(t, defaultFixture().canon(t, AgentClaude, evalURL), KindSkill, "alpha")
	editByHand(t, tg, alpha)

	res := runUninstall(t, tg, uninstallOf(AgentClaude))
	wantUninstall(t, res, ActionRemoved, map[string]Action{"skill:alpha": ActionSkipped})
	if r := uresultFor(t, res, KindSkill, "alpha"); r.State != StateModified || !r.Owned {
		t.Errorf("alpha %+v", r)
	}
	if !tg.Exists(alpha.Path + "/local-notes.md") {
		t.Fatal("skipped skill changed")
	}
	m := loadManifest(t, tg)
	if _, ok := m.Record(AgentClaude, KindSkill, "alpha"); !ok || !m.Created(alpha.Path) {
		t.Fatalf("record or created path dropped: %+v", m)
	}
	want := []string{"/.claude/skills/alpha/", "/" + ManifestPath}
	if got := excludeBlock(t, tg); !reflect.DeepEqual(got, want) {
		t.Fatalf("block %v, want %v", got, want)
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}

	res = runUninstall(t, tg, forcedUninstall(uninstallOf(AgentClaude)))
	if len(res.Items) != 1 || res.Items[0].Action != ActionRemoved || res.Items[0].State != StateModified {
		t.Fatalf("forced %+v", res.Items)
	}
	if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
		t.Fatalf("not restored: %v", snapshot(t, root))
	}
}

func TestUninstall_TrackedNeverTouched(t *testing.T) {
	cases := []struct {
		name  string
		agent Agent
		path  string // the item path
		file  string // the file committed
	}{
		{"mcp.json", AgentClaude, ".mcp.json", ".mcp.json"},
		{"settings.local.json", AgentClaude, ".claude/settings.local.json", ".claude/settings.local.json"},
		{"skill", AgentClaude, ".claude/skills/alpha", ".claude/skills/alpha/SKILL.md"},
		{"codex config", AgentCodex, ".codex/config.toml", ".codex/config.toml"},
	}
	for _, tc := range cases {
		for _, force := range []bool{false, true} {
			t.Run(tc.name, func(t *testing.T) {
				root := newRepo(t)
				tg := mustTarget(t, root)
				runInstall(t, tg, defaultFixture().dir(t), InstallOpts{Agent: tc.agent, URL: evalURL})
				git(t, root, "add", "-f", "--", tc.file)
				git(t, root, "commit", "-q", "-m", "tracked")
				content := readFile(t, tg.Abs(tc.file))

				o := uninstallOf(tc.agent)
				o.Force = force
				res := runUninstall(t, tg, o)
				m := loadManifest(t, tg)
				n := 0
				for _, r := range res.Items {
					if r.Path != tc.path {
						if r.Action != ActionRemoved {
							t.Errorf("untracked item %+v", r)
						}
						continue
					}
					n++
					if r.Action != ActionManual || !r.Tracked || !strings.Contains(r.Snippet, tc.path) || !strings.Contains(r.Snippet, r.ID) {
						t.Errorf("tracked item %+v", r)
					}
					if _, ok := m.Record(tc.agent, r.Kind, r.ID); !ok {
						t.Errorf("record dropped: %+v", r)
					}
				}
				if n == 0 {
					t.Fatal("no tracked item reported")
				}
				if got := readFile(t, tg.Abs(tc.file)); got != content {
					t.Fatalf("tracked file changed: %q", got)
				}
				if p := porcelain(t, root); p != "" {
					t.Fatalf("porcelain %q", p)
				}
			})
		}
	}

	// A tracked path whose lens entry is already gone: the record is dropped and
	// nothing is written, even though the file is empty and install created it.
	t.Run("tracked and absent", func(t *testing.T) {
		root := newRepo(t)
		tg := mustTarget(t, root)
		runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
		write(t, tg.Abs(".mcp.json"), "{}\n")
		git(t, root, "add", "-f", "--", ".mcp.json")
		git(t, root, "commit", "-q", "-m", "tracked")
		res := runUninstall(t, tg, uninstallOf(AgentClaude))
		wantUninstall(t, res, ActionRemoved, nil)
		if r := uresultFor(t, res, KindMCP, "projectlens"); !r.Tracked || r.State != StateMissing || r.Snippet != "" {
			t.Errorf("mcp %+v", r)
		}
		if got := readFile(t, tg.Abs(".mcp.json")); got != "{}\n" {
			t.Fatalf("tracked file changed: %q", got)
		}
		wantGone(t, tg, ".claude")
		// The file install created still exists, so it keeps the manifest alive.
		if m := loadManifest(t, tg); len(m.Agents) != 0 || !reflect.DeepEqual(m.CreatedPaths, []string{".mcp.json"}) {
			t.Fatalf("manifest %+v", m)
		}
		if p := porcelain(t, root); p != "" {
			t.Fatalf("porcelain %q", p)
		}
	})
}

func TestUninstall_OtherWorktreeKeepsExclusions(t *testing.T) {
	root := newRepo(t)
	wt := addWorktree(t, root)
	main, linked := mustTarget(t, root), mustTarget(t, wt)
	excl := excludeBytes(t, main)
	assets := defaultFixture().dir(t)
	runInstall(t, main, assets, claudeOpts())
	runInstall(t, linked, assets, claudeOpts())
	mainBlock, mainPorcelain := excludeBlock(t, main), porcelain(t, root)

	res := runUninstall(t, linked, uninstallOf(AgentClaude))
	wantUninstall(t, res, ActionRemoved, nil)
	for _, st := range evaluateSaved(t, main, AgentClaude, defaultFixture().canon(t, AgentClaude, evalURL)) {
		if st.State != StateCurrent || !st.Owned {
			t.Errorf("main %+v", st)
		}
	}
	if got := excludeBlock(t, main); !reflect.DeepEqual(got, mainBlock) {
		t.Fatalf("main block %v, want %v", got, mainBlock)
	}
	if p := porcelain(t, root); p != mainPorcelain {
		t.Fatalf("main porcelain %q, want %q", p, mainPorcelain)
	}
	if got := excludeBlock(t, linked); len(got) != 0 {
		t.Fatalf("worktree block %v", got)
	}
	wantGone(t, linked, ".claude", ".mcp.json", ".projectlens")
	if p := porcelain(t, wt); p != "" {
		t.Fatalf("worktree porcelain %q", p)
	}

	runUninstall(t, main, uninstallOf(AgentClaude))
	if got := excludeBytes(t, main); got != excl {
		t.Fatalf("exclude file %q, want %q", got, excl)
	}
}

func TestUninstall_SymlinkedPathNeverDeletedThrough(t *testing.T) {
	t.Run(".agents", func(t *testing.T) {
		root := newRepo(t)
		tg := mustTarget(t, root)
		runInstall(t, tg, defaultFixture().dir(t), codexOpts())
		outside := t.TempDir()
		if err := CopyDir(tg.Abs(".agents"), filepath.Join(outside, "agents")); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(tg.Abs(".agents")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "agents"), tg.Abs(".agents")); err != nil {
			t.Fatal(err)
		}
		outsideSnap, before := snapshot(t, outside), porcelain(t, root)

		for _, force := range []bool{false, true} {
			o := uninstallOf(AgentCodex)
			o.Force = force
			res := runUninstall(t, tg, o)
			m := loadManifest(t, tg)
			skills := 0
			for _, r := range res.Items {
				if r.Kind != KindSkill {
					if r.Action != ActionRemoved {
						t.Errorf("force=%v: %+v", force, r)
					}
					continue
				}
				skills++
				if r.Action != ActionBlocked || r.State != StateBlocked || r.Detail != "symlinked parent .agents" ||
					r.Snippet != ".agents is a symlink; lens will not write through it" {
					t.Errorf("force=%v: %+v", force, r)
				}
				if _, ok := m.Record(AgentCodex, r.Kind, r.ID); !ok {
					t.Errorf("force=%v: record dropped %+v", force, r)
				}
			}
			if skills != 2 {
				t.Fatalf("force=%v: %d skill results", force, skills)
			}
			if !reflect.DeepEqual(snapshot(t, outside), outsideSnap) || !tg.IsSymlink(".agents") {
				t.Fatalf("force=%v: link or its target changed", force)
			}
			if p := porcelain(t, root); p != before {
				t.Fatalf("force=%v: porcelain %q, want %q", force, p, before)
			}
		}
	})

	t.Run("skill dir", func(t *testing.T) {
		root := newRepo(t)
		tg := mustTarget(t, root)
		runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
		outside := t.TempDir()
		if err := CopyDir(tg.Abs(".claude/skills/alpha"), filepath.Join(outside, "alpha")); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(tg.Abs(".claude/skills/alpha")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "alpha"), tg.Abs(".claude/skills/alpha")); err != nil {
			t.Fatal(err)
		}
		outsideSnap, before := snapshot(t, outside), porcelain(t, root)
		if before != "?? .claude/skills/alpha\n" {
			t.Fatalf("setup porcelain %q", before)
		}

		for _, force := range []bool{false, true} {
			o := uninstallOf(AgentClaude)
			o.Force = force
			res := runUninstall(t, tg, o)
			r := uresultFor(t, res, KindSkill, "alpha")
			if r.Action != ActionBlocked || r.State != StateBlocked || r.Detail != "symlink" ||
				r.Snippet != ".claude/skills/alpha is a symlink; lens will not write through it" {
				t.Errorf("force=%v: %+v", force, r)
			}
			if _, ok := loadManifest(t, tg).Record(AgentClaude, KindSkill, "alpha"); !ok {
				t.Errorf("force=%v: record dropped", force)
			}
			if !reflect.DeepEqual(snapshot(t, outside), outsideSnap) || !tg.IsSymlink(".claude/skills/alpha") {
				t.Fatalf("force=%v: link or its target changed", force)
			}
			// The link stays visible: its directory pattern never hides it.
			if p := porcelain(t, root); p != before {
				t.Fatalf("force=%v: porcelain %q, want %q", force, p, before)
			}
		}
	})
}

func TestUninstall_TrackedManifestRefused(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	git(t, root, "add", "-f", "--", ManifestPath)
	git(t, root, "commit", "-q", "-m", "tracked manifest")
	snap, excl := snapshot(t, root), excludeBytes(t, tg)
	for _, force := range []bool{false, true} {
		o := uninstallOf(AgentClaude)
		o.Force = force
		if _, err := Uninstall(tg, o); !errors.Is(err, ErrManifestTracked) {
			t.Fatalf("force=%v: want ErrManifestTracked, got %v", force, err)
		}
	}
	if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
		t.Fatal("something changed")
	}
}

func TestUninstall_SharedFileKeepsOwnershipUntilEmpty(t *testing.T) {
	const settings = ".claude/settings.local.json"
	root := newRepo(t)
	tg := mustTarget(t, root)
	snap, excl := snapshot(t, root), excludeBytes(t, tg)
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	if !loadManifest(t, tg).Created(settings) {
		t.Fatal("setup: settings.local.json not created by install")
	}
	items := loadManifest(t, tg).Agents[AgentClaude].Items
	first, hooks := -1, 0
	for i, r := range items {
		if r.Kind == KindHook {
			hooks++
			if first < 0 {
				first = i
			}
		}
	}
	if first < 0 || hooks < 2 || first+1 >= len(items) {
		t.Fatalf("setup: records %+v", items)
	}

	// Every record before it is a journaled removal (two steps each); stop at the
	// odd step of the next item, after the first hook's record was dropped and saved.
	o := uninstallOf(AgentClaude)
	o.failAt = 2*first + 3
	if _, err := Uninstall(tg, o); err == nil {
		t.Fatal("no error")
	}
	m := loadManifest(t, tg)
	if _, ok := m.Record(AgentClaude, KindHook, items[first].ID); ok {
		t.Fatalf("first hook record kept: %+v", m.Agents[AgentClaude])
	}
	left := 0
	for _, r := range m.Agents[AgentClaude].Items {
		if r.Kind == KindHook {
			left++
		}
	}
	if left != hooks-1 {
		t.Fatalf("%d hook records left, want %d", left, hooks-1)
	}
	if !tg.Exists(settings) || !m.Created(settings) {
		t.Fatalf("settings file gone or no longer owned: %v", m.CreatedPaths)
	}
	found := false
	for _, e := range excludeBlock(t, tg) {
		found = found || e == "/"+settings
	}
	if !found {
		t.Fatalf("settings not excluded: %v", excludeBlock(t, tg))
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}

	runUninstall(t, tg, uninstallOf(AgentClaude))
	wantGone(t, tg, settings, ".claude")
	if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
		t.Fatalf("not restored: %v", snapshot(t, root))
	}
}

func TestUninstall_Interrupted(t *testing.T) {
	assets := defaultFixture().dir(t)
	for _, agents := range [][]Agent{{AgentClaude}, {AgentCodex}, {AgentClaude, AgentCodex}} {
		n := 0
		for _, a := range agents {
			cs, err := LoadCanonical(assets, a, evalURL)
			if err != nil {
				t.Fatal(err)
			}
			n += len(cs)
		}
		for failAt := 1; failAt <= 2*n; failAt++ {
			root := newRepo(t)
			tg := mustTarget(t, root)
			snap, excl := snapshot(t, root), excludeBytes(t, tg)
			for _, a := range agents {
				runInstall(t, tg, assets, InstallOpts{Agent: a, URL: evalURL})
			}
			o := uninstallOf(agents...)
			o.failAt = failAt
			if _, err := Uninstall(tg, o); err == nil || !strings.Contains(err.Error(), "uninstall interrupted") {
				t.Fatalf("%v failAt %d: err %v", agents, failAt, err)
			}
			if p := porcelain(t, root); p != "" {
				t.Fatalf("%v failAt %d: porcelain %q", agents, failAt, p)
			}

			// The rerun finishes without --force.
			res := runUninstall(t, tg, uninstallOf(agents...))
			k := (failAt + 1) / 2 // the interrupted item, 1-based; earlier records are gone
			if len(res.Items) != n-(k-1) {
				t.Errorf("%v failAt %d: %d items on rerun, want %d", agents, failAt, len(res.Items), n-(k-1))
			}
			for _, r := range res.Items {
				if r.Action != ActionRemoved {
					t.Errorf("%v failAt %d: rerun %+v", agents, failAt, r)
				}
			}
			if got := snapshot(t, root); !reflect.DeepEqual(got, snap) {
				t.Fatalf("%v failAt %d: work tree %v", agents, failAt, got)
			}
			if excludeBytes(t, tg) != excl {
				t.Fatalf("%v failAt %d: exclude file %q", agents, failAt, excludeBytes(t, tg))
			}
		}
	}
}

// An interrupted forced removal of edited content is finished without --force.
func TestUninstall_InterruptedForcedRemoval(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		root := newRepo(t)
		tg := mustTarget(t, root)
		snap, excl := snapshot(t, root), excludeBytes(t, tg)
		runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
		cs := defaultFixture().canon(t, AgentClaude, evalURL)
		// The first run removes every other record, so alpha's removal is steps 1 and 2.
		editByHand(t, tg, findCanon(t, cs, KindSkill, "alpha"))
		res := runUninstall(t, tg, uninstallOf(AgentClaude))
		wantUninstall(t, res, ActionRemoved, map[string]Action{"skill:alpha": ActionSkipped})

		o := forcedUninstall(uninstallOf(AgentClaude))
		o.failAt = failAt
		if _, err := Uninstall(tg, o); err == nil {
			t.Fatalf("failAt %d: no error", failAt)
		}
		if p := porcelain(t, root); p != "" {
			t.Fatalf("failAt %d: porcelain %q", failAt, p)
		}
		res = runUninstall(t, tg, uninstallOf(AgentClaude))
		wantUninstall(t, res, ActionRemoved, nil)
		if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
			t.Fatalf("failAt %d: not restored: %v", failAt, snapshot(t, root))
		}
	}
}

// A symlink inside an installed skill makes it differ from its record: skipped
// without --force; with --force the directory is removed without following the link.
func TestUninstall_SymlinkInsideSkill(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	snap, excl := snapshot(t, root), excludeBytes(t, tg)
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	outside := t.TempDir()
	write(t, filepath.Join(outside, "sentinel"), "sentinel")
	outsideSnap := snapshot(t, outside)
	if err := os.Symlink(outside, tg.Abs(".claude/skills/alpha/link")); err != nil {
		t.Fatal(err)
	}

	res := runUninstall(t, tg, uninstallOf(AgentClaude))
	r := uresultFor(t, res, KindSkill, "alpha")
	if r.Action != ActionSkipped || r.State != StateModified || r.Detail != "contains a symlink" {
		t.Fatalf("alpha %+v", r)
	}
	if !tg.IsSymlink(".claude/skills/alpha/link") {
		t.Fatal("skipped skill changed")
	}

	res = runUninstall(t, tg, forcedUninstall(uninstallOf(AgentClaude)))
	wantUninstall(t, res, ActionRemoved, nil)
	if !reflect.DeepEqual(snapshot(t, outside), outsideSnap) {
		t.Fatal("outside dir changed")
	}
	if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
		t.Fatalf("not restored: %v", snapshot(t, root))
	}
}

// A crash inside a skill removal, after the rename aside, leaves the real path
// absent and the aside copy present. Both stay hidden; the next run finishes.
func TestUninstall_SkillAsideLeftByCrash(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	snap, excl := snapshot(t, root), excludeBytes(t, tg)
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	idx := -1
	for i, r := range loadManifest(t, tg).Agents[AgentClaude].Items {
		if r.Kind == KindSkill && r.ID == "alpha" {
			idx = i
		}
	}
	o := uninstallOf(AgentClaude)
	o.failAt = 2*idx + 1 // alpha's removal is journaled, not done
	if _, err := Uninstall(tg, o); err == nil {
		t.Fatal("no error")
	}
	if err := os.Rename(tg.Abs(".claude/skills/alpha"), tg.Abs(".claude/skills/.alpha.lens-old")); err != nil {
		t.Fatal(err)
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("crash state visible: %q", p)
	}
	res := runUninstall(t, tg, uninstallOf(AgentClaude))
	if r := uresultFor(t, res, KindSkill, "alpha"); r.Action != ActionRemoved || r.State != StateMissing {
		t.Fatalf("alpha %+v", r)
	}
	if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
		t.Fatalf("not restored: %v", snapshot(t, root))
	}
}

// Uninstall straight after an interrupted install (journaled writes pending,
// done or not) restores the clean repo without --force.
func TestUninstall_AfterInterruptedInstall(t *testing.T) {
	assets := defaultFixture().dir(t)
	for _, base := range []InstallOpts{claudeOpts(), codexOpts()} {
		cs, err := LoadCanonical(assets, base.Agent, evalURL)
		if err != nil {
			t.Fatal(err)
		}
		for failAt := 1; failAt <= 2*len(cs); failAt++ {
			root := newRepo(t)
			tg := mustTarget(t, root)
			snap, excl := snapshot(t, root), excludeBytes(t, tg)
			o := base
			o.failAt = failAt
			if _, err := Install(tg, assets, o); err == nil {
				t.Fatalf("%s failAt %d: no error", base.Agent, failAt)
			}
			res := runUninstall(t, tg, uninstallOf(base.Agent))
			wantUninstall(t, res, ActionRemoved, nil)
			if got := snapshot(t, root); !reflect.DeepEqual(got, snap) {
				t.Fatalf("%s failAt %d: work tree %v", base.Agent, failAt, got)
			}
			if excludeBytes(t, tg) != excl {
				t.Fatalf("%s failAt %d: exclude file %q", base.Agent, failAt, excludeBytes(t, tg))
			}
		}
	}
}

// A remover can rewrite a file it emptied and crash before deleting it. The next
// run finds the entry absent, drops the record, and deletes the empty file.
func TestUninstall_EmptiedMergedFileCrash(t *testing.T) {
	for _, tc := range []struct {
		agent   Agent
		path    string
		emptied string
	}{
		{AgentClaude, ".mcp.json", "{}\n"},
		{AgentCodex, ".codex/config.toml", "\n"},
	} {
		t.Run(string(tc.agent), func(t *testing.T) {
			root := newRepo(t)
			tg := mustTarget(t, root)
			snap, excl := snapshot(t, root), excludeBytes(t, tg)
			runInstall(t, tg, defaultFixture().dir(t), InstallOpts{Agent: tc.agent, URL: evalURL})
			m := loadManifest(t, tg)
			ar := m.Agents[tc.agent]
			for i := range ar.Items {
				if ar.Items[i].Kind == KindMCP {
					ar.Items[i].Pending = &Pending{Op: "remove"}
				}
			}
			if err := m.Save(tg); err != nil {
				t.Fatal(err)
			}
			write(t, tg.Abs(tc.path), tc.emptied)
			if p := porcelain(t, root); p != "" {
				t.Fatalf("crash state visible: %q", p)
			}

			res := runUninstall(t, tg, uninstallOf(tc.agent))
			wantUninstall(t, res, ActionRemoved, nil)
			if r := uresultFor(t, res, KindMCP, "projectlens"); r.State != StateMissing {
				t.Errorf("mcp %+v", r)
			}
			if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
				t.Fatalf("not restored: %v", snapshot(t, root))
			}
		})
	}
}

// The save that deletes the manifest first removes the empty directories install
// created, so a crash right after it cannot leave them behind without a manifest.
func TestJournal_SaveThatDeletesManifestRemovesEmptyDirs(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	snap, excl := snapshot(t, root), excludeBytes(t, tg)
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	for _, p := range []string{".mcp.json", ".claude/settings.local.json", ".claude/skills/alpha", ".claude/skills/beta"} {
		if err := os.RemoveAll(tg.Abs(p)); err != nil {
			t.Fatal(err)
		}
	}
	m := loadManifest(t, tg)
	delete(m.Agents, AgentClaude)
	j := &journal{t: tg, m: m, name: "uninstall", skills: skillPaths(nil, m), dirsBeforeDelete: true}
	if err := j.save(nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
		t.Fatalf("not restored: %v", snapshot(t, root))
	}
}

func TestUninstall_DryRun(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	editByHand(t, tg, findCanon(t, defaultFixture().canon(t, AgentClaude, evalURL), KindSkill, "alpha"))
	snap, excl := snapshot(t, root), excludeBytes(t, tg)

	o := uninstallOf(AgentClaude)
	o.DryRun = true
	res := runUninstall(t, tg, o)
	wantUninstall(t, res, ActionRemoved, map[string]Action{"skill:alpha": ActionSkipped})
	res = runUninstall(t, tg, forcedUninstall(o))
	wantUninstall(t, res, ActionRemoved, nil)
	if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
		t.Fatal("dry run changed something")
	}
}

func TestUninstall_NothingInstalled(t *testing.T) {
	check := func(t *testing.T, tg Target, o UninstallOpts) {
		t.Helper()
		snap, excl := snapshot(t, tg.Root), excludeBytes(t, tg)
		res := runUninstall(t, tg, o)
		if !res.Nothing || len(res.Items) != 0 {
			t.Fatalf("result %+v", res)
		}
		if !reflect.DeepEqual(snapshot(t, tg.Root), snap) || excludeBytes(t, tg) != excl {
			t.Fatal("something changed")
		}
	}

	t.Run("fresh repo", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		check(t, tg, uninstallOf(AgentClaude, AgentCodex))
		wantGone(t, tg, ".projectlens")
	})
	t.Run("other agent installed", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		runInstall(t, tg, defaultFixture().dir(t), codexOpts())
		check(t, tg, uninstallOf(AgentClaude))
	})
	t.Run("only unrecorded created paths", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		assets := defaultFixture().dir(t)
		runInstall(t, tg, assets, claudeOpts())
		runInstall(t, tg, assets, codexOpts())
		deleteManifest(t, tg)
		runInstall(t, tg, assets, claudeOpts())
		runUninstall(t, tg, uninstallOf(AgentClaude))
		// The manifest and block stay: they keep the codex paths hidden.
		check(t, tg, uninstallOf(AgentClaude, AgentCodex))
		if !tg.Exists(ManifestPath) {
			t.Fatal("manifest removed")
		}
	})
}

// A directory named like a skill sibling that lens did not create (its pattern is
// not in the exclude block when the run starts) is never deleted or hidden; the
// skill that would need that name is blocked instead.
func TestSkillSibling_DevelopersOwnDirSurvives(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	const dev = ".claude/skills/.alpha.lens-old"
	write(t, tg.Abs(dev+"/notes.md"), "mine")
	devSnap, before := snapshot(t, tg.Abs(dev)), porcelain(t, root)
	if !strings.Contains(before, dev) {
		t.Fatalf("setup porcelain %q", before)
	}
	check := func(stage string) {
		t.Helper()
		if !reflect.DeepEqual(snapshot(t, tg.Abs(dev)), devSnap) {
			t.Fatalf("%s: developer's directory changed", stage)
		}
		if p := porcelain(t, root); p != before {
			t.Fatalf("%s: porcelain %q, want %q", stage, p, before)
		}
	}
	wantBlocked := func(stage string, r ItemResult) {
		t.Helper()
		if r.Action != ActionBlocked || r.Detail != "foreign sibling "+dev || !strings.Contains(r.Snippet, dev) {
			t.Fatalf("%s: alpha %+v", stage, r)
		}
	}

	res := runInstall(t, tg, assets, claudeOpts())
	wantActions(t, res, ActionCurrent, nil)
	check("install")

	v2 := defaultFixture()
	v2.alpha = "alpha v2"
	res = runInstall(t, tg, v2.dir(t), claudeOpts())
	wantBlocked("stale install", resultFor(t, res, KindSkill, "alpha"))
	if readFile(t, tg.Abs(".claude/skills/alpha/SKILL.md")) != "alpha v1" {
		t.Fatal("blocked skill written")
	}
	check("stale install")

	for _, force := range []bool{false, true} {
		o := uninstallOf(AgentClaude)
		o.Force = force
		ures := runUninstall(t, tg, o)
		wantBlocked("uninstall", uresultFor(t, ures, KindSkill, "alpha"))
		if !tg.Exists(".claude/skills/alpha/SKILL.md") {
			t.Fatal("blocked skill removed")
		}
		if _, ok := loadManifest(t, tg).Record(AgentClaude, KindSkill, "alpha"); !ok {
			t.Fatal("blocked skill lost its record")
		}
		check("uninstall")
	}

	if err := os.RemoveAll(tg.Abs(dev)); err != nil {
		t.Fatal(err)
	}
	ures := runUninstall(t, tg, uninstallOf(AgentClaude))
	wantUninstall(t, ures, ActionRemoved, nil)
	wantGone(t, tg, ".claude", ".projectlens")
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}

	t.Run("orphan removal", func(t *testing.T) {
		root := newRepo(t)
		tg := mustTarget(t, root)
		runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
		const dev = ".claude/skills/.beta.lens-tmp"
		write(t, tg.Abs(dev+"/x"), "mine")
		before := porcelain(t, root)
		noBeta := defaultFixture()
		noBeta.beta = ""
		res := runInstall(t, tg, noBeta.dir(t), claudeOpts())
		if r := resultFor(t, res, KindSkill, "beta"); r.Action != ActionBlocked || r.Detail != "foreign sibling "+dev {
			t.Fatalf("beta %+v", r)
		}
		if !tg.Exists(".claude/skills/beta") || readFile(t, tg.Abs(dev+"/x")) != "mine" || porcelain(t, root) != before {
			t.Fatal("orphan or developer's directory changed")
		}
	})
}

// The save sequence never exposes a path to git: the block is only widened
// before the manifest is written or deleted, and narrowed only after.
func TestJournal_SaveNeverExposesPaths(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	stages := 0
	hook := func(stage string) {
		stages++
		if p := porcelain(t, root); p != "" {
			t.Errorf("%s: porcelain %q", stage, p)
		}
	}
	assets := defaultFixture().dir(t)
	for _, o := range []InstallOpts{claudeOpts(), codexOpts()} {
		o.onSave = hook
		runInstall(t, tg, assets, o)
	}
	o := uninstallOf(AgentClaude, AgentCodex)
	o.onSave = hook
	runUninstall(t, tg, o)
	if stages == 0 {
		t.Fatal("hook never called")
	}
	wantGone(t, tg, ".projectlens")
}

// crashAt runs f with o's onSave hook panicking at the n-th save point (1-based).
func crashAt(t *testing.T, n int, f func(hook func(string))) {
	t.Helper()
	calls := 0
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("no crash at save point %d (%d points)", n, calls)
		}
	}()
	f(func(string) {
		calls++
		if calls == n {
			panic("crash")
		}
	})
}

// A crash at either point of the save that deletes the manifest is finished by
// the next run, uninstall or an install of the other agent.
func TestUninstall_CrashInManifestDeletingSave(t *testing.T) {
	assets := defaultFixture().dir(t)
	// Find the save points of the manifest-deleting save in a clean run.
	deleting := 0
	{
		tg := mustTarget(t, newRepo(t))
		runInstall(t, tg, assets, claudeOpts())
		o := uninstallOf(AgentClaude)
		calls := 0
		o.onSave = func(stage string) {
			calls++
			if deleting == 0 && stage == "manifest saved" && !tg.Exists(ManifestPath) {
				deleting = calls
			}
		}
		runUninstall(t, tg, o)
		if deleting < 2 {
			t.Fatalf("manifest-deleting save not found (%d)", deleting)
		}
	}

	for _, point := range []int{deleting - 1, deleting} {
		for _, next := range []string{"uninstall", "install codex"} {
			root := newRepo(t)
			tg := mustTarget(t, root)
			snap, excl := snapshot(t, root), excludeBytes(t, tg)
			runInstall(t, tg, assets, claudeOpts())
			crashAt(t, point, func(hook func(string)) {
				o := uninstallOf(AgentClaude)
				o.onSave = hook
				_, _ = Uninstall(tg, o)
			})
			if p := porcelain(t, root); p != "" {
				t.Fatalf("point %d: crash state visible %q", point, p)
			}
			// The first write dropped the patterns of deleted paths; only the
			// manifest's was left for the crash to strand.
			if got := excludeBlock(t, tg); point == deleting && !reflect.DeepEqual(got, []string{"/" + ManifestPath}) {
				t.Fatalf("block after the crash %v", got)
			}
			switch next {
			case "uninstall":
				runUninstall(t, tg, uninstallOf(AgentClaude))
			case "install codex":
				runInstall(t, tg, assets, codexOpts())
				if got := excludeBlock(t, tg); !reflect.DeepEqual(got, codexBlockPaths) {
					t.Fatalf("point %d: stale patterns after install: %v", point, got)
				}
				// Before the manifest is deleted, claude's last record is still journaled.
				runUninstall(t, tg, uninstallOf(AgentClaude, AgentCodex))
			}
			if got := excludeBytes(t, tg); got != excl {
				t.Fatalf("point %d, %s: exclude file %q, want %q", point, next, got, excl)
			}
			if !reflect.DeepEqual(snapshot(t, root), snap) || porcelain(t, root) != "" {
				t.Fatalf("point %d, %s: not restored: %v", point, next, snapshot(t, root))
			}
		}
	}

	t.Run("dry run writes nothing", func(t *testing.T) {
		root := newRepo(t)
		tg := mustTarget(t, root)
		runInstall(t, tg, assets, claudeOpts())
		crashAt(t, deleting, func(hook func(string)) {
			o := uninstallOf(AgentClaude)
			o.onSave = hook
			_, _ = Uninstall(tg, o)
		})
		excl := excludeBytes(t, tg)
		o := uninstallOf(AgentClaude)
		o.DryRun = true
		if res := runUninstall(t, tg, o); !res.Nothing {
			t.Fatalf("result %+v", res)
		}
		if excludeBytes(t, tg) != excl {
			t.Fatal("dry run changed the exclude file")
		}
	})
}

// The exclude file is developer-editable: a sibling-shaped line outside a
// skills directory never makes lens delete anything.
func TestSkillSibling_ShapedLineOutsideSkillsDir(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	const dev = "src/.notes.lens-old"
	write(t, tg.Abs(dev+"/mine"), "mine")
	if err := tg.SetExcludeBlock(append(excludeBlock(t, tg), "/"+dev+"/")); err != nil {
		t.Fatal(err)
	}
	devSnap := snapshot(t, tg.Abs(dev))
	runInstall(t, tg, assets, codexOpts())
	if !reflect.DeepEqual(snapshot(t, tg.Abs(dev)), devSnap) {
		t.Fatal("deleted by install")
	}
	if err := tg.SetExcludeBlock(append(excludeBlock(t, tg), "/"+dev+"/")); err != nil {
		t.Fatal(err)
	}
	runUninstall(t, tg, uninstallOf(AgentClaude, AgentCodex))
	if !reflect.DeepEqual(snapshot(t, tg.Abs(dev)), devSnap) {
		t.Fatal("deleted by uninstall")
	}
}

// A hand-added pattern that is not a path install writes is never recovered
// into CreatedPaths after manifest loss, and never deleted.
func TestInstall_ManifestLossIgnoresForeignPatterns(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	runInstall(t, tg, assets, claudeOpts())
	write(t, tg.Abs("src/secret.txt"), "secret")
	if err := tg.SetExcludeBlock(append(excludeBlock(t, tg), "/src/secret.txt")); err != nil {
		t.Fatal(err)
	}
	deleteManifest(t, tg)
	runInstall(t, tg, assets, claudeOpts())
	if loadManifest(t, tg).Created("src/secret.txt") {
		t.Fatal("foreign pattern recovered into CreatedPaths")
	}
	runUninstall(t, tg, uninstallOf(AgentClaude))
	if readFile(t, tg.Abs("src/secret.txt")) != "secret" {
		t.Fatal("foreign file changed")
	}
}

// Sibling patterns match only directories, so a regular file at a sibling's
// name is the developer's even when the block lists the name: never deleted,
// and the skill that needs the name is blocked.
func TestSkillSibling_RegularFileIsForeign(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	assets := defaultFixture().dir(t)
	o := claudeOpts()
	o.failAt = 3 // alpha's write is journaled: its sibling patterns are in the block
	if _, err := Install(tg, assets, o); err == nil {
		t.Fatal("no error")
	}
	const file = ".claude/skills/.alpha.lens-old"
	write(t, tg.Abs(file), "mine")
	res := runInstall(t, tg, assets, claudeOpts())
	if r := resultFor(t, res, KindSkill, "alpha"); r.Action != ActionBlocked || r.Detail != "foreign sibling "+file {
		t.Fatalf("alpha %+v", r)
	}
	if readFile(t, tg.Abs(file)) != "mine" {
		t.Fatal("file changed by install")
	}
	runUninstall(t, tg, uninstallOf(AgentClaude))
	if readFile(t, tg.Abs(file)) != "mine" {
		t.Fatal("file changed by uninstall")
	}
}

// addPermissions adds a key of the developer's own to a JSON file, as Claude
// Code does to .claude/settings.local.json on any "always allow".
func addPermissions(t *testing.T, p string) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(readFile(t, p)), &doc); err != nil {
		t.Fatal(err)
	}
	doc["permissions"] = map[string]any{"allow": []any{"Bash(ls:*)"}}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	write(t, p, string(b))
}

// A config file install created and the developer added content to is kept by
// uninstall, so only CreatedPaths keeps the manifest alive. Once the file is
// deleted, the next uninstall must finish the lifecycle.
func TestUninstall_CreatedPathsOnlyFinishesLifecycle(t *testing.T) {
	const settings = ".claude/settings.local.json"
	root := newRepo(t)
	tg := mustTarget(t, root)
	snap, excl := snapshot(t, root), excludeBytes(t, tg)
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	addPermissions(t, tg.Abs(settings))
	runUninstall(t, tg, uninstallOf(AgentClaude))

	m := loadManifest(t, tg)
	if len(m.Agents) != 0 || !reflect.DeepEqual(m.CreatedPaths, []string{settings}) {
		t.Fatalf("setup: manifest %+v", m)
	}
	// (a) The file is still there: it is reported as excluded but not managed.
	res := runUninstall(t, tg, uninstallOf(AgentClaude, AgentCodex))
	if !res.Nothing || !reflect.DeepEqual(res.Unmanaged, []string{settings}) || len(res.Recoverable) != 0 {
		t.Fatalf("file present: result %+v", res)
	}
	if !tg.Exists(ManifestPath) || !tg.Exists(settings) {
		t.Fatal("file present: manifest or file removed")
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}

	// (b) The developer deletes it: uninstall removes the manifest, .projectlens/,
	// the created directory, and the block.
	if err := os.Remove(tg.Abs(settings)); err != nil {
		t.Fatal(err)
	}
	res = runUninstall(t, tg, uninstallOf(AgentClaude, AgentCodex))
	if !res.Nothing || len(res.Unmanaged) != 0 || len(res.Recoverable) != 0 {
		t.Fatalf("file deleted: result %+v", res)
	}
	wantGone(t, tg, ManifestPath, ".projectlens", ".claude")
	if !reflect.DeepEqual(snapshot(t, root), snap) || excludeBytes(t, tg) != excl {
		t.Fatalf("not restored: %v\nexclude:\n%s", snapshot(t, root), excludeBytes(t, tg))
	}
}

// (c) With no manifest, live wiring the block still hides is reported as
// recoverable, never as "nothing installed".
func TestUninstall_NoManifestReportsRecoverablePaths(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	deleteManifest(t, tg)
	snap := snapshot(t, root)
	for _, dry := range []bool{true, false} {
		o := uninstallOf(AgentClaude, AgentCodex)
		o.DryRun = dry
		res := runUninstall(t, tg, o)
		want := []string{".claude/settings.local.json", ".claude/skills/alpha", ".claude/skills/beta", ".mcp.json"}
		if !res.Nothing || !reflect.DeepEqual(res.Recoverable, want) {
			t.Fatalf("dry=%v: result %+v", dry, res)
		}
	}
	if !reflect.DeepEqual(snapshot(t, root), snap) {
		t.Fatal("work tree changed")
	}
	if p := porcelain(t, root); p != "" {
		t.Fatalf("porcelain %q", p)
	}
	if got := RecoveryAgents([]string{".mcp.json", ".agents/skills/x", ".claude/skills/y"}); !reflect.DeepEqual(got, []Agent{AgentClaude, AgentCodex}) {
		t.Fatalf("RecoveryAgents = %v", got)
	}
}
