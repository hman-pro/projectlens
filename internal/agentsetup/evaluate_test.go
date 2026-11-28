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

const (
	evalURL      = "http://localhost:8080/mcp"
	evalURLOther = "http://localhost:9999/mcp"
)

func findCanon(t *testing.T, cs []Canon, kind Kind, id string) Canon {
	t.Helper()
	for _, c := range cs {
		if c.Kind == kind && c.ID == id {
			return c
		}
	}
	t.Fatalf("no canonical %s %s", kind, id)
	return Canon{}
}

func findStatus(t *testing.T, sts []ItemStatus, kind Kind, id string) ItemStatus {
	t.Helper()
	for _, s := range sts {
		if s.Kind == kind && s.ID == id {
			return s
		}
	}
	t.Fatalf("no status for %s %s in %+v", kind, id, sts)
	return ItemStatus{}
}

// place writes the canonical content of c to its destination.
func place(t *testing.T, tg Target, c Canon) {
	t.Helper()
	abs := tg.Abs(c.Path)
	var err error
	switch {
	case c.Kind == KindSkill:
		if err = os.RemoveAll(abs); err == nil {
			if err = os.MkdirAll(filepath.Dir(abs), 0o755); err == nil {
				err = CopyDir(c.SkillSrc, abs)
			}
		}
	case c.Kind == KindHook:
		err = WriteHook("", abs, c.Hook)
	case c.Agent == AgentCodex:
		err = WriteCodexBlock("", abs, c.URL)
	default:
		err = WriteMCPJSON("", abs, c.URL)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// editByHand changes c's installed content the way a developer might.
func editByHand(t *testing.T, tg Target, c Canon) {
	t.Helper()
	abs := tg.Abs(c.Path)
	var err error
	switch {
	case c.Kind == KindSkill:
		err = os.WriteFile(filepath.Join(abs, "local-notes.md"), []byte("mine"), 0o644)
	case c.Kind == KindHook:
		h := c.Hook
		h.Command += " # tweaked" // marker kept
		err = WriteHook("", abs, h)
	case c.Agent == AgentCodex:
		var b []byte
		if b, err = os.ReadFile(abs); err == nil {
			s := strings.Replace(string(b), codexBlockEnd, "startup_timeout_sec = 5\n"+codexBlockEnd, 1)
			err = os.WriteFile(abs, []byte(s), 0o644)
		}
	default:
		err = WriteMCPJSON("", abs, "http://edited.example/mcp")
	}
	if err != nil {
		t.Fatal(err)
	}
}

func recordFor(c Canon, hash string) Record {
	return Record{Kind: c.Kind, ID: c.ID, Path: c.Path, Hash: hash}
}

func manifestWith(a Agent, recs ...Record) *Manifest {
	m := &Manifest{Version: 1, Agents: map[Agent]*AgentRecord{}}
	if len(recs) > 0 {
		m.Agents[a] = &AgentRecord{URL: evalURL, Items: recs}
	}
	return m
}

func evaluate(t *testing.T, tg Target, a Agent, cs []Canon, m *Manifest) []ItemStatus {
	t.Helper()
	sts, err := Evaluate(tg, a, cs, m)
	if err != nil {
		t.Fatal(err)
	}
	return sts
}

func TestEvaluate_States(t *testing.T) {
	type kindCase struct {
		name  string
		agent Agent
		kind  Kind
		id    string
	}
	kinds := []kindCase{
		{"claude mcp", AgentClaude, KindMCP, "projectlens"},
		{"codex mcp", AgentCodex, KindMCP, "projectlens"},
		{"claude skill", AgentClaude, KindSkill, "alpha"},
		{"codex skill", AgentCodex, KindSkill, "alpha"},
		{"hook", AgentClaude, KindHook, "start"},
	}
	v2 := defaultFixture()
	v2.alpha = "alpha v2"
	v2.startCmd = ": projectlens-hook=start; echo start v2"

	type row struct {
		name      string
		onDisk    bool // place v1 content
		edit      bool // then edit it by hand
		record    bool // record with v1's hash
		canonV2   bool // canonical moved on to v2
		wantState State
		wantOwned bool
	}
	rows := []row{
		{"nothing on disk", false, false, false, false, StateMissing, false},
		{"disk == canonical, no record", true, false, false, false, StateCurrent, false},
		{"disk == canonical, record", true, false, true, false, StateCurrent, true},
		{"disk == record, canonical changed", true, false, true, true, StateStale, true},
		{"disk edited, record present", true, true, true, false, StateModified, true},
		{"disk present and different, no record", true, true, false, false, StateUnmanaged, false},
	}
	for _, k := range kinds {
		for _, r := range rows {
			t.Run(k.name+"/"+r.name, func(t *testing.T) {
				tg := mustTarget(t, newRepo(t))
				c1 := defaultFixture().canon(t, k.agent, evalURL)
				cs := c1
				if r.canonV2 {
					cs = v2.canon(t, k.agent, evalURLOther)
				}
				item := findCanon(t, c1, k.kind, k.id)
				if r.onDisk {
					place(t, tg, item)
				}
				if r.edit {
					editByHand(t, tg, item)
				}
				m := manifestWith(k.agent)
				if r.record {
					m = manifestWith(k.agent, recordFor(item, item.Hash))
				}
				st := findStatus(t, evaluate(t, tg, k.agent, cs, m), k.kind, k.id)
				if st.State != r.wantState || st.Owned != r.wantOwned || st.Tracked || st.Detail != "" || st.foreign || st.symlinkAt != "" {
					t.Fatalf("got %+v, want state %s owned %v", st, r.wantState, r.wantOwned)
				}
			})
		}
	}

	t.Run("skill dir is a symlink", func(t *testing.T) {
		root := newRepo(t)
		tg := mustTarget(t, root)
		cs := defaultFixture().canon(t, AgentClaude, evalURL)
		alpha := findCanon(t, cs, KindSkill, "alpha")
		outside := filepath.Join(t.TempDir(), "alpha")
		if err := CopyDir(alpha.SkillSrc, outside); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, tg.Abs(alpha.Path)); err != nil {
			t.Fatal(err)
		}
		for _, m := range []*Manifest{manifestWith(AgentClaude), manifestWith(AgentClaude, recordFor(alpha, alpha.Hash))} {
			st := findStatus(t, evaluate(t, tg, AgentClaude, cs, m), KindSkill, "alpha")
			if st.State != StateUnmanaged || st.Detail != "symlink" {
				t.Fatalf("got %+v", st)
			}
		}
	})

	t.Run("hook recorded, marker removed from file", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		cs := defaultFixture().canon(t, AgentClaude, evalURL)
		start := findCanon(t, cs, KindHook, "start")
		place(t, tg, start)
		place(t, tg, findCanon(t, cs, KindHook, "edit"))
		b, err := os.ReadFile(tg.Abs(start.Path))
		if err != nil {
			t.Fatal(err)
		}
		stripped := strings.Replace(string(b), "projectlens-hook=start; ", "", 1)
		if stripped == string(b) {
			t.Fatal("marker not found in hooks file")
		}
		write(t, tg.Abs(start.Path), stripped)
		st := findStatus(t, evaluate(t, tg, AgentClaude, cs, manifestWith(AgentClaude, recordFor(start, start.Hash))), KindHook, "start")
		if st.State != StateRemovedLocally || !st.Owned {
			t.Fatalf("got %+v", st)
		}
	})

	t.Run("hook recorded, command edited, marker kept", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		cs := defaultFixture().canon(t, AgentClaude, evalURL)
		start := findCanon(t, cs, KindHook, "start")
		place(t, tg, start)
		editByHand(t, tg, start)
		hooks, err := ReadHooks(tg.Abs(start.Path))
		if err != nil || len(hooks) != 1 || !strings.HasSuffix(hooks["start"].Command, "# tweaked") {
			t.Fatalf("edit not in place: %+v %v", hooks, err)
		}
		st := findStatus(t, evaluate(t, tg, AgentClaude, cs, manifestWith(AgentClaude, recordFor(start, start.Hash))), KindHook, "start")
		if st.State != StateModified || !st.Owned {
			t.Fatalf("got %+v", st)
		}
	})

	t.Run("record for a skill absent from canonical", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		cs := defaultFixture().canon(t, AgentClaude, evalURL)
		gone := Record{Kind: KindSkill, ID: "gamma", Path: ".claude/skills/gamma", Hash: "h"}
		alpha := findCanon(t, cs, KindSkill, "alpha")
		sts := evaluate(t, tg, AgentClaude, cs, manifestWith(AgentClaude, recordFor(alpha, alpha.Hash), gone))
		if len(sts) != len(cs)+1 {
			t.Fatalf("want %d statuses, got %d", len(cs)+1, len(sts))
		}
		for i, c := range cs {
			if sts[i].Item != c.Item {
				t.Fatalf("status %d is %+v, want canonical order %+v", i, sts[i].Item, c.Item)
			}
		}
		last := sts[len(sts)-1]
		want := Item{Agent: AgentClaude, Kind: KindSkill, ID: "gamma", Path: ".claude/skills/gamma"}
		if last.Item != want || last.State != StateOrphaned || !last.Owned {
			t.Fatalf("orphan status %+v", last)
		}
	})
}

