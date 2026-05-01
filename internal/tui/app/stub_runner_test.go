package app_test

import (
	"sync"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/jobs"
)

// stubRunner is the minimal Runner used by app tests. It records
// Start calls and exposes a settable Snapshot.
type stubRunner struct {
	mu       sync.Mutex
	started  []jobs.Spec
	state    jobs.Snapshot
	cancelled bool
}

func newStubRunner() *stubRunner { return &stubRunner{} }

func (s *stubRunner) Start(spec jobs.Spec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = append(s.started, spec)
	s.state.Status = "running"
	s.state.Current = spec
	s.state.StartedAt = time.Now()
	return nil
}

func (s *stubRunner) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelled = true
	s.state.Status = "cancelling"
}

func (s *stubRunner) State() jobs.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *stubRunner) SetStatus(st string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Status = st
}

func (s *stubRunner) Started() []jobs.Spec {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]jobs.Spec, len(s.started))
	copy(out, s.started)
	return out
}
