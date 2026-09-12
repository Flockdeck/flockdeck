//go:build !windows

package main

// showStartupError has nothing to add here: off Windows the build is not
// linked for a subsystem that takes standard error away, so the message has
// already been printed where the program was started from.
func showStartupError(string) {}
