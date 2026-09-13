//go:build darwin

package idle

import "time"

// Since asks ioreg for HIDIdleTime, in nanoseconds, which the kernel's HID
// system keeps of its own accord and which counts keyboard and mouse input
// anywhere on the machine, not any one window's. There is no cgo-free way to
// read it directly, so it is asked of a short-lived command instead, as
// GetIdletime is on Linux.
//
// Locking is left to the idle time alone: nothing here has been found that
// reads it without cgo as cheaply and reliably as this one command already
// answers idle time, and locking a Mac stops input, which HIDIdleTime already
// shows growing.
func Since() (time.Duration, bool, bool) {
	out, err := runProbe("ioreg", "-c", "IOHIDSystem", "-d", "4", "-r", "-k", "HIDIdleTime")
	if err != nil {
		return 0, false, false
	}
	d, ok := parseHIDIdleTime(out)
	if !ok {
		return 0, false, false
	}
	return d, false, true
}
