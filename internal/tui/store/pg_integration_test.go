//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/tui/store"
)

func TestPGStore_AllSnapshots(t *testing.T) {
	dsn := os.Getenv("PROJECTLENS_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	cfg := &config.Config{
		DatabaseURL: dsn,
		Embeddings: config.EmbeddingsConfig{
			Provider: "ollama", Model: "mxbai-embed-large", Dimensions: 1024,
			Endpoint: "http://localhost:11434",
		},
		Summarization: config.SummarizationConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"},
	}
	s := store.NewPG(pool, cfg, "")

	if _, err := s.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	if _, err := s.Pipeline(ctx); err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	st, err := s.Storage(ctx)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	if len(st.Tables) == 0 {
		t.Fatalf("storage: expected at least one table row")
	}
	if _, err := s.Runs(ctx, 10); err != nil {
		t.Fatalf("runs: %v", err)
	}
	c, err := s.Config(ctx)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if c.DBHost == "" {
		t.Fatalf("config: expected DBHost")
	}
}

func TestPGStore_QuitCancelsInFlight(t *testing.T) {
	dsn := os.Getenv("PROJECTLENS_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable"
	}
	parent, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(parent, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	ctx, cancelInflight := context.WithCancel(parent)
	done := make(chan error, 1)
	go func() {
		_, err := pool.Exec(ctx, "SELECT pg_sleep(5)")
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancelInflight()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected cancellation error, got nil")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected cancellation within 500ms")
	}
}
