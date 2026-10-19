//go:build !windows

package manifest

import (
	"os/exec"
	"syscall"
)

// contain puts the adapter in a process group of its own, so that the group can
// be ended with it.
func contain(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// kill ends the adapter and what it started.
func kill(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
