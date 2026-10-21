//go:build !windows

package acp

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// endProcess ends a process a test started, so a failing test leaves nothing behind.
func endProcess(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }

// alive reports whether a process is still running. A zombie is not: it has
// ended, and only waits for whoever inherited it to notice.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	stat := string(raw)
	state := strings.TrimSpace(stat[strings.LastIndexByte(stat, ')')+1:])
	return !strings.HasPrefix(state, "Z")
}
