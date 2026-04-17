package history

import (
	"math"
	"testing"
)

func TestComputeCoupling_BasicPair(t *testing.T) {
	// Files A and B appear together in 3 of 7 qualifying commits.
	// a.go is changed 5 times total (3 with b + 2 with c).
	// b.go is changed 5 times total (3 with a + 2 with d).
	// Strength = 3 / max(5, 5) = 0.6
	commits := []Commit{
		{Hash: "c1", Timestamp: 100, Files: []string{"a.go", "b.go"}},
		{Hash: "c2", Timestamp: 200, Files: []string{"a.go", "b.go"}},
		{Hash: "c3", Timestamp: 300, Files: []string{"a.go", "b.go"}},
		{Hash: "c4", Timestamp: 400, Files: []string{"a.go", "c.go"}},
		{Hash: "c5", Timestamp: 500, Files: []string{"a.go", "c.go"}},
		{Hash: "c6", Timestamp: 600, Files: []string{"b.go", "d.go"}},
		{Hash: "c7", Timestamp: 700, Files: []string{"b.go", "d.go"}},
	}

	pairs := ComputeCoupling(commits, 1, 50)

	var found bool
	for _, p := range pairs {
		if p.FileA == "a.go" && p.FileB == "b.go" {
			found = true
			if p.CoChangeCount != 3 {
				t.Errorf("expected CoChangeCount=3, got %d", p.CoChangeCount)
			}
			if math.Abs(p.Strength-0.6) > 0.001 {
				t.Errorf("expected Strength≈0.6, got %f", p.Strength)
			}
			break
		}
	}
	if !found {
		t.Fatal("expected to find pair (a.go, b.go)")
	}
}

func TestComputeCoupling_NoCoChanges(t *testing.T) {
	// Each file is changed alone — no co-changes possible.
	commits := []Commit{
		{Hash: "c1", Timestamp: 100, Files: []string{"a.go"}},
		{Hash: "c2", Timestamp: 200, Files: []string{"b.go"}},
		{Hash: "c3", Timestamp: 300, Files: []string{"c.go"}},
	}

	pairs := ComputeCoupling(commits, 1, 50)

	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs, got %d", len(pairs))
	}
}

func TestComputeCoupling_ExcludeHighDiffusion(t *testing.T) {
	// The commit with 25 files exceeds maxFilesPerCommit=20 and is excluded.
	// Only the 2-file commit contributes a pair.
	bigCommitFiles := make([]string, 25)
	for i := range bigCommitFiles {
		bigCommitFiles[i] = "file" + string(rune('a'+i)) + ".go"
	}

	commits := []Commit{
		{Hash: "c1", Timestamp: 100, Files: bigCommitFiles},
		{Hash: "c2", Timestamp: 200, Files: []string{"x.go", "y.go"}},
		{Hash: "c3", Timestamp: 300, Files: []string{"x.go", "y.go"}},
	}

	pairs := ComputeCoupling(commits, 1, 20)

	// Only x.go/y.go pair should exist
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0].FileA != "x.go" || pairs[0].FileB != "y.go" {
		t.Errorf("expected (x.go, y.go), got (%s, %s)", pairs[0].FileA, pairs[0].FileB)
	}
	if pairs[0].CoChangeCount != 2 {
		t.Errorf("expected CoChangeCount=2, got %d", pairs[0].CoChangeCount)
	}
}

func TestComputeCoupling_MinCoChangesFilter(t *testing.T) {
	// Pair has 2 co-changes but minCoChanges=5, so it's filtered out.
	commits := []Commit{
		{Hash: "c1", Timestamp: 100, Files: []string{"a.go", "b.go"}},
		{Hash: "c2", Timestamp: 200, Files: []string{"a.go", "b.go"}},
	}

	pairs := ComputeCoupling(commits, 5, 50)

	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs (minCoChanges=5), got %d", len(pairs))
	}
}

func TestComputeCoupling_MultipleFiles(t *testing.T) {
	// A commit with 3 files produces 3 pairs: (a,b), (a,c), (b,c).
	commits := []Commit{
		{Hash: "c1", Timestamp: 100, Files: []string{"a.go", "b.go", "c.go"}},
	}

	pairs := ComputeCoupling(commits, 1, 50)

	if len(pairs) != 3 {
		t.Fatalf("expected 3 pairs from 3 files, got %d", len(pairs))
	}

	pairSet := make(map[string]bool)
	for _, p := range pairs {
		pairSet[p.FileA+"|"+p.FileB] = true
	}
	expected := []string{"a.go|b.go", "a.go|c.go", "b.go|c.go"}
	for _, e := range expected {
		if !pairSet[e] {
			t.Errorf("missing expected pair %s", e)
		}
	}
}

func TestComputeCoupling_EmptyCommits(t *testing.T) {
	pairs := ComputeCoupling(nil, 1, 50)

	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs for nil input, got %d", len(pairs))
	}

	pairs = ComputeCoupling([]Commit{}, 1, 50)

	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs for empty input, got %d", len(pairs))
	}
}

func TestComputeCoupling_LastCoChangeTimestamp(t *testing.T) {
	// Two co-change commits at timestamps 100 and 500.
	// LastCoChange should be 500.
	commits := []Commit{
		{Hash: "c1", Timestamp: 100, Files: []string{"a.go", "b.go"}},
		{Hash: "c2", Timestamp: 500, Files: []string{"a.go", "b.go"}},
		{Hash: "c3", Timestamp: 300, Files: []string{"a.go", "b.go"}},
	}

	pairs := ComputeCoupling(commits, 1, 50)

	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0].LastCoChange != 500 {
		t.Errorf("expected LastCoChange=500, got %d", pairs[0].LastCoChange)
	}
}

func TestComputeCoupling_SortedByStrength(t *testing.T) {
	// Create pairs with different strengths:
	// (a.go, b.go): 3 co-changes out of 3 each → strength 1.0
	// (c.go, d.go): 2 co-changes, c changed 4 times → strength 0.5
	// (e.go, f.go): 1 co-change out of 1 each → strength 1.0 (tie with a/b)
	commits := []Commit{
		{Hash: "c1", Timestamp: 100, Files: []string{"a.go", "b.go"}},
		{Hash: "c2", Timestamp: 200, Files: []string{"a.go", "b.go"}},
		{Hash: "c3", Timestamp: 300, Files: []string{"a.go", "b.go"}},
		{Hash: "c4", Timestamp: 400, Files: []string{"c.go", "d.go"}},
		{Hash: "c5", Timestamp: 500, Files: []string{"c.go", "d.go"}},
		{Hash: "c6", Timestamp: 600, Files: []string{"c.go", "x.go"}},
		{Hash: "c7", Timestamp: 700, Files: []string{"c.go", "x.go"}},
		{Hash: "c8", Timestamp: 800, Files: []string{"e.go", "f.go"}},
	}

	pairs := ComputeCoupling(commits, 1, 50)

	if len(pairs) < 2 {
		t.Fatalf("expected at least 2 pairs, got %d", len(pairs))
	}

	// Verify descending order
	for i := 1; i < len(pairs); i++ {
		if pairs[i].Strength > pairs[i-1].Strength {
			t.Errorf("pairs not sorted by strength descending: index %d (%.2f) > index %d (%.2f)",
				i, pairs[i].Strength, i-1, pairs[i-1].Strength)
		}
	}

	// The c.go/d.go pair has strength 2/4 = 0.5 and should be near the end
	// The a.go/b.go pair has strength 3/3 = 1.0 and should be near the top
	if pairs[0].Strength < pairs[len(pairs)-1].Strength {
		t.Error("first pair should have highest strength")
	}
}
