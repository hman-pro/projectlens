package config

import "github.com/hman-pro/projectlens/internal/tui/store"

const ID = "config"

type RefreshedMsg struct {
	Snap store.ConfigSnapshot
	Err  error
	Gen  uint64
}
