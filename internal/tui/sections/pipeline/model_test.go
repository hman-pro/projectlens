package pipeline_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/sections/pipeline"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestPipeline_RendersStages(t *testing.T) {
	f := store.NewFake()
	f.SetPipeline(store.PipelineSnapshot{Stages: []store.StageStat{
		{Name: "code", LastRunStartedAt: time.Now(), Status: "ok", FilesProcessed: 4150, Duration: 95 * time.Second},
		{Name: "embed", LastRunStartedAt: time.Now(), Status: "running", FilesProcessed: 100},
	}})
	m := pipeline.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	v := next.View()
	for _, want := range []string{"code", "embed", "running", "4150"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}
