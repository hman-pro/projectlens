package store

import (
	"context"
	"sync"
	"time"
)

// Fake is a controllable in-memory Store for unit tests.
//
//	f := store.NewFake()
//	f.SetHealth(snap)
//	f.SetLatency(50 * time.Millisecond)
//	f.SetErr("Health", errors.New("boom"))
type Fake struct {
	mu       sync.Mutex
	health   HealthSnapshot
	pipeline PipelineSnapshot
	storage  StorageSnapshot
	runs     RunsSnapshot
	config   ConfigSnapshot
	latency  time.Duration
	errs     map[string]error
}

func NewFake() *Fake {
	return &Fake{errs: map[string]error{}}
}

func (f *Fake) SetHealth(s HealthSnapshot)     { f.mu.Lock(); f.health = s; f.mu.Unlock() }
func (f *Fake) SetPipeline(s PipelineSnapshot) { f.mu.Lock(); f.pipeline = s; f.mu.Unlock() }
func (f *Fake) SetStorage(s StorageSnapshot)   { f.mu.Lock(); f.storage = s; f.mu.Unlock() }
func (f *Fake) SetRuns(s RunsSnapshot)         { f.mu.Lock(); f.runs = s; f.mu.Unlock() }
func (f *Fake) SetConfig(s ConfigSnapshot)     { f.mu.Lock(); f.config = s; f.mu.Unlock() }
func (f *Fake) SetLatency(d time.Duration)     { f.mu.Lock(); f.latency = d; f.mu.Unlock() }
func (f *Fake) SetErr(method string, err error) {
	f.mu.Lock()
	f.errs[method] = err
	f.mu.Unlock()
}

func (f *Fake) wait(ctx context.Context, method string) error {
	f.mu.Lock()
	d := f.latency
	err := f.errs[method]
	f.mu.Unlock()
	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *Fake) Health(ctx context.Context) (HealthSnapshot, error) {
	if err := f.wait(ctx, "Health"); err != nil {
		return HealthSnapshot{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.health, nil
}
func (f *Fake) Pipeline(ctx context.Context) (PipelineSnapshot, error) {
	if err := f.wait(ctx, "Pipeline"); err != nil {
		return PipelineSnapshot{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pipeline, nil
}
func (f *Fake) Storage(ctx context.Context) (StorageSnapshot, error) {
	if err := f.wait(ctx, "Storage"); err != nil {
		return StorageSnapshot{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.storage, nil
}
func (f *Fake) Runs(ctx context.Context, limit int) (RunsSnapshot, error) {
	if err := f.wait(ctx, "Runs"); err != nil {
		return RunsSnapshot{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 || limit > RunsMaxRows {
		limit = RunsMaxRows
	}
	out := f.runs
	if len(out.Runs) > limit {
		out.Runs = out.Runs[:limit]
	}
	return out, nil
}
func (f *Fake) Config(ctx context.Context) (ConfigSnapshot, error) {
	if err := f.wait(ctx, "Config"); err != nil {
		return ConfigSnapshot{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.config, nil
}
