package agentsetup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookID(t *testing.T) {
	id, ok := HookID(": projectlens-hook=pre-edit-impact; echo hi")
	if !ok || id != "pre-edit-impact" {
		t.Fatal(id, ok)
	}
	if _, ok := HookID("echo hi"); ok {
		t.Fatal("no marker")
	}
	if _, ok := HookID(": projectlens-hook=Bad_ID; echo"); ok {
		t.Fatal("bad id")
	}
}

func TestHookHash_CoversPlacement(t *testing.T) {
	base := Hook{ID: "x", Event: "Stop", Matcher: "", Type: "command", Command: ": projectlens-hook=x; echo"}
	variants := map[string]Hook{
		"event":   {ID: "x", Event: "PreToolUse", Matcher: "", Type: "command", Command: base.Command},
		"matcher": {ID: "x", Event: "Stop", Matcher: "Edit", Type: "command", Command: base.Command},
		"type":    {ID: "x", Event: "Stop", Matcher: "", Type: "other", Command: base.Command},
		"command": {ID: "x", Event: "Stop", Matcher: "", Type: "command", Command: ": projectlens-hook=x; echo hi"},
	}
	for name, v := range variants {
		if v.Hash() == base.Hash() {
			t.Errorf("changing %s did not change Hash()", name)
		}
	}
}

func writeTree(t *testing.T, root string) {
	t.Helper()
	for p, c := range map[string]string{"SKILL.md": "skill", "a/b/run.sh": "#!/bin/sh\n"} {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHashDir_NestedAndStable(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	writeTree(t, d1)
	writeTree(t, d2)
	h1, err := HashDir(d1)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashDir(d2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatal("identical trees must hash equal")
	}

	if err := os.WriteFile(filepath.Join(d2, "a", "b", "run.sh"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	h3, _ := HashDir(d2)
	if h3 == h1 {
		t.Fatal("nested content change must change hash")
	}

	d3 := t.TempDir()
	writeTree(t, d3)
	if err := os.Rename(filepath.Join(d3, "SKILL.md"), filepath.Join(d3, "OTHER.md")); err != nil {
		t.Fatal(err)
	}
	h4, _ := HashDir(d3)
	if h4 == h1 {
		t.Fatal("rename must change hash")
	}

	d4 := t.TempDir()
	writeTree(t, d4)
	if err := os.Symlink("SKILL.md", filepath.Join(d4, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := HashDir(d4); err == nil {
		t.Fatal("symlink must be an error")
	}
}

func TestCopyDir_PreservesModeAndNesting(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src)
	if err := os.Chmod(filepath.Join(src, "a", "b", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := CopyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{"a/b/run.sh": 0o755, "SKILL.md": 0o644} {
		fi, err := os.Stat(filepath.Join(dst, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s mode = %v, want %v", p, fi.Mode().Perm(), want)
		}
	}
	hs, _ := HashDir(src)
	hd, err := HashDir(dst)
	if err != nil {
		t.Fatal(err)
	}
	if hs != hd {
		t.Fatal("hash mismatch after copy")
	}
	if err := CopyDir(src, dst); err == nil {
		t.Fatal("CopyDir must fail when dst exists")
	}
}

func TestLoadCanonical_RealAssets(t *testing.T) {
	assets := filepath.Join("..", "..", "agent")
	const url = "http://localhost:8080/mcp"

	cs, err := LoadCanonical(assets, AgentClaude, url)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		kind Kind
		id   string
		path string
	}
	want := []row{
		{KindMCP, "projectlens", ".mcp.json"},
		{KindSkill, "capture-knowledge", ".claude/skills/capture-knowledge"},
		{KindSkill, "use-projectlens", ".claude/skills/use-projectlens"},
		{KindHook, "session-index-status", ".claude/settings.local.json"},
		{KindHook, "pre-edit-impact", ".claude/settings.local.json"},
		{KindHook, "stop-capture-knowledge", ".claude/settings.local.json"},
		{KindHook, "stop-compliance-audit", ".claude/settings.local.json"},
	}
	if len(cs) != len(want) {
		t.Fatalf("got %d items, want %d: %+v", len(cs), len(want), cs)
	}
	for i, w := range want {
		c := cs[i]
		if c.Agent != AgentClaude || c.Kind != w.kind || c.ID != w.id || c.Path != w.path {
			t.Errorf("item %d = %+v, want %+v", i, c.Item, w)
		}
		if c.Hash == "" {
			t.Errorf("item %d has empty hash", i)
		}
	}
	if cs[0].Hash != MCPHash(url) || cs[0].URL != url {
		t.Errorf("mcp hash/url wrong: %+v", cs[0])
	}
	if cs[3].Hook.Hash() != cs[3].Hash || cs[3].Hook.Event != "SessionStart" || cs[4].Hook.Matcher != "Edit|Write|MultiEdit" {
		t.Errorf("hook fields wrong: %+v %+v", cs[3], cs[4])
	}
	if !filepath.IsAbs(cs[1].SkillSrc) {
		t.Errorf("SkillSrc not absolute: %s", cs[1].SkillSrc)
	}

	cx, err := LoadCanonical(assets, AgentCodex, url)
	if err != nil {
		t.Fatal(err)
	}
	wantX := []row{
		{KindMCP, "projectlens", ".codex/config.toml"},
		{KindSkill, "capture-knowledge", ".agents/skills/capture-knowledge"},
		{KindSkill, "use-projectlens", ".agents/skills/use-projectlens"},
	}
	if len(cx) != len(wantX) {
		t.Fatalf("codex got %d items: %+v", len(cx), cx)
	}
	for i, w := range wantX {
		if cx[i].Kind != w.kind || cx[i].ID != w.id || cx[i].Path != w.path {
			t.Errorf("codex item %d = %+v, want %+v", i, cx[i].Item, w)
		}
	}
}

func fixtureAssets(t *testing.T, snippet string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "s1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "s1", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "claude", "settings-snippet.json"), []byte(snippet), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadCanonical_HookWithoutID(t *testing.T) {
	root := fixtureAssets(t, `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"echo hi"}]}]}}`)
	if _, err := LoadCanonical(root, AgentClaude, "u"); err == nil {
		t.Fatal("expected error for hook without ID marker")
	}
}

func TestLoadCanonical_DuplicateHookID(t *testing.T) {
	root := fixtureAssets(t, `{"hooks":{"Stop":[{"matcher":"","hooks":[
		{"type":"command","command":": projectlens-hook=dup; echo a"},
		{"type":"command","command":": projectlens-hook=dup; echo b"}]}]}}`)
	if _, err := LoadCanonical(root, AgentClaude, "u"); err == nil {
		t.Fatal("expected error for duplicate hook ID")
	}
}

func TestHashDir_SymlinkSentinel(t *testing.T) {
	d := t.TempDir()
	writeTree(t, d)
	if err := os.Symlink("SKILL.md", filepath.Join(d, "a", "link")); err != nil {
		t.Fatal(err)
	}
	_, err := HashDir(d)
	if !errors.Is(err, ErrSymlinkInSkill) || !strings.Contains(err.Error(), "symlink not allowed") {
		t.Fatalf("want ErrSymlinkInSkill with the original text, got %v", err)
	}
}

func TestLoadCanonical_SymlinkInSkillSourceErrors(t *testing.T) {
	root := fixtureAssets(t, `{"hooks":{}}`)
	if err := os.Symlink("SKILL.md", filepath.Join(root, "skills", "s1", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCanonical(root, AgentClaude, "u"); !errors.Is(err, ErrSymlinkInSkill) {
		t.Fatalf("want ErrSymlinkInSkill, got %v", err)
	}
}
