package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/hman-pro/projectlens/internal/tui/sections/storage"
	pgstore "github.com/hman-pro/projectlens/internal/tui/store"
)

func TestStorage_RendersTablesAndChunks(t *testing.T) {
	f := pgstore.NewFake()
	f.SetStorage(pgstore.StorageSnapshot{
		Tables: []pgstore.TableStat{
			{Name: "files", EstRows: 4150, Bytes: 12_345_678},
			{Name: "symbols", EstRows: 28432, Bytes: 89_000_000},
		},
		Chunks: pgstore.ChunkStats{
			Total: 30000, Embedded: 28500,
			ByType: map[string]int64{"code": 28000, "knowledge": 2000},
		},
	})
	m := storage.New(context.Background(), f)
	msg := m.Refresh()()
	next, _ := m.Update(msg)
	v := next.View()
	for _, want := range []string{"files", "symbols", "4150", "code", "knowledge", "28500"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q\n%s", want, v)
		}
	}
}