func TestEvaluate_OwnedIsIndependentOfState(t *testing.T) {
	for _, a := range []Agent{AgentClaude, AgentCodex} {
		tg := mustTarget(t, newRepo(t))
		cs := defaultFixture().canon(t, a, evalURL)
		var recs []Record
		for _, c := range cs {
			place(t, tg, c)
			recs = append(recs, recordFor(c, c.Hash))
		}
		for _, owned := range []bool{false, true} {
			m := manifestWith(a)
			if owned {
				m = manifestWith(a, recs...)
			}
			sts := evaluate(t, tg, a, cs, m)
			if len(sts) != len(cs) {
				t.Fatalf("%s: %d statuses for %d items", a, len(sts), len(cs))
			}
			for _, st := range sts {
				if st.State != StateCurrent || st.Owned != owned {
					t.Errorf("%s owned=%v: %+v", a, owned, st)
				}
			}
		}
	}
}

func TestEvaluate_SymlinkedAncestorBlocked(t *testing.T) {
	t.Run("codex .agents", func(t *testing.T) {
		root := newRepo(t)
		tg := mustTarget(t, root)
		if err := os.Symlink(t.TempDir(), filepath.Join(root, ".agents")); err != nil {
			t.Fatal(err)
		}
		cs := defaultFixture().canon(t, AgentCodex, evalURL)
		alpha := findCanon(t, cs, KindSkill, "alpha")
		sts := evaluate(t, tg, AgentCodex, cs, manifestWith(AgentCodex, recordFor(alpha, alpha.Hash)))
		skills := 0
		for _, st := range sts {
			if st.Kind != KindSkill {
				if st.State != StateMissing {
					t.Errorf("non-skill item affected: %+v", st)
				}
				continue
			}
			skills++
			if st.State != StateBlocked || st.Detail != "symlinked parent .agents" || st.symlinkAt != ".agents" {
				t.Errorf("got %+v", st)
			}
		}
		if skills != 2 {
			t.Fatalf("want 2 skill statuses, got %d", skills)
		}
		if st := findStatus(t, sts, KindSkill, "alpha"); !st.Owned {
			t.Errorf("Owned must still reflect the record: %+v", st)
		}
	})
	t.Run("claude .mcp.json is a symlink", func(t *testing.T) {
		root := newRepo(t)
		tg := mustTarget(t, root)
		outside := filepath.Join(t.TempDir(), "mcp.json")
		write(t, outside, "{}")
		if err := os.Symlink(outside, filepath.Join(root, ".mcp.json")); err != nil {
			t.Fatal(err)
		}
		cs := defaultFixture().canon(t, AgentClaude, evalURL)
		st := findStatus(t, evaluate(t, tg, AgentClaude, cs, manifestWith(AgentClaude)), KindMCP, "projectlens")
		if st.State != StateBlocked || st.symlinkAt != ".mcp.json" {
			t.Fatalf("got %+v", st)
		}
	})
	t.Run("claude hooks file is a symlink", func(t *testing.T) {
		root := newRepo(t)
		tg := mustTarget(t, root)
		outside := filepath.Join(t.TempDir(), "settings.json")
		write(t, outside, "{not json") // never read through the link
		if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, ".claude", "settings.local.json")); err != nil {
			t.Fatal(err)
		}
		cs := defaultFixture().canon(t, AgentClaude, evalURL)
		hooks := 0
		for _, st := range evaluate(t, tg, AgentClaude, cs, manifestWith(AgentClaude)) {
			if st.Kind != KindHook {
				continue
			}
			hooks++
			if st.State != StateBlocked || st.Detail != "symlinked file .claude/settings.local.json" {
				t.Errorf("got %+v", st)
			}
		}
		if hooks != 2 {
			t.Fatalf("want 2 hook statuses, got %d", hooks)
		}
	})
}

