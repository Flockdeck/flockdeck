package workspace

import (
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// FanoutJobPane is one pane's outcome in a captured FanoutJob. Kind mirrors
// the three the webui's outcomeOf reads a live settled pane for: "done",
// "needs" (input) and "failed" -- so the history view can draw a row the
// same way buildSummaryCard already does for a job still on screen.
type FanoutJobPane struct {
	Name   string
	Branch string
	// Task is what the pane was spawned to do (see Pane.Task), which is what
	// a history entry has to say "what was fanned out" once the pane itself
	// is gone.
	Task   string
	Kind   string
	Detail string
}

// FanoutJob is one past fan-out job, captured by AddFanoutHistory when its
// tab -- or its last pane -- closed while every pane in it had already
// settled. Kept in memory only, per project; see Workspace.fanoutHistory.
type FanoutJob struct {
	ID    string
	Title string
	// At is when the job was captured, which is close enough to when it
	// actually finished for a history entry: a settled tab is ordinarily
	// closed soon after, and nothing before this ever asked when it did.
	At    time.Time
	Panes []FanoutJobPane
}

// fanoutHistoryLimit bounds how many jobs a project's history keeps, so a
// long-running project does not grow this without bound over a run that
// never restarts.
const fanoutHistoryLimit = 50

// AddFanoutHistory records a finished fan-out job against the project it
// belongs to, most recent first.
func (w *Workspace) AddFanoutHistory(root string, job FanoutJob) {
	if w.fanoutHistory == nil {
		w.fanoutHistory = map[string][]FanoutJob{}
	}
	list := append([]FanoutJob{job}, w.fanoutHistory[root]...)
	if len(list) > fanoutHistoryLimit {
		list = list[:fanoutHistoryLimit]
	}
	w.fanoutHistory[root] = list
}

// FanoutHistory returns a project's past fan-out jobs, most recent first.
func (w *Workspace) FanoutHistory(root string) []FanoutJob {
	return append([]FanoutJob(nil), w.fanoutHistory[root]...)
}

// FanoutTabSettled reports whether a delegated tab has genuinely finished --
// nothing left working or starting -- by the same rule the webui's
// tabSettleInfo applies to collapse a settled fan-out into its summary card:
// two or more agent panes, none of them a shell and none carrying a parent
// (a helper split into its own manager's tab, which is that manager's own
// conversation rather than delegated work). It is what lets a close capture
// a fan-out job's history only for a tab that actually ran one to
// completion, not one closed mid-run or an ordinary split a person happens
// to have built the same shape of by hand.
func (w *Workspace) FanoutTabSettled(t *Tab) bool {
	if t == nil || !t.Delegated {
		return false
	}
	ids := t.Tree.Panes()
	if len(ids) < 2 {
		return false
	}
	for _, id := range ids {
		p := w.Pane(id)
		if p == nil || !p.IsAgent() || p.Parent != "" {
			return false
		}
		if st, _ := p.Status(); st == session.StatusWorking || st == session.StatusStarting {
			return false
		}
	}
	return true
}
