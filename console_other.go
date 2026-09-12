//go:build !windows

package main

// useConsole and releaseConsole have nothing to do outside Windows, where a
// program's standard handles do not depend on how it was linked.
func useConsole()     {}
func releaseConsole() {}
