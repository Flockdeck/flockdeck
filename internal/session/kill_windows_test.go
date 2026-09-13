//go:build windows

package session

import (
	"os/exec"
	"syscall"
)

// detach starts cmd with no console at all, which is what a program started
// to run on in the background looks like: nothing ends it when the pane's
// console goes.
func detach(cmd *exec.Cmd) {
	const detachedProcess = 0x00000008
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP}
}

// ignoreHangups does nothing: Windows sends no hangup.
func ignoreHangups() {}

// alive reports whether a process is still running.
func alive(pid int) bool {
	const synchronize = 0x00100000
	const queryLimitedInformation = 0x1000
	h, err := syscall.OpenProcess(synchronize|queryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	ev, err := syscall.WaitForSingleObject(h, 0)
	return err == nil && ev == syscall.WAIT_TIMEOUT
}
