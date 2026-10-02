//go:build windows

package appwindow

import (
	"os/exec"
	"syscall"
)

// setRawCommandLine gives cmd the command line line as it is written, when there
// is one, rather than one Go builds from the arguments.
func setRawCommandLine(cmd *exec.Cmd, line string) {
	if line == "" {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = line
}
