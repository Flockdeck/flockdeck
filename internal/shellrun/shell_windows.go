//go:build windows

package shellrun

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// shellCommand is cmdline run through cmd.exe. The line is given to it as it
// is, in the quoting cmd.exe reads with /s, because exec would otherwise quote
// the argument for a program that reads its line differently and break any
// command that holds a quote.
func shellCommand(ctx context.Context, cmdline string) *exec.Cmd {
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	c := exec.CommandContext(ctx, comspec)
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + comspec + `" /d /s /c "` + cmdline + `"`}
	return c
}
