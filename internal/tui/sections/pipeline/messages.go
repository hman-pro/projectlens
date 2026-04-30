package pipeline

import "github.com/hman-pro/projectlens/internal/tui/store"

const ID = "pipeline"

type RefreshedMsg struct {
	Snap store.PipelineSnapshot
	Err  error
	Gen  uint64
}
