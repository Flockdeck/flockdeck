//go:build !windows && !linux

package store

import "time"

// processStarted cannot say when a process started here. On macOS it takes
// sysctl's kern.proc.pid and the kinfo_proc layout, which only
// golang.org/x/sys wraps, and Flockdeck does not depend on that directly for
// one check. Instance.StillRunning then takes a live process id at its word.
func processStarted(int) (time.Time, bool) { return time.Time{}, false }
