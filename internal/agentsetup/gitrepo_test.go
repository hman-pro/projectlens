package agentsetup

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustTarget(t *testing.T, dir string) Target {
	t.Helper()
	tg, err := OpenTarget(dir)
	if err != nil {
		t.Fatal(err)
	}
	return tg
}

func TestOpenTarget_NotARepo(t *testing.T) {
	if _, err := OpenTarget(t.TempDir()); err == nil {
		t.Fatal("want error")
	}
}

func TestOpenTarget_Subdirectory(t *testing.T) {
	root := newRepo(t)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenTarget(sub); err == nil || !strings.Contains(err.Error(), "not the root of a git work tree") {
		t.Fatal(err)
	}
	got, err := GitRoot(sub)
	if err != nil || got != root {
		t.Fatal(got, err)
	}
	tg := mustTarget(t, root)
	if tg.Root != root || tg.LinkedWorktree {
		t.Fatal(tg)
	}
}

func TestIsTracked(t *testing.T) {
	root := newRepo(t)
	write(t, filepath.Join(root, "a.txt"), "a")
	write(t, filepath.Join(root, "d", "b.txt"), "b")
	write(t, filepath.Join(root, "untracked.txt"), "u")
	git(t, root, "add", "a.txt", "d/b.txt")
	git(t, root, "commit", "-q", "-m", "files")
	tg := mustTarget(t, root)
	for rel, want := range map[string]bool{"a.txt": true, "untracked.txt": false, "d": true, "missing": false} {
		got, err := tg.IsTracked(rel)
		if err != nil || got != want {
			t.Errorf("IsTracked(%q) = %v, %v; want %v", rel, got, err, want)
		}
	}
}

