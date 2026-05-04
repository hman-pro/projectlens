package jobs

import "time"

const ID = "jobs"

type RefreshedMsg struct {
	Runs []JobRun
	Err  error
	Gen  uint64
}

// LiveStateMsg pushes the currently-running (or just-completed) job
// from the runner into the Jobs section so the section can host the
// live tail. Status="" or "idle" clears live state. Status="running"
// or "cancelling" shows live tail; any terminal status keeps the
// final tail visible briefly until LiveClearMsg arrives.
type LiveStateMsg struct {
	Status   string
	Spec     string
	Started  time.Time
	Duration time.Duration
	Tail     []string
	LogPath  string
}

// LiveClearMsg drops live state and returns the section to history view.
type LiveClearMsg struct{}
