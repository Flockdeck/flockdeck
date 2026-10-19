//go:build !windows

package proctree

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// hangupGrace is how long a closing pane's processes are given to end on the
// hangup before they are killed. It is what a terminal closing does, and an
// agent told of it saves what it has and exits; one that ignores it is not
// waited on for long.
const hangupGrace = 500 * time.Millisecond

// tree is nothing on Unix. A process started with Prepare, or by go-pty for a
// pane, is the leader of
// a session of its own (Setsid), so its process id is also the id of that
// session, which everything it starts stays in.
type tree struct{}

func containTree(int) tree { return tree{} }

// End ends the pane's process and everything else in its session, and
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
func (t *Tree) End(p *os.Process, reaped <-chan struct{}, grace time.Duration) {
	if t == nil {
		t = &Tree{}
	}
	pid := p.Pid
	if !closedChan(reaped) {
		signalSession(pid, syscall.SIGHUP)
		waitClosed(reaped, hangupGrace)
		// Whatever ignored the hangup. The pane's process may have been reaped
		// in the grace, but an id given back is not handed out again within it.
		signalSession(pid, syscall.SIGKILL)
	} else if signalLeftBehind(pid, syscall.SIGHUP) {
		time.Sleep(hangupGrace)
		signalLeftBehind(pid, syscall.SIGKILL)
	}
	_ = p.Kill()
	waitClosed(reaped, grace)
}

// signalSession sends sig to the process group sid leads and to every other
// process in the session sid leads.
func signalSession(sid int, sig syscall.Signal) {
	_ = syscall.Kill(-sid, sig)
	for _, pid := range SessionMembers(sid) {
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
	for _, pid := range SessionMembers(sid) {
		if syscall.Kill(pid, sig) == nil {
			found = true
		}
	}
	return found
}

// KillTree ends pid and everything left in its session, the same way End
// ends a pane's own process tree -- see signalSession -- except that pid was
// never this run's own process, so there is no reaped channel to wait on: SIGHUP is
// given hangupGrace to be noticed before SIGKILL follows.
//
// go-pty makes a pane's process the leader of a session of its own, and pid
// is read back exactly as it was recorded at that moment, so it is still fit
// to signal as one here. It is exported for a startup sweep to call directly,
// once it has already confirmed pid still names the same process it was
// recorded under (see store.ProcessAlive and store.ProcessStartedAt);
// nothing here repeats that check. See internal/workspace's reaper.
func KillTree(pid int) {
	signalSession(pid, syscall.SIGHUP)
	time.Sleep(hangupGrace)
	signalSession(pid, syscall.SIGKILL)
}

// prepare makes the process the leader of a session of its own, which
// everything it starts stays in, so End and KillTree can find all of it.
func prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
