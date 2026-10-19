// Package proctree puts a process Flockdeck started into something its
// descendants share, and ends all of it together. A terminal pane uses it for
// the process in the pane; a client that starts an agent with no pane (the ACP
// client) uses it for the agent, whose adapter may start a child of its own.
//
// On Windows the process is put in a job object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, so the tree also ends if Flockdeck dies
// without running any Go. On Unix the process leads a session of its own and
// End signals the session. Only console programs are ended on Windows; see
// tree_windows.go for why windowed programs are left running.
//
// What to know before relying on it:
//
//   - End must always be called, also after the process has exited. On Windows
//     the Tree holds a job handle until End, and what the process left running
//     is only found through it. Call End with the channel closed when Wait
//     returned; a second End does nothing.
//   - On Unix End sends SIGHUP, waits 500 ms, then sends SIGKILL, to the
//     process's group and to the other members of its session. It never sends
//     SIGTERM, so a program that treats HUP as a reload is killed after 500 ms.
//   - Containment has limits. A descendant that calls setsid or setpgid leaves
//     the session and is not reached, as a closing terminal does not reach it.
//     On macOS the standard library cannot list a session's members or the
//     process table, so Parents and SessionMembers return nothing and only the
//     process group is signalled. Unix has nothing like Windows'
//     KILL_ON_JOB_CLOSE: if Flockdeck dies without running Go, the agent's tree
//     is left running on Unix.
//   - After the process has been reaped its id can belong to another process.
//     Pass End the reaped channel so it can tell; the guards depend on it.
package proctree

import (
	"os/exec"
	"sync"
	"time"
)

const (
	// MaxProcs bounds a walk of the process tree, so a parent map that
	// disagrees with itself -- which a table assembled from processes that are
	// starting and exiting underneath it can -- costs a bounded amount rather
	// than everything on the machine.
	MaxProcs = 512

	// killGrace is how long KillTree waits for what it ended to be gone.
	killGrace = 3 * time.Second
)

// Tree is what a started process's descendants are tracked by. A nil or zero
// Tree is safe to End: it tracks nothing, so End ends only the process it is
// given (on Windows it does not walk the process table for a process the Tree
// was not made for). A second End returns immediately, even while the first is
// still running on another goroutine.
type Tree struct {
	mu   sync.Mutex
	tree tree
}

// Prepare sets cmd up so that Contain can track what it starts. Call it before
// cmd.Start, then Contain with the new process's id. Start does both.
func Prepare(cmd *exec.Cmd) { prepare(cmd) }

// Contain tracks the process with the given id and everything it starts from
// now on. Call it as soon as the process has started: what the process has
// already started is not tracked on Windows.
func Contain(pid int) *Tree { return &Tree{tree: containTree(pid)} }

// Start prepares and starts cmd and returns the Tree that holds it.
func Start(cmd *exec.Cmd) (*Tree, error) {
	Prepare(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return Contain(cmd.Process.Pid), nil
}

// closedChan reports whether ch has been closed. A nil channel never is.
func closedChan(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// waitClosed waits for ch to be closed, for at most d. A nil channel is not
// waited on.
func waitClosed(ch <-chan struct{}, d time.Duration) {
	if ch == nil || d <= 0 {
		return
	}
	select {
	case <-ch:
	case <-time.After(d):
	}
}
