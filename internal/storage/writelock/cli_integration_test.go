//go:build integration

package writelock_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestCLI_TwoHoldersSerialize launches two `debug-hold-lock --hold 3s`
// invocations concurrently. The winner holds for 3 seconds,
// guaranteeing the loser observes ErrBusy. Deterministic — does not
// depend on whatever index-embed would actually do on the dev DB.
func TestCLI_TwoHoldersSerialize(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = testDB
	}

	dir := t.TempDir()
	binPath := filepath.Join(dir, "projectlens")
	build := exec.Command("go", "build", "-o", binPath, "../../../cmd/projectlens/")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build projectlens: %v", err)
	}

	type result struct {
		exit int
		err  error
		out  string
	}
	run := func() result {
		var stderr bytes.Buffer
		c := exec.Command(binPath, "debug-hold-lock", "--hold", "3s",
			"--db", dsn, "--repo", t.TempDir(),
			"--config", "../../../configs/index.yaml")
		c.Env = append(os.Environ(), "PROJECTLENS_DEBUG_HOLD_LOCK=1")
		c.Stderr = &stderr
		err := c.Run()
		exit := 0
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else if err != nil {
			exit = -1
		}
		return result{exit: exit, err: err, out: stderr.String()}
	}
	var wg sync.WaitGroup
	results := make([]result, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = run()
		}(i)
	}
	wg.Wait()

	wins, busies := 0, 0
	for _, r := range results {
		switch r.exit {
		case 0:
			wins++
		case 75:
			if !strings.Contains(r.out, "another writer holds the lock") {
				t.Errorf("exit 75 but stderr lacks busy text: %q", r.out)
			}
			busies++
		default:
			t.Errorf("unexpected exit %d (err=%v stderr=%q)", r.exit, r.err, r.out)
		}
	}
	if wins != 1 || busies != 1 {
		t.Errorf("wins=%d busies=%d, want 1/1", wins, busies)
	}
}
