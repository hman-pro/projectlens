package jobs

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
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrJobInFlight is returned by Start when another job is already
// running.
var ErrJobInFlight = errors.New("job already in flight")

// ErrInvalidSpec is returned by Start when the supplied Spec is not
// Valid.
var ErrInvalidSpec = errors.New("invalid spec")

// Runner is a single-slot subprocess executor.
type Runner struct {
	target RunnerTarget

	mu              sync.Mutex
	send            func(tea.Msg)
	status          string
	current         Spec
	startedAt       time.Time
	cmd             *exec.Cmd
	cancelFn        context.CancelFunc
	cancelRequested bool
	tail            *ringBuffer
	logPath         string
}

// Snapshot is the runner's externally observable state.
type Snapshot struct {
	Status    string
	Current   Spec
	StartedAt time.Time
	LogPath   string
	Tail      []string
}

// NewRunner builds a fresh runner. The send func is how it dispatches
// messages back into the Bubbletea program; pass nil to disable
// dispatch (used by unit tests that don't have a Program). Use
// SetSend to install Send after the program exists.
func NewRunner(target RunnerTarget, send func(tea.Msg)) *Runner {
	return &Runner{
		target: target,
		send:   send,
		status: "idle",
		tail:   newRingBuffer(200),
	}
}

// SetSend installs the program send function. Used by main.go because
// the runner is constructed before the tea.Program (the program needs
// the model, the model needs the runner).
func (r *Runner) SetSend(send func(tea.Msg)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.send = send
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
func (r *Runner) Start(spec Spec) error {
	if !spec.Valid() {
		return ErrInvalidSpec
	}
	r.mu.Lock()
	if r.status == "running" || r.status == "cancelling" {
		running := r.current
		send := r.send
		r.mu.Unlock()
		if send != nil {
			send(JobBusyMsg{Wanted: spec, Running: running})
		}
		return ErrJobInFlight
	}
	r.status = "running"
	r.current = spec
	r.startedAt = time.Now()
	r.mu.Unlock()
	go r.run(spec)
	return nil
}

// Cancel signals the running job. It flips cancelRequested under the
// mutex (the completion classifier reads this) AND invokes cancelFn so
// the exec.CommandContext ctx kills the child even if SIGTERM is
// ignored. SIGTERM is sent first; SIGKILL after 5s if the child is
// still alive.
func (r *Runner) Cancel() {
	r.mu.Lock()
	cmd := r.cmd
	cancelFn := r.cancelFn
	if r.status == "running" {
		r.status = "cancelling"
	}
	r.cancelRequested = true
	r.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	go func() {
		t := time.NewTimer(5 * time.Second)
		defer t.Stop()
		<-t.C
		r.mu.Lock()
		stillCmd := r.cmd
		r.mu.Unlock()
		if stillCmd != nil && stillCmd.Process != nil {
			if cancelFn != nil {
				cancelFn()
			}
			_ = stillCmd.Process.Kill()
		}
	}()
}

// scanLine is the unit pushed from scanner goroutines to the writer
// goroutine.
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
	send := r.send
	r.mu.Unlock()

	if send != nil {
		send(JobStartedMsg{Spec: spec, LogPath: logPath, Argv: argv})
	}

	// Tick goroutine.
	tickStop := make(chan struct{})
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-tickStop:
				return
			case <-t.C:
				r.mu.Lock()
				s := r.send
				r.mu.Unlock()
				if s != nil {
					s(JobTickMsg{})
				}
			}
		}
	}()

	// Single writer goroutine owns logFile + tail mutations.
	lines := make(chan scanLine, 256)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for ln := range lines {
			fmt.Fprintf(logFile, "%s\t%s\n", ln.stream, ln.text)
			r.tail.push(ln.text)
			r.mu.Lock()
			s := r.send
			r.mu.Unlock()
			if s != nil {
				s(JobLineMsg{Line: ln.text, Stream: ln.stream})
			}
		}
	}()

	var scanWG sync.WaitGroup
	scanWG.Add(2)
	go r.scan(stdout, "stdout", lines, &scanWG)
	go r.scan(stderr, "stderr", lines, &scanWG)

	waitErr := cmd.Wait()
	scanWG.Wait()
	close(lines)
	<-writerDone
	close(tickStop)
	_ = logFile.Sync()
	_ = logFile.Close()

	r.mu.Lock()
	cancelled := r.cancelRequested
	r.mu.Unlock()

	exit := 0
	status := "succeeded"
	switch {
	case cancelled:
		status = "cancelled"
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	case ctx.Err() != nil:
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
	send := r.send
	r.mu.Unlock()
	if send != nil {
		send(JobCompletedMsg{
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
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

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
