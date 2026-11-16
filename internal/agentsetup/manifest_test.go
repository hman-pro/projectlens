package agentsetup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestManifest_LoadMissingIsEmpty(t *testing.T) {
	tg := mustTarget(t, newRepo(t))
	m, err := LoadManifest(tg)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 || len(m.Agents) != 0 || len(m.CreatedPaths) != 0 || len(m.CreatedDirs) != 0 {
		t.Fatalf("not empty: %+v", m)
	}
	if got := m.InstalledAgents(); len(got) != 0 {
		t.Fatalf("InstalledAgents = %v", got)
	}
}

func TestManifest_RoundTripAndCleanup(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	write(t, filepath.Join(root, ".mcp.json"), "{}\n")
	if err := os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{
		Version: 1,
		Agents: map[Agent]*AgentRecord{
			AgentCodex: {URL: "http://x/mcp", Items: []Record{{Kind: KindMCP, ID: "projectlens", Path: ".codex/config.toml", Hash: "c1"}}},
			AgentClaude: {URL: "http://x/mcp", Items: []Record{
				{Kind: KindMCP, ID: "projectlens", Path: ".mcp.json", Hash: "h1"},
				{Kind: KindHook, ID: "start", Path: ".claude/settings.local.json", Hash: "", Pending: &Pending{Op: "write", Hash: "h2"}},
			}},
		},
		CreatedPaths: []string{".mcp.json"},
		CreatedDirs:  []string{".claude/skills", ".claude"},
	}
	if err := m.Save(tg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadManifest(tg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, m) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, m)
	}

	// The JSON keys are part of the on-disk contract.
	raw, err := os.ReadFile(tg.Abs(ManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "agents", "created_paths", "created_dirs"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("manifest JSON lacks %q: %s", k, raw)
		}
	}
	item := doc["agents"].(map[string]any)["claude"].(map[string]any)["items"].([]any)[1].(map[string]any)
	for _, k := range []string{"kind", "id", "path", "hash", "pending"} {
		if _, ok := item[k]; !ok {
			t.Errorf("record JSON lacks %q: %v", k, item)
		}
	}
	if _, ok := doc["agents"].(map[string]any)["claude"].(map[string]any)["url"]; !ok {
		t.Errorf("agent record lacks url")
	}

	if r, ok := got.Record(AgentClaude, KindHook, "start"); !ok || r.Pending == nil || r.Pending.Hash != "h2" {
		t.Errorf("Record lookup: %+v %v", r, ok)
	}
	if _, ok := got.Record(AgentClaude, KindSkill, "start"); ok {
		t.Error("Record must match kind")
	}
	if _, ok := got.Record(AgentCodex, KindHook, "start"); ok {
		t.Error("Record must match agent")
	}
	if a := got.InstalledAgents(); !reflect.DeepEqual(a, []Agent{AgentClaude, AgentCodex}) {
		t.Errorf("InstalledAgents = %v", a)
	}
	if !got.Created(".mcp.json") || got.Created(".claude/skills") {
		t.Error("Created wrong")
	}

	empty := &Manifest{Version: 1, Agents: map[Agent]*AgentRecord{}}
	if err := empty.Save(tg); err != nil {
		t.Fatal(err)
	}
	if tg.Exists(ManifestPath) || tg.Exists(".projectlens") {
		t.Fatal("Save with no agents and no created paths must remove the manifest and .projectlens/")
	}
}

func TestManifest_SaveKeepsNonEmptyManifestDir(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	write(t, filepath.Join(root, ".projectlens", "other"), "keep")
	write(t, tg.Abs(ManifestPath), `{"version":1,"agents":{},"created_paths":[],"created_dirs":[]}`)
	m := &Manifest{Version: 1, Agents: map[Agent]*AgentRecord{}}
	if err := m.Save(tg); err != nil {
		t.Fatal(err)
	}
	if tg.Exists(ManifestPath) || !tg.Exists(".projectlens/other") {
		t.Fatal("manifest must go, unrelated files in .projectlens/ must stay")
	}
}

