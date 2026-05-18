//go:build !windows

package jobs

import (
	"os/exec"
	"syscall"
)

// setPgid puts the child into its own process group so that
// signalGroup can signal the entire group (parent + descendants).
func setPgid(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalGroup sends sig to the process group led by pid. Uses kill(-pid)
// so that go/packages, git, provider HTTP, and any other descendants
// receive the signal — not just the direct projectlens child.
func signalGroup(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}
