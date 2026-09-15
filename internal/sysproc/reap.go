package sysproc

import "time"

// StaleProc is a process a previous run started and did not clean up after
// itself, as reported by whatever kept the record of it.
type StaleProc struct {
	// ID names whatever recorded this process -- a pane's id, say -- purely
	// for Reap's caller to say afterwards which records to forget.
	ID string
	// PID is the process id on record.
	PID int
	// Started is when the process was recorded as having started, for telling
	// it apart from a different process the system has since given the same
	// id; see Reap's same parameter. Zero when that was never known.
	Started time.Time
	// Path is where the process was working, carried through untouched for
	// the caller's own use once Reap is done -- deciding whether a leftover
	// folder can now be removed, say -- since Reap itself has no opinion
	// about it.
	Path string
}

// Reap ends every stale process that is still running under its recorded
// identity.
//
// A record alone is never enough to act on. The process id on record may by
// now belong to something that was never this application's -- ids are
// handed out again once their last owner is gone -- so same reports whether
// the process still running under a record's PID really is the one that made
// it, typically by comparing when it started against Started. Which records
// are worth ending at all -- a helper left running in a fan-out's worktree,
// say, as opposed to some other process nothing here should ever touch -- is
// for the caller to decide before a record ever reaches Reap; this asks only
// whether the one it was given is still the process it claims to be.
//
// terminate is what actually ends a confirmed process -- injected rather
// than fixed, so a caller that can reach more than the one pid a record names
// can end whatever it started too, the way closing a pane ends its whole
// process tree rather than the one process at the root of it; see
// internal/session.KillTree, which is what Flockdeck's own callers pass.
//
// killed is every record Reap ended the process for. settled is every record
// Reap is done with, whether or not it ended anything -- killed is always a
// subset of it, the rest being processes already gone. Either way there is
// nothing more for this sweep to do about them, and a caller keeping its own
// record of what is still worth watching should forget them along with the
// ones it reaped.
func Reap(procs []StaleProc, same func(StaleProc) bool, terminate func(pid int) error) (killed, settled []StaleProc) {
	for _, p := range procs {
		if p.PID <= 0 || !same(p) {
			settled = append(settled, p)
			continue
		}
		// Settled either way: a terminate that failed still leaves nothing
		// more for this sweep to do about the record, and a caller that kept
		// retrying it forever would do no better than this one attempt did.
		settled = append(settled, p)
		if terminate(p.PID) == nil {
			killed = append(killed, p)
		}
	}
	return killed, settled
}