func TestEvaluate_Pending(t *testing.T) {
	// A version is the canonical content at one point in time: assets plus MCP URL.
	type version struct {
		f   evalFixture
		url string
	}
	oldV := version{defaultFixture(), evalURL}
	newV := version{defaultFixture(), evalURLOther}
	newV.f.alpha = "alpha v2"
	newV.f.startCmd = ": projectlens-hook=start; echo start v2"
	newerV := version{defaultFixture(), "http://localhost:7777/mcp"}
	newerV.f.alpha = "alpha v3"

	pWrite := &Pending{Op: "write", Hash: "new"}
	pRemove := &Pending{Op: "remove"}
	type row struct {
		name    string
		agent   Agent
		kind    Kind
		id      string
		disk    *version // nil: nothing on disk
		canon   version
		hash    string // "old", "new", or ""
		pending *Pending
		want    State
	}
	tests := []row{
		{"write pending, disk old, canonical new", AgentClaude, KindSkill, "alpha", &oldV, newV, "old", pWrite, StateStale},
		{"write pending, disk new", AgentClaude, KindSkill, "alpha", &newV, newV, "old", pWrite, StateCurrent},
		{"write pending, disk new, canonical moved on", AgentClaude, KindSkill, "alpha", &newV, newerV, "old", pWrite, StateStale},
		{"first write never happened", AgentClaude, KindHook, "start", nil, newV, "", pWrite, StateMissing},
		{"update write interrupted, hook then removed", AgentClaude, KindHook, "start", nil, newV, "old", pWrite, StateRemovedLocally},
		{"hook absent from the file", AgentClaude, KindHook, "start", nil, oldV, "old", nil, StateRemovedLocally},
		{"remove pending, disk old", AgentClaude, KindSkill, "alpha", &oldV, oldV, "old", pRemove, StateCurrent},
		{"remove pending, disk old, canonical new", AgentClaude, KindSkill, "alpha", &oldV, newV, "old", pRemove, StateStale},
		{"hook write pending, disk old, canonical new", AgentClaude, KindHook, "start", &oldV, newV, "old", pWrite, StateStale},
		// A remove that finished before the journal was cleared is not a local edit.
		{"remove pending, hook absent", AgentClaude, KindHook, "start", nil, oldV, "old", pRemove, StateMissing},
		{"remove pending, skill dir absent", AgentClaude, KindSkill, "alpha", nil, oldV, "old", pRemove, StateMissing},
	}
	for _, a := range []Agent{AgentClaude, AgentCodex} {
		tests = append(tests, []row{
			{string(a) + " mcp write pending, disk old", a, KindMCP, "projectlens", &oldV, newV, "old", pWrite, StateStale},
			{string(a) + " mcp write pending, disk new", a, KindMCP, "projectlens", &newV, newV, "old", pWrite, StateCurrent},
			{string(a) + " mcp first write never happened", a, KindMCP, "projectlens", nil, newV, "", pWrite, StateMissing},
		}...)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := mustTarget(t, newRepo(t))
			canonOf := func(v version) []Canon { return v.f.canon(t, tc.agent, v.url) }
			hashes := map[string]string{
				"old": findCanon(t, canonOf(oldV), tc.kind, tc.id).Hash,
				"new": findCanon(t, canonOf(newV), tc.kind, tc.id).Hash,
				"":    "",
			}
			cs := canonOf(tc.canon)
			if tc.disk != nil {
				place(t, tg, findCanon(t, canonOf(*tc.disk), tc.kind, tc.id))
			} else if tc.kind == KindHook {
				// The hooks file exists and holds the other hook only.
				place(t, tg, findCanon(t, cs, KindHook, "edit"))
			}
			rec := Record{Kind: tc.kind, ID: tc.id, Path: findCanon(t, cs, tc.kind, tc.id).Path, Hash: hashes[tc.hash]}
			if tc.pending != nil {
				p := *tc.pending
				p.Hash = hashes[p.Hash]
				rec.Pending = &p
			}
			m := manifestWith(tc.agent, rec)
			before, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}

			st := findStatus(t, evaluate(t, tg, tc.agent, cs, m), tc.kind, tc.id)
			if st.State != tc.want || !st.Owned {
				t.Fatalf("got %+v, want %s", st, tc.want)
			}
			after, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatalf("Evaluate changed the manifest:\n%s\n%s", before, after)
			}
			if tg.Exists(".projectlens") {
				t.Fatal("Evaluate wrote the manifest")
			}
		})
	}
}

