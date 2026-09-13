// Package idle answers how long this computer has gone without keyboard or
// mouse input, in any application, and whether its screen is locked -- the
// two facts the push loop needs to tell that somebody is actually at the
// desk, rather than merely that a Flockdeck window is in front of them.
//
// Flockdeck is one binary built with CGO_ENABLED=0 for every platform. On
// Windows Since is read through the plain syscall package, in keeping with
// internal/session/usage_windows.go. On Linux and macOS there is no such
// call available without cgo, so it is read by running a short-lived
// command, under probeTimeout, and parsing what it says. Since is meant to be
// asked only when a push is actually due, not on every tick of the wait
// loop.
package idle

import "time"

// probeTimeout bounds how long any command Since runs, on Linux or macOS, is
// waited for. A machine having a bad moment must not hold up the wait loop.
const probeTimeout = 2 * time.Second
