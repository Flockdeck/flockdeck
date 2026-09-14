package session

// Started reads an opaque marker of when pid began -- comparable only
// against another reading of the same pid taken on the same machine, never
// parsed or shown. ok is false where the process cannot be read at all,
// which is also what a pid answers once its process has exited.
//
// It is what a pane's process is read by when it is recorded as working in a
// git worktree (see workspace's worktree reaper), so that a bare process id
// read back at the start of a later run can be told apart from whatever
// unrelated process has since been given the same one.
func Started(pid int) (uint64, bool) {
	// procMetrics alone is not a liveness check: on Windows a process object,
	// and the creation time on it, stays readable through OpenProcess for as
	// long as anything holds a handle to it, which is not the same as the
	// process still running -- see stillRunning.
	if !stillRunning(pid) {
		return 0, false
	}
	m, ok := procMetrics(pid)
	if !ok {
		return 0, false
	}
	return m.started, true
}

// KillProcessTree forcefully ends pid and everything the machine's whole
// process table shows descended from it -- the same thing closing a pane
// does to its own process tree (see endTree), except that nothing here was
// started by this run, or is tracked by anything of its own: the process
// table is all that is left to find them by, the way Usage already reads it
// for CPU and memory.
//
// startedHint, when not zero, guards against pid having been handed to an
// unrelated process since it was recorded: a pid whose own Started reading
// no longer matches is left alone. It reports whether pid named a process
// that was actually found running -- ending nothing is not a failure by
// itself, since the process may simply have exited on its own since it was
// last seen.
func KillProcessTree(pid int, startedHint uint64) bool {
	started, ok := Started(pid)
	if !ok {
		return false
	}
	if startedHint != 0 && started != startedHint {
		return false
	}
	killTree(pid)
	return true
}