func TestEvaluate_HookPlacementChanges(t *testing.T) {
	cs := defaultFixture().canon(t, AgentClaude, evalURL)
	edit := findCanon(t, cs, KindHook, "edit")
	rec := manifestWith(AgentClaude, recordFor(edit, edit.Hash))

	check := func(t *testing.T, tg Target, cs []Canon, m *Manifest, want State, detail string) {
		t.Helper()
		st := findStatus(t, evaluate(t, tg, AgentClaude, cs, m), KindHook, "edit")
		if st.State != want || st.Detail != detail {
			t.Fatalf("got %+v, want %s %q", st, want, detail)
		}
	}

	t.Run("moved to another event", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		h := edit.Hook
		h.Event = "PostToolUse"
		if err := WriteHook("", tg.Abs(edit.Path), h); err != nil {
			t.Fatal(err)
		}
		check(t, tg, cs, rec, StateModified, "")
	})
	t.Run("matcher group changed", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		h := edit.Hook
		h.Matcher = "Write"
		if err := WriteHook("", tg.Abs(edit.Path), h); err != nil {
			t.Fatal(err)
		}
		check(t, tg, cs, rec, StateModified, "")
	})
	t.Run("canonical matcher changed", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		place(t, tg, edit)
		f := defaultFixture()
		f.editMatcher = "Edit|Write"
		check(t, tg, f.canon(t, AgentClaude, evalURL), rec, StateStale, "")
	})
	t.Run("same ID present twice", func(t *testing.T) {
		tg := mustTarget(t, newRepo(t))
		entry := map[string]string{"type": edit.Hook.Type, "command": edit.Hook.Command}
		doc := map[string]any{"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{"matcher": edit.Hook.Matcher, "hooks": []any{entry}}},
			"Stop":       []any{map[string]any{"matcher": "", "hooks": []any{entry}}},
		}}
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		write(t, tg.Abs(edit.Path), string(b))
		check(t, tg, cs, rec, StateModified, "duplicate entries")
		check(t, tg, cs, manifestWith(AgentClaude), StateModified, "duplicate entries")
		// A forced dedupe that was journaled but not done is stale, not modified.
		disk, err := installedHash(tg, edit.Item)
		if err != nil {
			t.Fatal(err)
		}
		journaled := recordFor(edit, disk.hash)
		journaled.Pending = &Pending{Op: "write", Hash: edit.Hash}
		check(t, tg, cs, manifestWith(AgentClaude, journaled), StateStale, "")
		journaled.Hash = "other"
		check(t, tg, cs, manifestWith(AgentClaude, journaled), StateModified, "duplicate entries")
	})
}

