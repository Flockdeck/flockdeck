//go:build !windows

package sysproc

import "os/exec"

// NoWindow does nothing here. Only Windows gives the console child of a
// windowless program a window of its own; everywhere else a child with no
// terminal simply has none.
func NoWindow(cmd *exec.Cmd) {}
