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

// WithProject returns a derived logger carrying project_slug + storage_schema.
// Pass empty strings to get the current L unchanged. Use when the caller has
// a logger reference and wants per-call scope.
func WithProject(slug, schema string) *log.Logger {
	if slug == "" && schema == "" {
		return L
	}
	return L.With("project_slug", slug, "storage_schema", schema)
}

// Bind rebinds the package-level L to a derived logger that carries
// project_slug + storage_schema. Returns a restore function that the
// caller MUST defer. Bind is intended for one-shot CLI commands so
// package-level helpers and indexer stage callbacks inherit the project
// fields without each call site needing a logger handle.
//
// Bind is NOT safe from concurrent server contexts — the MCP server must
// instead pass a scoped logger explicitly through its handlers. CLI
// processes run one command per process, so the rebind window is the whole
// process lifetime and there is no contention.
func Bind(slug, schema string) (restore func()) {
	prev := L
	L = WithProject(slug, schema)
	return func() { L = prev }
}