func TestEvaluate_CodexForeign(t *testing.T) {
	cs := defaultFixture().canon(t, AgentCodex, evalURL)
	mcp := findCanon(t, cs, KindMCP, "projectlens")
	setup := func(t *testing.T, content string) Target {
		t.Helper()
		tg := mustTarget(t, newRepo(t))
		write(t, tg.Abs(mcp.Path), content)
		return tg
	}

	tg := setup(t, "[mcp_servers.\"projectlens\"]\nurl = \""+evalURL+"\"\n")
	st := findStatus(t, evaluate(t, tg, AgentCodex, cs, manifestWith(AgentCodex)), KindMCP, "projectlens")
	if st.State != StateCurrent || st.Owned || !st.foreign {
		t.Fatalf("canonical URL: %+v", st)
	}

	tg = setup(t, "[mcp_servers.projectlens]\nurl = \""+evalURLOther+"\"\n")
	for _, m := range []*Manifest{manifestWith(AgentCodex), manifestWith(AgentCodex, recordFor(mcp, mcp.Hash))} {
		st = findStatus(t, evaluate(t, tg, AgentCodex, cs, m), KindMCP, "projectlens")
		if st.State != StateBlocked || st.Detail != "defined outside the managed block" || !st.foreign {
			t.Fatalf("other URL: %+v", st)
		}
	}

	// A managed block is not a foreign definition.
	tg = setup(t, codexBlock(evalURL))
	st = findStatus(t, evaluate(t, tg, AgentCodex, cs, manifestWith(AgentCodex, recordFor(mcp, mcp.Hash))), KindMCP, "projectlens")
	if st.State != StateCurrent || st.foreign {
		t.Fatalf("managed block: %+v", st)
	}

	// A foreign definition next to the managed block makes the file invalid for Codex.
	tg = setup(t, "[mcp_servers.projectlens]\nurl = \""+evalURL+"\"\n\n"+codexBlock(evalURL))
	if _, err := Evaluate(tg, AgentCodex, cs, manifestWith(AgentCodex)); !errors.Is(err, ErrInvalidTOML) {
		t.Fatalf("foreign definition plus block: want ErrInvalidTOML, got %v", err)
	}
}

