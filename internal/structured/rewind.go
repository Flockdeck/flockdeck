package structured

import (
	"errors"

	"github.com/google/uuid"

	"github.com/jmwri/flockdeck/internal/acp"
)

// Going back to an earlier point of the conversation, and starting a new
// conversation from one. ACP has neither; Flockdeck's own adapters have both
// through the extension in internal/acp/rewind.go. This file is the runtime's
// half: every prompt sent gets a turn token, the runtime remembers which prompt
// entry each token belongs to, and a window names a point by the entry's id.
//
// A point is "before this prompt": the prompt and everything after it go. The
// first prompt of a session has no point before it. A prompt whose predecessor
// did not end by itself (it was cancelled, or failed) has none either, because
// the agent cannot be cut there cleanly. Prompts from before a resume have no
// token, and no entry in a resumed Claude or Codex pane, so they cannot be
// named.

// The fixed texts a window shows for a refusal. What the agent said is not
// passed on.
var (
	// ErrRewindNotOffered: the agent is not one that can go back.
	ErrRewindNotOffered = errors.New("structured: this agent cannot go back to an earlier point")
	// ErrRewindBusy: a turn is running, a prompt is waiting, or a rewind is out.
	ErrRewindBusy = errors.New("structured: the agent is busy")
	// ErrNoPoint: the entry is not a prompt that has a point before it.
	ErrNoPoint = errors.New("structured: there is no earlier point before that prompt")
	// ErrRewindFailed: the agent did not go back.
	ErrRewindFailed = errors.New("structured: the agent could not go back")
	// ErrForkNotOffered: the agent cannot fork a session.
	ErrForkNotOffered = errors.New("structured: this agent cannot fork a conversation")
)

const (
	// maxMarks bounds the turns remembered; it matches what the adapters keep.
	maxMarks = 2000

	// ForkedText and ForkFailedText are the lines a forked pane starts with.
	// They are fixed: nothing the agent said about it is shown.
	ForkedText     = "Forked from another pane. The agent has the conversation up to that point; this window does not show it."
	ForkFailedText = "Could not fork, started a new session."
)

// ForkFrom says a pane's session is to be made by forking another: the agent's
// own id of the session, and the point the adapter gave for the cut, empty for
// the whole conversation.
type ForkFrom struct {
	Session string
	At      string
}

// mark is one turn the agent has: its token, the prompt entry it came from (empty
// for a turn that was not typed, such as mail), and whether it ended by itself.
type mark struct {
	token    string
	entry    string
	complete bool
}

func newToken() string { return uuid.NewString() }

// addMark records a turn that is about to be sent and returns its token.
func (r *Runtime) addMark(entry string) string {
	m := mark{token: newToken(), entry: entry}
	r.mu.Lock()
	if len(r.marks) >= maxMarks {
		r.marks = append(r.marks[:0], r.marks[len(r.marks)-maxMarks+1:]...)
	}
	r.marks = append(r.marks, m)
	r.mu.Unlock()
	return m.token
}

// endMark notes whether the turn ended by itself.
func (r *Runtime) endMark(token string, complete bool) {
	r.mu.Lock()
	for i := len(r.marks) - 1; i >= 0; i-- {
		if r.marks[i].token == token {
			r.marks[i].complete = complete
			break
		}
	}
	r.mu.Unlock()
}

// pointLocked is the index of the mark whose prompt entry is id, if it has a
// point before it.
func (r *Runtime) pointLocked(id string) (int, bool) {
	// After the agent moved the conversation to another session id (/clear), the
	// uuids the adapter holds for earlier turns name messages of the old
	// session file: no turn is offered as a point.
	if id == "" || r.moved != "" {
		return 0, false
	}
	for i, m := range r.marks {
		if m.entry != id {
			continue
		}
		return i, i > 0 && r.marks[i-1].complete
	}
	return 0, false
}

// RewindPoints are the ids of the prompt entries that have a point before them,
// oldest first. It is empty for an agent that cannot go back.
func (r *Runtime) RewindPoints() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil || !r.client.Capabilities().Rewind || r.moved != "" {
		return nil
	}
	var out []string
	for i, m := range r.marks {
		if m.entry != "" && i > 0 && r.marks[i-1].complete {
			out = append(out, m.entry)
		}
	}
	return out
}

// Epoch counts the rewinds so far. A window that holds the conversation starts
// again when it changes, since entries were taken out.
func (r *Runtime) Epoch() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.epoch
}

