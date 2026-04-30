package app

import (
	"io"
	"os"

	"github.com/charmbracelet/log"
)

// InitLogger routes charm log to PROJECTLENS_TUI_LOG_FILE (or io.Discard if empty/unset).
// Must be called BEFORE tea.Program.Run() — once tea grabs the alt screen,
// any stdout/stderr writes corrupt the display.
func InitLogger() {
	path := os.Getenv("PROJECTLENS_TUI_LOG_FILE")
	if path == "" {
		path = "/tmp/projectlens-tui.log"
	}
	if path == "-" {
		log.SetOutput(io.Discard)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.SetOutput(io.Discard)
		return
	}
	log.SetOutput(f)
	log.SetLevel(log.DebugLevel)
}
