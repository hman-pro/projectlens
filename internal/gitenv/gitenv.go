// Package gitenv runs git against an explicit repository directory without
// inheriting the caller's repository-local git variables.
//
// A git hook exports GIT_DIR (and friends) to every child process. `git -C dir`
// does not override an inherited GIT_DIR, so without scrubbing, lens run from a
// hook in repository A would read history and HEAD from A while indexing B.
package gitenv

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// isLocalVar reports whether name is one of the repository-local variables git
// lists with `git rev-parse --local-env-vars`.
func isLocalVar(name string) bool {
	switch name {
	case "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_CONFIG", "GIT_CONFIG_COUNT",
		"GIT_CONFIG_PARAMETERS", "GIT_DIR", "GIT_GRAFT_FILE", "GIT_IMPLICIT_WORK_TREE",
		"GIT_INDEX_FILE", "GIT_INTERNAL_SUPER_PREFIX", "GIT_NAMESPACE", "GIT_NO_REPLACE_OBJECTS",
		"GIT_OBJECT_DIRECTORY", "GIT_PREFIX", "GIT_REPLACE_REF_BASE", "GIT_SHALLOW_FILE", "GIT_WORK_TREE":
		return true
	}
	return false
}

// Scrub returns env without the repository-local git variables, so a git
// command run with it finds the repository from its -C directory alone.
func Scrub(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); !isLocalVar(name) {
			out = append(out, kv)
		}
	}
	return out
}

// Command returns `git -C dir args...` with a scrubbed environment.
func Command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = Scrub(os.Environ())
	return cmd
}