func TestManifest_SavePrunesAndRetains(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	write(t, filepath.Join(root, ".mcp.json"), "{}\n")

	m := &Manifest{Version: 1, Agents: map[Agent]*AgentRecord{}, CreatedPaths: []string{".mcp.json", ".gone"}}
	if err := m.Save(tg); err != nil {
		t.Fatal(err)
	}
	if !tg.Exists(ManifestPath) {
		t.Fatal("manifest must be kept while CreatedPaths has an entry")
	}
	if !reflect.DeepEqual(m.CreatedPaths, []string{".mcp.json"}) {
		t.Fatalf("CreatedPaths = %v", m.CreatedPaths)
	}
	got, err := LoadManifest(tg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.CreatedPaths, []string{".mcp.json"}) {
		t.Fatalf("saved CreatedPaths = %v", got.CreatedPaths)
	}

	if err := os.Remove(filepath.Join(root, ".mcp.json")); err != nil {
		t.Fatal(err)
	}
	if err := got.Save(tg); err != nil {
		t.Fatal(err)
	}
	if tg.Exists(ManifestPath) || tg.Exists(".projectlens") {
		t.Fatal("manifest and .projectlens/ must be removed once nothing is left")
	}
}

func TestManifest_SaveRefusesUnwritableManifest(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".projectlens")); err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Version: 1, Agents: map[Agent]*AgentRecord{AgentClaude: {URL: "u"}}}
	if err := m.Save(tg); !errors.Is(err, ErrEscapesTarget) {
		t.Fatalf("Save through symlinked .projectlens: %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote through the symlink: %v", entries)
	}
}

func TestCheckManifestWritable(t *testing.T) {
	t.Run("tracked", func(t *testing.T) {
		root := newRepo(t)
		write(t, filepath.Join(root, ".projectlens", "agent-manifest.json"), "{}\n")
		git(t, root, "add", "-f", "--", ManifestPath)
		git(t, root, "commit", "-q", "-m", "manifest")
		if err := CheckManifestWritable(mustTarget(t, root)); !errors.Is(err, ErrManifestTracked) {
			t.Fatalf("want ErrManifestTracked, got %v", err)
		}
	})
	t.Run("symlinked dir", func(t *testing.T) {
		root := newRepo(t)
		if err := os.Symlink(t.TempDir(), filepath.Join(root, ".projectlens")); err != nil {
			t.Fatal(err)
		}
		if err := CheckManifestWritable(mustTarget(t, root)); !errors.Is(err, ErrEscapesTarget) {
			t.Fatalf("want ErrEscapesTarget, got %v", err)
		}
	})
	t.Run("symlinked file", func(t *testing.T) {
		root := newRepo(t)
		outside := filepath.Join(t.TempDir(), "m.json")
		write(t, outside, "{}")
		if err := os.MkdirAll(filepath.Join(root, ".projectlens"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, ".projectlens", "agent-manifest.json")); err != nil {
			t.Fatal(err)
		}
		if err := CheckManifestWritable(mustTarget(t, root)); !errors.Is(err, ErrEscapesTarget) {
			t.Fatalf("want ErrEscapesTarget, got %v", err)
		}
	})
	t.Run("nothing on disk", func(t *testing.T) {
		if err := CheckManifestWritable(mustTarget(t, newRepo(t))); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRecoverCreated(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	if err := tg.SetExcludeBlock([]string{"/.mcp.json", "/.claude/skills/a/", "/.gone", "/.projectlens/agent-manifest.json"}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".mcp.json"), "{}\n")
	write(t, filepath.Join(root, ".claude", "skills", "a", "SKILL.md"), "a")
	write(t, tg.Abs(ManifestPath), "{}") // present, but never recovered as a created path

	got, err := RecoverCreated(tg)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".claude/skills/a", ".mcp.json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RecoverCreated = %v, want %v", got, want)
	}
}

func TestManifest_NilSafeLookups(t *testing.T) {
	var m *Manifest
	if _, ok := m.Record(AgentClaude, KindMCP, "projectlens"); ok {
		t.Error("Record on nil manifest")
	}
	if a := m.InstalledAgents(); len(a) != 0 {
		t.Errorf("InstalledAgents on nil manifest = %v", a)
	}
	if m.Created(".mcp.json") {
		t.Error("Created on nil manifest")
	}
}
