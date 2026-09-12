//go:build !windows && !linux && !darwin

package main

// useConsole and releaseConsole have nothing to do here, where a program's
// standard handles do not depend on how it was linked, and -detach stays in
// the terminal it was started from, as it always has outside the platforms
// the release is built for. A detached run that loses its terminal carries
// on without letting its outputs go.
func useConsole()                               {}
func releaseConsole()                           {}
func letTerminalGo()                            {}
func detachConsole()                            {}
func ctrlCStops() bool                          { return true }
func detachFromTerminal() (done bool, code int) { return false, 0 }