func TestExcludeBlock_RoundTrip(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	exclude := filepath.Join(tg.CommonDir, "info", "exclude")
	write(t, exclude, "*.log\n")

	paths := []string{"/.mcp.json", "/.claude/skills/a/"}
	if err := tg.SetExcludeBlock(paths); err != nil {
		t.Fatal(err)
	}
	got, err := tg.ExcludeBlock()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/.claude/skills/a/", "/.mcp.json"}; !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	b, _ := os.ReadFile(exclude)
	want := "*.log\n# >>> projectlens agent wiring: " + root + " >>>\n/.claude/skills/a/\n/.mcp.json\n# <<< projectlens agent wiring: " + root + " <<<\n"
	if string(b) != want {
		t.Fatalf("exclude file:\n%s\nwant:\n%s", b, want)
	}

	write(t, filepath.Join(root, ".mcp.json"), "{}")
	if out := git(t, root, "status", "--porcelain"); strings.Contains(out, ".mcp.json") {
		t.Fatalf("status still lists .mcp.json: %s", out)
	}

	if err := tg.SetExcludeBlock(nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(exclude)
	if string(b) != "*.log\n" {
		t.Fatalf("got %q", b)
	}
	if got, err := tg.ExcludeBlock(); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}

func TestExcludeBlock_MissingFile(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	if got, err := tg.ExcludeBlock(); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	if err := os.RemoveAll(filepath.Join(tg.CommonDir, "info")); err != nil {
		t.Fatal(err)
	}
	if err := tg.SetExcludeBlock([]string{"/x"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := tg.ExcludeBlock(); !reflect.DeepEqual(got, []string{"/x"}) {
		t.Fatal(got)
	}
}

func TestLinkedWorktree(t *testing.T) {
	root := newRepo(t)
	wtDir := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-wt")
	git(t, root, "worktree", "add", "-q", wtDir)
	t.Cleanup(func() { os.RemoveAll(wtDir) })
	wtRoot, _ := filepath.EvalSymlinks(wtDir)

	wt := mustTarget(t, wtRoot)
	main := mustTarget(t, root)
	if !wt.LinkedWorktree || main.LinkedWorktree {
		t.Fatal(wt.LinkedWorktree, main.LinkedWorktree)
	}
	if wt.CommonDir != main.CommonDir || wt.CommonDir != filepath.Join(root, ".git") {
		t.Fatal(wt.CommonDir, main.CommonDir)
	}
	if err := wt.SetExcludeBlock([]string{"/.mcp.json"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(b), wtRoot) {
		t.Fatal(string(b), err)
	}
}

func TestExcludeBlock_PerWorktree(t *testing.T) {
	root := newRepo(t)
	wtDir := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-wt")
	git(t, root, "worktree", "add", "-q", wtDir)
	t.Cleanup(func() { os.RemoveAll(wtDir) })
	wtRoot, _ := filepath.EvalSymlinks(wtDir)
	wt := mustTarget(t, wtRoot)
	main := mustTarget(t, root)

	for _, tg := range []Target{main, wt} {
		if err := tg.SetExcludeBlock([]string{"/.mcp.json"}); err != nil {
			t.Fatal(err)
		}
	}
	exclude := filepath.Join(main.CommonDir, "info", "exclude")
	b, _ := os.ReadFile(exclude)
	if n := strings.Count(string(b), "# >>> projectlens agent wiring:"); n != 2 {
		t.Fatalf("want 2 blocks, got %d:\n%s", n, b)
	}

	if err := wt.SetExcludeBlock(nil); err != nil {
		t.Fatal(err)
	}
	if got, err := main.ExcludeBlock(); err != nil || !reflect.DeepEqual(got, []string{"/.mcp.json"}) {
		t.Fatal(got, err)
	}
	write(t, filepath.Join(root, ".mcp.json"), "{}")
	if out := git(t, root, "status", "--porcelain"); strings.Contains(out, ".mcp.json") {
		t.Fatalf("main .mcp.json exposed: %s", out)
	}

	// Re-add the wt block, then remove the wt directory: main's next Set prunes it.
	if err := wt.SetExcludeBlock([]string{"/.mcp.json"}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(wtRoot); err != nil {
		t.Fatal(err)
	}
	if err := main.SetExcludeBlock([]string{"/.mcp.json"}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(exclude)
	if strings.Contains(string(b), wtRoot) || strings.Count(string(b), "# >>> projectlens agent wiring:") != 1 {
		t.Fatalf("wt block not pruned:\n%s", b)
	}
}

func TestCheckContained(t *testing.T) {
	root := newRepo(t)
	tg := mustTarget(t, root)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".agents")); err != nil {
		t.Fatal(err)
	}
	err := tg.CheckContained(".agents/skills/x")
	if !errors.Is(err, ErrEscapesTarget) || !strings.Contains(err.Error(), ".agents") {
		t.Fatal(err)
	}
	if err := tg.CheckContained(".claude/skills/x"); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(root, "real"), "x")
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "leaf")); err != nil {
		t.Fatal(err)
	}
	if err := tg.CheckContained("leaf"); !errors.Is(err, ErrEscapesTarget) {
		t.Fatal(err)
	}
	if err := tg.CheckParentsContained("leaf"); err != nil {
		t.Fatal(err)
	}
	if !tg.IsSymlink("leaf") || tg.IsSymlink("real") || !tg.Exists("leaf") || tg.Exists("nope") {
		t.Fatal("IsSymlink/Exists wrong")
	}

	// A path that is not local to the root, or that reaches into .git, is
	// refused before anything is inspected.
	for _, rel := range []string{
		"", ".", "/abs", root, "..", "../x", "a/../../x", "a/../b", ".git", ".git/info/exclude", ".GIT/config",
	} {
		for name, check := range map[string]func(string) error{
			"CheckContained": tg.CheckContained, "CheckParentsContained": tg.CheckParentsContained,
		} {
			if err := check(rel); !errors.Is(err, ErrEscapesTarget) {
				t.Errorf("%s(%q) = %v, want ErrEscapesTarget", name, rel, err)
			}
		}
	}
	for _, rel := range []string{".claude/skills/x", ".mcp.json", ".gitignore", "a/.git"} {
		if err := tg.CheckContained(rel); err != nil {
			t.Errorf("CheckContained(%q) = %v", rel, err)
		}
	}
}
