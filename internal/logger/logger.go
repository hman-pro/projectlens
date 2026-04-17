// Package logger provides a shared structured logger for the ProjectLens
// pipeline. It wraps charmbracelet/log for colorful, readable terminal output
// with structured key-value pairs.
package logger

import (
	"fmt"
	"os"

	"github.com/charmbracelet/log"
)

// L is the shared logger instance used across all packages.
var L *log.Logger

func init() {
	L = log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: true,
	})
}

// Step logs a prominent step header.
func Step(name string) {
	L.Info("── " + name + " ──")
}

// Stage logs a major stage header (for index-all).
func Stage(name string) {
	L.Info("═══ " + name + " ═══")
}

// Info logs an informational message with optional key-value pairs.
func Info(msg string, keyvals ...interface{}) {
	L.Info(msg, keyvals...)
}

// Warn logs a warning message.
func Warn(msg string, keyvals ...interface{}) {
	L.Warn(msg, keyvals...)
}

// Error logs an error message.
func Error(msg string, keyvals ...interface{}) {
	L.Error(msg, keyvals...)
}

// Progress logs a progress update (e.g., "500/2913").
func Progress(msg string, current, total int, keyvals ...interface{}) {
	args := append([]interface{}{"progress", progressStr(current, total)}, keyvals...)
	L.Info(msg, args...)
}

func progressStr(current, total int) string {
	pct := 0
	if total > 0 {
		pct = current * 100 / total
	}
	return fmt.Sprintf("%d/%d (%d%%)", current, total, pct)
}
