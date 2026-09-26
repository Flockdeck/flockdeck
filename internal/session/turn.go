package session

// Event is one lifecycle event as the workspace hands it to a session: the
// event's name, the tool it names, a Notification's type, a SessionStart's
// source, the tool input it carries, and the subagent a tool event came from
// (empty for the main agent). See hooks.Event, which it is taken from.
type Event struct {
	Name             string
	Tool             string
	NotificationType string
	Source           string
	ToolInput        string
	Agent            string
}

// turnState is where a pane is in a turn, as far as its hooks have said.
type turnState int

const (
	// turnUnknown is a pane nothing has said anything about yet -- a fresh
	// start, or a restart that resumed a conversation mid-way. Events are
	// taken as they come, as they always were.
	turnUnknown turnState = iota
	// turnOpen is a turn a UserPromptSubmit began and nothing has ended.
	turnOpen
	// turnClosed is a turn that has ended: a Stop, a StopFailure, the user
	// interrupting, or Claude Code's idle nudge saying it is back at its
	// prompt.
	turnClosed
)

// ApplyEvent applies a lifecycle event to the pane's status: StatusForEvent's
// reading of it, ResolveBlocked's, and SetStatusFull, with what they cannot
// see on their own -- whether the turn the event belongs to is still going.
//
// Events are applied in the order they arrive, and that is not always the
// order they fired in. A hook that gave up (hooks.Emit's deadline, or Claude
// Code's own hook timeout) is still applied when it lands, after the events
// that followed it; a background subagent goes on calling tools after the
// turn that started it has ended; and some ends are never reported at all --
// Esc while the model is writing fires no Stop. Before, any of these left a
// pane showing working at an empty prompt for good, because nothing a hook
// reports decays. So, once a turn is over:
//
//   - A PostToolUse or PostToolUseFailure with no PreToolUse since belongs to
//     the turn that ended, and is dropped.
//   - A subagent's tool calls -- a background subagent's, going on after the
//     turn that started it -- change nothing: the pane shows the turn's end,
//     idle with its background work counted, steadily rather than flashing
//     working for each call. Claude Code says which they are with agent_id.
//     A question or permission prompt one of them puts is still shown, and
//     that same subagent's next tool event returns the pane to the turn's
//     end once it is answered.
//   - A PreToolUse that says no subagent is either one of those that lost
//     the race too, or a background subagent's from a Claude Code too old
//     to name it; nothing tells the two apart when it arrives, so it shows
//     working as it always did, and is counted. Each one that finishes is
//     uncounted, and once none is left the pane goes back to what the turn's
//     end left it showing. A SubagentStop does the same for anything left
//     counted.
//   - Claude Code's idle nudge, sent once it has sat at its prompt for about
//     a minute, is the one signal that it is there whatever was lost on the
//     way, and turns a pane still showing working idle. It leaves a pane
//     waiting on a question, or blocked, alone.
//
// A real ask -- a permission prompt, a question, an elicitation -- is applied
// whenever it comes: a background subagent can ask after the turn ended.
func (s *Session) ApplyEvent(ev Event) {
	st, detail, ok := StatusForEvent(ev.Name, ev.Tool, ev.NotificationType)
	s.mu.Lock()
	mapped := ok
	st, detail, ok = s.turnLocked(ev, st, detail, ok)
	changed := false
	switch {
	case ok:
		changed = s.setStatusLocked(st, detail, ev.ToolInput)
	case mapped:
		// Dropped as stale, but still the agent reporting: see hookSeq.
		s.hookSeq++
	}
	s.mu.Unlock()
	if changed {
		s.changed()
	}
}

