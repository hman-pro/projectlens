package pipeline_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/tui/sections"
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
	next, _ := m.Update(sections.SizeMsg{SectionID: "pipeline", W: 80, H: 30})
	msg := next.Refresh()()
	next, _ = next.Update(msg)
	v := next.View()
	for _, want := range []string{"Code", "Embed", "running", "4150"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}

func TestPipeline_RendersAllStageCards(t *testing.T) {
	f := store.NewFake()
	m := pipeline.New(context.Background(), f)
	next, _ := m.Update(sections.SizeMsg{SectionID: "pipeline", W: 80, H: 40})
	msg := next.Refresh()()
	next, _ = next.Update(msg)
	v := next.View()
	for _, want := range []string{"Code", "Embed", "Summarize", "History", "Datastore", "Docs", "planned", "never run", "[D run]"} {
		if !strings.Contains(v, want) {
			t.Errorf("cards missing %q\n%s", want, v)
		}
	}
}

func TestPipeline_RendersFooterHints(t *testing.T) {
	f := store.NewFake()
	m := pipeline.New(context.Background(), f)
	next, _ := m.Update(sections.SizeMsg{SectionID: "pipeline", W: 80, H: 30})
	msg := next.Refresh()()
	next, _ = next.Update(msg)
	v := next.View()
	for _, want := range []string{"R reindex", "F reindex --full", "↑/↓"} {
		if !strings.Contains(v, want) {
			t.Errorf("footer missing %q\n%s", want, v)
		}
	}
}

func TestPipeline_ImplementsActionableSection(t *testing.T) {
	var _ sections.ActionableSection = pipeline.New(context.Background(), store.NewFake())
}