// idleLocked says nothing is running or waiting.
func (r *Runtime) idleLocked() bool {
	return r.phase == PhaseReady && !r.inTurn && len(r.queue) == 0 && !r.rewinding
}

// Rewind takes the agent back to before the prompt entry id, and takes that
// prompt and everything after it out of the conversation. It returns the
// prompt's text. The agent's own history is cut by the agent; if it does not
// say it did, nothing here changes.
func (r *Runtime) Rewind(id string) (string, error) {
	r.mu.Lock()
	c, session := r.client, r.session
	if c == nil || r.phase != PhaseReady {
		r.mu.Unlock()
		return "", ErrNotRunning
	}
	if !c.Capabilities().Rewind {
		r.mu.Unlock()
		return "", ErrRewindNotOffered
	}
	if !r.idleLocked() {
		r.mu.Unlock()
		return "", ErrRewindBusy
	}
	i, ok := r.pointLocked(id)
	if !ok {
		r.mu.Unlock()
		return "", ErrNoPoint
	}
	token := r.marks[i].token
	// Nothing is sent to the agent until the rewind ends (see pop).
	r.rewinding = true
	r.mu.Unlock()

	err := c.Rewind(r.ctx, session, token)

	r.mu.Lock()
	r.rewinding = false
	if err != nil {
		r.mu.Unlock()
		r.wakeLoop()
		if r.ctx.Err() != nil {
			return "", ErrNotRunning
		}
		return "", ErrRewindFailed
	}
	text, found := r.br.TruncateBefore(id)
	// The marks are matched again by token: a prompt cannot have been added
	// meanwhile, but the slice is not trusted to be where it was.
	for j, m := range r.marks {
		if m.token == token {
			r.marks = r.marks[:j:j]
			break
		}
	}
	r.epoch++
	r.mu.Unlock()
	r.notify()
	r.wakeLoop()
	if !found {
		return "", ErrNoPoint
	}
	return text, nil
}

// wakeLoop lets the loop look at the queue again.
func (r *Runtime) wakeLoop() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// ForkSource says where a new pane forks this one from: the agent's id of the
// session, and, for a point before the prompt entry id, what the adapter gives
// for it. An empty id forks the whole conversation, which needs only the
// agent's own session/fork.
func (r *Runtime) ForkSource(id string) (ForkFrom, error) {
	r.mu.Lock()
	c, session := r.client, r.session
	if c == nil || r.phase != PhaseReady || session == "" {
		r.mu.Unlock()
		return ForkFrom{}, ErrNotRunning
	}
	if !c.Capabilities().Fork {
		r.mu.Unlock()
		return ForkFrom{}, ErrForkNotOffered
	}
	if !r.idleLocked() {
		r.mu.Unlock()
		return ForkFrom{}, ErrRewindBusy
	}
	if id == "" {
		// The whole conversation is forked by the id the agent resumes it by,
		// which is the moved one after a /clear (see SessionID).
		if r.moved != "" {
			session = r.moved
		}
		r.mu.Unlock()
		return ForkFrom{Session: session}, nil
	}
	if !c.Capabilities().Rewind {
		r.mu.Unlock()
		return ForkFrom{}, ErrRewindNotOffered
	}
	i, ok := r.pointLocked(id)
	if !ok {
		r.mu.Unlock()
		return ForkFrom{}, ErrNoPoint
	}
	token := r.marks[i].token
	r.mu.Unlock()
	at, err := c.ForkPoint(r.ctx, session, token)
	if err != nil || at == "" {
		return ForkFrom{}, ErrRewindFailed
	}
	return ForkFrom{Session: session, At: at}, nil
}

// openFork makes the pane's session by forking the one in Options.Fork. An agent
// that cannot, or a fork that fails, gets a new session and a fixed line saying
// so. A fork the agent refused is not asked again.
func (r *Runtime) openFork(c *acp.Client, mcp []acp.MCPServer) (acp.SessionNew, Resume, error) {
	f := r.opts.Fork
	created, err := c.ForkSession(r.ctx, f.Session, r.opts.Cwd, mcp, f.At)
	if err == nil {
		r.br.Notice(ForkedText)
		return created, ResumeNone, nil
	}
	if r.reopenEnds(c, err) {
		return acp.SessionNew{}, ResumeNone, err
	}
	created, err = c.NewSessionInfo(r.ctx, r.opts.Cwd, mcp)
	if err != nil {
		return created, ResumeNone, err
	}
	r.br.Notice(ForkFailedText)
	return created, ResumeNone, nil
}
