//go:build windows

package store

import (
	"syscall"
	"time"
)

// processQueryLimitedInformation is the least access that answers
// GetExitCodeProcess. Unlike PROCESS_QUERY_INFORMATION it is granted across
// integrity levels, so an instance the user started from an elevated prompt
// can still be asked about rather than being called dead because this process
// was not allowed to look.
const processQueryLimitedInformation = 0x1000

// stillActive is the exit code Windows reports for a process that has not
// exited yet. A process really exiting with 259 is indistinguishable from a
// running one, which is a Windows quirk nothing can do anything about; flockdeck
// exits 0 or 1.
const stillActive = 259

// pidAlive reports whether a process id names a process that is still running.
//
// Opening a handle is not the test it looks like. Windows keeps a process
// object alive for as long as anything holds a handle to it, so a flockdeck that
// exited an hour ago is still openable by whatever started it, and asking only
// whether the handle came back says "running" of a process that has been gone
// since. The exit code is the state itself.
func pidAlive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		// The handle opened but will not answer. Saying "running" is the safe
		// way round: the only thing it can do is leave a record in place.
		return true
	}
	return code == stillActive
}

// processStarted reports when a process started. ok is false when it cannot
// be asked, as for a process that is not there.
func processStarted(pid int) (started time.Time, ok bool) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return time.Time{}, false
	}
	defer syscall.CloseHandle(h)
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, created.Nanoseconds()), true
}
