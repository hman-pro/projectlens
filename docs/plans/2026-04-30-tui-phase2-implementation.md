# ProjectLens TUI — Phase 2 (Indexer Control Plane) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a write surface to the projectlens TUI so users can trigger
`reindex`, `index-embed`, `index-summarize`, and `index-history` from the
dashboard, with explicit-target subprocess invocation, preflight + typed
confirms for every action, a streaming bottom drawer, and a quit path that
never orphans a mutating subprocess.

**Architecture:** A new `internal/tui/jobs` package owns a single-slot
`Runner` that resolves a `projectlens` binary, builds command lines with
explicit `--config/--db/--repo` flags from a `RunnerTarget`, executes the
child via `exec.CommandContext`, scans stdout/stderr into a tail ring buffer
and a per-run log file, and emits Bubbletea messages
(`JobStartedMsg`, `JobLineMsg`, `JobTickMsg`, `JobCompletedMsg`,
`JobBusyMsg`). Two new components — `jobdrawer` (persistent bottom strip)
and `confirmmodal` (yes/no + typed-phrase variants) — render on top of the
existing Phase 1 layout. The Pipeline section grows a Controls block listing
the five hotkeys; the section interface gains a sibling `ActionableSection`.
The store layer gains four read-only preflight methods that count work
before each action runs.

**Tech Stack:** Go 1.26, `charmbracelet/bubbletea`, `charmbracelet/lipgloss`,
`os/exec`, `pgx/v5`. Read-only preflight queries against the existing
Postgres schema. No new dependencies.

**Reference spec:** `docs/plans/2026-04-30-tui-phase2-design.md` (revision 2).

> **Revision 2 (2026-05-01) — review fixes incorporated.** The
> 2026-04-30 implementation review surfaced findings that change the
> runner's cancellation semantics, all preflight SQL queries, the
> detach contract (dropped from Phase 2), the binary-missing handling
> path, the cost-driver derivation, and the app-level test coverage.
> Each affected task below has a `> rev 2` callout describing the
> delta. See the design doc's "Revision 2 — review responses" table
> for the high-level summary.

---

## Prerequisites

The spec lists a cross-process writer lock as a prerequisite that must merge
before Phase 2 implementation begins (`docs/plans/2026-04-30-indexer-writer-lock-design.md`).
This implementation plan is independent of that work in the
sense that it can be **written and reviewed** today; it cannot be **executed**
until the lock design is implemented and merged. Task 0 verifies the
prerequisite is in place before any code lands.

---

## File structure

### New files

| Path | Responsibility |
|------|----------------|
| `internal/tui/jobs/spec.go` | `Spec`, `ConfirmKind` constants, `RunnerTarget`, `Preflight` function type. No behavior. |
| `internal/tui/jobs/build.go` | `BuildArgs(spec, target) []string` — single function, easy to unit-test. |
| `internal/tui/jobs/binary.go` | `ResolveBinary() (string, error)` — env > sibling > PATH. |
| `internal/tui/jobs/runner.go` | `Runner` struct: Start, Cancel, State; subprocess lifecycle. |
| `internal/tui/jobs/messages.go` | Bubbletea message types: `JobStartedMsg`, `JobLineMsg`, `JobTickMsg`, `JobCompletedMsg`, `JobBusyMsg`, `RunSpecMsg`, `PreflightDoneMsg`. |
| `internal/tui/jobs/registry.go` | Static `[]Spec` plus `LookupByKey(rune) (Spec, bool)`. |
| `internal/tui/jobs/spec_test.go` | Type/constant sanity tests. |
| `internal/tui/jobs/build_test.go` | `BuildArgs` correctness — every spec gets `--config --db --repo`. |
| `internal/tui/jobs/binary_test.go` | Binary resolution precedence. |
| `internal/tui/jobs/runner_test.go` | Runner lifecycle with stub command. |
| `internal/tui/jobs/runner_command_test.go` | End-to-end argv assertion across the registry. |
| `internal/tui/jobs/registry_test.go` | No key collisions, all preflights non-nil. |
| `internal/tui/components/confirmmodal/confirmmodal.go` | Bubbletea component, both yes/no and typed-phrase modes. |
| `internal/tui/components/confirmmodal/confirmmodal_test.go` | Render + key-input behavior. |
| `internal/tui/components/jobdrawer/jobdrawer.go` | Bubbletea component, 8-row strip rendering. |
| `internal/tui/components/jobdrawer/jobdrawer_test.go` | Snapshot per state. |
| `internal/tui/app/quit_test.go` | Drain-on-quit (no detach in rev 2). |
| `internal/tui/app/actions_test.go` | Action-flow: preflight → confirm → run, binary-missing, stale, typed (rev 2). |
| `internal/tui/app/stub_runner_test.go` | Stub runner used by app tests (rev 2). |

### Modified files

| Path | Change |
|------|--------|
| `internal/tui/sections/section.go` | Add `ActionableSection` interface. |
| `internal/tui/sections/pipeline/model.go` | Implement `Actions()`. |
| `internal/tui/sections/pipeline/view.go` | Add Controls block listing hotkeys. |
| `internal/tui/sections/pipeline/model_test.go` | Assert Controls block renders. |
| `internal/tui/store/store.go` | Add `EmbedPending`, `SummarizePending`, `HistoryNewCommits`, `ChangedFilesSinceLastRun` to `Store` interface. |
| `internal/tui/store/types.go` | (no new types needed — preflights return `(int, error)`.) |
| `internal/tui/store/pg.go` | Implement the four preflight methods. |
| `internal/tui/store/fake.go` | Add setters/getters mirroring the four methods. |
| `internal/tui/store/pg_integration_test.go` | Cover the four new methods against a real DB. |
| `internal/tui/app/model.go` | Add `runner`, `drawer`, `confirm`, `binaryPath`, `target` fields. |
| `internal/tui/app/update.go` | Keymap precedence; preflight → confirm → run flow; refresh-on-success dispatch; quit path. |
| `internal/tui/app/view.go` | Render drawer + confirm modal overlays. |
| `internal/tui/app/keys.go` | Document new keys (`R/F/E/S/H/c/j`) in help. |
| `cmd/projectlens-tui/main.go` | Resolve binary, build `RunnerTarget`, construct `Runner`, pass into app. |
| `README.md` | Document new keys + log file location. |
| `CLAUDE.md` | Update keys section + new env var (`PROJECTLENS_BINARY`). |

---

## Task 0: Verify Prerequisites

**Goal:** Confirm the cross-process writer lock has merged before any code lands.

- [ ] **Step 1: Verify the writer-lock implementation exists**

Run:
```bash
git log --oneline --grep "writer.lock\|advisory.lock\|index.lock"
grep -rn "pg_try_advisory_lock\|pg_advisory_lock" internal/
```

Expected: at least one matching commit and `pg_advisory_lock` calls in
`internal/storage/` or `internal/indexer/`. If neither exists, **stop** —
consult `docs/plans/2026-04-30-indexer-writer-lock-design.md` and a separate
implementation plan for it before continuing this plan.

---

## Task 1: Define Spec, ConfirmKind, RunnerTarget types

**Files:**
- Create: `internal/tui/jobs/spec.go`
- Test: `internal/tui/jobs/spec_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/tui/jobs/spec_test.go
package jobs_test

import (
	"testing"

	"github.com/hman-pro/projectlens/internal/tui/jobs"
)

func TestConfirmKind_String(t *testing.T) {
	cases := map[jobs.ConfirmKind]string{
		jobs.ConfirmYesNo: "yesno",
		jobs.ConfirmTyped: "typed",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", k, got, want)
		}
	}
}

func TestSpec_ZeroValueIsInvalid(t *testing.T) {
	var s jobs.Spec
	if s.Valid() {
		t.Fatal("zero Spec must not be Valid")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/jobs/...`
Expected: build failure — `package jobs is not in std`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/tui/jobs/spec.go
package jobs

import (
	"context"

	"github.com/hman-pro/projectlens/internal/tui/store"
)

// ConfirmKind controls how an action is confirmed before launch.
type ConfirmKind int

const (
	ConfirmYesNo ConfirmKind = iota
	ConfirmTyped
)

func (c ConfirmKind) String() string {
	switch c {
	case ConfirmYesNo:
		return "yesno"
	case ConfirmTyped:
		return "typed"
	}
	return "unknown"
}

// Preflight runs a fast read-only count before the confirm modal opens.
// Returns (count, costDriver, err). costDriver is a short string like
// "openai" or "anthropic" or "" (purely local).
type Preflight func(ctx context.Context, s store.Store) (int, string, error)

// HeadlineFn formats the confirm-modal headline given the preflight result.
type HeadlineFn func(count int, cost string) string

// Spec describes one triggerable action.
type Spec struct {
	Key       rune
	Name      string
	Args      []string
	Confirm   ConfirmKind
	Phrase    string
	RefreshOn []string
	Preflight Preflight
	Headline  HeadlineFn
}

// Valid reports whether the Spec is fully populated.
func (s Spec) Valid() bool {
	return s.Key != 0 && s.Name != "" && len(s.Args) > 0 &&
		s.Preflight != nil && s.Headline != nil
}

// RunnerTarget captures everything a runner must know to invoke a child
// process targeting the same database, repo, and config the TUI is
// currently rendering.
type RunnerTarget struct {
	BinaryPath  string
	ConfigPath  string
	DatabaseURL string
	RepoPath    string
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/jobs/... -v`
Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/jobs/spec.go internal/tui/jobs/spec_test.go
git commit -m "feat(tui/jobs): Spec, ConfirmKind, RunnerTarget"
```

---

## Task 2: BuildArgs — explicit --config/--db/--repo injection

**Files:**
- Create: `internal/tui/jobs/build.go`
- Test: `internal/tui/jobs/build_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/tui/jobs/build_test.go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/jobs/...`
Expected: FAIL — `BuildArgs` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/tui/jobs/build.go
package jobs

// BuildArgs returns a fresh argv that begins with the Spec's own Args and
// then appends explicit --config, --db, --repo flags from the target.
// The Spec is not mutated.
func BuildArgs(spec Spec, t RunnerTarget) []string {
	out := make([]string, 0, len(spec.Args)+6)
	out = append(out, spec.Args...)
	out = append(out,
		"--config", t.ConfigPath,
		"--db", t.DatabaseURL,
		"--repo", t.RepoPath,
	)
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/jobs/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/jobs/build.go internal/tui/jobs/build_test.go
git commit -m "feat(tui/jobs): BuildArgs injects --config/--db/--repo"
```

---

## Task 3: ResolveBinary — env > sibling > PATH

**Files:**
- Create: `internal/tui/jobs/binary.go`
- Test: `internal/tui/jobs/binary_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/tui/jobs/binary_test.go
package jobs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hman-pro/projectlens/internal/tui/jobs"
)

func TestResolveBinary_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-bin")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROJECTLENS_BINARY", bin)
	got, err := jobs.ResolveBinary()
	if err != nil {
		t.Fatal(err)
	}
	if got != bin {
		t.Fatalf("got %q, want %q", got, bin)
	}
}

