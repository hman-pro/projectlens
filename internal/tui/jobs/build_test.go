package jobs_test

import (
	"reflect"
	"testing"

	"github.com/hman-pro/projectlens/internal/tui/jobs"
)

func TestBuildArgs_AppendsConfigDBRepo(t *testing.T) {
	target := jobs.RunnerTarget{
		BinaryPath:  "/bin/projectlens",
		ConfigPath:  "/etc/index.yaml",
		DatabaseURL: "postgres://u:p@h:5432/d",
		RepoPath:    "/repos/ingest",
	}
	spec := jobs.Spec{Args: []string{"reindex", "--full"}}
	got := jobs.BuildArgs(spec, target)
	want := []string{
		"reindex", "--full",
		"--config", "/etc/index.yaml",
		"--db", "postgres://u:p@h:5432/d",
		"--repo", "/repos/ingest",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgs mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestBuildArgs_DoesNotMutateSpec(t *testing.T) {
	spec := jobs.Spec{Args: []string{"reindex"}}
	target := jobs.RunnerTarget{ConfigPath: "/c", DatabaseURL: "/d", RepoPath: "/r"}
	_ = jobs.BuildArgs(spec, target)
	if len(spec.Args) != 1 || spec.Args[0] != "reindex" {
		t.Fatalf("spec.Args mutated: %v", spec.Args)
	}
}
