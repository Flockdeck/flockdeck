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
// a session of its own (Setsid), so its process id is also the id of that
// session, which everything it starts stays in.
type procTree struct{}

func containTree(int) procTree { return procTree{} }

// endTree ends the pane's process and everything else in its session, and
// waits for the process to be gone.
//
// Killing the process alone left the rest running: a shell pane's background
// jobs (`npm run dev &`), a tool that ignores the hangup the terminal going
// sends it, whatever an agent started and did not wait for. The session is
// hung up first, as closing a terminal does, so that an agent can end the way
// it would at a window being closed, and then killed.
//
// It is the session and not the process group because an interactive shell
// puts each job in a group of its own: signalling the pane's group reached
// the shell and left a job that ignores the hangup (nohup, a trap) running.
// On Linux the session's members are found in /proc. Elsewhere -- macOS --
// there is no way to list them from the standard library alone, so only the
// pane's own group is signalled, and such a job does outlive the pane there.
//
// A process that has made a session of its own on purpose -- a daemon, an
// editor's window started from a shell -- has left the session, and is left
// alone as a terminal closing leaves it.
//
// While the pane's process has not been reaped its id cannot be anybody
// else's. Once it has -- a shell told `exit`, with a job still running -- what
// it left is found through signalLeftBehind instead.
func (s *Session) endTree() {
	pid := s.cmd.Process.Pid
	if !closedChan(s.reaped) {
		signalSession(pid, syscall.SIGHUP)
		waitClosed(s.reaped, hangupGrace)
		// Whatever ignored the hangup. The pane's process may have been reaped
		// in the grace, but an id given back is not handed out again within it.
		signalSession(pid, syscall.SIGKILL)
	} else if signalLeftBehind(pid, syscall.SIGHUP) {
		time.Sleep(hangupGrace)
		signalLeftBehind(pid, syscall.SIGKILL)
	}
	_ = s.cmd.Process.Kill()
	waitClosed(s.reaped, closeGrace)
}

// signalSession sends sig to the process group sid leads and to every other
// process in the session sid leads.
func signalSession(sid int, sig syscall.Signal) {
	_ = syscall.Kill(-sid, sig)
	for _, pid := range sessionMembers(sid) {
		_ = syscall.Kill(pid, sig)
	}
}

// signalLeftBehind sends sig to what is still running of the session and
// group sid led, once its leader has been reaped, and reports whether
// anything was.
//
// While any process is in the session or the leader's group, the leader's id
// is not handed out again, so what is in them is what the pane left running.
// If the id is a running process's again, they had all ended first, and a
// session or group by that id is somebody else's, so nothing is signalled.
func signalLeftBehind(sid int, sig syscall.Signal) bool {
	if err := syscall.Kill(sid, 0); err != syscall.ESRCH {
		return false
	}
	found := syscall.Kill(-sid, sig) == nil
	for _, pid := range sessionMembers(sid) {
		if syscall.Kill(pid, sig) == nil {
			found = true
		}
	}
	return found
}

// stillRunning reports whether pid names a process that is still running.
// Signal 0 delivers nothing and only checks that the process exists, which
// says "running" of a zombie -- a child that has exited and not been waited
// for -- the same blind spot Windows has for an open handle; not fixable from
// here, since only the parent that started it can reap it.
func stillRunning(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// killTree ends pid and everything left in its session, the same way endTree
// ends a pane's own process tree -- see signalSession -- except that pid was
// never this run's own process, so there is no s.reaped to wait on: SIGHUP is
// given hangupGrace to be noticed before SIGKILL follows.
//
// go-pty makes a pane's process the leader of a session of its own, and pid
// is read back exactly as it was recorded at that moment (see
// KillProcessTree's startedHint), so it is still fit to signal as one here.
func killTree(pid int) {
	signalSession(pid, syscall.SIGHUP)
	time.Sleep(hangupGrace)
	signalSession(pid, syscall.SIGKILL)
}
