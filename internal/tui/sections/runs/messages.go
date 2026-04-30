package runs

import "github.com/hman-pro/projectlens/internal/tui/store"

const ID = "runs"

type RefreshedMsg struct {
	Snap store.RunsSnapshot
	Err  error
	Gen  uint64
}
