//go:build !windows

package tool

// oemText is for Windows, whose console programs write their own code page
// into a pipe; everywhere else a program's output is taken as it is.
func oemText([]byte) (string, bool) { return "", false }
