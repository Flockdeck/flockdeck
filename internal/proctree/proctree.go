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

// Tree is what a started process's descendants are tracked by. The zero value
// tracks nothing, and End then falls back to the process table, so a nil or
// zero Tree is safe to End.
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