func TestEvaluate_ParseErrorIsReturned(t *testing.T) {
	tg := mustTarget(t, newRepo(t))
	write(t, tg.Abs(".claude/settings.local.json"), "{bad")
	cs := defaultFixture().canon(t, AgentClaude, evalURL)
	if _, err := Evaluate(tg, AgentClaude, cs, manifestWith(AgentClaude)); !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("want ErrInvalidJSON, got %v", err)
	}
}

func TestEvaluate_TrackedPaths(t *testing.T) {
	commit := func(t *testing.T, root string, paths ...string) {
		t.Helper()
		git(t, root, append([]string{"add", "-f", "--"}, paths...)...)
		git(t, root, "commit", "-q", "-m", "tracked")
	}
	cs := defaultFixture().canon(t, AgentClaude, evalURL)
	mcp := findCanon(t, cs, KindMCP, "projectlens")

	tests := []struct {
		name  string
		url   string // "" writes a file without the entry
		m     *Manifest
		want  State
		owned bool
	}{
		{"exact expected entry", evalURL, manifestWith(AgentClaude), StateCurrent, false},
		{"without the entry", "", manifestWith(AgentClaude), StateManual, false},
		{"different URL", evalURLOther, manifestWith(AgentClaude), StateManual, false},
		{"different URL, recorded with that content", evalURLOther, manifestWith(AgentClaude, recordFor(mcp, MCPHash(evalURLOther))), StateManual, true},
	}
	for _, tc := range tests {
		t.Run(".mcp.json "+tc.name, func(t *testing.T) {
			root := newRepo(t)
			tg := mustTarget(t, root)
			if tc.url == "" {
				write(t, tg.Abs(mcp.Path), "{\"mcpServers\": {}}\n")
			} else if err := WriteMCPJSON("", tg.Abs(mcp.Path), tc.url); err != nil {
				t.Fatal(err)
			}
			commit(t, root, mcp.Path)
			st := findStatus(t, evaluate(t, tg, AgentClaude, cs, tc.m), KindMCP, "projectlens")
			if st.State != tc.want || !st.Tracked || st.Owned != tc.owned {
				t.Fatalf("got %+v, want %s", st, tc.want)
			}
		})
	}

	t.Run("tracked file under a skill dir, content equal", func(t *testing.T) {
		root := newRepo(t)
		tg := mustTarget(t, root)
		alpha := findCanon(t, cs, KindSkill, "alpha")
		place(t, tg, alpha)
		commit(t, root, alpha.Path+"/SKILL.md")
		st := findStatus(t, evaluate(t, tg, AgentClaude, cs, manifestWith(AgentClaude)), KindSkill, "alpha")
		if st.State != StateCurrent || !st.Tracked {
			t.Fatalf("got %+v", st)
		}
		if beta := findStatus(t, evaluate(t, tg, AgentClaude, cs, manifestWith(AgentClaude)), KindSkill, "beta"); beta.Tracked {
			t.Fatalf("untracked skill reported tracked: %+v", beta)
		}
	})
}

