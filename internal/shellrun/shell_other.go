//go:build !windows

package shellrun

import (
	"context"
	"os/exec"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// shellCommand is cmdline run through the shell.
func shellCommand(ctx context.Context, cmdline string) *exec.Cmd {
	c := exec.CommandContext(ctx, "/bin/sh", "-c", cmdline)
	sysproc.NoWindow(c)
	return c
}
