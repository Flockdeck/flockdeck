//go:build !windows

package changes

import (
	"context"
	"os/exec"
)

// shellCommand is cmdline run through the shell.
func shellCommand(ctx context.Context, cmdline string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/sh", "-c", cmdline)
}
