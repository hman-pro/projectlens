package jobs_test

import (
	"slices"
	"testing"

	"github.com/hman-pro/projectlens/internal/tui/jobs"
)

func TestEverySpec_BuildArgsContainsExplicitTarget(t *testing.T) {
	target := jobs.RunnerTarget{
		BinaryPath:  "/usr/local/bin/projectlens",
		ConfigPath:  "/etc/projectlens/index.yaml",
		DatabaseURL: "postgres://projectlens@localhost/projectlens",
		RepoPath:    "/repos/ingest",
	}
	for _, s := range jobs.DefaultRegistry(testCfg()) {
		t.Run(s.Name, func(t *testing.T) {
			args := jobs.BuildArgs(s, target)
			for _, want := range []string{
				"--config", target.ConfigPath,
				"--db", target.DatabaseURL,
				"--repo", target.RepoPath,
			} {
				if !slices.Contains(args, want) {
					t.Errorf("argv missing %q: %v", want, args)
				}
			}
			if len(args) == 0 || args[0] != s.Args[0] {
				t.Errorf("first arg = %q, want %q", args[0], s.Args[0])
			}
		})
	}
}
