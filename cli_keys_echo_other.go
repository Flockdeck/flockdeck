//go:build !windows && !linux && !darwin

package main

// hideInput has no way to hide what is typed here, and says so with nil.
func hideInput() func() { return nil }
