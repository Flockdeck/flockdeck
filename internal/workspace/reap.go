package workspace

import (
	"os"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// recordWorktreeProcess notes a pane's process as working in a git worktree,
// once it has actually started, so that a run which never gets the chance to
// close it -- killed outright, the machine losing power under it, an update
// relaunching before the old run finished tearing down -- leaves something
// the next run can still find it and end it by.
//
// Only a pane whose Cwd sits outside its own project is worth remembering
// this way: a worktree a fan-out cut sits beside the repository rather than
// under it (see gitx.WorktreePaths), which is what tells it apart from an
// ordinary pane working somewhere under the project it belongs to -- whose
// folder Flockdeck is never the one to go on and delete out from under it.
func (w *Workspace) recordWorktreeProcess(p *Pane) {
	if p == nil || p.Sess == nil || p.Root == "" || underDir(p.Cwd, p.Root) {
		return
	}
	pid := p.Sess.Pid()
	if pid == 0 {
		return
	}
	started, _ := session.Started(pid)
	_ = store.RecordWorktreeProcess(p.ID, store.WorktreeProcess{
		PID:     pid,
		Started: started,
		Cwd:     p.Cwd,
		Repo:    p.Root,
	})
}

// forgetWorktreeProcess removes a pane's record, once its process has ended
// the ordinary way. It is cheap and harmless to call for every pane closed,
// whether or not it was ever recorded to begin with.
func forgetWorktreeProcess(paneID string) {
	_ = store.ForgetWorktreeProcess(paneID)
}

// ReapStaleWorktreeProcesses ends every process a past run of Flockdeck put to
// work in a git worktree and never got the chance to close itself, and
// reports how many it found still running.
//
// It exists as a backstop for the whole class of bug a leak like that is,
// rather than as a fix for any one way it can happen. Whatever this run's own
// pane lifecycle gets right, a process nothing here remembers starting -- one
// left by a version of Flockdeck since replaced, or by a future bug this
// cannot yet know the shape of -- can still be found and ended by what it was
// put to work in, read back from the one run that did remember it. Called
// once, at the start of every run, before anything looks at what is open;
// see New.
func ReapStaleWorktreeProcesses() int {
	procs, err := store.WorktreeProcesses()
	if err != nil {
		return 0
	}
	reaped := 0
	for _, p := range procs {
		if session.KillProcessTree(p.PID, p.Started) {
			reaped++
			// Best effort: git may already have deleted everything under the
			// folder and left only the empty shell the process was holding
			// open, which can now come free. A repository git still lists it
			// as a worktree of is left alone -- that is the panel's own
			// remove or prune to do, since a process found running there
			// need not be the only reason it still stands.
			if !stillRegisteredWorktree(p.Repo, p.Cwd) {
				_ = os.Remove(p.Cwd)
			}
		}
		_ = store.ForgetWorktreeProcess(p.PaneID)
	}
	return reaped
}

// stillRegisteredWorktree reports whether path is a worktree git still has a
// record of, asked from repo. A repository that cannot be asked -- moved,
// deleted, on a drive that is not there -- answers false, since nothing safer
// can be said about a folder whose repository is itself gone.
func stillRegisteredWorktree(repo, path string) bool {
	if repo == "" || path == "" || !gitx.Available() {
		return false
	}
	wts, err := gitx.List(repo)
	if err != nil {
		return false
	}
	for _, wt := range wts {
		if sameDir(wt.Path, path) {
			return true
		}
	}
	return false
}
