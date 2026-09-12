//go:build !windows && !linux && !darwin

package chat

// terminalWidth has no way to ask here, and says so with 0.
func terminalWidth() int { return 0 }