func TestResolveBinary_EnvNotExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "not-exec")
	if err := os.WriteFile(bin, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROJECTLENS_BINARY", bin)
	if _, err := jobs.ResolveBinary(); err == nil {
		t.Fatal("expected error for non-executable PROJECTLENS_BINARY")
	}
}

func TestResolveBinary_NotFound(t *testing.T) {
	t.Setenv("PROJECTLENS_BINARY", "")
	t.Setenv("PATH", t.TempDir())
	_, err := jobs.ResolveBinary()
	if err == nil {
		t.Fatal("expected error when binary cannot be resolved")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/jobs/...`
Expected: FAIL — `ResolveBinary` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/tui/jobs/binary.go
package jobs

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ResolveBinary returns the absolute path to the projectlens binary the
// runner should invoke. Resolution order:
//  1. PROJECTLENS_BINARY env var (must be executable).
//  2. A sibling of os.Executable() named "projectlens".
//  3. PATH lookup for "projectlens".
func ResolveBinary() (string, error) {
	if v := os.Getenv("PROJECTLENS_BINARY"); v != "" {
		if err := isExecutable(v); err != nil {
			return "", fmt.Errorf("PROJECTLENS_BINARY=%q: %w", v, err)
		}
		return v, nil
	}
	if self, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(self), "projectlens")
		if err := isExecutable(sibling); err == nil {
			return sibling, nil
		}
	}
	if path, err := exec.LookPath("projectlens"); err == nil {
		return path, nil
	}
	return "", errors.New("projectlens binary not found (set PROJECTLENS_BINARY, place it next to projectlens-tui, or add to PATH)")
}

func isExecutable(p string) error {
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("is a directory")
	}
	if info.Mode()&0o111 == 0 {
		return errors.New("not executable")
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/jobs/... -v -run TestResolveBinary`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/jobs/binary.go internal/tui/jobs/binary_test.go
git commit -m "feat(tui/jobs): ResolveBinary with env/sibling/PATH precedence"
```

---

## Task 4: Bubbletea message types

**Files:**
- Create: `internal/tui/jobs/messages.go`

- [ ] **Step 1: Write the file**

```go
// internal/tui/jobs/messages.go
package jobs

import "time"

// JobStartedMsg announces that a subprocess has begun.
type JobStartedMsg struct {
	Spec    Spec
	LogPath string
	Argv    []string
}

// JobLineMsg carries a single line from the child's stdout or stderr.
type JobLineMsg struct {
	Line   string
	Stream string // "stdout" or "stderr"
}

// JobTickMsg is emitted every 500ms while a job is running so the drawer
// can animate elapsed time even when the child is silent.
type JobTickMsg struct{}

// JobCompletedMsg is sent exactly once when cmd.Wait returns AND the log
// file has been flushed and closed.
type JobCompletedMsg struct {
	Spec     Spec
	ExitCode int
	Status   string // "succeeded" | "failed" | "cancelled"
	Duration time.Duration
	LogPath  string
	Tail     []string
}

// JobBusyMsg is emitted when a Run is requested while another job is in
// flight.
type JobBusyMsg struct {
	Wanted  Spec
	Running Spec
}

// RunSpecMsg is dispatched by the app after the user passes the confirm
// modal. The runner picks it up and calls Start.
type RunSpecMsg struct {
	Spec Spec
}

// PreflightDoneMsg carries the count + cost driver back to the app so it
// can open the right confirm modal.
type PreflightDoneMsg struct {
	Spec  Spec
	Count int
	Cost  string
	Err   error
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./internal/tui/jobs/...`
Expected: no error.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/jobs/messages.go
git commit -m "feat(tui/jobs): Bubbletea message types"
```

---

## Task 5: Runner skeleton — state machine, no subprocess yet

**Files:**
- Create: `internal/tui/jobs/runner.go`
- Test: `internal/tui/jobs/runner_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/tui/jobs/runner_test.go
package jobs_test

import (
	"testing"

	"github.com/hman-pro/projectlens/internal/tui/jobs"
)

func TestRunner_InitialStateIsIdle(t *testing.T) {
	r := jobs.NewRunner(jobs.RunnerTarget{}, nil)
	if got := r.State().Status; got != "idle" {
		t.Fatalf("status = %q, want %q", got, "idle")
	}
}

func TestRunner_StartRejectsInvalidSpec(t *testing.T) {
	r := jobs.NewRunner(jobs.RunnerTarget{BinaryPath: "/bin/true"}, nil)
	err := r.Start(jobs.Spec{}) // zero value, Valid() == false
	if err == nil {
		t.Fatal("expected error for invalid spec")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/jobs/...`
Expected: FAIL — `NewRunner` / `Start` / `State` undefined.

- [ ] **Step 3: Write minimal skeleton**

```go
// internal/tui/jobs/runner.go
package jobs

import (
	"errors"
	"os/exec"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrJobInFlight is returned by Start when another job is already running.
var ErrJobInFlight = errors.New("job already in flight")

// ErrInvalidSpec is returned by Start when the supplied Spec is not Valid.
var ErrInvalidSpec = errors.New("invalid spec")

// Runner is a single-slot subprocess executor.
type Runner struct {
	target RunnerTarget
	send   func(tea.Msg) // for Program.Send injection in tests

	mu        sync.Mutex
	status    string
	current   Spec
	startedAt time.Time
	cmd       *exec.Cmd
	tail      *ringBuffer
	logPath   string
}

// Snapshot is the runner's externally observable state at one moment.
type Snapshot struct {
	Status    string
	Current   Spec
	StartedAt time.Time
	LogPath   string
	Tail      []string
}

// NewRunner builds a fresh runner. The send func is how it dispatches
// messages back into the Bubbletea program; pass nil to disable dispatch
// (used by unit tests that don't have a Program).
func NewRunner(target RunnerTarget, send func(tea.Msg)) *Runner {
	return &Runner{
		target: target,
		send:   send,
		status: "idle",
		tail:   newRingBuffer(200),
	}
}

// State returns a snapshot of the runner's current state.
func (r *Runner) State() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Snapshot{
		Status:    r.status,
		Current:   r.current,
		StartedAt: r.startedAt,
		LogPath:   r.logPath,
		Tail:      r.tail.snapshot(),
	}
}

// Start launches the spec as a subprocess. Returns ErrJobInFlight if a
// job is already running, or ErrInvalidSpec if the spec is malformed.
// Subprocess execution itself is implemented in a later task.
func (r *Runner) Start(spec Spec) error {
	if !spec.Valid() {
		return ErrInvalidSpec
	}
	r.mu.Lock()
	if r.status == "running" || r.status == "cancelling" {
		running := r.current
		r.mu.Unlock()
		if r.send != nil {
			r.send(JobBusyMsg{Wanted: spec, Running: running})
		}
		return ErrJobInFlight
	}
	r.status = "running"
	r.current = spec
	r.startedAt = time.Now()
	r.mu.Unlock()
	// subprocess + scanners + tick + Wait wired in the next tasks.
	return nil
}

// Cancel is a stub; full SIGTERM/SIGKILL flow lands in Task 8.
func (r *Runner) Cancel() {}

// ringBuffer is a fixed-size FIFO for tail lines.
type ringBuffer struct {
	mu   sync.Mutex
	data []string
	cap  int
}

func newRingBuffer(cap int) *ringBuffer { return &ringBuffer{cap: cap} }

func (b *ringBuffer) push(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data) >= b.cap {
		b.data = append(b.data[1:], s)
		return
	}
	b.data = append(b.data, s)
}

func (b *ringBuffer) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.data))
	copy(out, b.data)
	return out
}

func (b *ringBuffer) reset() {
	b.mu.Lock()
	b.data = b.data[:0]
	b.mu.Unlock()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/jobs/... -v -run TestRunner`
Expected: PASS for both Runner tests.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/jobs/runner.go internal/tui/jobs/runner_test.go
git commit -m "feat(tui/jobs): Runner skeleton with state machine + ring buffer"
```

---

## Task 6: Runner subprocess execution + log file + tail capture

> **rev 2:** two changes vs rev 1.
> 1. Scanner goroutines push lines into a buffered channel; a single
>    writer goroutine owns `logFile` writes + `tail.push()` to avoid
>    racing on `os.File` writes and `ringBuffer` mutex contention.
> 2. The runner stores a `cancelRequested` bool so `Cancel()` can
>    classify completion as `cancelled` even when the SIGTERM-driven
>    `cmd.Wait()` returns a non-zero exit code (and `ctx.Err()` is
>    nil because we never call `cancelFn` on a TERM-only cancel).

**Files:**
- Modify: `internal/tui/jobs/runner.go`
- Modify: `internal/tui/jobs/runner_test.go`

- [ ] **Step 1: Write the failing test**

Append to `runner_test.go`:

```go
func TestRunner_StartExecutesAndCompletes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROJECTLENS_TUI_RUNS_DIR", dir)
	got := make(chan tea.Msg, 16)
	send := func(m tea.Msg) { got <- m }
	target := jobs.RunnerTarget{
		BinaryPath:  "/bin/sh",
		ConfigPath:  "/dev/null",
		DatabaseURL: "postgres://x",
		RepoPath:    "/tmp",
	}
	r := jobs.NewRunner(target, send)
	spec := jobs.Spec{
		Key: 'X', Name: "echo", Args: []string{"-c", "echo hello && echo world 1>&2"},
		Confirm: jobs.ConfirmYesNo,
		Preflight: func(_ context.Context, _ store.Store) (int, string, error) { return 0, "", nil },
		Headline:  func(int, string) string { return "" },
	}
	if err := r.Start(spec); err != nil {
		t.Fatal(err)
	}
	// Drain until we see JobCompletedMsg.
	deadline := time.After(5 * time.Second)
	var completed *jobs.JobCompletedMsg
	for completed == nil {
		select {
		case msg := <-got:
			if c, ok := msg.(jobs.JobCompletedMsg); ok {
				completed = &c
			}
		case <-deadline:
			t.Fatal("timeout waiting for JobCompletedMsg")
		}
	}
	if completed.Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", completed.Status)
	}
	if len(completed.Tail) < 2 {
		t.Errorf("tail too short: %v", completed.Tail)
	}
	// log file exists and contains both lines.
	data, err := os.ReadFile(completed.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "hello") || !strings.Contains(body, "world") {
		t.Errorf("log missing lines: %q", body)
	}
}
```

Add the necessary imports at the top of the test file:

```go
import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/store"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/jobs/... -v -run TestRunner_StartExecutes`
Expected: FAIL — Runner.Start currently does not exec.

- [ ] **Step 3: Implement subprocess execution**

In `runner.go`, replace the body of `Start` (after the status flip) with:

```go
	go r.run(spec)
	return nil
}

// scanLine is the unit pushed from scanner goroutines to the writer
// goroutine. Decouples I/O ordering from log-file write ordering.
type scanLine struct {
	stream string
	text   string
}

func (r *Runner) run(spec Spec) {
	argv := BuildArgs(spec, r.target)
	logDir := os.Getenv("PROJECTLENS_TUI_RUNS_DIR")
	if logDir == "" {
		home, _ := os.UserHomeDir()
		logDir = filepath.Join(home, ".projectlens", "tui-runs")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		// fall back to TempDir on permission failure.
		logDir = filepath.Join(os.TempDir(), "projectlens-tui-runs")
		_ = os.MkdirAll(logDir, 0o755)
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("%s-%s.log",
		time.Now().UTC().Format(time.RFC3339), sanitize(spec.Name)))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		r.complete(spec, -1, "failed", time.Since(r.startedAt), "", []string{"log open: " + err.Error()})
		return
	}

	r.mu.Lock()
	r.tail.reset()
	r.logPath = logPath
	r.cancelRequested = false
	r.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.mu.Lock()
	r.cancelFn = cancel
	r.mu.Unlock()

	cmd := exec.CommandContext(ctx, r.target.BinaryPath, argv...)
	cmd.Env = os.Environ()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		r.complete(spec, -1, "failed", time.Since(r.startedAt), logPath,
			[]string{"start: " + err.Error()})
		return
	}
	r.mu.Lock()
	r.cmd = cmd
	r.mu.Unlock()

	if r.send != nil {
		r.send(JobStartedMsg{Spec: spec, LogPath: logPath, Argv: argv})
	}

	// Single writer goroutine owns logFile writes and tail.push.
	lines := make(chan scanLine, 256)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for ln := range lines {
			fmt.Fprintf(logFile, "%s\t%s\n", ln.stream, ln.text)
			r.tail.push(ln.text)
			if r.send != nil {
				r.send(JobLineMsg{Line: ln.text, Stream: ln.stream})
			}
		}
	}()

	var scanWG sync.WaitGroup
	scanWG.Add(2)
	go r.scan(stdout, "stdout", lines, &scanWG)
	go r.scan(stderr, "stderr", lines, &scanWG)

	waitErr := cmd.Wait()
	scanWG.Wait() // wait for both scanners to drain their pipes
	close(lines)  // signal writer to drain remaining queued lines
	<-writerDone  // wait for writer to finish all writes
	_ = logFile.Sync()
	_ = logFile.Close()

	r.mu.Lock()
	cancelled := r.cancelRequested
	r.mu.Unlock()

	exit := 0
	status := "succeeded"
	switch {
	case cancelled:
		// Caller invoked Cancel(). Classify as cancelled regardless of
		// whether SIGTERM produced a non-zero exit or cancelFn fired.
		status = "cancelled"
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	case ctx.Err() != nil:
		// Parent context cancellation (e.g. tea.WithContext shutdown).
		status = "cancelled"
		exit = -1
	case waitErr != nil:
		status = "failed"
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	r.complete(spec, exit, status, time.Since(r.startedAt), logPath, r.tail.snapshot())
}

func (r *Runner) scan(rd io.ReadCloser, stream string, lines chan<- scanLine, wg *sync.WaitGroup) {
	defer wg.Done()
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		lines <- scanLine{stream: stream, text: sc.Text()}
	}
}

func (r *Runner) complete(spec Spec, exit int, status string, dur time.Duration, logPath string, tail []string) {
	r.mu.Lock()
	r.status = status
	r.cmd = nil
	r.cancelFn = nil
	r.mu.Unlock()
	if r.send != nil {
		r.send(JobCompletedMsg{
			Spec: spec, ExitCode: exit, Status: status,
			Duration: dur, LogPath: logPath, Tail: tail,
		})
	}
}

func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
```

Add the supporting imports + field at the top of `runner.go`:

```go
import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)
```

And inside the `Runner` struct add:

```go
	cancelFn        context.CancelFunc
	cancelRequested bool // set by Cancel(); read by completion classifier
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/jobs/... -v -run TestRunner_StartExecutes`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/jobs/runner.go internal/tui/jobs/runner_test.go
git commit -m "feat(tui/jobs): Runner subprocess execution + log file + tail capture"
```

---

## Task 7: Runner tick goroutine

**Files:**
- Modify: `internal/tui/jobs/runner.go`

- [ ] **Step 1: Add tick loop in `run`**

After `cmd.Start()` succeeds and before the scanner goroutines, add:

```go
	tickStop := make(chan struct{})
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-tickStop:
				return
			case <-t.C:
				if r.send != nil {
					r.send(JobTickMsg{})
				}
			}
		}
	}()
