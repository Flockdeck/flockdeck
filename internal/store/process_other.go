//go:build !windows

package store

import (
	"os"
	"syscall"
)

// pidAlive reports whether a process id names a process that is still running.
//
// Signal 0 is delivered to nothing and only checks the process exists. It says
// "running" of a zombie — a child that has exited and not been waited for —
// which is the same blind spot Windows has for an open handle, and is not
// fixable from here: only the parent that spawned it can reap it.
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
