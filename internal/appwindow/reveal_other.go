//go:build !windows

package appwindow

import "os/exec"

// setRawCommandLine does nothing here: only Windows is given a command line
// rather than arguments.
func setRawCommandLine(cmd *exec.Cmd, line string) {}
