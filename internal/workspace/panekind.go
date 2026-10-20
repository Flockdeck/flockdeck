package workspace

import (
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// PaneKind is what sort of pane something is: the thing the graph, the lists
// and the phone are told, and the thing a consumer asks the pane for instead of
// looking at which agent runs in it.
type PaneKind string

const (
	// PaneKindTerminal is a pane whose agent or shell runs in a terminal that
	// Flockdeck reads the screen, hooks and transcript of. Every pane is one.
	PaneKindTerminal PaneKind = "terminal"
	// PaneKindStructured is a pane whose agent speaks the Agent Client Protocol,
	// so its state is told and not read. No pane is one yet.
	PaneKindStructured PaneKind = "structured"
)

// PaneCaps are what a pane can do that depends on its kind.
type PaneCaps struct {
	// Structured says the pane's state comes from a protocol and not from a
	// terminal, so it has no screen to read and no keys to type.
	Structured bool
	// AnswerPermission says a waiting permission can be answered by choosing
	// one of its options from a window, not by typing into the agent.
	AnswerPermission bool
}

// PermissionOption is one choice of a permission prompt: an id a window can send
// back, the words the agent's own prompt uses for it, and its kind. The kind
// uses the names of the Agent Client Protocol; the id is what the terminal path
// sends.
type PermissionOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// The kinds of a permission option. Terminal panes use only the first and the
// third; allow_always and reject_always are for structured panes.
const (
	OptionAllowOnce    = "allow_once"
	OptionAllowAlways  = "allow_always"
	OptionRejectOnce   = "reject_once"
	OptionRejectAlways = "reject_always"
)

// The ids of a terminal permission prompt's options.
const (
	PermissionYes = "yes"
	PermissionNo  = "no"
)

// optionTools are the tools whose Claude Code prompt has the shape documented in
// docs/status-matrix.md (B4 and B7). Another tool can offer other choices: the
// plan approval of ExitPlanMode does, and its first choice is not a plain Yes.
var optionTools = map[string]bool{"Bash": true, "Edit": true, "MultiEdit": true, "Write": true}

// TerminalPermissionOptions are the choices of a terminal permission prompt that
// are documented for the agent: Claude Code answers Yes with Enter, which is its
// default choice, and "No, and tell Claude what to do differently" with Esc (see
// docs/status-matrix.md, B4 and B7). Its middle choice, "Yes, and don't ask
// again", names a rule that depends on the tool and the version, so it is not
// listed. Any other agent, a pane that has not recorded its agent, and any tool
// outside optionTools gets none: those prompts are not parsed. The list is
// empty, never nil.
func TerminalPermissionOptions(agentID, tool string) []PermissionOption {
	if agentID != "claude" || !optionTools[tool] {
		return []PermissionOption{}
	}
	return []PermissionOption{
		{ID: PermissionYes, Name: "Yes", Kind: OptionAllowOnce},
		{ID: PermissionNo, Name: "No, and tell Claude what to do differently", Kind: OptionRejectOnce},
	}
}

// WaitingOn is what a waiting pane says it is waiting for: the detail its status
// carries (the tool's name) and the tool's input as the pane keeps it, an
// already-capped JSON object the permission view is made from.
type WaitingOn struct {
	Detail    string
	ToolInput string
}

// BackgroundWork is the work an agent has started that outlives its turn.
type BackgroundWork struct {
	// Tasks is how many pieces of work are counted, and Verified how many of them
	// something has shown to be running lately.
	Tasks, Verified int
	Running         []session.BackgroundWork
	Ended           []session.BackgroundEnded
}

// LastReply is a pane's latest assistant reply, for a row to quote.
type LastReply struct {
	Text string
	TS   string
	Kind string
}

// ConversationSource is what a pane's conversation is stored as and by whom: the
// agent's spec, the reader that turns the stored conversation into a transcript,
// and the conversation's id.
type ConversationSource struct {
	Spec agent.Spec
	ID   string
	// Exporter is nil when the agent keeps no conversation Flockdeck can read.
	Exporter transcript.Exporter
}

// Readable says the conversation can be recorded and exported.
func (c ConversationSource) Readable() bool { return c.Exporter != nil }

// PaneRuntime is everything the rest of Flockdeck reads from, or asks of, a pane.
// The graph, the needs-you list, a window's pane view, the agents list, spend
// rollups and recording start all go through it and none of them looks at which
// agent a pane runs. The terminal implementation is the only real one; a pane
// of another kind answers the same way.
//
// A runtime is read from several goroutines and must be safe for that. The
// methods that read the pane's own fields do not take the workspace lock; the
// ones documented as such are called by code that already holds it.
type PaneRuntime interface {
	Kind() PaneKind
	Caps() PaneCaps

	// Status is the pane's status and its detail, StatusExited and "" for a
	// pane that never started. StatusSince is when the status last changed and
	// StartedAt when the pane started, the zero time for one that has not.
	Status() (session.Status, string)
	StatusSince() time.Time
	StartedAt() time.Time
	// Activity is what the pane is shown as doing. It is derived from the
	// status and the background work, and is the only place that decides it.
	Activity() Activity
	// HasRun says an agent has been started, so a figure for its spend is
	// expected: no figure then makes a total a floor.
	HasRun() bool

	// Waiting is what a waiting pane is waiting for, and PermissionOptions the
	// choices its permission prompt for tool offers; an empty list where it
	// offers none a window can name.
	Waiting() WaitingOn
	PermissionOptions(tool string) []PermissionOption

	// Background is the work still running or just ended behind an idle agent.
	Background() BackgroundWork
	// LastReply is the latest reply of the pane's conversation, if one has been
	// seen.
	LastReply() (LastReply, bool)

	// Wake types message into the pane as Flockdeck's own input, to bring an
	// idle agent back to its mail.
	Wake(message string) error
	// Close ends the pane's process or connection.
	Close() error
	// Record turns the recording of the pane's conversation on or off.
	Record(on bool) error
	// Conversation is where the pane's conversation is kept. It is called with
	// the workspace lock held.
	Conversation() ConversationSource
}

// Runtime is the pane's runtime: the one set with SetRuntime, else the terminal
// one.
func (w *Workspace) Runtime(p *Pane) PaneRuntime {
	if p != nil && p.rt != nil {
		return p.rt
	}
	return terminalRuntime{w: w, p: p}
}

// SetRuntime gives a pane another runtime in place of the terminal one, and
// reports whether the pane was found. A pane keeps the runtime it is given for
// as long as it lives.
func (w *Workspace) SetRuntime(id string, rt PaneRuntime) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	p := w.panes[id]
	if p == nil {
		return false
	}
	p.rt = rt
	return true
}

