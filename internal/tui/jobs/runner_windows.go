//go:build windows

package jobs

import (
	"os"
	"os/exec"
	"syscall"
)

// setPgid is a no-op on Windows. The TUI is darwin/linux-primary;
// this stub exists only to keep cross-compilation green.
func setPgid(_ *exec.Cmd) {}

// signalGroup on Windows falls back to signalling only the direct
// child — there is no portable process-group equivalent. The TUI is
// not supported on Windows; this is a build-only stub.
func signalGroup(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(sig)
}
