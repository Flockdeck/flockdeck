//go:build !windows

package session

// shortPath is a path's 8.3 short name, which only Windows has.
func shortPath(string) string { return "" }
