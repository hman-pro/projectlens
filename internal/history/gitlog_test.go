package history

import (
	"os/exec"
	"testing"
)

func TestParseGitLogOutput_MultipleCommits(t *testing.T) {
	input := `COMMIT:abc123|Alice|1700000000|add feature X
internal/foo/bar.go
internal/foo/baz.go

COMMIT:def456|Bob|1700001000|fix bug in Y
cmd/main.go
`

	commits, err := parseGitLogOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}

	// First commit
	if commits[0].Hash != "abc123" {
		t.Errorf("commit 0 hash: got %q, want %q", commits[0].Hash, "abc123")
	}
	if commits[0].Author != "Alice" {
		t.Errorf("commit 0 author: got %q, want %q", commits[0].Author, "Alice")
	}
	if commits[0].Timestamp != 1700000000 {
		t.Errorf("commit 0 timestamp: got %d, want %d", commits[0].Timestamp, 1700000000)
	}
	if commits[0].Message != "add feature X" {
		t.Errorf("commit 0 message: got %q, want %q", commits[0].Message, "add feature X")
	}
	if len(commits[0].Files) != 2 {
		t.Fatalf("commit 0 files: got %d, want 2", len(commits[0].Files))
	}
	if commits[0].Files[0] != "internal/foo/bar.go" {
		t.Errorf("commit 0 file 0: got %q, want %q", commits[0].Files[0], "internal/foo/bar.go")
	}
	if commits[0].Files[1] != "internal/foo/baz.go" {
		t.Errorf("commit 0 file 1: got %q, want %q", commits[0].Files[1], "internal/foo/baz.go")
	}

	// Second commit
	if commits[1].Hash != "def456" {
		t.Errorf("commit 1 hash: got %q, want %q", commits[1].Hash, "def456")
	}
	if commits[1].Author != "Bob" {
		t.Errorf("commit 1 author: got %q, want %q", commits[1].Author, "Bob")
	}
	if len(commits[1].Files) != 1 {
		t.Fatalf("commit 1 files: got %d, want 1", len(commits[1].Files))
	}
	if commits[1].Files[0] != "cmd/main.go" {
		t.Errorf("commit 1 file 0: got %q, want %q", commits[1].Files[0], "cmd/main.go")
	}
}

func TestParseGitLogOutput_SingleCommit(t *testing.T) {
	input := `COMMIT:aaa111|Carol|1700002000|refactor storage layer
internal/storage/client.go
internal/storage/queries.go
`

	commits, err := parseGitLogOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(commits))
	}
	if commits[0].Hash != "aaa111" {
		t.Errorf("hash: got %q, want %q", commits[0].Hash, "aaa111")
	}
	if len(commits[0].Files) != 2 {
		t.Fatalf("files: got %d, want 2", len(commits[0].Files))
	}
	if commits[0].Files[0] != "internal/storage/client.go" {
		t.Errorf("file 0: got %q, want %q", commits[0].Files[0], "internal/storage/client.go")
	}
	if commits[0].Files[1] != "internal/storage/queries.go" {
		t.Errorf("file 1: got %q, want %q", commits[0].Files[1], "internal/storage/queries.go")
	}
}

func TestParseGitLogOutput_EmptyOutput(t *testing.T) {
	commits, err := parseGitLogOutput("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) != 0 {
		t.Errorf("got %d commits, want 0", len(commits))
	}
}

func TestParseGitLogOutput_CommitNoFiles(t *testing.T) {
	input := `COMMIT:bbb222|Dave|1700003000|empty commit no files
`

	commits, err := parseGitLogOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(commits))
	}
	if commits[0].Hash != "bbb222" {
		t.Errorf("hash: got %q, want %q", commits[0].Hash, "bbb222")
	}
	if commits[0].Message != "empty commit no files" {
		t.Errorf("message: got %q, want %q", commits[0].Message, "empty commit no files")
	}
	if len(commits[0].Files) != 0 {
		t.Errorf("files: got %d, want 0", len(commits[0].Files))
	}
}

func TestParseGitLogOutput_SpecialCharsInMessage(t *testing.T) {
	input := `COMMIT:ccc333|Eve|1700004000|fix: handle edge|case with pipes|in message
internal/parser/parse.go
`

	commits, err := parseGitLogOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(commits))
	}
	wantMsg := "fix: handle edge|case with pipes|in message"
	if commits[0].Message != wantMsg {
		t.Errorf("message: got %q, want %q", commits[0].Message, wantMsg)
	}
	if len(commits[0].Files) != 1 {
		t.Fatalf("files: got %d, want 1", len(commits[0].Files))
	}
}

func TestParseGitLog_LiveRepo(t *testing.T) {
	// Skip if git is not available or this is not a git repo.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	repoPath := "/Users/hamed.zohrehvand/source/projectlens"
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--git-dir")
	if err := cmd.Run(); err != nil {
		t.Skipf("not a git repo at %s: %v", repoPath, err)
	}

	commits, err := ParseGitLog(repoPath, "")
	if err != nil {
		t.Fatalf("ParseGitLog returned error: %v", err)
	}

	if len(commits) == 0 {
		t.Fatal("expected at least one commit from the live repo")
	}

	// Verify the first commit has basic fields populated.
	c := commits[0]
	if c.Hash == "" {
		t.Error("first commit has empty hash")
	}
	if c.Author == "" {
		t.Error("first commit has empty author")
	}
	if c.Timestamp == 0 {
		t.Error("first commit has zero timestamp")
	}
	if c.Message == "" {
		t.Error("first commit has empty message")
	}
}
