//go:build windows

package acp

import "syscall"

const (
	synchronize                    = 0x00100000
	processQueryLimitedInformation = 0x1000
	processTerminate               = 0x0001
)

// alive reports whether a process is still running.
func alive(pid int) bool {
	h, err := syscall.OpenProcess(synchronize|processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	ev, err := syscall.WaitForSingleObject(h, 0)
	return err == nil && ev == syscall.WAIT_TIMEOUT
}

// endProcess ends a process a test started, so a failing test leaves nothing behind.
func endProcess(pid int) {
	h, err := syscall.OpenProcess(processTerminate|synchronize, false, uint32(pid))
	if err != nil {
		return
	}
	defer syscall.CloseHandle(h)
	_ = syscall.TerminateProcess(h, 1)
	_, _ = syscall.WaitForSingleObject(h, 5000)
}
