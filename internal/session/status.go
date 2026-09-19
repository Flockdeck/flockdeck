package session

// Status describes what a session is currently doing. An agent whose Spec says
// it reports a lifecycle drives this from the events it sends Flockdeck; for a
// shell, for an agent that reports nothing, and until the first event of one
// that does arrives, it is read out of the pane's output instead -- the
// terminal bell, how long it has been quiet, and the lines the Spec says the
// agent prints when it is blocked on you or back at its prompt.
type Status int

const (
	// StatusStarting means the process has been spawned but has not yet
	// reported any lifecycle event.
	StatusStarting Status = iota
	// StatusWorking means the agent is actively producing output or running
	// tools, and does not need you.
	StatusWorking
	// StatusWaiting means the agent is blocked on you: a permission prompt or
	// a question. This is the status worth surfacing loudly. Claude Code's own
	// idle nudge -- sent about a minute after a turn ends, to say a pane has
	// simply gone quiet -- is not this: nobody is blocked on anything.
	StatusWaiting
	// StatusBlocked means a tool call was refused outright -- a permission or
	// safety classifier's own denial, not an interactive prompt -- and the
	// turn then ended with nothing since to say it recovered: no successful
	// tool call, no fresh prompt. Nobody is being asked anything, so there is
	// no prompt to answer, but it is stuck the same way StatusWaiting is, and
	// is worth surfacing the same way. See Session.resolveBlocked.
	StatusBlocked
	// StatusIdle means the agent finished its turn and is waiting for a new
	// prompt.
	StatusIdle
	// StatusExited means the process is gone.
	StatusExited
)

// String returns a short lowercase name for the status.
func (s Status) String() string {
	switch s {
	case StatusStarting:
		return "starting"
	case StatusWorking:
		return "working"
	case StatusWaiting:
		return "waiting"
	case StatusBlocked:
		return "blocked"
	case StatusIdle:
		return "idle"
	case StatusExited:
		return "exited"
	default:
		return "unknown"
	}
}

// Symbol returns the glyph used to represent the status in the tab bar and
// pane titles.
func (s Status) Symbol() string {
	switch s {
	case StatusStarting:
		return "◌"
	case StatusWorking:
		return "●"
	case StatusWaiting:
		return "▲"
	case StatusBlocked:
		return "!"
	case StatusIdle:
		return "○"
	case StatusExited:
		return "×"
	default:
		return "·"
	}
}

// NeedsAttention reports whether the session is blocked waiting on the user,
// or stuck for the same reason without an open prompt to say so.
func (s Status) NeedsAttention() bool { return s == StatusWaiting || s == StatusBlocked }
