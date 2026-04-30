package health

import "github.com/hman-pro/projectlens/internal/tui/store"

const ID = "health"

// RefreshedMsg is delivered to the health section after a Refresh() completes.
type RefreshedMsg struct {
	Snap store.HealthSnapshot
	Err  error
	Gen  uint64
}
