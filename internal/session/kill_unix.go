//go:build !windows

package session

import (
	"syscall"
	"time"
)

// hangupGrace is how long a closing pane's processes are given to end on the
// hangup before they are killed. It is what a terminal closing does, and an
// agent told of it saves what it has and exits; one that ignores it is not
// waited on for long.
const hangupGrace = 500 * time.Millisecond

// procTree is nothing on Unix. go-pty starts a pane's process as the leader of
// a session of its own (Setsid), so its process id is also the id of the
// process group everything it starts joins, and the group is the tree.
type procTree struct{}

func containTree(int) procTree { return procTree{} }

// endTree ends the pane's process and everything in its process group, and
// waits for the process to be gone.
//
// Killing the process alone left the rest running: a shell pane's background
// jobs (`npm run dev &`), a tool that ignores the hangup the terminal going
// sends it, whatever an agent started and did not wait for. The group is
// hung up first, as closing a terminal does, so that an agent can end the way
// it would at a window being closed, and then killed.
//
// A process that has made a session of its own on purpose -- a daemon, an
// editor's window started from a shell -- has left the group, and is left
// alone as a terminal closing leaves it.
//
// The group is only signalled while the pane's process has not been reaped.
// Until then its id cannot be anybody else's; after, it may be.
func (s *Session) endTree() {
	pid := s.cmd.Process.Pid
	if !closedChan(s.reaped) {
		_ = syscall.Kill(-pid, syscall.SIGHUP)
		waitClosed(s.reaped, hangupGrace)
		// Whatever ignored the hangup. The pane's process may have been reaped
		// in the grace, but an id given back is not handed out again within it.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	_ = s.cmd.Process.Kill()
	waitClosed(s.reaped, closeGrace)
}