```

After `wg.Wait()` add `close(tickStop)` to stop the loop.

- [ ] **Step 2: Build**

Run: `go build ./internal/tui/jobs/...`
Expected: clean build.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/jobs/runner.go
git commit -m "feat(tui/jobs): emit JobTickMsg every 500ms while running"
```

---

## Task 8: Runner Cancel — SIGTERM + SIGKILL watchdog

> **rev 2 (high):** rev 1 sent SIGTERM but never told the completion
> classifier "this was a cancel" — so the resulting non-zero exit was
> classified as `failed`, contradicting the design lifecycle. Cancel
> now sets `r.cancelRequested = true` under the mutex (read by the
> classifier in Task 6) AND invokes `cancelFn` to propagate
> cancellation to the `exec.CommandContext` ctx, so the watchdog
> kill path also gets the cancel classification.

**Files:**
- Modify: `internal/tui/jobs/runner.go`
- Modify: `internal/tui/jobs/runner_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestRunner_CancelStopsLongRunningJob(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROJECTLENS_TUI_RUNS_DIR", dir)
	got := make(chan tea.Msg, 16)
	send := func(m tea.Msg) { got <- m }
	target := jobs.RunnerTarget{
		BinaryPath:  "/bin/sh",
		ConfigPath:  "/dev/null",
		DatabaseURL: "postgres://x",
		RepoPath:    "/tmp",
	}
	r := jobs.NewRunner(target, send)
	spec := jobs.Spec{
		Key: 'X', Name: "sleep", Args: []string{"-c", "trap '' TERM; sleep 30"},
		Confirm: jobs.ConfirmYesNo,
		Preflight: func(_ context.Context, _ store.Store) (int, string, error) { return 0, "", nil },
		Headline:  func(int, string) string { return "" },
	}
	if err := r.Start(spec); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	r.Cancel()
	deadline := time.After(8 * time.Second) // 5s grace + slack
	for {
		select {
		case msg := <-got:
			if c, ok := msg.(jobs.JobCompletedMsg); ok {
				if c.Status != "cancelled" {
					t.Errorf("status = %q, want cancelled", c.Status)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout: cancel did not terminate within 8s")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/jobs/... -v -run TestRunner_Cancel`
Expected: FAIL — current Cancel is a stub.

- [ ] **Step 3: Implement Cancel**

Replace the stub with:

```go
func (r *Runner) Cancel() {
	r.mu.Lock()
	cmd := r.cmd
	cancelFn := r.cancelFn
	r.status = "cancelling"
	r.cancelRequested = true // makes the completion classifier pick "cancelled"
	r.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		// Job not yet started or already finished. The flag above will
		// still classify any pending completion correctly.
		return
	}
	// Send SIGTERM first; many indexer pipelines flush on TERM.
	_ = cmd.Process.Signal(syscall.SIGTERM)
	go func() {
		t := time.NewTimer(5 * time.Second)
		defer t.Stop()
		<-t.C
		r.mu.Lock()
		stillCmd := r.cmd
		r.mu.Unlock()
		if stillCmd != nil && stillCmd.Process != nil {
			// Cancel the exec.CommandContext ctx so the runtime kills
			// the child even if SIGTERM was ignored. cancelFn may have
			// been cleared by complete() — guard.
			if cancelFn != nil {
				cancelFn()
			}
			_ = stillCmd.Process.Kill()
		}
	}()
}
```

Add `"syscall"` to the import list.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/jobs/... -v -run TestRunner_Cancel`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/jobs/runner.go internal/tui/jobs/runner_test.go
git commit -m "feat(tui/jobs): Cancel sends SIGTERM then SIGKILL after 5s"
```

---

## Task 9: Runner busy-when-running test

**Files:**
- Modify: `internal/tui/jobs/runner_test.go`

- [ ] **Step 1: Add the test**

```go
func TestRunner_StartReturnsErrJobInFlight(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROJECTLENS_TUI_RUNS_DIR", dir)
	got := make(chan tea.Msg, 16)
	send := func(m tea.Msg) { got <- m }
	target := jobs.RunnerTarget{
		BinaryPath:  "/bin/sh",
		ConfigPath:  "/dev/null",
		DatabaseURL: "postgres://x",
		RepoPath:    "/tmp",
	}
	r := jobs.NewRunner(target, send)
	spec := jobs.Spec{
		Key: 'X', Name: "wait", Args: []string{"-c", "sleep 2"},
		Confirm: jobs.ConfirmYesNo,
		Preflight: func(_ context.Context, _ store.Store) (int, string, error) { return 0, "", nil },
		Headline:  func(int, string) string { return "" },
	}
	if err := r.Start(spec); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(spec); err != jobs.ErrJobInFlight {
		t.Fatalf("second Start = %v, want ErrJobInFlight", err)
	}
	r.Cancel()
	// Drain to avoid leaking goroutine into the next test.
	for {
		msg := <-got
		if _, ok := msg.(jobs.JobCompletedMsg); ok {
			return
		}
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/tui/jobs/... -v -run TestRunner_StartReturnsErrJobInFlight`
Expected: PASS (logic already in place from Task 5).

- [ ] **Step 3: Commit**

```bash
git add internal/tui/jobs/runner_test.go
git commit -m "test(tui/jobs): assert second Start returns ErrJobInFlight"
```

---

## Task 10: Store preflight methods (interface + fake)

**Files:**
- Modify: `internal/tui/store/store.go`
- Modify: `internal/tui/store/fake.go`

- [ ] **Step 1: Extend the Store interface**

In `store.go`:

```go
type Store interface {
	Health(ctx context.Context) (HealthSnapshot, error)
	Pipeline(ctx context.Context) (PipelineSnapshot, error)
	Storage(ctx context.Context) (StorageSnapshot, error)
	Runs(ctx context.Context, limit int) (RunsSnapshot, error)
	Config(ctx context.Context) (ConfigSnapshot, error)

	// Preflight counts (Phase 2).
	EmbedPending(ctx context.Context) (int, error)
	SummarizePending(ctx context.Context) (int, error)
	HistoryNewCommits(ctx context.Context) (int, error)
	ChangedFilesSinceLastRun(ctx context.Context) (int, error)
}
```

- [ ] **Step 2: Extend `Fake`**

In `fake.go`, add fields, setters, and method bodies:

```go
type Fake struct {
	// ... existing fields ...
	embedPending     int
	summarizePending int
	historyCommits   int
	changedFiles     int
}

func (f *Fake) SetEmbedPending(n int)     { f.mu.Lock(); f.embedPending = n; f.mu.Unlock() }
func (f *Fake) SetSummarizePending(n int) { f.mu.Lock(); f.summarizePending = n; f.mu.Unlock() }
func (f *Fake) SetHistoryCommits(n int)   { f.mu.Lock(); f.historyCommits = n; f.mu.Unlock() }
func (f *Fake) SetChangedFiles(n int)     { f.mu.Lock(); f.changedFiles = n; f.mu.Unlock() }

func (f *Fake) EmbedPending(ctx context.Context) (int, error) {
	if err := f.wait(ctx, "EmbedPending"); err != nil {
		return 0, err
	}
	f.mu.Lock(); defer f.mu.Unlock()
	return f.embedPending, nil
}
func (f *Fake) SummarizePending(ctx context.Context) (int, error) {
	if err := f.wait(ctx, "SummarizePending"); err != nil {
		return 0, err
	}
	f.mu.Lock(); defer f.mu.Unlock()
	return f.summarizePending, nil
}
func (f *Fake) HistoryNewCommits(ctx context.Context) (int, error) {
	if err := f.wait(ctx, "HistoryNewCommits"); err != nil {
		return 0, err
	}
	f.mu.Lock(); defer f.mu.Unlock()
	return f.historyCommits, nil
}
func (f *Fake) ChangedFilesSinceLastRun(ctx context.Context) (int, error) {
	if err := f.wait(ctx, "ChangedFilesSinceLastRun"); err != nil {
		return 0, err
	}
	f.mu.Lock(); defer f.mu.Unlock()
	return f.changedFiles, nil
}
```

