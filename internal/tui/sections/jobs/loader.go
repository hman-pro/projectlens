package jobs

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const logSuffix = ".log"

func ResolveDir() string {
	if d := os.Getenv("PROJECTLENS_TUI_RUNS_DIR"); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".projectlens", "tui-runs")
	}
	return filepath.Join(os.TempDir(), "projectlens-tui-runs")
}

func ListRuns(dir string) ([]JobRun, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]JobRun, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), logSuffix) {
			continue
		}
		run, ok := parseEntry(dir, e)
		if !ok {
			continue
		}
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}

func parseEntry(dir string, e fs.DirEntry) (JobRun, bool) {
	name := strings.TrimSuffix(e.Name(), logSuffix)
	// Filename layout: <RFC3339>-<action>. RFC3339 contains colons and a
	// trailing 'Z' or offset; the action follows the first '-' AFTER the
	// timezone marker, so we scan to the end of the timestamp explicitly.
	idx := timestampEnd(name)
	if idx < 0 || idx+1 >= len(name) {
		return JobRun{}, false
	}
	tsPart := name[:idx]
	action := name[idx+1:]
	started, err := time.Parse(time.RFC3339, tsPart)
	if err != nil {
		return JobRun{}, false
	}
	info, err := e.Info()
	if err != nil {
		return JobRun{}, false
	}
	completed := info.ModTime()
	dur := completed.Sub(started)
	if dur < 0 {
		dur = 0
	}
	path := filepath.Join(dir, e.Name())
	status := readStatus(path)
	return JobRun{
		Started:   started,
		Completed: completed,
		Duration:  dur,
		Action:    action,
		Status:    status,
		LogPath:   path,
	}, true
}

func timestampEnd(name string) int {
	for i := 0; i < len(name); i++ {
		switch name[i] {
		case 'Z':
			return i + 1
		case '+':
			if i+5 < len(name) && name[i+3] == ':' {
				return i + 6
			}
		}
	}
	return -1
}

func readStatus(path string) string {
	tail, err := ReadTail(path, 50)
	if err != nil {
		return "completed"
	}
	for _, line := range tail {
		if strings.HasPrefix(line, "stderr\tError:") || strings.HasPrefix(line, "stderr\tpanic:") {
			return "failed"
		}
	}
	return "completed"
}

func ReadTail(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	ring := make([]string, 0, n)
	for scanner.Scan() {
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return ring, err
	}
	return ring, nil
}
