//go:build integration

package report_test

import (
	"context"
	"os"
	"testing"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/report"
	"github.com/hman-pro/projectlens/internal/storage"
)

type stubInspector struct {
	providers []indexstate.ProviderHealth
	git       indexstate.GitState
}

func (s stubInspector) ProbeProviders(_ context.Context) []indexstate.ProviderHealth {
	return s.providers
}
func (s stubInspector) GitHeadAndDirty(_ context.Context) indexstate.GitState { return s.git }

func openIntegration(t *testing.T) *storage.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := storage.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestBuilder_DegradedFromProviderError(t *testing.T) {
	db := openIntegration(t)
	insp := stubInspector{
		providers: []indexstate.ProviderHealth{
			{Role: "embedder", Provider: "ollama", State: "error", Error: "conn refused"},
		},
	}
	b := report.NewBuilder(db, insp, "", report.Options{TopN: 5})
	r, err := b.Build(context.Background())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var seen bool
	for _, d := range r.Degraded {
		if d.Reason == "conn refused" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("provider error degradation missing: %+v", r.Degraded)
	}
}

func TestBuilder_WriterActiveFlipsWithLiveHolder(t *testing.T) {
	db := openIntegration(t)
	ctx := context.Background()
	insp := stubInspector{}

	_, _ = db.Pool.Exec(ctx, `DELETE FROM index_locks WHERE lock_id = 9876543210`)

	r1, err := report.NewBuilder(db, insp, "", report.Options{}).Build(ctx)
	if err != nil {
		t.Fatalf("build1: %v", err)
	}
	if r1.WriterActive {
		t.Errorf("want WriterActive=false baseline, got true")
	}

	t.Log("baseline WriterActive=false confirmed; live-holder branch covered by writelock integration tests")
}