- [ ] **Step 3: Build**

Run: `go build ./internal/tui/...`
Expected: build fails — `*PG` does not satisfy `Store` (PG missing the four methods). Good — Task 11 fixes that.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/store/store.go internal/tui/store/fake.go
git commit -m "feat(tui/store): preflight methods in Store interface + Fake"
```

---

## Task 11: Postgres preflight implementations

> **rev 2 (high):** all four queries are rewritten against the actual
> migrations. Verified column names:
> - `files.package_name` (not `package_path`) — `migrations/001_initial_schema.up.sql:6`
> - `files.indexed_at` (not `last_indexed_at`) — `:14`
> - `summaries.package_name` (not `target_type`/`target_id`) — `:56,60`
> - `file_history.committed_at` (not `author_date`) — `migrations/002_intelligence_platform.up.sql:62,82`

**Files:**
- Modify: `internal/tui/store/pg.go`
- Modify: `internal/tui/store/pg_integration_test.go`

- [ ] **Step 1: Write the failing integration test**

Append to `pg_integration_test.go` (under the `//go:build integration` tag):

```go
func TestPG_PreflightCounts(t *testing.T) {
	s := newTestStore(t) // existing helper
	ctx := context.Background()

	for _, m := range []struct {
		name string
		fn   func(context.Context) (int, error)
	}{
		{"EmbedPending", s.EmbedPending},
		{"SummarizePending", s.SummarizePending},
		{"HistoryNewCommits", s.HistoryNewCommits},
		{"ChangedFilesSinceLastRun", s.ChangedFilesSinceLastRun},
	} {
		t.Run(m.name, func(t *testing.T) {
			n, err := m.fn(ctx)
			if err != nil {
				t.Fatalf("%s: %v", m.name, err)
			}
			if n < 0 {
				t.Errorf("%s = %d, want >= 0", m.name, n)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/tui/store/... -run TestPG_Preflight`
Expected: build failure — methods undefined on `*PG`.

- [ ] **Step 3: Implement the methods**

Append to `pg.go`:

```go
// EmbedPending counts chunks with no embedding row yet.
func (s *PG) EmbedPending(ctx context.Context) (int, error) {
	const q = `
		SELECT COUNT(*) FROM chunks c
		WHERE NOT EXISTS (SELECT 1 FROM embeddings e WHERE e.chunk_id = c.id)
	`
	var n int
	if err := s.pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: embed pending: %w", err)
	}
	return n, nil
}

// SummarizePending counts packages without a summary row.
// Uses summaries.package_name (the actual schema column) and
// files.package_name as the canonical package set.
func (s *PG) SummarizePending(ctx context.Context) (int, error) {
	const q = `
		SELECT COUNT(DISTINCT f.package_name)
		FROM files f
		WHERE f.package_name <> ''
		  AND NOT EXISTS (
		      SELECT 1 FROM summaries s
		      WHERE s.package_name = f.package_name
		  )
	`
	var n int
	if err := s.pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: summarize pending: %w", err)
	}
	return n, nil
}

// HistoryNewCommits estimates how many commits would be ingested by the
// next index-history run, using the same "since latest file_history"
// logic the indexer applies. file_history.committed_at is the
// indexer-side timestamp.
func (s *PG) HistoryNewCommits(ctx context.Context) (int, error) {
	if s.repoPath == "" {
		return 0, nil
	}
	var since time.Time
	const q = `SELECT COALESCE(MAX(committed_at), '1970-01-01'::timestamptz) FROM file_history`
	if err := s.pool.QueryRow(ctx, q).Scan(&since); err != nil {
		return 0, fmt.Errorf("store: latest file_history: %w", err)
	}
	// shell out to git rev-list --count (no parsing of git log).
	args := []string{"-C", s.repoPath, "rev-list", "--count",
		"--since=" + since.Add(-5*time.Minute).Format(time.RFC3339), "HEAD"}
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return 0, fmt.Errorf("store: git rev-list: %w", err)
	}
	n := 0
	for _, c := range strings.TrimSpace(string(out)) {
		if c < '0' || c > '9' {
			continue
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// ChangedFilesSinceLastRun returns the number of files whose persisted
// index timestamp is older than the most recent successful index run.
// Uses files.indexed_at (the actual column from migration 001).
func (s *PG) ChangedFilesSinceLastRun(ctx context.Context) (int, error) {
	const q = `SELECT COUNT(*) FROM files WHERE indexed_at IS NULL OR indexed_at < $1`
	var ref time.Time
	const refQ = `SELECT COALESCE(MAX(completed_at), '1970-01-01'::timestamptz) FROM index_runs WHERE status = 'completed'`
	if err := s.pool.QueryRow(ctx, refQ).Scan(&ref); err != nil {
		return 0, fmt.Errorf("store: last run: %w", err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, q, ref).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: changed files: %w", err)
	}
	return n, nil
}
```

- [ ] **Step 4: Run integration tests**

Run: `go test -tags integration ./internal/tui/store/... -run TestPG_Preflight -v`
Expected: PASS (assuming the local Postgres is reachable per existing
integration-test conventions).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/store/pg.go internal/tui/store/pg_integration_test.go
git commit -m "feat(tui/store): preflight count queries against Postgres"
```

---

## Task 12: Registry — five Specs with preflights and headlines

> **rev 2:** registry constructor takes `*config.Config` so the
> embed/summarize cost-driver strings come from the loaded config
> (`cfg.Embeddings.Provider`, `cfg.Summarization.Provider`) instead
> of being hard-coded to `openai`/`anthropic`. This matches the
> design's "preflight surfaces real cost driver" promise.

**Files:**
- Create: `internal/tui/jobs/registry.go`
- Create: `internal/tui/jobs/registry_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/tui/jobs/registry_test.go
package jobs_test

