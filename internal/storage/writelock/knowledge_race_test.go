//go:build integration

package writelock_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hman-pro/projectlens/internal/storage/writelock"
)

// TestKnowledgeDelete_DoesNotRaceIndexerScan asserts the design
// invariant: deleting a knowledge_entries row + its paired chunk does
// not race an index-embed-style "chunks WHERE embedding IS NULL" scan.
// The scan reads only chunks; the delete simply removes a candidate.
func TestKnowledgeDelete_DoesNotRaceIndexerScan(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Acquire the writer lock to simulate index-embed running.
	lock, err := writelock.Acquire(ctx, db, "test-knowledge-race")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release(ctx)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = db.Pool.Exec(ctx,
				`SELECT id FROM chunks WHERE source_type = 'knowledge' LIMIT 100`)
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// The delete uses NO lock — it must succeed despite the scan loop.
	if _, err := db.Pool.Exec(ctx,
		`DELETE FROM knowledge_entries WHERE id = -1`); err != nil {
		t.Errorf("delete with concurrent scan: %v", err)
	}
	close(stop)
	wg.Wait()
}
