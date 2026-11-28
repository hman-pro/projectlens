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
// lists with `git rev-parse --local-env-vars`, plus GIT_NAMESPACE and
// GIT_INTERNAL_SUPER_PREFIX.
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

// isCommandConfigVar reports whether name carries command-scope config
// (`git -c`, or GIT_CONFIG_COUNT with GIT_CONFIG_KEY_n/GIT_CONFIG_VALUE_n).
func isCommandConfigVar(name string) bool {
	return name == "GIT_CONFIG_PARAMETERS" || name == "GIT_CONFIG_COUNT"
}

// Scrub returns env without the variables that point git at another
// repository, so a git command run with it finds the repository from its -C
// directory alone. Like git when it runs a command in a submodule, it keeps
// command-scope config: a container that trusts its bind mount through
// GIT_CONFIG_COUNT/safe.directory still works.
func Scrub(env []string) []string {
	return filter(env, func(name string) bool { return isLocalVar(name) && !isCommandConfigVar(name) })
}

// ScrubStrict is Scrub that also drops command-scope config, for callers that
// must not let a caller's config change what they read or write.
func ScrubStrict(env []string) []string {
	return filter(env, isLocalVar)
}

func filter(env []string, drop func(string) bool) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); !drop(name) {
			out = append(out, kv)
		}
	}
	return out
}

// Command returns `git -C dir args...` with a Scrub-bed environment.
func Command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = Scrub(os.Environ())
	return cmd
}
