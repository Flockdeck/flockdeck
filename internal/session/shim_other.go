//go:build !windows

package session

import "github.com/aymanbagabas/go-pty"

// command builds the process a pane runs. Only Windows runs some agents
// through a command interpreter with quoting rules of its own.
func command(p pty.Pty, exe string, args []string) *pty.Cmd {
	return p.Command(exe, args...)
}
