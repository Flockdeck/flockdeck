package workspace

import "github.com/jmwri/flockdeck/internal/session"

// Activity is what a pane, a tab or a project is shown as doing: the one value
// every mark and every count in the window and on the phone is drawn from. It
// is derived from the session's status and its background work and is never
// stored; the session's status itself, which hooks, notifications and closing
// finished panes read, is left exactly as inferred.
//
// The rule is written here and nowhere else. A pane's activity is its status,
// with two readings on top: an agent idle between turns with background work
// still running (a run_in_background command, a background subagent) is
// working, and a pane that exited badly is failed. A tab's or a project's
// activity is the most urgent activity of its panes (Aggregate), so the three
// can never disagree about what they are made of.
type Activity string

const (
	// ActivityNone is nothing worth a mark: no panes, or only idle, starting
	// and exited ones. It is what an aggregate of those reads as.
	ActivityNone Activity = ""
	// The rest are a pane's own activity, named as its status dot's shapes are.
	ActivityWaiting  Activity = "waiting"
	ActivityBlocked  Activity = "blocked"
	ActivityWorking  Activity = "working"
	ActivityIdle     Activity = "idle"
	ActivityStarting Activity = "starting"
	ActivityExited   Activity = "exited"
	ActivityFailed   Activity = "failed"
)

// PaneActivity is the activity of a pane, from its session's status and
// background work. It is the only place that decides it.
func PaneActivity(p *Pane) Activity {
	if p == nil {
		return ActivityNone
	}
	st, _ := p.Status()
	if _, failed := p.Failed(); failed && st == session.StatusExited {
		return ActivityFailed
	}
	switch st {
	case session.StatusWaiting:
		return ActivityWaiting
	case session.StatusBlocked:
		return ActivityBlocked
	case session.StatusWorking:
		return ActivityWorking
	case session.StatusStarting:
		return ActivityStarting
	case session.StatusExited:
		return ActivityExited
	}
	// Idle: its turn is over, but an agent with work still running in the
	// background is not finished. A shell has none to count, and a process
	// that has gone has nothing running whatever was last counted.
	if p.Sess != nil && p.IsAgent() && p.Sess.BackgroundTasks() > 0 {
		return ActivityWorking
	}
	return ActivityIdle
}

// rank orders what an aggregate shows; anything not listed shows nothing.
func (a Activity) rank() int {
	switch a {
	case ActivityWaiting, ActivityBlocked:
		return 2
	case ActivityWorking:
		return 1
	}
	return 0
}

// NeedsAttention reports whether the activity is waiting on a person: waiting,
// or blocked, which a person has to look at the same way.
func (a Activity) NeedsAttention() bool { return a.rank() == 2 }

// Aggregate is the activity of a group of panes given each one's own: waiting
// (blocked counts as waiting) outranks working, which outranks nothing.
func Aggregate(acts ...Activity) Activity {
	out := ActivityNone
	for _, a := range acts {
		if a.rank() > out.rank() {
			switch a.rank() {
			case 2:
				out = ActivityWaiting
			case 1:
				out = ActivityWorking
			}
		}
	}
	return out
}

// Tally counts panes by the activity they add to an aggregate: a pane waiting
// or blocked counts as waiting, a working one as working, nothing else counts.
// It is what AttentionCount and Projects are made of, so a count and a mark
// drawn from the same panes agree.
type Tally struct{ Waiting, Working int }

// Add counts one pane's activity.
func (t *Tally) Add(a Activity) {
	switch a.rank() {
	case 2:
		t.Waiting++
	case 1:
		t.Working++
	}
}

// Activity is the aggregate of the counted panes.
func (t Tally) Activity() Activity {
	switch {
	case t.Waiting > 0:
		return ActivityWaiting
	case t.Working > 0:
		return ActivityWorking
	}
	return ActivityNone
}

// TabActivity is the aggregate activity of the panes in a tab.
func (w *Workspace) TabActivity(t *Tab) Activity {
	var tally Tally
	for _, id := range t.Tree.Panes() {
		tally.Add(PaneActivity(w.Pane(id)))
	}
	return tally.Activity()
}
