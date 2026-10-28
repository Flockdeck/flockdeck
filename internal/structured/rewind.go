package structured

import (
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

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
	// ErrRewindStale: the window named a point of a conversation that has since
	// changed (a rewind, or a restart of the pane).
	ErrRewindStale = errors.New("structured: the conversation has changed since the window last saw it")
	// ErrRewindUnknown: the agent did not answer, so it is not known whether it
	// went back. The caller restarts the pane, so that window and agent agree.
	ErrRewindUnknown = errors.New("structured: the agent did not say whether it went back")
)

// runtimeSeq numbers the runtimes this program starts.
var runtimeSeq atomic.Int64

const (
	// maxMarks bounds the turns remembered; it matches what the adapters keep.
	maxMarks = 2000

	// ForkedText and ForkFailedText are the lines a forked pane starts with.
	// They are fixed: nothing the agent said about it is shown.
	ForkedText     = "Forked from another pane. The agent has the conversation up to that point; this window does not show it. It works in the same folder as the original, so the two can change the same files."
	ForkFailedText = "Could not fork, started a new session."
)

// ForkFrom says a pane's session is to be made by forking another: the agent's
// own id of the session, and the point the adapter gave for the cut, empty for
// the whole conversation.
type ForkFrom struct {
	Session string
	At      string
	// Model is the model the source session was on, if it said; the new session
	// is put on it when its models block offers it.
	Model string
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
	// After the agent moved the conversation to another session id on its own
	// (/clear; a move the runtime's own rewind asked for, rewindMove, is not that), the
	// uuids the adapter holds for earlier turns name messages of the old
	// session file: no turn is offered as a point.
	if id == "" || r.cleared {
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
	if r.client == nil || !r.client.Capabilities().Rewind || r.cleared {
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

// RewindToken names this runtime and how many rewinds it has had. A window
// sends the one it last saw with structuredRewind and structuredFork; the call
// is refused when it no longer matches, as it does after a rewind or a restart
// of the pane, when the entry ids the window holds mean something else.
func (r *Runtime) RewindToken() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprintf("%d.%d", r.seq, r.epoch)
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
func (r *Runtime) Rewind(id, token string) (string, error) {
	r.mu.Lock()
	c, session := r.client, r.session
	if c == nil || r.phase != PhaseReady {
		r.mu.Unlock()
		return "", ErrNotRunning
	}
	if token != fmt.Sprintf("%d.%d", r.seq, r.epoch) {
		r.mu.Unlock()
		return "", ErrRewindStale
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
	// The entry is checked before the agent is asked: once the agent has gone
	// back, the conversation must lose the same entries.
	if !ok || !r.br.HasPrompt(id) {
		r.mu.Unlock()
		return "", ErrNoPoint
	}
	turn := r.marks[i].token
	// Nothing is sent to the agent until the rewind ends (see pop). The agent
	// tells us, as part of the rewind, that the conversation moved to a new id.
	r.rewinding, r.rewindMove = true, true
	from := r.sessionIDLocked()
	r.rewindFrom = ""
	r.mu.Unlock()

	err := c.Rewind(r.ctx, session, turn)

	r.mu.Lock()
	r.rewinding, r.rewindMove = false, false
	if err != nil {
		r.mu.Unlock()
		r.wakeLoop()
		if r.ctx.Err() != nil {
			return "", ErrNotRunning
		}
		// The agent's own refusal means it did not go back. Anything else (no
		// answer in time, the agent gone) leaves it unknown.
		var rpc *acp.RPCError
		if errors.As(err, &rpc) {
			return "", ErrRewindFailed
		}
		r.mu.Lock()
		r.rewindFrom = from
		r.mu.Unlock()
		return "", ErrRewindUnknown
	}
	// The agent has gone back, so the marks are cut whatever the bridge finds:
	// the agent and the marks agree, and an entry the bridge no longer holds is
	// gone from the window already.
	text, _ := r.br.TruncateBefore(id)
	for j, m := range r.marks {
		if m.token == turn {
			r.marks = r.marks[:j:j]
			break
		}
	}
	r.epoch++
	r.mu.Unlock()
	r.notify()
	r.wakeLoop()
	return text, nil
}

// RewindFrom is the id the agent's conversation was resumed by before the last
// rewind, when that rewind got no answer, else "". The agent may have moved the
// conversation to a new id by then and the owner may have saved it; the old one
// is still whole, and is the one to resume by when the pane starts over.
func (r *Runtime) RewindFrom() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rewindFrom
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
func (r *Runtime) ForkSource(id, token string) (ForkFrom, error) {
	r.mu.Lock()
	c, session := r.client, r.session
	if c == nil || r.phase != PhaseReady || session == "" {
		r.mu.Unlock()
		return ForkFrom{}, ErrNotRunning
	}
	if token != fmt.Sprintf("%d.%d", r.seq, r.epoch) {
		r.mu.Unlock()
		return ForkFrom{}, ErrRewindStale
	}
	if !c.Capabilities().Fork {
		r.mu.Unlock()
		return ForkFrom{}, ErrForkNotOffered
	}
	// The conversation is forked by the id the agent resumes it by, which is the
	// moved one after a /clear or a rewind (see SessionID).
	acpSession := session
	if r.moved != "" {
		session = r.moved
	}
	if !r.idleLocked() {
		r.mu.Unlock()
		return ForkFrom{}, ErrRewindBusy
	}
	if id == "" {
		r.mu.Unlock()
		return ForkFrom{Session: session, Model: r.modelNow()}, nil
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
	turn := r.marks[i].token
	r.mu.Unlock()
	at, err := c.ForkPoint(r.ctx, acpSession, turn)
	if err != nil || at == "" {
		return ForkFrom{}, ErrRewindFailed
	}
	return ForkFrom{Session: session, At: at, Model: r.modelNow()}, nil
}

// openFork makes the pane's session by forking the one in Options.Fork. An agent
// that cannot, or a fork that fails, gets a new session and a fixed line saying
// so. A fork the agent refused is not asked again.
func (r *Runtime) openFork(c *acp.Client, mcp []acp.MCPServer) (acp.SessionNew, Resume, error) {
	f := r.opts.Fork
	created, err := c.ForkSession(r.ctx, f.Session, r.opts.Cwd, mcp, f.At)
	if err == nil {
		if m := created.Models; f.Model != "" && m != nil && m.Current != f.Model &&
			slices.ContainsFunc(m.Available, func(v acp.Model) bool { return v.ID == f.Model }) {
			if c.SetModel(r.ctx, created.SessionID, f.Model) == nil {
				cp := *m
				cp.Current = f.Model
				created.Models = &cp
			}
		}
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

// modelNow is the model the session is on, if the agent's models block says.
func (r *Runtime) modelNow() string {
	if m := r.br.Session().Models; m != nil {
		return m.Current
	}
	return ""
}

// OwnAdapter says the pane runs one of the adapters Flockdeck wrote (claude,
// codex-go) and not a command given for a test or an adapter from the manifest
// that someone else wrote. Only a card from one of these has option ids and
// kinds that can be read as they are named (see manifest.OwnAdapter).
func (r *Runtime) OwnAdapter() bool {
	return r.opts.Command == nil && r.opts.Spec.ACPOwnAdapter()
}

// Root is the directory the pane works in, absolute: the project root a phone's
// approval of an edit is held inside.
func (r *Runtime) Root() string { return r.opts.Cwd }
