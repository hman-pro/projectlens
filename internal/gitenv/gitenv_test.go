package gitenv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScrub(t *testing.T) {
	in := []string{
		"HOME=/h", "GIT_DIR=/a/.git", "GIT_WORK_TREE=/a", "GIT_INDEX_FILE=/a/i", "GIT_COMMON_DIR=/a",
		"GIT_OBJECT_DIRECTORY=/o", "GIT_ALTERNATE_OBJECT_DIRECTORIES=/o2", "GIT_CONFIG=/c",
		"GIT_CONFIG_PARAMETERS='x.y'='z'", "GIT_CONFIG_COUNT=1", "GIT_NAMESPACE=n", "GIT_PREFIX=p/",
		"GIT_IMPLICIT_WORK_TREE=0", "GIT_GRAFT_FILE=/g", "GIT_NO_REPLACE_OBJECTS=1", "GIT_REPLACE_REF_BASE=r",
		"GIT_SHALLOW_FILE=/s", "GIT_INTERNAL_SUPER_PREFIX=s/", "GIT_AUTHOR_NAME=A", "GIT_DIRX=keep", "PATH=/bin",
	}
	want := []string{"HOME=/h", "GIT_AUTHOR_NAME=A", "GIT_DIRX=keep", "PATH=/bin"}
	if got := Scrub(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("Scrub = %v, want %v", got, want)
	}
}

// An inherited GIT_DIR pointing at repository A must not change which
// repository `Command(dir=B)` reads.
func TestCommandIgnoresInheritedGitDir(t *testing.T) {
	a, b := newRepo(t), newRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(a, ".git"))

	out, err := Command(context.Background(), b, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	if got, want := strings.TrimSpace(string(out)), head(t, b); got != want {
		t.Fatalf("HEAD = %s, want %s (repo B)", got, want)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", dir},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(Scrub(os.Environ()), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func head(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	cmd.Env = Scrub(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", dir, err)
	}
	return strings.TrimSpace(string(out))
}