func TestEvaluate_TrackedCodexForeign(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	cs := defaultFixture().canon(t, AgentCodex, evalURL)
	mcp := findCanon(t, cs, KindMCP, "projectlens")
	write(t, tg.Abs(mcp.Path), "[mcp_servers.projectlens]\nurl = \""+evalURL+"\"\n")
	git(t, root, "add", "-f", "--", mcp.Path)
	git(t, root, "commit", "-q", "-m", "tracked")
	st := findStatus(t, evaluate(t, tg, AgentCodex, cs, manifestWith(AgentCodex)), KindMCP, "projectlens")
	if st.State != StateCurrent || !st.Tracked || !st.foreign {
		t.Fatalf("got %+v", st)
	}
}

// Guard against the fixture drifting: the test rows above assume these shapes.
func TestEvalFixture_Shape(t *testing.T) {
	cs := defaultFixture().canon(t, AgentClaude, evalURL)
	var got []string
	for _, c := range cs {
		got = append(got, string(c.Kind)+":"+c.ID)
	}
	want := []string{"mcp:projectlens", "skill:alpha", "skill:beta", "hook:edit", "hook:start"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fixture canon = %v, want %v", got, want)
	}
}

func TestEvaluate_SymlinkInsideSkill(t *testing.T) {
	const detail = "contains a symlink"
	setup := func(t *testing.T) (string, Target, []Canon, Canon) {
		t.Helper()
		root := newRepo(t)
		tg := mustTarget(t, root)
		cs := defaultFixture().canon(t, AgentClaude, evalURL)
		for _, c := range cs {
			place(t, tg, c)
		}
		alpha := findCanon(t, cs, KindSkill, "alpha")
		if err := os.Symlink("SKILL.md", filepath.Join(tg.Abs(alpha.Path), "link")); err != nil {
			t.Fatal(err)
		}
		return root, tg, cs, alpha
	}
	check := func(t *testing.T, tg Target, cs []Canon, m *Manifest, want State, tracked bool) {
		t.Helper()
		sts, err := Evaluate(tg, AgentClaude, cs, m)
		if err != nil {
			t.Fatalf("Evaluate must not fail on a symlink inside a skill: %v", err)
		}
		for _, st := range sts {
			if st.Kind == KindSkill && st.ID == "alpha" {
				if st.State != want || st.Detail != detail || st.Tracked != tracked {
					t.Errorf("alpha: got %+v, want %s %q", st, want, detail)
				}
			} else if st.State != StateCurrent {
				t.Errorf("other item affected: %+v", st)
			}
		}
	}

	t.Run("no record", func(t *testing.T) {
		_, tg, cs, _ := setup(t)
		check(t, tg, cs, manifestWith(AgentClaude), StateUnmanaged, false)
	})
	t.Run("record", func(t *testing.T) {
		_, tg, cs, alpha := setup(t)
		check(t, tg, cs, manifestWith(AgentClaude, recordFor(alpha, alpha.Hash)), StateModified, false)
	})
	t.Run("tracked", func(t *testing.T) {
		root, tg, cs, alpha := setup(t)
		git(t, root, "add", "-f", "--", alpha.Path+"/SKILL.md")
		git(t, root, "commit", "-q", "-m", "tracked")
		check(t, tg, cs, manifestWith(AgentClaude), StateManual, true)
	})
}