// turnLocked is ApplyEvent's reading of an event against the turn: it takes
// StatusForEvent's result and returns what to apply instead, if anything.
func (s *Session) turnLocked(ev Event, st Status, detail string, ok bool) (Status, string, bool) {
	closed := s.turn == turnClosed
	if closed && ev.Agent != "" {
		if st, detail, ok, handled := s.subagentLocked(ev, st, detail, ok); handled {
			return st, detail, ok
		}
	}
	switch ev.Name {
	case "UserPromptSubmit":
		s.turn, s.strayTools, s.askingAgent = turnOpen, 0, ""
	case "Stop", "StopFailure":
		s.turn, s.strayTools, s.askingAgent = turnClosed, 0, ""
	case "SessionStart":
		// A compaction fires this in the middle of a turn, and is the same
		// conversation going on; any other source is a new one, or another.
		if ev.Source == "compact" {
			return st, detail, ok
		}
		s.resolveBlockedLocked(ev.Name, ev.Tool, st, detail)
		if ev.Source == "clear" {
			s.turn, s.strayTools, s.askingAgent, s.agentCalls = turnClosed, 0, "", nil
		}
		return st, detail, ok
	case "PreToolUse":
		if closed {
			s.strayTools++
			return st, detail, ok
		}
	case "PostToolUse", "PostToolUseFailure", "PermissionDenied", "Interrupted":
		if closed {
			if s.strayTools == 0 {
				return st, detail, false
			}
			s.strayTools--
			if s.strayTools == 0 {
				st, detail = s.settledLocked()
				return st, detail, true
			}
			return StatusWorking, "", true
		}
		if ev.Name == "Interrupted" {
			s.turn, s.strayTools = turnClosed, 0
		}
	case "SubagentStop":
		if closed && s.strayTools > 0 && s.status == StatusWorking {
			s.strayTools = 0
			st, detail = s.settledLocked()
			return st, detail, true
		}
		return st, detail, ok
	case "Notification":
		if ev.NotificationType != idlePromptNotification {
			return st, detail, ok
		}
		if s.status != StatusWorking {
			return st, detail, false
		}
		// A turn still open when Claude Code says it is at its prompt ended
		// without saying so: Esc while the model was writing, or a Stop that
		// never arrived. Which, nothing says, so a denial in it is not
		// reported as blocked on a guess -- the user may well be sitting at
		// the pane, having pressed Esc.
		if !closed {
			s.blockedTool = ""
		}
		s.turn, s.strayTools = turnClosed, 0
		st, detail = s.settledLocked()
		return st, detail, true
	}
	if !ok {
		return st, detail, ok
	}
	st, detail = s.resolveBlockedLocked(ev.Name, ev.Tool, st, detail)
	return st, detail, true
}

// subagentLocked is turnLocked's reading of a subagent's own event once the
// turn that started it is over: see ApplyEvent. handled is false for an
// event it leaves to be read like any other.
func (s *Session) subagentLocked(ev Event, st Status, detail string, ok bool) (Status, string, bool, bool) {
	switch ev.Name {
	case "PreToolUse", "PermissionRequest":
		if ev.Name == "PreToolUse" && st == StatusWorking {
			// Not shown, but kept: a permission prompt for it names nothing
			// of its own, and says what it is asking by this.
			if s.agentCalls == nil {
				s.agentCalls = map[string]agentCall{}
			}
			s.agentCalls[ev.Agent] = agentCall{tool: ev.Tool, input: ev.ToolInput}
			return st, detail, false, true
		}
		if !ok {
			return st, detail, false, true
		}
		// A question, or a permission prompt: the user is wanted, whoever
		// asked. Which subagent asked is kept, so its own next event is the
		// one that says the ask is over; and a prompt naming nothing is
		// about the call that subagent just made, as it would be had that
		// call been shown.
		if call, kept := s.agentCalls[ev.Agent]; kept && st == StatusWaiting && detail == "" && s.status != StatusWaiting {
			s.status, s.detail, s.toolInput = StatusWorking, call.tool, call.input
		}
		s.askingAgent = ev.Agent
		return st, detail, true, true
	case "PostToolUse", "PostToolUseFailure", "PermissionDenied":
		delete(s.agentCalls, ev.Agent)
		if ev.Agent != s.askingAgent || (s.status != StatusWaiting && s.status != StatusWorking) {
			return st, detail, false, true
		}
		s.askingAgent = ""
		st, detail = s.settledLocked()
		return st, detail, true, true
	case "SubagentStop":
		delete(s.agentCalls, ev.Agent)
	}
	return st, detail, ok, false
}

// agentCall is a subagent's tool call kept back from a pane whose turn is
// over: see subagentLocked.
type agentCall struct {
	tool, input string
}

// settledLocked is what a pane whose turn has ended shows once nothing is
// running in it: blocked, if the turn ended on a denial nothing has cleared
// since, and otherwise idle.
func (s *Session) settledLocked() (Status, string) {
	if s.blockedTool != "" {
		return StatusBlocked, s.blockedTool
	}
	return StatusIdle, ""
}
