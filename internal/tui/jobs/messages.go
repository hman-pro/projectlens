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

// JobTickMsg is emitted every 500ms while a job is running so the
// drawer can animate elapsed time even when the child is silent.
type JobTickMsg struct{}

// JobCompletedMsg is sent exactly once when cmd.Wait returns AND the
// log file has been flushed and closed.
type JobCompletedMsg struct {
	Spec     Spec
	ExitCode int
	Status   string // "succeeded" | "failed" | "cancelled"
	Duration time.Duration
	LogPath  string
	Tail     []string
}

// JobBusyMsg is emitted when a Run is requested while another job is
// in flight.
type JobBusyMsg struct {
	Wanted  Spec
	Running Spec
}

// RunSpecMsg is dispatched by the app after the user passes the
// confirm modal. The runner picks it up and calls Start.
type RunSpecMsg struct {
	Spec Spec
}

// PreflightDoneMsg carries the count + cost driver back to the app so
// it can open the right confirm modal. Token must equal the app's
// pendingToken at handle time; stale results are dropped.
type PreflightDoneMsg struct {
	Spec  Spec
	Count int
	Cost  string
	Err   error
	Token uint64
}
