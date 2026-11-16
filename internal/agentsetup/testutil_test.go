package agentsetup

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
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
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}
