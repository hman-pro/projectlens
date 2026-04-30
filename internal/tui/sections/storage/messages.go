package storage

import "github.com/hman-pro/projectlens/internal/tui/store"

const ID = "storage"

type RefreshedMsg struct {
	Snap store.StorageSnapshot
	Err  error
	Gen  uint64
}
