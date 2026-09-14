package workspace

import (
	"os"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/sysproc"
)

// procStartSlack allows for the precision a process's start is known to, the
// same margin store.Instance is given: Linux reports it only to the second.
const procStartSlack = 2 * time.Second

// recordWorktreeProcess notes a pane's process as working in a git worktree,
// once it has actually started, so that a run which never gets the chance to
// close it -- killed outright, the machine losing power under it, an update
// relaunching before the old run finished tearing down, a bug in containTree
// or in endTree's own fallback -- leaves something the next run can still
// find it and end it by.
//
// Only a pane whose Cwd sits outside its own project is worth remembering
// this way: a worktree a fan-out cut sits beside the repository rather than
// under it (see gitx.WorktreePaths), which is what tells it apart from an
// ordinary pane working somewhere under the project it belongs to -- whose
// folder Flockdeck is never the one to go on and delete out from under it.
// This is what keeps ReapStaleWorktreeProcesses from ever needing to ask git
// which records are its to act on: only a worktree pane's process is ever
// written here to begin with.
func (w *Workspace) recordWorktreeProcess(p *Pane) {
	if p == nil || p.Sess == nil || p.Root == "" || underDir(p.Cwd, p.Root) {
		return
	}
	pid := p.Sess.Pid()
	if pid == 0 {
		return
	}
	_ = store.TrackWorktreeProc(p.ID, pid, p.Sess.StartedAt(), p.Cwd)
}

// forgetWorktreeProcess removes a pane's record, once its process has ended
// the ordinary way: closed with the pane, or with the run that started it. A
// pane that was never recorded -- one that never worked in a worktree -- has
// nothing to remove, which is not an error, and it is cheap and harmless to
// call for every pane closed.
func forgetWorktreeProcess(paneID string) {
	_ = store.UntrackWorktreeProc(paneID)
}

// ReapStaleWorktreeProcesses ends every process a past run of Flockdeck put to
// work in a git worktree and never got the chance to close itself, and
// reports how many it found still running.
//
// It is the backstop for the whole class of bug a leak like that is, rather
// than a fix for any one way it can happen: containTree's job object now sets
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, so Windows itself takes a pane's tree
// down within a moment of Flockdeck's own process disappearing, however that
// happens, and endTree falls back to walking the machine's process table
// itself when the job could never be made in the first place. What this
// still catches is whatever slips past both of those -- a version of
// Flockdeck built before either existed, security software that blocks a job
// object outright, a bug in either path this cannot yet know the shape of --
// found and ended by what it was put to work in, read back from the one run
// that recorded it. Called once, at the start of every run, before anything
// looks at what is open; see New.
//
// Nothing here asks git whether a record's path is still a worktree before
// ending the process in it: recordWorktreeProcess already only ever wrote one
// for a pane working outside its own project, and the very failure this
// exists for -- a process still holding a worktree's folder open -- is often
// exactly what has made git stop being able to answer for that folder at all
// (see gitx.Remove). Once a process is ended, though, whether its folder can
// safely be removed too is asked of git: a worktree git still has a record of
// is left alone, since clearing it out from under a checkout still registered
// is the worktree panel's own job, not this sweep's to do on its way past.
//
// The whole sweep is skipped while another instance of Flockdeck is already
// running. `-solo` starts a second instance deliberately, beside one that
// answers, and the two keep their own panes independently -- the registry
// they record into is shared, since there is only ever one state directory
// per user, but a record in it that names a process still running is not
// necessarily this launch's leftover; it may be that other instance's own
// pane, going about its work. Only once nothing is recorded as running, or
// what is recorded has itself gone, can a record here be trusted to be what
// it looks like: something nobody is left to answer for.
func ReapStaleWorktreeProcesses() int {
	if inst, err := store.LoadInstance(); err == nil && inst != nil && inst.PID != os.Getpid() && inst.StillRunning() {
		return 0
	}
	recs, err := store.LoadWorktreeProcs()
	if err != nil || len(recs) == 0 {
		return 0
	}
	procs := make([]sysproc.StaleProc, 0, len(recs))
	for id, r := range recs {
		procs = append(procs, sysproc.StaleProc{ID: id, PID: r.PID, Started: r.Started, Path: r.Path})
	}
	killed, settled := sysproc.Reap(procs, sameProcess, terminateTree)
	for _, p := range killed {
		// Best effort: git may already have deleted everything under the
		// folder and left only the empty shell the process was holding open,
		// which can now come free. A repository git still lists it as a
		// worktree of is left alone -- that is the panel's own remove or
		// prune to do, since a process found running there need not be the
		// only reason it still stands.
		if is, err := gitx.IsWorktree(p.Path); err != nil || !is {
			_ = os.Remove(p.Path)
		}
	}
	for _, p := range settled {
		_ = store.UntrackWorktreeProc(p.ID)
	}
	return len(killed)
}

// terminateTree ends pid and every process the machine's whole process table
// shows descended from it, the same way closing a pane ends its own tree --
// see session.KillTree -- rather than the bare process a single-pid kill
// would leave everything under it to outlive.
func terminateTree(pid int) error {
	session.KillTree(pid)
	return nil
}

// sameProcess reports whether the process still running under p.PID is the
// one that made the record, rather than one the system has since handed that
// id to. See store.Instance.StillRunning, which answers the same question
// for the running-instance record.
func sameProcess(p sysproc.StaleProc) bool {
	if p.PID <= 0 || !store.ProcessAlive(p.PID) {
		return false
	}
	if p.Started.IsZero() {
		return true
	}
	started, ok := store.ProcessStartedAt(p.PID)
	if !ok {
		return true
	}
	return !started.After(p.Started.Add(procStartSlack))
}
