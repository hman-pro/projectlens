package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runWithFlags wires the command under test into a root command that mirrors
// the persistent flags the real CLI exposes, then executes it with the given
// flag arguments. The validateMutex check inside openCmdStorage fires before
// any I/O, so passing --project together with --repo deterministically yields
// the mutex error without requiring a database.
func runWithFlags(t *testing.T, factory func() *cobra.Command, flags ...string) error {
	t.Helper()
	c := factory()
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("project", "", "")
	root.PersistentFlags().String("projects", "configs/projects.yaml", "")
	root.PersistentFlags().String("repo", "", "")
	root.PersistentFlags().String("config", "configs/index.yaml", "")
	root.PersistentFlags().String("db", "", "")
	root.AddCommand(c)
	// c.Use may contain positional hints (e.g. "inspect-symbol [symbol]");
	// cobra dispatches on the Name() (first whitespace-delimited token).
	root.SetArgs(append([]string{c.Name()}, flags...))
	return root.Execute()
}

func expectMutex(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("got %v", err)
	}
}

func TestStatusRejectsProjectAndRepo(t *testing.T) {
	expectMutex(t, runWithFlags(t, newStatusCmd, "--project", "foo", "--repo", "/tmp"))
}

func TestReportRejectsProjectAndRepo(t *testing.T) {
	expectMutex(t, runWithFlags(t, newReportCmd, "--project", "foo", "--repo", "/tmp"))
}

func TestExportRejectsProjectAndRepo(t *testing.T) {
	// newExportCmd is a parent; the graph subcommand is what opens storage.
	expectMutex(t, runWithFlags(t, newExportCmd, "graph", "--project", "foo", "--repo", "/tmp"))
}

func TestQueryRejectsProjectAndRepo(t *testing.T) {
	expectMutex(t, runWithFlags(t, newQueryCmd, "q", "--project", "foo", "--repo", "/tmp"))
}

func TestInspectSymbolRejectsProjectAndRepo(t *testing.T) {
	expectMutex(t, runWithFlags(t, newInspectSymbolCmd, "Foo", "--project", "foo", "--repo", "/tmp"))
}

func TestInspectPackageRejectsProjectAndRepo(t *testing.T) {
	expectMutex(t, runWithFlags(t, newInspectPackageCmd, "pkg", "--project", "foo", "--repo", "/tmp"))
}

func TestUnlockRejectsProjectAndRepo(t *testing.T) {
	expectMutex(t, runWithFlags(t, newUnlockCmd, "--force", "--project", "foo", "--repo", "/tmp"))
}

func TestKnowledgeListRejectsProjectAndRepo(t *testing.T) {
	expectMutex(t, runWithFlags(t, newKnowledgeListCmd, "--project", "foo", "--repo", "/tmp"))
}

func TestKnowledgeShowRejectsProjectAndRepo(t *testing.T) {
	expectMutex(t, runWithFlags(t, newKnowledgeShowCmd, "1", "--project", "foo", "--repo", "/tmp"))
}

func TestKnowledgeDeleteRejectsProjectAndRepo(t *testing.T) {
	expectMutex(t, runWithFlags(t, newKnowledgeDeleteCmd, "1", "--project", "foo", "--repo", "/tmp"))
}

func TestKnowledgeSearchRejectsProjectAndRepo(t *testing.T) {
	expectMutex(t, runWithFlags(t, newKnowledgeSearchCmd, "q", "--project", "foo", "--repo", "/tmp"))
}
