package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/storage"
)

// buildStageDetailFromRun mirrors the logic in Builder.Build so we can test
// StageDetail construction in isolation.
func buildStageDetailFromRun(stage string, run storage.IndexRunRecord) StageDetail {
	freshness := indexstate.StageFreshness{
		Stage:          stage,
		Status:         run.Status,
		CommitSHA:      run.CommitSHA,
		StartedAt:      run.StartedAt.Format(time.RFC3339),
		FilesProcessed: run.FilesProcessed,
	}
	detail := StageDetail{
		StageFreshness: freshness,
		Providers: StageProviders{
			Embed:     run.ProviderEmbed,
			Summarize: run.ProviderSummarize,
		},
		Metrics: run.Metrics,
		Error:   run.ErrorText,
	}
	if run.CompletedAt != nil {
		detail.CompletedAt = run.CompletedAt.Format(time.RFC3339)
		detail.AgeMinutes = time.Since(*run.CompletedAt).Minutes()
		detail.DurationSeconds = run.CompletedAt.Sub(run.StartedAt).Seconds()
	}
	return detail
}

func TestStageDetail_BuildFromIndexRunRecord(t *testing.T) {
	started := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)
	completed := time.Date(2026, 5, 23, 10, 2, 30, 0, time.UTC) // 2m30s later

	run := storage.IndexRunRecord{
		ID:                1,
		StartedAt:         started,
		CompletedAt:       &completed,
		CommitSHA:         "deadbeef",
		FilesProcessed:    42,
		SymbolsExtracted:  100,
		EdgesCreated:      200,
		Status:            "completed",
		Stage:             "embed",
		ProviderEmbed:     "ollama/nomic-embed-text",
		ProviderSummarize: "",
		ErrorText:         "",
		Metrics:           map[string]any{"chunks": float64(99), "skipped": float64(3)},
	}

	detail := buildStageDetailFromRun("embed", run)

	// StageFreshness fields
	if detail.Stage != "embed" {
		t.Errorf("Stage: got %q want %q", detail.Stage, "embed")
	}
	if detail.Status != "completed" {
		t.Errorf("Status: got %q want %q", detail.Status, "completed")
	}
	if detail.CommitSHA != "deadbeef" {
		t.Errorf("CommitSHA: got %q want %q", detail.CommitSHA, "deadbeef")
	}
	if detail.FilesProcessed != 42 {
		t.Errorf("FilesProcessed: got %d want 42", detail.FilesProcessed)
	}

	// New fields
	if detail.Providers.Embed != "ollama/nomic-embed-text" {
		t.Errorf("Providers.Embed: got %q", detail.Providers.Embed)
	}
	if detail.Providers.Summarize != "" {
		t.Errorf("Providers.Summarize: want empty, got %q", detail.Providers.Summarize)
	}
	if detail.Metrics["chunks"] != float64(99) {
		t.Errorf("Metrics[chunks]: got %v want 99", detail.Metrics["chunks"])
	}
	if detail.Error != "" {
		t.Errorf("Error: want empty, got %q", detail.Error)
	}
	if detail.DurationSeconds != 150 { // 2m30s = 150s
		t.Errorf("DurationSeconds: got %g want 150", detail.DurationSeconds)
	}
}

func TestStageDetail_BuildFromIndexRunRecord_WithError(t *testing.T) {
	started := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)
	completed := time.Date(2026, 5, 23, 10, 0, 5, 0, time.UTC)

	run := storage.IndexRunRecord{
		StartedAt:   started,
		CompletedAt: &completed,
		Status:      "failed",
		Stage:       "summarize",
		ErrorText:   "anthropic: rate limited",
	}

	detail := buildStageDetailFromRun("summarize", run)

	if detail.Error != "anthropic: rate limited" {
		t.Errorf("Error: got %q want %q", detail.Error, "anthropic: rate limited")
	}
	if detail.Status != "failed" {
		t.Errorf("Status: got %q", detail.Status)
	}
}

func TestStageDetail_JSONRoundTrip(t *testing.T) {
	started := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)
	completed := time.Date(2026, 5, 23, 10, 1, 0, 0, time.UTC)

	run := storage.IndexRunRecord{
		StartedAt:         started,
		CompletedAt:       &completed,
		CommitSHA:         "abc123",
		FilesProcessed:    7,
		Status:            "completed",
		Stage:             "code",
		ProviderEmbed:     "ollama/nomic",
		ProviderSummarize: "anthropic/claude",
		Metrics:           map[string]any{"symbols": float64(42)},
		ErrorText:         "",
	}

	detail := buildStageDetailFromRun("code", run)

	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded StageDetail
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}

	if decoded.Stage != "code" {
		t.Errorf("Stage: got %q", decoded.Stage)
	}
	if decoded.Providers.Embed != "ollama/nomic" {
		t.Errorf("Providers.Embed: got %q", decoded.Providers.Embed)
	}
	if decoded.Providers.Summarize != "anthropic/claude" {
		t.Errorf("Providers.Summarize: got %q", decoded.Providers.Summarize)
	}
	if decoded.Metrics["symbols"] != float64(42) {
		t.Errorf("Metrics[symbols]: got %v", decoded.Metrics["symbols"])
	}
	if decoded.DurationSeconds != 60 {
		t.Errorf("DurationSeconds: got %g want 60", decoded.DurationSeconds)
	}
}

func TestStageDetail_MarkdownContainsProviderAndMetrics(t *testing.T) {
	started := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)
	completed := time.Date(2026, 5, 23, 10, 0, 30, 0, time.UTC)

	run := storage.IndexRunRecord{
		StartedAt:         started,
		CompletedAt:       &completed,
		CommitSHA:         "cafebabe",
		FilesProcessed:    10,
		Status:            "completed",
		Stage:             "embed",
		ProviderEmbed:     "ollama/nomic-embed-text",
		ProviderSummarize: "",
		Metrics:           map[string]any{"chunks": float64(5)},
		ErrorText:         "",
	}

	detail := buildStageDetailFromRun("embed", run)

	r := &Report{
		Stages: map[string]StageDetail{
			"embed": detail,
		},
	}

	var buf bytes.Buffer
	if err := (MarkdownRenderer{}).Render(&buf, r); err != nil {
		t.Fatalf("render: %v", err)
	}
	s := buf.String()

	if !strings.Contains(s, "## Stages") {
		t.Errorf("missing ## Stages header:\n%s", s)
	}
	if !strings.Contains(s, "embed=ollama/nomic-embed-text") {
		t.Errorf("missing provider cell in:\n%s", s)
	}
	if !strings.Contains(s, "chunks=5") {
		t.Errorf("missing metrics cell in:\n%s", s)
	}
}