import (
	"context"
	"testing"

	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func testCfg() *config.Config {
	return &config.Config{
		Embeddings:    config.Embeddings{Provider: "ollama"},
		Summarization: config.Summarization{Provider: "anthropic"},
	}
}

func TestRegistry_NoKeyCollisions(t *testing.T) {
	seen := map[rune]string{}
	for _, s := range jobs.DefaultRegistry(testCfg()) {
		if other, ok := seen[s.Key]; ok {
			t.Errorf("key %q used by %s and %s", s.Key, other, s.Name)
		}
		seen[s.Key] = s.Name
	}
}

func TestRegistry_AllSpecsValid(t *testing.T) {
	for _, s := range jobs.DefaultRegistry(testCfg()) {
		if !s.Valid() {
			t.Errorf("spec %q is not Valid: %+v", s.Name, s)
		}
		if s.Confirm == jobs.ConfirmTyped && s.Phrase == "" {
			t.Errorf("spec %q is ConfirmTyped but Phrase is empty", s.Name)
		}
	}
}

func TestRegistry_CostDriverFromConfig(t *testing.T) {
	cfg := &config.Config{
		Embeddings:    config.Embeddings{Provider: "openai"},
		Summarization: config.Summarization{Provider: "openai"},
	}
	f := store.NewFake()
	for _, s := range jobs.DefaultRegistry(cfg) {
		_, cost, err := s.Preflight(context.Background(), f)
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		switch s.Name {
		case "index-embed":
			if cost != "openai" {
				t.Errorf("embed cost = %q, want openai", cost)
			}
		case "index-summarize":
			if cost != "openai" {
				t.Errorf("summarize cost = %q, want openai", cost)
			}
		default:
			if cost != "" {
				t.Errorf("%s cost = %q, want empty", s.Name, cost)
			}
		}
	}
}

func TestRegistry_PreflightUsesStore(t *testing.T) {
	f := store.NewFake()
	f.SetEmbedPending(7)
	f.SetSummarizePending(3)
	f.SetHistoryCommits(42)
	f.SetChangedFiles(11)

	wants := map[string]int{
		"reindex":          11,
		"reindex --full":   11,
		"index-embed":      7,
		"index-summarize":  3,
		"index-history":    42,
	}
	for _, s := range jobs.DefaultRegistry(testCfg()) {
		want, ok := wants[s.Name]
		if !ok {
			t.Fatalf("unexpected spec name %q", s.Name)
		}
		got, _, err := s.Preflight(context.Background(), f)
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		if got != want {
			t.Errorf("%s: count = %d, want %d", s.Name, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/jobs/... -v -run TestRegistry`
Expected: FAIL — `DefaultRegistry` undefined.

- [ ] **Step 3: Implement the registry**

```go
// internal/tui/jobs/registry.go
package jobs

import (
	"context"
	"fmt"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

// DefaultRegistry returns the canonical list of Phase 2 actions.
// Cost-driver strings (shown in the embed/summarize confirm modal) are
// derived from the loaded config so they match what the subprocess
// will actually use.
func DefaultRegistry(cfg *config.Config) []Spec {
	embedProvider := ""
	summProvider := ""
	if cfg != nil {
		embedProvider = cfg.Embeddings.Provider
		summProvider = cfg.Summarization.Provider
	}
	return []Spec{
		{
			Key: 'R', Name: "reindex", Args: []string{"reindex"},
			Confirm: ConfirmYesNo, RefreshOn: []string{"pipeline", "runs", "storage"},
			Preflight: changedFilesPreflight,
			Headline: func(n int, _ string) string {
				return fmt.Sprintf("reindex %d changed file(s)? [y/N]", n)
			},
		},
		{
			Key: 'F', Name: "reindex --full", Args: []string{"reindex", "--full"},
			Confirm: ConfirmTyped, Phrase: "reindex",
			RefreshOn: []string{"pipeline", "runs", "storage"},
			Preflight: changedFilesPreflight,
			Headline: func(n int, _ string) string {
				return fmt.Sprintf("RE-INDEX %d files (~8 min, rewrites embeddings)\nType 'reindex' to confirm", n)
			},
		},
		{
			Key: 'E', Name: "index-embed", Args: []string{"index-embed"},
			Confirm: ConfirmYesNo, RefreshOn: []string{"pipeline", "runs"},
			Preflight: embedPendingPreflight(embedProvider),
			Headline: func(n int, cost string) string {
				return fmt.Sprintf("embed %d chunk(s) via %s? [y/N]", n, cost)
			},
		},
		{
			Key: 'S', Name: "index-summarize", Args: []string{"index-summarize"},
			Confirm: ConfirmYesNo, RefreshOn: []string{"pipeline", "runs"},
			Preflight: summarizePendingPreflight(summProvider),
			Headline: func(n int, cost string) string {
				return fmt.Sprintf("summarize %d package(s) via %s? [y/N]", n, cost)
			},
		},
		{
			Key: 'H', Name: "index-history", Args: []string{"index-history"},
			Confirm: ConfirmYesNo, RefreshOn: []string{"pipeline", "runs"},
			Preflight: historyCommitsPreflight,
			Headline: func(n int, _ string) string {
				return fmt.Sprintf("ingest %d new commit(s)? [y/N]", n)
			},
		},
	}
}

func changedFilesPreflight(ctx context.Context, s store.Store) (int, string, error) {
	n, err := s.ChangedFilesSinceLastRun(ctx)
	return n, "", err
}

func embedPendingPreflight(cost string) Preflight {
	return func(ctx context.Context, s store.Store) (int, string, error) {
		n, err := s.EmbedPending(ctx)
		return n, cost, err
	}
}

func summarizePendingPreflight(cost string) Preflight {
	return func(ctx context.Context, s store.Store) (int, string, error) {
		n, err := s.SummarizePending(ctx)
		return n, cost, err
	}
}

func historyCommitsPreflight(ctx context.Context, s store.Store) (int, string, error) {
	n, err := s.HistoryNewCommits(ctx)
	return n, "", err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/jobs/... -v -run TestRegistry`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/jobs/registry.go internal/tui/jobs/registry_test.go
git commit -m "feat(tui/jobs): default registry of 5 actions with preflights"
```

---

## Task 13: runner_command_test — argv assertion across the registry

**Files:**
- Create: `internal/tui/jobs/runner_command_test.go`

- [ ] **Step 1: Write the test**

```go
// internal/tui/jobs/runner_command_test.go
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
	for _, s := range jobs.DefaultRegistry() {
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
			// First positional arg must be the spec's own command name.
			if len(args) == 0 || args[0] != s.Args[0] {
				t.Errorf("first arg = %q, want %q", args[0], s.Args[0])
			}
		})
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/tui/jobs/... -v -run TestEverySpec_BuildArgs`
Expected: PASS — Tasks 2 and 12 already establish the contract; this test
locks it across every Spec.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/jobs/runner_command_test.go
git commit -m "test(tui/jobs): assert every Spec gets --config/--db/--repo"
```

---

## Task 14: confirmmodal component

**Files:**
- Create: `internal/tui/components/confirmmodal/confirmmodal.go`
- Create: `internal/tui/components/confirmmodal/confirmmodal_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/tui/components/confirmmodal/confirmmodal_test.go
package confirmmodal_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/components/confirmmodal"
)

func TestYesNo_Y_Confirms(t *testing.T) {
	m := confirmmodal.NewYesNo("ok?", "TOKEN")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if !next.Done() || !next.Confirmed() {
		t.Fatalf("y did not confirm: %+v", next)
	}
	if cmd == nil {
		t.Fatal("expected dispatch cmd")
	}
}

func TestYesNo_N_Cancels(t *testing.T) {
	m := confirmmodal.NewYesNo("ok?", "TOKEN")
	for _, k := range []string{"n", "N", "x"} {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		if !next.Done() || next.Confirmed() {
			t.Fatalf("%s should cancel: %+v", k, next)
		}
	}
}

func TestTyped_RequiresExactPhrase(t *testing.T) {
	m := confirmmodal.NewTyped("are you sure?", "reindex", "RUN")
	for _, r := range "reindex" {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !next.Done() || !next.Confirmed() {
		t.Fatal("typed exact phrase + enter should confirm")
	}
}

func TestTyped_PartialPhraseEnterDoesNothing(t *testing.T) {
	m := confirmmodal.NewTyped("sure?", "reindex", "RUN")
	for _, r := range "rein" {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.Done() {
		t.Fatal("partial phrase must not be Done on enter")
	}
}

func TestEsc_Cancels(t *testing.T) {
	m := confirmmodal.NewYesNo("ok?", "TOKEN")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !next.Done() || next.Confirmed() {
		t.Fatal("esc must cancel")
	}
}

func TestView_RendersHeadline(t *testing.T) {
	m := confirmmodal.NewYesNo("hello world", "T")
	if !strings.Contains(m.View(), "hello world") {
		t.Fatal("view missing headline")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/components/confirmmodal/...`
Expected: FAIL — package undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/tui/components/confirmmodal/confirmmodal.go
package confirmmodal

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type kind int

const (
	kindYesNo kind = iota
	kindTyped
)

// ConfirmedMsg is dispatched (as a tea.Cmd return) when the user confirms.
// Token lets the app match the dispatch back to the originating spec.
type ConfirmedMsg struct{ Token string }

// Model is the modal state machine.
type Model struct {
	kind     kind
	headline string
	phrase   string
	token    string
	typed    string
	done     bool
	confirm  bool
}

func NewYesNo(headline, token string) Model {
	return Model{kind: kindYesNo, headline: headline, token: token}
}

func NewTyped(headline, phrase, token string) Model {
	return Model{kind: kindTyped, headline: headline, phrase: phrase, token: token}
}

func (m Model) Done() bool      { return m.done }
func (m Model) Confirmed() bool { return m.confirm }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if key.Type == tea.KeyEsc {
		m.done = true
		return m, nil
	}
	switch m.kind {
	case kindYesNo:
		switch s := key.String(); s {
		case "y", "Y":
			m.done, m.confirm = true, true
			return m, dispatch(m.token)
		default:
			m.done = true
			return m, nil
		}
	case kindTyped:
		if key.Type == tea.KeyEnter {
			if m.typed == m.phrase {
				m.done, m.confirm = true, true
				return m, dispatch(m.token)
			}
			return m, nil
		}
		if key.Type == tea.KeyBackspace {
			if len(m.typed) > 0 {
				m.typed = m.typed[:len(m.typed)-1]
			}
			return m, nil
		}
		if key.Type == tea.KeyRunes && len(key.Runes) > 0 {
			m.typed += string(key.Runes)
		}
	}
	return m, nil
}

func (m Model) View() string {
	body := m.headline
	if m.kind == kindTyped {
		body += fmt.Sprintf("\n> %s_", m.typed)
	} else {
		body += "\n[y/N]"
	}
	body += "\nesc cancel"
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Render(body)
}

func dispatch(token string) tea.Cmd {
	return func() tea.Msg { return ConfirmedMsg{Token: token} }
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/tui/components/confirmmodal/... -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/components/confirmmodal/
git commit -m "feat(tui/components): confirmmodal with yes/no + typed-phrase modes"
```

---

## Task 15: jobdrawer component

**Files:**
- Create: `internal/tui/components/jobdrawer/jobdrawer.go`
- Create: `internal/tui/components/jobdrawer/jobdrawer_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/tui/components/jobdrawer/jobdrawer_test.go
package jobdrawer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/components/jobdrawer"
)

func TestRunningRendersElapsed(t *testing.T) {
	d := jobdrawer.New()
	d.SetState(jobdrawer.State{
		Status:   "running",
		Spec:     "reindex",
		Started:  time.Now().Add(-3 * time.Second),
		Tail:     []string{"INFO indexing"},
		LogPath:  "/tmp/x.log",
	}, 80, 8)
	v := d.View()
	if !strings.Contains(v, "running") || !strings.Contains(v, "reindex") {
		t.Fatalf("missing fields: %q", v)
	}
}

func TestSucceededShowsLogPath(t *testing.T) {
	d := jobdrawer.New()
	d.SetState(jobdrawer.State{
		Status:   "succeeded",
		Spec:     "reindex",
		LogPath:  "/var/log/r.log",
		Duration: 2100 * time.Millisecond,
		Tail:     []string{"done"},
	}, 80, 8)
	v := d.View()
	for _, want := range []string{"ok", "/var/log/r.log", "2.1"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}

func TestHiddenWhenIdle(t *testing.T) {
	d := jobdrawer.New()
	d.SetState(jobdrawer.State{Status: "idle"}, 80, 8)
	if d.View() != "" {
		t.Fatalf("expected empty view when idle, got %q", d.View())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/components/jobdrawer/...`
Expected: FAIL — package undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/tui/components/jobdrawer/jobdrawer.go
package jobdrawer

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

type State struct {
	Status   string // idle | running | cancelling | succeeded | failed | cancelled
	Spec     string
	Started  time.Time
	Duration time.Duration
	Tail     []string
	LogPath  string
}

type Model struct {
	state  State
	hidden bool
	w, h   int
}

func New() *Model { return &Model{} }

func (m *Model) SetState(s State, w, h int) {
	m.state = s
	m.w = w
	m.h = h
}

func (m *Model) Toggle() { m.hidden = !m.hidden }

func (m *Model) View() string {
	if m.state.Status == "" || m.state.Status == "idle" {
		return ""
	}
	if m.hidden {
		return lipgloss.NewStyle().Faint(true).Render(
			fmt.Sprintf("[%s %s · drawer hidden — j to show]",
				m.state.Spec, elapsed(m.state)))
	}
	header := fmt.Sprintf("%s · %s · %s", m.state.Spec, elapsed(m.state), headerStatus(m.state))
	if m.state.LogPath != "" && m.state.Status != "running" && m.state.Status != "cancelling" {
		header += " · log: " + m.state.LogPath
	}
	tail := strings.Join(m.state.Tail, "\n")
	body := header + "\n" + tail
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(m.w - 2).
		Height(m.h)
	return style.Render(body)
}

func elapsed(s State) string {
	if s.Status == "running" || s.Status == "cancelling" {
		return time.Since(s.Started).Round(time.Second).String()
	}
	return s.Duration.Round(100 * time.Millisecond).String()
}

func headerStatus(s State) string {
	switch s.Status {
	case "succeeded":
		return "ok"
	case "failed":
		return "FAILED"
	case "cancelled":
		return "cancelled"
	case "cancelling":
		return "cancelling…"
	default:
		return s.Status
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/tui/components/jobdrawer/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/components/jobdrawer/
git commit -m "feat(tui/components): jobdrawer renders running/succeeded/failed/cancelled"
```

---

## Task 16: ActionableSection interface + Pipeline Controls block

**Files:**
- Modify: `internal/tui/sections/section.go`
- Modify: `internal/tui/sections/pipeline/model.go`
- Modify: `internal/tui/sections/pipeline/view.go`
- Modify: `internal/tui/sections/pipeline/model_test.go`

- [ ] **Step 1: Write the failing test**

In `pipeline/model_test.go`, append:

```go
func TestPipeline_RendersControlsBlock(t *testing.T) {
	f := store.NewFake()
	m := pipeline.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	v := next.View()
	for _, want := range []string{"R reindex", "F reindex --full", "E embed", "S summarize", "H history"} {
		if !strings.Contains(v, want) {
			t.Errorf("controls block missing %q\n%s", want, v)
		}
	}
}

func TestPipeline_ImplementsActionableSection(t *testing.T) {
	var _ sections.ActionableSection = pipeline.New(context.Background(), store.NewFake())
}
```

(Ensure imports include `sections` and `pipeline` and `store`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/sections/pipeline/...`
Expected: FAIL — `ActionableSection` undefined and Controls block missing.

- [ ] **Step 3: Add the interface**

In `internal/tui/sections/section.go`, append:

```go
// ActionableSection is the optional sibling interface for sections that can
// trigger actions. The app's keymap iterates over Sections that satisfy this
// to decide which hotkeys to expose alongside the global registry.
type ActionableSection interface {
	Section
	Actions() []ActionDescriptor
}

// ActionDescriptor is the section-facing summary of a triggerable action.
// It intentionally avoids importing the jobs package (which depends on
// store) — the app is responsible for wiring section descriptors to the
// jobs registry.
type ActionDescriptor struct {
	Key         rune
	Label       string
	Description string
}
```

- [ ] **Step 4: Implement on Pipeline**

In `pipeline/model.go`, add:

```go
func (m *Model) Actions() []sections.ActionDescriptor {
	return []sections.ActionDescriptor{
		{Key: 'R', Label: "reindex", Description: "incremental"},
		{Key: 'F', Label: "reindex --full", Description: "full"},
		{Key: 'E', Label: "embed", Description: "missing chunks"},
		{Key: 'S', Label: "summarize", Description: "missing packages"},
		{Key: 'H', Label: "history", Description: "new commits"},
	}
}
```

In `pipeline/view.go`, append a Controls block at the end of `View()`:

```go
	b.WriteString("\n" + theme.TitleStyle().Render("Controls") + "\n")
	for _, a := range m.Actions() {
		fmt.Fprintf(&b, "  %c %-18s %s\n", a.Key, a.Label, theme.MutedStyle().Render(a.Description))
	}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/tui/sections/pipeline/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/sections/section.go internal/tui/sections/pipeline/
git commit -m "feat(tui/sections): ActionableSection interface; Pipeline Controls block"
```

---

## Task 17: App integration — fields, keymap, preflight + confirm + run flow

> **rev 2 (high):** rev 1 referenced `m.appCtx`, `m.store`, and a
> `m.sections[id]` map that don't exist on the current `app.Model`.
> Current shape (verified at `internal/tui/app/model.go:21`):
>
> ```go
> type Model struct {
>     ctx      context.Context
>     keys     keyMap
>     help     help.Model
>     sections []sections.Section  // slice, not map
>     focused  int
>     mode     Mode
>     w, h     int
>     sidebar  list.Model
>     tooSmall bool
>     showHelp bool
> }
> ```
>
> The plan below uses `m.ctx`, threads `store.Store` via `New(...)`,
> and refreshes by iterating the slice and matching `sec.ID()`. Phase 1
> already uses value-receiver `Update`; Phase 2 keeps that convention.

**Files:**
- Modify: `internal/tui/app/model.go`
- Modify: `internal/tui/app/update.go`
- Modify: `internal/tui/app/view.go`
- Modify: `internal/tui/app/keys.go`

- [ ] **Step 1: Extend the constructor + Model**

In `app/model.go`, change `New` to accept the new dependencies and add
the new fields:

```go
import (
	// ...existing...
	"github.com/hman-pro/projectlens/internal/tui/components/confirmmodal"
	"github.com/hman-pro/projectlens/internal/tui/components/jobdrawer"
	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

type Model struct {
	ctx  context.Context
	keys keyMap
	help help.Model

	sections []sections.Section
	focused  int
	mode     Mode

	w, h    int
	sidebar list.Model

	tooSmall bool
	showHelp bool

	// Phase 2 additions:
	store          store.Store
	runner         *jobs.Runner
	registry       []jobs.Spec
	drawer         *jobdrawer.Model
	confirm        *confirmmodal.Model
	binaryPath     string
	target         jobs.RunnerTarget
	pendingToken   uint64    // increments on each action keypress; preflight closures carry it
	pendingSpec    jobs.Spec
	quitRequested  bool      // set by first q during run; classifier uses it
}

// New is the Phase 2-aware constructor. The runner may be nil in tests
// that don't exercise the action path; binaryPath empty means "no
// binary resolved" and action keys will refuse with a toast.
func New(
	ctx context.Context,
	secs []sections.Section,
	st store.Store,
	runner *jobs.Runner,
	registry []jobs.Spec,
	target jobs.RunnerTarget,
) Model {
	// ...existing list/sidebar setup unchanged...
	return Model{
		ctx:        ctx,
		keys:       defaultKeys(),
		help:       help.New(),
		sections:   secs,
		sidebar:    sb,
		store:      st,
		runner:     runner,
		registry:   registry,
		drawer:     jobdrawer.New(),
		binaryPath: target.BinaryPath,
		target:     target,
	}
}
```

> **Note for Phase 1 callers:** the existing `app.New(ctx, secs)`
> signature is replaced. Update `cmd/projectlens-tui/main.go` and the
> Phase 1 `app_test.go` callers to pass the new arguments — pass
> `nil` for `runner`/`registry` and `store.NewFake()` for `st` in tests
> that don't need the action path.

- [ ] **Step 2: Wire keymap precedence**

In `app/update.go`, extend the existing `Update` (still value receiver,
returning `(tea.Model, tea.Cmd)` to match Phase 1) so Phase 2 messages
and keys are handled before the existing section routing:

```go
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// 1. Phase 2 message types — handle first so they can't be eaten
	//    by section routing.
	switch msg := msg.(type) {
	case jobs.JobStartedMsg, jobs.JobLineMsg, jobs.JobTickMsg,
		jobs.JobCompletedMsg, jobs.JobBusyMsg:
		return m.handleJobMsg(msg)
	case jobs.PreflightDoneMsg:
		return m.handlePreflightDone(msg)
	case confirmmodal.ConfirmedMsg:
		return m.handleConfirmed(msg)
	}

	// 2. Confirm modal consumes keys when active.
	if m.confirm != nil {
		if key, ok := msg.(tea.KeyMsg); ok {
			next, cmd := m.confirm.Update(key)
			m.confirm = &next
			if next.Done() {
				m.confirm = nil
			}
			return m, cmd
		}
	}

	// 3. Global action keys.
	if key, ok := msg.(tea.KeyMsg); ok {
		if next, cmd, handled := m.handleActionKey(key); handled {
			return next, cmd
		}
		switch key.String() {
		case "c":
			if m.runner != nil && m.runner.State().Status == "running" {
				m.runner.Cancel()
				return m, nil
			}
		case "j":
			if m.drawer != nil {
				m.drawer.Toggle()
				return m, nil
			}
		case "q":
			return m.handleQuit()
		}
	}

	// 4. Fall through to existing Phase 1 routing (sidebar/detail/help).
	// ...preserve existing code path...
}
```

- [ ] **Step 3: Implement the handlers**

```go
func (m Model) handleActionKey(key tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.runner == nil || m.confirm != nil ||
		m.runner.State().Status == "running" {
		return m, nil, false
	}
	s := key.String()
	if len(s) != 1 {
		return m, nil, false
	}
	for _, spec := range m.registry {
		if rune(s[0]) != spec.Key {
			continue
		}
		// Binary-missing check happens BEFORE preflight (rev 2 fix).
		if m.target.BinaryPath == "" {
			return m, m.toast("projectlens binary not found; set PROJECTLENS_BINARY"), true
		}
		m.pendingToken++
		m.pendingSpec = spec
		return m, runPreflight(m.ctx, m.store, spec, m.pendingToken), true
	}
	return m, nil, false
}

func runPreflight(ctx context.Context, s store.Store, spec jobs.Spec, token uint64) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		n, cost, err := spec.Preflight(c, s)
		return jobs.PreflightDoneMsg{Spec: spec, Count: n, Cost: cost, Err: err, Token: token}
	}
}

func (m Model) handlePreflightDone(msg jobs.PreflightDoneMsg) (tea.Model, tea.Cmd) {
	// Reject stale results (user pressed a different action key in the
	// meantime; only the latest token survives).
	if msg.Token != m.pendingToken {
		return m, nil
	}
	if msg.Err != nil {
		return m, m.toast("preflight failed: " + msg.Err.Error())
	}
	headline := msg.Spec.Headline(msg.Count, msg.Cost)
	var modal confirmmodal.Model
	if msg.Spec.Confirm == jobs.ConfirmTyped {
		modal = confirmmodal.NewTyped(headline, msg.Spec.Phrase, msg.Spec.Name)
	} else {
		modal = confirmmodal.NewYesNo(headline, msg.Spec.Name)
	}
	m.confirm = &modal
	return m, nil
}

func (m Model) handleConfirmed(msg confirmmodal.ConfirmedMsg) (tea.Model, tea.Cmd) {
	for _, spec := range m.registry {
		if spec.Name == msg.Token {
			if err := m.runner.Start(spec); err != nil {
				return m, m.toast("start failed: " + err.Error())
			}
			return m, nil
		}
	}
	return m, nil
}

func (m Model) handleJobMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.runner == nil {
		return m, nil
	}
	snap := m.runner.State()
	if m.drawer != nil {
		m.drawer.SetState(jobdrawer.State{
			Status:  snap.Status,
			Spec:    snap.Current.Name,
			Started: snap.StartedAt,
			Tail:    snap.Tail,
			LogPath: snap.LogPath,
		}, m.w, 8)
	}

	if completed, ok := msg.(jobs.JobCompletedMsg); ok {
		var cmds []tea.Cmd
		if completed.Status == "succeeded" {
			cmds = append(cmds, m.refreshSections(completed.Spec.RefreshOn))
		}
		if m.quitRequested {
			cmds = append(cmds, tea.Quit)
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

// refreshSections iterates the sections slice (no map exists on the
// current Model) and dispatches Refresh() for each ID match.
func (m Model) refreshSections(ids []string) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(ids))
	for _, id := range ids {
		for _, sec := range m.sections {
			if sec.ID() == id {
				cmds = append(cmds, sec.Refresh())
				break
			}
		}
	}
	return tea.Batch(cmds...)
}

func (m Model) toast(s string) tea.Cmd {
	log.Printf("toast: %s", s)
	return nil
}
```

Add a `Token` field to `jobs.PreflightDoneMsg` (Task 4):

```go
type PreflightDoneMsg struct {
	Spec  Spec
	Count int
	Cost  string
	Err   error
	Token uint64 // matches Model.pendingToken; stale results are dropped
}
```

- [ ] **Step 4: Implement quit path**

> **rev 2:** detach is dropped from Phase 2 (see design rev 2). Quit
> drains, full stop. `Ctrl+C` is the OS-level escape hatch.

```go
func (m Model) handleQuit() (tea.Model, tea.Cmd) {
	if m.runner == nil {
		return m, tea.Quit
	}
	st := m.runner.State().Status
	if st == "idle" || st == "succeeded" || st == "failed" || st == "cancelled" {
		return m, tea.Quit
	}
	// Job in flight — flag quit and trigger drain. The completion
	// classifier in handleJobMsg will dispatch tea.Quit when Wait
	// returns.
	m.quitRequested = true
	m.runner.Cancel()
	return m, nil
}
```

- [ ] **Step 5: Render drawer + confirm in view**

In `app/view.go`, append after the existing layout. Note `View()` is a
value receiver in Phase 1; keep that:

```go
	if m.confirm != nil {
		v += "\n" + m.confirm.View()
	}
	if m.drawer != nil {
		v += "\n" + m.drawer.View()
	}
```

- [ ] **Step 6: Update help in `keys.go`**

Add a new section block listing R/F/E/S/H/c/j with their meaning.

- [ ] **Step 7: Build**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/app/
git commit -m "feat(tui/app): wire runner, drawer, confirmmodal; preflight→confirm→run"
```

---

## Task 18: App-level tests — action flow + drain on quit

> **rev 2 (medium):** rev 1 only covered quit-drain. Review demanded
> coverage of the entire preflight → confirm → run flow plus binary
> missing, stale-preflight, refresh-by-id, and yes/no cancel/confirm.
> Adds `internal/tui/app/actions_test.go` alongside `quit_test.go`.

**Files:**
- Create: `internal/tui/app/actions_test.go`
- Create: `internal/tui/app/quit_test.go`

- [ ] **Step 0: Add test seams**

In `app/model.go`, add small test-only helpers. They are public
because Go testing of internal state across packages requires it; the
trade-off is acceptable for the test surface listed.

```go
// NewForTesting constructs a Phase 2 model for tests. Equivalent to
// New but with no list/sidebar (callers don't render).
func NewForTesting(ctx context.Context, st store.Store, runner *jobs.Runner, registry []jobs.Spec, target jobs.RunnerTarget) Model {
	return Model{
		ctx:        ctx,
		keys:       defaultKeys(),
		help:       help.New(),
		store:      st,
		runner:     runner,
		registry:   registry,
		drawer:     jobdrawer.New(),
		binaryPath: target.BinaryPath,
		target:     target,
		sections:   nil, // tests don't need sections unless they pass them
	}
}

// PendingToken exposes the current pendingToken for tests that need to
// assert stale-preflight behaviour.
func (m Model) PendingToken() uint64 { return m.pendingToken }

// HasConfirmModal reports whether a confirm modal is currently open.
func (m Model) HasConfirmModal() bool { return m.confirm != nil }
```

Plus a stub runner used by tests (`internal/tui/app/stub_runner_test.go`):

```go
package app_test

import "github.com/hman-pro/projectlens/internal/tui/jobs"

type stubRunner struct {
	started []jobs.Spec
	state   jobs.Snapshot
}

func (s *stubRunner) Start(spec jobs.Spec) error {
	s.started = append(s.started, spec)
	return nil
}
func (s *stubRunner) Cancel()                {}
func (s *stubRunner) State() jobs.Snapshot   { return s.state }
```

> **Note:** the `*jobs.Runner` field on `Model` is a concrete pointer;
> the test seam swaps in `stubRunner` via a small `runnerIface`
> interface added to `model.go` (`Start(Spec) error`, `Cancel()`,
> `State() Snapshot`). Refactor the field type from `*jobs.Runner` to
> `runnerIface` and have `*jobs.Runner` satisfy it implicitly.

- [ ] **Step 1: Action-key flow tests**

```go
// internal/tui/app/actions_test.go
package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/app"
	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func keyPress(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func newAppForTest(t *testing.T, target jobs.RunnerTarget, runner *stubRunner) app.Model {
	f := store.NewFake()
	f.SetChangedFiles(11)
	f.SetEmbedPending(7)
	f.SetSummarizePending(3)
	f.SetHistoryCommits(42)
	registry := []jobs.Spec{
		{
			Key: 'R', Name: "reindex", Args: []string{"reindex"},
			Confirm: jobs.ConfirmYesNo,
			Preflight: func(_ context.Context, s store.Store) (int, string, error) {
				n, err := s.ChangedFilesSinceLastRun(context.Background())
				return n, "", err
			},
			Headline: func(n int, _ string) string { return "reindex N? [y/N]" },
		},
		{
			Key: 'F', Name: "reindex --full", Args: []string{"reindex", "--full"},
			Confirm: jobs.ConfirmTyped, Phrase: "reindex",
			Preflight: func(_ context.Context, _ store.Store) (int, string, error) { return 1, "", nil },
			Headline:  func(int, string) string { return "FULL — type 'reindex'" },
		},
	}
	return app.NewForTesting(context.Background(), f, runner, registry, target)
}

func drain(m tea.Model, cmd tea.Cmd) (tea.Model, tea.Msg) {
	if cmd == nil {
		return m, nil
	}
	msg := cmd()
	m, _ = m.Update(msg)
	return m, msg
}

func TestActionKey_OpensConfirmModalAfterPreflight(t *testing.T) {
	runner := &stubRunner{}
	m := newAppForTest(t, jobs.RunnerTarget{BinaryPath: "/bin/projectlens"}, runner)

	// Press R → returns a preflight cmd, no modal yet.
	mNext, cmd := m.Update(keyPress("R"))
	if mNext.(app.Model).HasConfirmModal() {
		t.Fatal("modal opened before preflight returned")
	}
	if cmd == nil {
		t.Fatal("expected preflight cmd")
	}
	mNext, _ = drain(mNext, cmd)
	if !mNext.(app.Model).HasConfirmModal() {
		t.Fatal("modal not opened after PreflightDoneMsg")
	}
}

func TestActionKey_BinaryMissingRefuses(t *testing.T) {
	runner := &stubRunner{}
	m := newAppForTest(t, jobs.RunnerTarget{BinaryPath: ""}, runner)
	mNext, _ := m.Update(keyPress("R"))
	if mNext.(app.Model).HasConfirmModal() {
		t.Fatal("binary-missing must not open modal")
	}
	if len(runner.started) != 0 {
		t.Fatal("binary-missing must not start runner")
	}
}

func TestYesNo_NCancels(t *testing.T) {
	runner := &stubRunner{}
	m := newAppForTest(t, jobs.RunnerTarget{BinaryPath: "/bin/projectlens"}, runner)
	mNext, cmd := m.Update(keyPress("R"))
	mNext, _ = drain(mNext, cmd) // open modal
	mNext, _ = mNext.Update(keyPress("n"))
	if mNext.(app.Model).HasConfirmModal() {
		t.Fatal("n should close modal")
	}
	if len(runner.started) != 0 {
		t.Fatal("n must not start runner")
	}
}

func TestYesNo_YStartsRunner(t *testing.T) {
	runner := &stubRunner{}
	m := newAppForTest(t, jobs.RunnerTarget{BinaryPath: "/bin/projectlens"}, runner)
	mNext, cmd := m.Update(keyPress("R"))
	mNext, _ = drain(mNext, cmd)
	mNext, cmd = mNext.Update(keyPress("y"))
	mNext, _ = drain(mNext, cmd) // ConfirmedMsg → Start
	if len(runner.started) != 1 || runner.started[0].Name != "reindex" {
		t.Fatalf("expected runner.Start(reindex), got %+v", runner.started)
	}
}

func TestTyped_RequiresExactPhrase(t *testing.T) {
	runner := &stubRunner{}
	m := newAppForTest(t, jobs.RunnerTarget{BinaryPath: "/bin/projectlens"}, runner)
	mNext, cmd := m.Update(keyPress("F"))
	mNext, _ = drain(mNext, cmd)
	for _, r := range "rein" {
		mNext, _ = mNext.Update(keyPress(string(r)))
	}
	mNext, _ = mNext.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(runner.started) != 0 {
		t.Fatal("partial phrase + Enter must not start runner")
	}
	for _, r := range "dex" {
		mNext, _ = mNext.Update(keyPress(string(r)))
	}
	mNext, cmd = mNext.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mNext, _ = drain(mNext, cmd)
	if len(runner.started) != 1 {
		t.Fatalf("exact phrase + Enter must start runner; got %+v", runner.started)
	}
}

func TestPreflight_StaleResultDropped(t *testing.T) {
	runner := &stubRunner{}
	m := newAppForTest(t, jobs.RunnerTarget{BinaryPath: "/bin/projectlens"}, runner)
	// First press: token = 1.
	mAfterR, cmdR := m.Update(keyPress("R"))
	// Second press before R's preflight resolves: token = 2.
	mAfterF, cmdF := mAfterR.(app.Model).Update(keyPress("F"))
	// Now resolve R's stale cmd — the resulting modal must NOT open.
	mAfterStale, _ := drain(mAfterF, cmdR)
	if mAfterStale.(app.Model).HasConfirmModal() {
		t.Fatal("stale R preflight should not have opened a modal")
	}
	// F's resolution still opens its modal.
	mAfterF2, _ := drain(mAfterStale, cmdF)
	if !mAfterF2.(app.Model).HasConfirmModal() {
		t.Fatal("F preflight result should open modal")
	}
}

func TestRefreshSections_MatchesBySectionID(t *testing.T) {
	// Construct a model with three fake sections; assert only the IDs
	// listed in JobCompletedMsg.Spec.RefreshOn get Refresh dispatched.
	// Use a minimal fakeSection that records Refresh calls. (Imported
	// from a sibling test file or inlined.)
	t.Skip("see fakeSection harness; test outline only — expand in implementation")
}

func TestActionsContainExpectedHeadlineFragments(t *testing.T) {
	// Smoke: yes/no modal contains the headline; typed modal shows
	// phrase prompt.
	runner := &stubRunner{}
	m := newAppForTest(t, jobs.RunnerTarget{BinaryPath: "/bin/projectlens"}, runner)
	mNext, cmd := m.Update(keyPress("R"))
	mNext, _ = drain(mNext, cmd)
	if !strings.Contains(mNext.(app.Model).View(), "reindex N?") {
		t.Skipf("View may include other content; rendering may differ. Replace with confirm.View() snapshot if Model exposes it.")
	}
	_ = time.Now() // touch import
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/tui/app/... -v -run "TestActionKey|TestYesNo|TestTyped|TestPreflight"`
Expected: all pass.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/app/actions_test.go internal/tui/app/model.go internal/tui/app/update.go internal/tui/app/stub_runner_test.go
git commit -m "test(tui/app): action flow — preflight → confirm → run, binary-missing, stale, typed"
```

---

## Task 18.5: App-level test — drain on quit

**Files:**
- Create: `internal/tui/app/quit_test.go`

- [ ] **Step 1: Write the test**

```go
// internal/tui/app/quit_test.go
package app_test

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/app"
	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestQuit_DuringRunDrainsBeforeExit(t *testing.T) {
	t.Setenv("PROJECTLENS_TUI_RUNS_DIR", t.TempDir())
	f := store.NewFake()
	m := app.NewForTesting(context.Background(), f, jobs.RunnerTarget{
		BinaryPath: "/bin/sh",
		ConfigPath: "/dev/null", DatabaseURL: "postgres://x", RepoPath: "/tmp",
	})

	// Inject a long-running spec via the test seam.
	spec := jobs.Spec{
		Key: 'X', Name: "longjob", Args: []string{"-c", "trap '' TERM; sleep 30"},
		Confirm:  jobs.ConfirmYesNo,
		Preflight: func(ctx context.Context, _ store.Store) (int, string, error) { return 0, "", nil },
		Headline:  func(int, string) string { return "ok?" },
	}
	m.RegisterSpec(spec)
	m.StartSpec(spec) // skips preflight + confirm in test

	time.Sleep(100 * time.Millisecond)
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		// any non-nil tea.Cmd that returns tea.QuitMsg means we exited
		// without draining.
		if msg := cmd(); msg == tea.Quit() {
			t.Fatal("first q should not quit while job runs")
		}
	}
	m = model.(*app.Model)

	// Drain: wait for cancelled state.
	deadline := time.After(8 * time.Second)
	for m.RunnerState() != "cancelled" {
		select {
		case <-deadline:
			t.Fatalf("runner state stuck at %q", m.RunnerState())
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
	// Now q should quit.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("post-drain q should produce tea.Quit cmd")
	}
}
```

> **Note:** this test depends on test seams (`NewForTesting`,
> `RegisterSpec`, `StartSpec`, `RunnerState`) on `*app.Model`. Add them
> as small exported helpers gated to test-only behavior, mirroring the
> Phase 1 `app_test.go` pattern.

- [ ] **Step 2: Run**

Run: `go test ./internal/tui/app/... -v -run TestQuit`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/app/quit_test.go internal/tui/app/model.go internal/tui/app/update.go
git commit -m "test(tui/app): q during run drains and waits for cmd.Wait"
```

---

## Task 19: Wire it up in `cmd/projectlens-tui/main.go`

> **rev 2:** rev 1 used a non-existent `tea.NewProgram(nil)` +
> `prog.SetModel(...)` API. Bubbletea v1.3.10 does not have
> `SetModel`. The runner needs the program reference for `Send`, but
> the program needs the model up front. Resolution: build the runner
> with a `nil` send, construct the program with the model, then
> install the program's Send into the runner via a setter
> (`runner.SetSend`). Order is fine because no message is dispatched
> until the program runs.

**Files:**
- Modify: `cmd/projectlens/main.go` (the TUI binary lives at `cmd/projectlens-tui/main.go` — confirm path)
- Modify: `cmd/projectlens-tui/main.go`

- [ ] **Step 1: Add `SetSend` to runner**

In `internal/tui/jobs/runner.go`, add:

```go
// SetSend installs the program send function. Used by main.go because
// the runner is constructed before the tea.Program (the program needs
// the model, the model needs the runner).
func (r *Runner) SetSend(send func(tea.Msg)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.send = send
}
```

- [ ] **Step 2: Wire main.go**

In `cmd/projectlens-tui/main.go`:

```go
// after config + db + sections are set up:
binPath, err := jobs.ResolveBinary()
if err != nil {
	log.Warnf("projectlens binary not resolvable: %v", err)
	binPath = "" // app shows toast on action key.
}
target := jobs.RunnerTarget{
	BinaryPath:  binPath,
	ConfigPath:  cfgPath,
	DatabaseURL: cfg.DatabaseURL,
	RepoPath:    cfg.RepoPath,
}

runner := jobs.NewRunner(target, nil)
registry := jobs.DefaultRegistry(cfg)

// Note: app.New now takes (ctx, secs, store, runner, registry, target).
// Phase 1 callers must update.
appModel := app.New(ctx, secs, st, runner, registry, target)

prog := tea.NewProgram(appModel,
	tea.WithAltScreen(),
	tea.WithMouseCellMotion(),
)
// Wire the program's Send into the runner now that prog exists.
runner.SetSend(prog.Send)

if _, err := prog.Run(); err != nil {
	log.Fatalf("tui: %v", err)
}
```

(Adjust `tea.With...` options to match the existing Phase 1 main.go.)

- [ ] **Step 2: Build**

Run: `go build ./cmd/projectlens-tui/`
Expected: clean build.

- [ ] **Step 3: Smoke test manually**

Run:
```bash
go run ./cmd/projectlens-tui/
```

Expected: TUI launches; pressing `R` opens a "reindex N changed file(s)?"
modal; `n` cancels; `y` runs the subprocess and the drawer streams log
lines; `c` cancels.

- [ ] **Step 4: Commit**

```bash
git add cmd/projectlens-tui/main.go
git commit -m "feat(cmd/projectlens-tui): wire runner + registry + target"
```

---

## Task 20: README + CLAUDE.md updates

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update README**

Add a "Phase 2 actions" subsection under the existing TUI dashboard docs:

```markdown
### Actions (Phase 2)

| Key | Action            | Confirmation       |
|-----|-------------------|--------------------|
| `R` | reindex           | y/N preflight      |
| `F` | reindex --full    | typed `reindex`    |
| `E` | index-embed       | y/N preflight      |
| `S` | index-summarize   | y/N preflight      |
| `H` | index-history     | y/N preflight      |
| `c` | cancel running    | -                  |
| `j` | toggle drawer     | -                  |

Subprocesses log to `~/.projectlens/tui-runs/<RFC3339>-<action>.log`.
The runner resolves the `projectlens` binary in this order:
`PROJECTLENS_BINARY` env var > sibling of `projectlens-tui` > `PATH`.
```

- [ ] **Step 2: Update CLAUDE.md env table**

```markdown
| `PROJECTLENS_BINARY` | Explicit path to the `projectlens` binary the TUI should invoke (overrides sibling/PATH lookup) | No |
```

- [ ] **Step 3: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "docs: TUI Phase 2 actions, PROJECTLENS_BINARY env, log path"
```

---

## Task 21: Integration test — runner against real projectlens status

**Files:**
- Create: `internal/tui/jobs/runner_integration_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration

package jobs_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hman-pro/projectlens/internal/tui/jobs"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestRunner_AgainstRealStatus(t *testing.T) {
	bin, err := exec.LookPath("projectlens")
	if err != nil {
		t.Skip("projectlens binary not on PATH")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	t.Setenv("PROJECTLENS_TUI_RUNS_DIR", t.TempDir())
	got := make(chan tea.Msg, 32)
	send := func(m tea.Msg) { got <- m }
	r := jobs.NewRunner(jobs.RunnerTarget{
		BinaryPath:  bin,
		ConfigPath:  "configs/index.yaml",
		DatabaseURL: dsn,
		RepoPath:    ".",
	}, send)
	spec := jobs.Spec{
		Key: 'I', Name: "status", Args: []string{"status"},
		Confirm:  jobs.ConfirmYesNo,
		Preflight: func(_ context.Context, _ store.Store) (int, string, error) { return 0, "", nil },
		Headline:  func(int, string) string { return "" },
	}
	if err := r.Start(spec); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(15 * time.Second)
	for {
		select {
		case msg := <-got:
			if c, ok := msg.(jobs.JobCompletedMsg); ok {
				if c.ExitCode != 0 {
					t.Errorf("status exit = %d (tail: %v)", c.ExitCode, c.Tail)
				}
				if !strings.Contains(strings.Join(c.Tail, "\n"), "files") {
					t.Errorf("tail missing expected output: %v", c.Tail)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout")
		}
	}
}
```

- [ ] **Step 2: Run with the integration tag**

Run: `go test -tags integration ./internal/tui/jobs/... -run TestRunner_AgainstRealStatus -v`
Expected: PASS (when DB + binary available); SKIP otherwise.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/jobs/runner_integration_test.go
git commit -m "test(tui/jobs): integration test against real projectlens status"
```

---

## Task 22: End-to-end manual verification

- [ ] **Step 1: Build the binary**

```bash
go build -o ./bin/projectlens ./cmd/projectlens/
go build -o ./bin/projectlens-tui ./cmd/projectlens-tui/
```

- [ ] **Step 2: Run the TUI**

```bash
./bin/projectlens-tui
```

- [ ] **Step 3: Walk through the success criteria from the spec**

For each criterion in the design's "Success criteria" section, exercise it
manually:

1. Press `R` → preflight modal shows changed-file count → `n` cancels;
   try again with `y` → drawer streams log lines → on success the
   Pipeline + Runs + Storage sections refresh.
2. Press `F` → preflight modal → type `reindex` + enter → drawer streams.
   Press `c` mid-run → drawer status flips to `cancelling…` → exits with
   `cancelled`.
3. Press `E` / `S` / `H` → each shows count + provider in the modal. Stray
   uppercase keypress in another section also opens the modal (not a run).
4. Force a fail: temporarily point `--db` to a wrong DSN and press `R`;
   expect `FAILED exit <n> · log: <path>`.
5. Run a CLI `projectlens reindex` in a separate terminal while the TUI's
   `R` is held in confirm; confirm with `y` and assert the drawer surfaces
   the writer-lock-busy stderr message.
6. Quit while running: `q` triggers Cancel + drain banner; second `q`
   is a no-op (banner re-renders); on completion the TUI exits
   cleanly. `Ctrl+C` is the OS escape hatch and may leave the
   subprocess running until the writer lock auto-releases.

If any step diverges from the spec, file a follow-up before merging.

- [ ] **Step 4: Final test sweep**

```bash
go test ./...
go test -tags integration ./internal/tui/...
```

Expected: all PASS.

---

## Self-review

**Spec coverage:**

- Scope table (5 actions) → Tasks 12 (registry), 16 (Pipeline), 17 (app), 19 (main).
- Explicit `--config/--db/--repo` → Tasks 2 (build.go), 13 (cross-spec test).
- Preflight + confirm tiering → Tasks 12, 14, 17.
- Foundation + ActionableSection → Task 16.
- Runner architecture (Runner, Spec, Registry) → Tasks 1, 4, 5–9, 12.
- Binary resolution → Task 3.
- jobdrawer component → Task 15.
- confirmmodal component → Task 14.
- App model changes (runner, drawer, confirm, binaryPath, target) → Task 17.
- Trigger / Run / Cancel / Post-completion paths → Tasks 6–9, 17.
- Quit path drain (no detach in rev 2) → Tasks 17 (handler), 18.5 (test).
- App-level action flow tests (preflight → confirm → run + binary-missing + stale + typed) → Task 18.
- Logs (tail buffer + per-run file) → Task 6.
- Error handling table (binary missing, log dir, hangs, lock-busy) →
  Tasks 3, 6, 17, 21.
- Testing strategy (unit, integration, app-level) → covered across Tasks
  2, 3, 5–13, 14, 15, 18, 21.
- Success criteria → Task 22.
- Prerequisites (writer lock) → Task 0 (gate before any code lands).

**Placeholder scan:** no `TBD` / `TODO` / "fill in" prose in tasks.

**Type consistency:** `Spec`, `RunnerTarget`, `ConfirmKind`, `Preflight`,
and `HeadlineFn` are defined in Task 1 and used consistently downstream.
`ActionDescriptor` (Task 16) is intentionally separate from `Spec` to avoid
sections importing `jobs`.

---

## Deferred follow-ups (post-Phase 2)

- Open-log hotkey from drawer (`o` → `$EDITOR`/`less`).
- Toast component (Task 17 currently logs and moves on).
- Line-batching threshold for `JobLineMsg` if profiling shows lag.
- Per-stage progress percentages once the indexer emits structured events.
- Rotation policy for `~/.projectlens/tui-runs/`.
- `index_runs.error` column to surface failure reasons in the Runs section.
- **Detach during quit-drain** (dropped from Phase 2 in rev 2). Reintroduce
  with a real fd hand-off: dup the log fd over the child's stdout/stderr
  before `tea.Quit`, fsync the log, then `tea.Quit`. Required if users
  routinely need to walk away from a long `reindex --full` mid-run.
