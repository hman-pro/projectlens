package agentsetup

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// testGitEnv is the environment for test git commands: no repository-local
// variables inherited from the caller (see ScrubGitEnv), and no global or system
// git config, so the developer's settings never leak into a test.
func testGitEnv() []string {
	return append(ScrubGitEnv(os.Environ()), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = testGitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// newRepo creates a git repository with one empty commit and returns its
// symlink-resolved root.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "user.email", "test@example.com")
	// Keep the developer's global ignore file from hiding test files.
	git(t, dir, "config", "core.excludesFile", os.DevNull)
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// evalFixture describes a tiny asset dir: skills alpha and beta, and a snippet
// with two hooks, "start" (SessionStart) and "edit" (PreToolUse).
// An empty beta leaves the beta skill out.
type evalFixture struct {
	alpha       string // content of skills/alpha/SKILL.md
	beta        string // content of skills/beta/SKILL.md; "" omits the skill
	startCmd    string // command of the "start" hook
	editMatcher string // matcher of the "edit" hook's group
}

func defaultFixture() evalFixture {
	return evalFixture{alpha: "alpha v1", beta: "beta v1", startCmd: ": projectlens-hook=start; echo start", editMatcher: "Edit"}
}

func (f evalFixture) dir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "skills", "alpha", "SKILL.md"), f.alpha)
	if f.beta != "" {
		write(t, filepath.Join(root, "skills", "beta", "SKILL.md"), f.beta)
	}
	snippet := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"matcher": "", "hooks": []any{
			map[string]any{"type": "command", "command": f.startCmd}}}},
		"PreToolUse": []any{map[string]any{"matcher": f.editMatcher, "hooks": []any{
			map[string]any{"type": "command", "command": ": projectlens-hook=edit; echo edit"}}}},
	}}
	b, err := json.Marshal(snippet)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "claude", "settings-snippet.json"), string(b))
	return root
}

func (f evalFixture) canon(t *testing.T, a Agent, url string) []Canon {
	t.Helper()
	cs, err := LoadCanonical(f.dir(t), a, url)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}