// terminalRuntime is a pane that runs in a terminal, the behaviour every pane
// had before kinds: each answer is what its consumer used to read off the
// pane and its session.
type terminalRuntime struct {
	w *Workspace
	p *Pane
}

func (terminalRuntime) Kind() PaneKind { return PaneKindTerminal }
func (terminalRuntime) Caps() PaneCaps { return PaneCaps{} }

func (t terminalRuntime) Status() (session.Status, string) { return t.p.Status() }

func (t terminalRuntime) StatusSince() time.Time {
	if t.p.Sess == nil || t.p.Err != nil {
		return time.Time{}
	}
	return t.p.Sess.StatusSince()
}

func (t terminalRuntime) StartedAt() time.Time {
	if t.p.Sess == nil {
		return time.Time{}
	}
	return t.p.Sess.StartedAt()
}

func (t terminalRuntime) Activity() Activity { return PaneActivity(t.p) }

func (t terminalRuntime) HasRun() bool { return t.p.IsAgent() && t.p.Sess != nil }

func (t terminalRuntime) Waiting() WaitingOn {
	st, detail := t.p.Status()
	if st != session.StatusWaiting || t.p.Sess == nil {
		return WaitingOn{Detail: detail}
	}
	return WaitingOn{Detail: detail, ToolInput: t.p.Sess.ToolInput()}
}

func (t terminalRuntime) PermissionOptions(tool string) []PermissionOption {
	return TerminalPermissionOptions(t.p.Agent, tool)
}

func (t terminalRuntime) Background() BackgroundWork {
	// Nothing is left running once the process has gone, whatever was last
	// counted, and a shell has none to count.
	st, _ := t.p.Status()
	if t.p.Sess == nil || !t.p.IsAgent() || st == session.StatusExited {
		return BackgroundWork{}
	}
	s := t.p.Sess
	return BackgroundWork{Tasks: s.BackgroundTasks(), Verified: s.BackgroundVerified(),
		Running: s.BackgroundWork(), Ended: s.BackgroundEnded()}
}

// LastReply asks the lookup the server fills from the pane's transcript.
func (t terminalRuntime) LastReply() (LastReply, bool) {
	t.w.mu.RLock()
	look := t.w.replyLookup
	t.w.mu.RUnlock()
	if look == nil {
		return LastReply{}, false
	}
	return look(t.p.ID)
}

func (t terminalRuntime) Wake(message string) error {
	if t.p.Sess == nil {
		return ErrSendExited
	}
	typeIntoFrom(t.p.Sess, session.InputFlockdeck, message, true)
	return nil
}

func (t terminalRuntime) Close() error {
	if t.p.Sess == nil {
		return nil
	}
	return t.p.Sess.Close()
}

func (t terminalRuntime) Record(on bool) error {
	_, err := t.w.SetPaneRecording(t.p.ID, on)
	return err
}

func (t terminalRuntime) Conversation() ConversationSource {
	spec, ex, _ := t.w.transcriptSourceLocked(t.p)
	return ConversationSource{Spec: spec, ID: paneConversation(t.p), Exporter: ex}
}

// SetReplyLookup sets where a terminal pane's last reply comes from: the
// server's preview of the conversation it follows.
func (w *Workspace) SetReplyLookup(fn func(paneID string) (LastReply, bool)) {
	w.mu.Lock()
	w.replyLookup = fn
	w.mu.Unlock()
}

// conversationOfLocked is what the pane's conversation is made from, as its
// runtime says: the spec, the reader, and whether there is one. It is called
// with w.mu held.
func (w *Workspace) conversationOfLocked(p *Pane) (agent.Spec, transcript.Exporter, bool) {
	c := w.Runtime(p).Conversation()
	return c.Spec, c.Exporter, c.Readable()
}
