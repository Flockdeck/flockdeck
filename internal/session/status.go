package session

// Status describes what a session is currently doing. For Claude sessions this
// is driven by lifecycle hooks the wrapper installs via --settings; for shell
// sessions (and as a fallback when hooks are unavailable) it is inferred from
// PTY output activity and the terminal bell.
type Status int

const (
	// StatusStarting means the process has been spawned but has not yet
	// reported any lifecycle event.
	StatusStarting Status = iota
	// StatusWorking means the agent is actively producing output or running
	// tools, and does not need you.
	StatusWorking
	// StatusWaiting means the agent is blocked on you: a permission prompt, a
	// question, or an idle nudge. This is the status worth surfacing loudly.
	StatusWaiting
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
	case StatusIdle:
		return "○"
	case StatusExited:
		return "×"
	default:
		return "·"
	}
}

// NeedsAttention reports whether the session is blocked waiting on the user.
func (s Status) NeedsAttention() bool { return s == StatusWaiting }
