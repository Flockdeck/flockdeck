// Package structured is the runtime of one structured pane: an agent that
// speaks the Agent Client Protocol, started without a terminal, its turns, its
// permission questions and its transcript.
//
// A Runtime owns an acp.Client, a bridge.Bridge and the loop that sends the
// person's prompts one at a time. It has no knowledge of panes, windows or the
// server: the workspace adapts it to the pane interface, and everything the
// interface needs is on the Runtime (Start, Send, Cancel, Answer, Close,
// Snapshot, Subscribe).
//
// The agent is not trusted, and neither is what it says about itself: every
// string that comes from it and is kept for display (an error, a stderr tail,
// an authentication method) is cleaned and cut before Snapshot returns it.
//
// Nothing here answers a permission request by itself. A request waits until
// Answer names one of its options, or until the turn is cancelled or the
// Runtime closed, which answer it cancelled.
package structured

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/acp/manifest"
	"github.com/jmwri/flockdeck/internal/acp/permprobe"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Phase is where the Runtime is in its life.
type Phase string

const (
	// PhaseStarting: the agent is being started, initialized and given a session.
	PhaseStarting Phase = "starting"
	// PhaseReady: there is a session. Snapshot.Status says whether it is working.
	PhaseReady Phase = "ready"
	// PhaseSetup: the agent cannot run until the person signs in to it.
	// Snapshot.Setup says how. The state is final: the runtime does not retry after the person has signed in, and the pane must
	// be started again.
	PhaseSetup Phase = "needs setup"
	// PhaseExited: the agent's process ended. Snapshot.Err says how.
	PhaseExited Phase = "exited"
	// PhaseFailed: the agent could not be started or did not complete setup.
	PhaseFailed Phase = "failed"
	// PhaseClosed: Close was called.
	PhaseClosed Phase = "closed"
)

// The kinds of setup a pane can be waiting on. An agent whose program cannot be
// found or started is not one of them: it is unavailable, and says only that.
const (
	SetupLogin = "login"
)

const (
	// maxQueue is how many prompts wait behind the running turn.
	maxQueue = 16
	// maxPrompt is the longest prompt Send takes.
	maxPrompt = 1 << 20
	// closeWait bounds how long Close waits for the loop to end.
	closeWait = 15 * time.Second
	// tapPoll is how often the recorder's lost lines are looked for.
	tapPoll = 500 * time.Millisecond

	errCap   = 300
	fieldCap = 200
)

var (
	// ErrNotRunning is returned by Send and Cancel when there is no live
	// session to use: the pane is still starting (Send queues, Cancel does not),
	// or it is over.
	ErrNotRunning = errors.New("structured: the agent is not running")
	// ErrQueueFull is returned by Send when too many prompts are waiting.
	ErrQueueFull = errors.New("structured: too many prompts are waiting")
	// ErrStale is returned by Answer when the request is not open any more,
	// is not the one the answer was meant for, or was never asked.
	ErrStale = errors.New("structured: no such open permission request")
	// ErrBadOption is returned by Answer for an option the request did not offer.
	ErrBadOption = errors.New("structured: the request has no such option")
)

// Command is a program to start in place of the one the manifest names. It is
// for tests, which start the fake agent. Its environment is always the one
// built from the entry; the unexported env field lets a test in this package
// give another, and nothing outside it can, so the allowlist cannot be bypassed.
type Command struct {
	Path string
	Args []string
	env  []string
}

// Recorder takes the agent's traffic. *record.RPCRecorder is one.
type Recorder interface {
	Tap(dir string, raw []byte)
	// Gap notes n lines of traffic that never reached Tap, because the
	// client's queue for them was full.
	Gap(n int)
	Flush() error
	Close() error
}

// env is the environment the agent is started with: acp.MinimalEnv and exactly
// the variables the entry lists, from the environment given in the options.
func (r *Runtime) env() []string {
	env := append(acp.MinimalEnv(), r.opts.Spec.ACPPassEnv(r.opts.Environ)...)
	// What the manifest sets for the adapter comes last and replaces a variable
	// of the same name that was passed on, so the user's environment cannot undo it.
	set := r.opts.Spec.ACPLaunchEnv()
	if len(set) == 0 {
		return env
	}
	names := map[string]bool{}
	for _, kv := range set {
		name, _, _ := strings.Cut(kv, "=")
		names[strings.ToUpper(name)] = true
	}
	kept := env[:0:0]
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); !names[strings.ToUpper(name)] {
			kept = append(kept, kv)
		}
	}
	return append(kept, set...)
}

// Options describes the pane to start.
type Options struct {
	// Spec is the agent: a catalog entry with Runner agent.RunnerACP. The
	// environment the agent gets is acp.MinimalEnv plus Spec.ACPPassEnv, and
	// nothing else.
	Spec agent.Spec
	// Cwd is the session's working directory, absolute.
	Cwd string
	// Environ is where Spec.ACP.PassEnv is read from. Nil means os.Environ().
	Environ []string
	// MCPServers are given to the session when Spec.ACP.MCP says the agent
	// takes them; otherwise they are not sent.
	MCPServers []acp.MCPServer
	// ResumeSession is the id of the agent's session this pane had before, kept
	// by the pane's owner. When set, the runtime reopens it in place of making a
	// new one: with session/load if the agent has it (which replays the
	// conversation into the transcript), else with session/resume. If the agent
	// has neither, or the call fails, a new session is made and the transcript
	// says so (Snapshot.Resume is ResumeFailed).
	ResumeSession string

	// Records and Meta start a stream recording of the traffic. Nil Records
	// means none, which is what a pane with recording off passes.
	Records *record.Manager
	Meta    record.Meta
	// PermissionRunner runs the permission check of an adapter that has one
	// (Spec.ACPPermissionProbe). Nil means the real program. It is for tests.
	PermissionRunner permprobe.Runner

	// Recorder is a recording that is already open. It is used in place of
	// Records, and closed by Close.
	Recorder Recorder

	// Command replaces the program the manifest names. Probe replaces how PATH is looked at.
	Command *Command
	Probe   *manifest.Probe
	// Executable is the path of the running program, which is what an adapter
	// of Flockdeck's own is started from. Nil means os.Executable. It is for tests.
	Executable func() (string, error)

	// OnTurn is told what becomes of each prompt sent with SendTagged. It is
	// called from the runtime's own goroutines, outside its lock, and must not
	// block for long. Nil means nobody is told.
	OnTurn func(TurnEvent)

	// Bridge tunes the transcript bridge; the zero value works.
	Bridge bridge.Config
	// Client lets a test shorten the client's waits.
	Client func(*acp.Config)
}

// TurnEventKind is what happened to a tagged prompt.
type TurnEventKind string

const (
	// TurnBegan: the prompt was sent to the agent and its turn began.
	TurnBegan TurnEventKind = "began"
	// TurnEnded: the turn ended and the agent gave a stop reason.
	TurnEnded TurnEventKind = "ended"
	// TurnLost: the prompt will not get a stop reason: it was dropped from the
	// queue (cancel, close, the agent exiting), the agent exited during its
	// turn, or the agent refused it with an error. It may follow TurnBegan.
	TurnLost TurnEventKind = "lost"
)

// TurnEvent is one thing that happened to the prompt sent with Tag.
type TurnEvent struct {
	What TurnEventKind
	Tag  int
	// Stop is the stop reason of a TurnEnded, as the agent sent it.
	Stop string
	// Asked is set on a TurnEnded when Cancel was called while the turn ran, so
	// the stop was asked for and did not happen by itself.
	Asked bool
}

// Resume says how the session of a pane that asked to resume one began.
type Resume string

const (
	// ResumeNone: the pane did not ask to resume a session.
	ResumeNone Resume = ""
	// ResumeLoaded: session/load reopened the session and the agent replayed its
	// conversation into the transcript.
	ResumeLoaded Resume = "loaded"
	// ResumeResumed: session/resume reopened the session. The agent sends no
	// history, so the transcript starts empty and the agent still has the
	// conversation.
	ResumeResumed Resume = "resumed"
	// ResumeFailed: the session could not be reopened and a new one was made.
	ResumeFailed Resume = "new"
)

// ResumeFailedText is the line shown in the conversation when a session could
// not be reopened. It is fixed: nothing the agent said about why is shown.
const ResumeFailedText = "Could not resume, started a new session."

// AuthMethod is a way of signing in that the agent listed.
type AuthMethod struct {
	ID          string
	Name        string
	Description string
}

// Setup is what the person has to do before the agent can run.
type Setup struct {
	Kind string // SetupLogin
	// Message is what the agent said.
	Message string
	// Methods are the ways of signing in the agent offered.
	Methods []AuthMethod
	// Login is what to do: the catalog entry's own advice for a login.
	Login string
}

// Snapshot is the state at one moment.
type Snapshot struct {
	Phase Phase
	// Status is working, waiting or idle while the Phase is PhaseReady, and
	// empty otherwise.
	Status   bridge.Status
	Activity string
	Pending  *bridge.Card
	Plan     []acp.PlanEntry
	Usage    bridge.Usage
	// Session is what the agent exposes for the session: slash commands, mode,
	// models, settings, markdown plan, title and capabilities.
	Session bridge.SessionState
	Entries []transcript.Entry
	// Queued is how many prompts wait behind the running turn.
	Queued int
	// Err is the last thing that went wrong, cleaned. After an exit it says
	// only that the agent is unavailable: what it wrote to stderr is not kept
	// for display. It is
	// kept after later turns succeed, so it says what last went wrong and not
	// that something is wrong now: a UI shows it with the phase and status,
	// never alone as the current state.
	Err   string
	Setup *Setup
	// Agent is the name the agent gave itself.
	Agent string
	// SessionID is the agent's id of the session, empty until there is one. It
	// is what a pane keeps to resume the session later (Options.ResumeSession).
	SessionID string
	// Resume says how the session began, when the pane asked to resume one.
	Resume Resume
	// Dropped is how many entries the bridge dropped to stay within its caps;
	// TapDropped how many lines of traffic the recording missed;
	// ProtocolErrors how many things the agent sent that could not be used.
	Dropped        int
	TapDropped     int64
	ProtocolErrors int64
}

// Runtime is one structured pane.
type Runtime struct {
	opts Options
	br   *bridge.Bridge

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when the loop has ended and all is torn down
	wake   chan struct{}

	protoErrs atomic.Int64

	mu      sync.Mutex
	phase   Phase
	setup   *Setup
	err     string
	agentNm string
	queue   []queued
	// inTurn says a prompt is with the agent, and asked that Cancel was called
	// during it.
	inTurn, asked bool
	closed        bool
	client        *acp.Client
	session       string
	resume        Resume
	// dead holds ids of sessions that could not be reopened and may still be
	// sending their replay (a load that timed out). Nothing for them is taken.
	dead map[string]struct{}
	// keep is the id a layout should keep in place of the live session's, set
	// when a reopen failed without an answer from the agent (a timeout).
	keep    string
	waiters []*waiter
	// settingCalls is how many setting requests are with the agent.
	settingCalls int
	subs    map[int]chan struct{}
	nextSub int
	rec     Recorder
	tapSeen int64
	// permRan says the launch check of the agent's permissions has run, and
	// permUnasked what it found; permNote says in a few words how it went.
	permRan, permUnasked bool
	permNote             string

	tearOnce sync.Once
	stopTap  chan struct{}
	tapDone  chan struct{}
}

// queued is a prompt waiting for its turn. tag is the caller's own number for it,
// or zero when the caller does not want to be told what became of it.
type queued struct {
	blocks []string
	tag    int
	// mail is set for a prompt that carries a message from a peer. The
	// conversation shows it as that message and not as something the person typed.
	mail *bridge.Mail
}

type waiter struct {
	card bridge.Card
	ch   chan string
}

// Start begins starting the agent and returns at once; Snapshot says how it is
// going. ctx ending closes the Runtime.
func Start(ctx context.Context, opts Options) (*Runtime, error) {
	if opts.Command == nil && opts.Spec.Runner != agent.RunnerACP {
		return nil, fmt.Errorf("structured: agent %q is not a structured agent", opts.Spec.ID)
	}
	if !filepath.IsAbs(opts.Cwd) {
		return nil, fmt.Errorf("structured: the working directory %q is not absolute", opts.Cwd)
	}
	if opts.Environ == nil {
		opts.Environ = os.Environ()
	}
	rctx, cancel := context.WithCancel(ctx)
	r := &Runtime{
		opts:    opts,
		br:      bridge.New(opts.Bridge),
		ctx:     rctx,
		cancel:  cancel,
		done:    make(chan struct{}),
		wake:    make(chan struct{}, 1),
		phase:   PhaseStarting,
		subs:    map[int]chan struct{}{},
		stopTap: make(chan struct{}),
		tapDone: make(chan struct{}),
	}
	go r.run()
	// ctx ending closes it, though nobody called Close.
	go func() {
		select {
		case <-rctx.Done():
			r.finish(PhaseClosed)
		case <-r.done:
		}
	}()
	return r, nil
}

// Subscribe returns a channel that has a signal in it whenever the state may
// have changed since it was last read, and a function that ends the
// subscription. Signals are merged: reading one means call Snapshot.
func (r *Runtime) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	id := r.nextSub
	r.nextSub++
	r.subs[id] = ch
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.subs, id)
		r.mu.Unlock()
	}
}

// notify wakes every subscriber. It never blocks.
func (r *Runtime) notify() {
	r.mu.Lock()
	for _, ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	r.mu.Unlock()
}

// Send queues a prompt. It is sent when the turn before it has ended, and
// right away if there is none. It may be called while the agent is starting.
func (r *Runtime) Send(text string) error { return r.send(0, text) }

// SendTagged is Send for a prompt in several text content blocks, which the
// agent is given as separate blocks. The tag is a positive number of the
// caller's, and Options.OnTurn is told it with what becomes of the prompt, so a
// result is matched to the prompt by its tag and not by its place in the order.
func (r *Runtime) SendTagged(tag int, blocks ...string) error {
	if tag <= 0 {
		return errors.New("structured: a tagged prompt needs a positive tag")
	}
	return r.send(tag, blocks...)
}

// SendMail is SendTagged for a prompt that carries a message from a peer. The tag
// is the message's id. The conversation shows the prompt as a message from peer
// with the body text, and never as a turn of the person's, whatever the body or
// the blocks say. The blocks are what the agent is sent.
func (r *Runtime) SendMail(tag int, peer, body string, blocks ...string) error {
	if tag <= 0 {
		return errors.New("structured: a mail prompt needs a positive tag")
	}
	return r.sendMail(tag, &bridge.Mail{ID: tag, Peer: peer, Text: body, State: "delivered"}, blocks...)
}

func (r *Runtime) send(tag int, blocks ...string) error {
	return r.sendMail(tag, nil, blocks...)
}

// NoteSent shows in the conversation that this session sent message id to peer.
// A message already shown is not shown twice.
func (r *Runtime) NoteSent(id int, peer, body, state string) {
	r.br.MailSent(bridge.Mail{ID: id, Peer: peer, Text: body, State: state})
	r.notify()
}

// NoteMailState moves the entries of message id, received or sent by this
// session, to a new delivery state. It does nothing for a message the
// conversation does not show.
func (r *Runtime) NoteMailState(id int, state string) {
	if r.br.MailState(id, state) {
		r.notify()
	}
}

func (r *Runtime) sendMail(tag int, mail *bridge.Mail, blocks ...string) error {
	size := 0
	for _, b := range blocks {
		size += len(b)
	}
	if size == 0 || strings.TrimSpace(strings.Join(blocks, "")) == "" {
		return errors.New("structured: the prompt is empty")
	}
	if size > maxPrompt {
		return fmt.Errorf("structured: the prompt is %d bytes; the most is %d", size, maxPrompt)
	}
	r.mu.Lock()
	if r.closed || r.phase != PhaseStarting && r.phase != PhaseReady {
		r.mu.Unlock()
		return ErrNotRunning
	}
	if len(r.queue) >= maxQueue {
		r.mu.Unlock()
		return ErrQueueFull
	}
	r.queue = append(r.queue, queued{blocks: slices.Clone(blocks), tag: tag, mail: mail})
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
	r.notify()
	return nil
}

// Cancel ends the running turn and drops the prompts waiting behind it. Open
// permission requests are answered cancelled.
func (r *Runtime) Cancel() error {
	r.mu.Lock()
	c, sid, phase := r.client, r.session, r.phase
	if r.inTurn {
		r.asked = true
	}
	dropped := r.dropQueueLocked()
	r.mu.Unlock()
	r.lose(dropped)
	r.notify()
	if phase != PhaseReady || c == nil || sid == "" {
		return ErrNotRunning
	}
	return c.Cancel(sid)
}

// Answer chooses an option of the oldest open request with this id. Prefer
// AnswerCard, which also names the card.
func (r *Runtime) Answer(requestID, optionID string) error {
	return r.answer(0, requestID, optionID)
}

// AnswerCard chooses an option of the open request whose card has this Seq and
// agent request id. An answer for any other card, even one with the same
// request id, is ErrStale.
func (r *Runtime) AnswerCard(seq uint64, requestID, optionID string) error {
	if seq == 0 {
		return ErrStale
	}
	return r.answer(seq, requestID, optionID)
}

func (r *Runtime) answer(seq uint64, requestID, optionID string) error {
	r.mu.Lock()
	var w *waiter
	for _, c := range r.waiters {
		if c.card.RequestID == requestID && (seq == 0 || c.card.Seq == seq) {
			w = c
			break
		}
	}
	if w == nil {
		r.mu.Unlock()
		return ErrStale
	}
	ok := false
	for _, o := range w.card.Options {
		if o.OptionID == optionID {
			ok = true
			break
		}
	}
	if !ok {
		r.mu.Unlock()
		return ErrBadOption
	}
	r.removeWaiterLocked(w)
	r.mu.Unlock()
	w.ch <- optionID // buffered, and one answer per waiter
	r.notify()
	return nil
}

func (r *Runtime) removeWaiterLocked(w *waiter) {
	for i, c := range r.waiters {
		if c == w {
			r.waiters = append(r.waiters[:i], r.waiters[i+1:]...)
			return
		}
	}
}

// ask is the bridge's answer function: it waits for Answer, or for the request
// to stop needing one.
func (r *Runtime) ask(ctx context.Context, card bridge.Card) string {
	w := &waiter{card: card, ch: make(chan string, 1)}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ""
	}
	r.waiters = append(r.waiters, w)
	r.mu.Unlock()
	r.notify()
	select {
	case id := <-w.ch:
		return id
	case <-ctx.Done():
	case <-r.ctx.Done():
	}
	// An answer that arrives as the wait ends is dropped: the request is over.
	r.mu.Lock()
	r.removeWaiterLocked(w)
	r.mu.Unlock()
	r.notify()
	return ""
}

// Detail returns what an entry has beyond what Snapshot shows: a tool's full
// output and diff, a thinking block's text. It reports false for an entry the
// bridge no longer holds the detail of, or never had one for.
func (r *Runtime) Detail(id string) (transcript.Detail, bool) { return r.br.Detail(id) }

// Snapshot returns the state now.
func (r *Runtime) Snapshot() Snapshot {
	r.mu.Lock()
	s := Snapshot{
		Phase:  r.phase,
		Queued: len(r.queue),
		Err:    r.err,
		Agent:  r.agentNm,

		SessionID: r.session,
		Resume:    r.resume,
	}
	if r.setup != nil {
		c := *r.setup
		c.Methods = append([]AuthMethod(nil), c.Methods...)
		s.Setup = &c
	}
	c := r.client
	r.mu.Unlock()

	s.Entries = r.br.Snapshot()
	s.Plan = r.br.Plan()
	s.Usage = r.br.Usage()
	s.Session = r.br.Session()
	s.Dropped = r.br.Dropped()
	s.Pending = r.br.Pending()
	s.Activity = r.br.Activity()
	if s.Phase == PhaseReady {
		s.Status = r.br.Status()
	}
	if c != nil {
		s.TapDropped = c.TapDropped()
	}
	s.ProtocolErrors = r.protoErrs.Load()
	return s
}

// Close answers every open request cancelled, then ends the agent and
// everything it started, and closes the recording. It is safe to call twice
// and from several goroutines, and returns within a bounded time.
func (r *Runtime) Close() error {
	r.finish(PhaseClosed)
	select {
	case <-r.done:
	case <-time.After(closeWait):
		return errors.New("structured: the agent did not end in time")
	}
	return nil
}

// finish moves to a final phase (unless one was reached already), releases
// every waiting request as cancelled and stops the loop.
func (r *Runtime) finish(p Phase) {
	r.mu.Lock()
	if p == PhaseClosed {
		r.closed = true
	}
	if r.phase == PhaseStarting || r.phase == PhaseReady {
		r.phase = p
	}
	dropped := r.dropQueueLocked()
	waiters := r.waiters
	r.waiters = nil
	r.mu.Unlock()
	r.lose(dropped)
	for _, w := range waiters {
		select {
		case w.ch <- "":
		default:
		}
	}
	r.cancel()
	r.notify()
}

// set records a final phase from inside the loop.
func (r *Runtime) set(p Phase, err string, setup *Setup) {
	r.mu.Lock()
	if r.phase == PhaseStarting || r.phase == PhaseReady {
		r.phase = p
	}
	if err != "" {
		r.err = err
	}
	r.setup = setup
	dropped := r.dropQueueLocked()
	r.mu.Unlock()
	r.lose(dropped)
	r.notify()
}

// handler is the bridge's handler with a signal after each update.
type handler struct {
	acp.Handler
	r *Runtime
}

func (h handler) Update(u acp.Update) {
	h.r.mu.Lock()
	sid := h.r.session
	_, dead := h.r.dead[u.SessionID]
	h.r.mu.Unlock()
	// Only this pane's session has a say. Before the session id is known an
	// update cannot be checked and is taken, except one for a session that
	// failed to reopen: its late replay is not the conversation.
	if dead || (u.SessionID != "" && sid != "" && u.SessionID != sid) {
		return
	}
	h.Handler.Update(u)
	h.r.notify()
}

// run starts the agent and then runs the turns until something ends them.
func (r *Runtime) run() {
	defer close(r.done)
	defer r.teardown()
	if !r.launch() {
		return
	}
	r.loop()
}

// resolve says what to start.
func (r *Runtime) resolve() (Command, *Setup, error) {
	if c := r.opts.Command; c != nil {
		cmd := *c
		if cmd.env == nil {
			cmd.env = r.env()
		}
		return cmd, nil, nil
	}
	a, ok := r.opts.Spec.ACPAdapter()
	if !ok {
		return Command{}, nil, fmt.Errorf("the adapter %q is not in the manifest", clean(r.opts.Spec.ACP.Adapter, fieldCap))
	}
	probe := manifest.SystemProbe()
	if r.opts.Probe != nil {
		probe = *r.opts.Probe
	}
	look := exec.LookPath
	if probe.LookPath != nil {
		look = probe.LookPath
	}
	own := a.Command == ownCommand
	var path string
	var err error
	if own {
		// Flockdeck's own adapter is a subcommand of the program that is running.
		// A `flockdeck` found on PATH could be another version, or not ours.
		path, err = r.executable()
	} else {
		path, err = look(a.Command)
	}
	if err != nil {
		// Why is not said: not the program's name, not where it was looked for,
		// not how to get it.
		return Command{}, nil, errors.New(unavailable)
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	env := r.env()
	if own {
		env = append(env, r.homeEnv()...)
	}
	if pp, ok := r.opts.Spec.ACPPermissionProbe(); ok {
		env = r.checkPermissions(path, pp, a, env)
	}
	return Command{Path: path, Args: append([]string(nil), a.Args...), env: env}, nil, nil
}

// ownCommand is the command of the adapters Flockdeck builds into its own
// binary.
const ownCommand = "flockdeck"

// executable is the running program's path, absolute with links resolved.
func (r *Runtime) executable() (string, error) {
	exe := r.opts.Executable
	if exe == nil {
		exe = os.Executable
	}
	path, err := exe()
	if err != nil {
		return "", err
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	return path, nil
}

// homeEnv is where the user's home directory and settings are, which an adapter
// of Flockdeck's own needs to find the agent's login and configuration. They
// are read from the options' environment like any passed variable, and only
// the adapters Flockdeck builds are given them.
func (r *Runtime) homeEnv() []string {
	s := agent.Spec{ACP: agent.ACPSpec{PassEnv: []string{"HOME", "USERPROFILE", "APPDATA"}}}
	return s.ACPPassEnv(r.opts.Environ)
}

// checkPermissions reads what the agent resolves in the pane's directory, sets
// the adapter's setting so that the tools ask while the user's denials stay, and
// reads it again to see whether a tool still allows. It returns env with the
// setting replaced.
func (r *Runtime) checkPermissions(path string, pp manifest.PermissionProbe, a manifest.Adapter, env []string) []string {
	launch := make([]string, 0, len(env))
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); !strings.EqualFold(name, pp.Setting) {
			launch = append(launch, kv)
		}
	}
	run := r.opts.PermissionRunner
	if run == nil {
		run = permprobe.Exec
	}
	res := permprobe.Check(r.ctx, run, path, pp.Args, r.opts.Cwd, launch, pp.Setting, a.LaunchEnv[pp.Setting], pp.Agent, pp.Tools...)
	r.mu.Lock()
	r.permRan, r.permUnasked, r.permNote = true, res.Unasked, res.Note
	r.mu.Unlock()
	r.notify()
	return append(launch, pp.Setting+"="+res.Inline)
}

// Unasked says the agent may run commands or edit files without a permission
// request. For an adapter with a launch check it is what the check found once it
// has run, and until then the manifest's word; for any other, the manifest's.
// A current mode or setting that reads as running without asking (LooksUnasked)
// makes it true as well.
func (r *Runtime) Unasked() bool {
	r.mu.Lock()
	base := r.opts.Spec.RunsWithoutAsking()
	if r.permRan {
		base = r.permUnasked
	}
	r.mu.Unlock()
	// A mode that runs without asking counts, whether the person chose it or the
	// agent did, until the pane is in another.
	return base || r.settingsUnasked()
}

// PermissionNote says in a few words how the launch check of the permissions went.
func (r *Runtime) PermissionNote() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.permNote
}

// launch gets the agent to a session. It reports whether there is one.
func (r *Runtime) launch() bool {
	cmd, setup, err := r.resolve()
	if err != nil {
		if setup != nil {
			r.set(PhaseSetup, setup.Message, setup)
		} else {
			// The error is ours, but is shown as the fixed text all the same.
			r.set(PhaseFailed, unavailable, nil)
		}
		return false
	}

	rec := r.opts.Recorder
	if rec == nil && r.opts.Records != nil {
		rr, err := r.opts.Records.StartRPC(record.RPCConfig{Meta: r.opts.Meta})
		if err != nil {
			// A pane that cannot be recorded still runs; the person is told.
			r.mu.Lock()
			r.err = "the stream recording could not be started: " + clean(err.Error(), errCap)
			r.mu.Unlock()
		} else {
			rec = rr
		}
	}
	r.mu.Lock()
	r.rec = rec
	r.mu.Unlock()

	cfg := acp.Config{
		Path:            cmd.Path,
		Args:            cmd.Args,
		Dir:             r.opts.Cwd,
		Env:             cmd.env,
		Handler:         handler{Handler: r.br.Handler(r.ask), r: r},
		OnProtocolError: func(string) { r.protoErrs.Add(1); r.notify() },
		ClientName:      "flockdeck",
	}
	if rec != nil {
		cfg.Tap = rec.Tap
	}
	if r.opts.Client != nil {
		r.opts.Client(&cfg)
	}
	c, err := acp.Start(cfg)
	if err != nil {
		r.set(PhaseFailed, unavailable, nil)
		return false
	}
	r.mu.Lock()
	r.client = c
	r.mu.Unlock()
	if rec != nil {
		go r.watchTap(c, rec)
	} else {
		close(r.tapDone)
	}

	init, err := c.Initialize(r.ctx)
	if err != nil {
		r.fail(c, err)
		return false
	}
	if init.AgentInfo != nil {
		name := init.AgentInfo.Title
		if name == "" {
			name = init.AgentInfo.Name
		}
		r.mu.Lock()
		r.agentNm = clean(name, fieldCap)
		r.mu.Unlock()
	}
	r.br.Capabilities(init.Capabilities)
	var mcp []acp.MCPServer
	if r.opts.Spec.ACP.MCP {
		mcp = r.opts.MCPServers
	}
	created, resume, err := r.open(c, init.Capabilities, mcp)
	if err != nil {
		r.fail(c, err)
		return false
	}
	r.br.SessionNew(created)
	r.mu.Lock()
	r.session = created.SessionID
	r.resume = resume
	if r.phase == PhaseStarting {
		r.phase = PhaseReady
	}
	r.mu.Unlock()
	r.notify()
	return true
}

// SessionID is the id for the owner to save for resuming this pane's session.
// It is the live session's, except that until the agent has made or reopened
// one it is the id the pane was asked to resume, so that an owner that saves it
// while the agent is still starting, or after it could not start, does not lose
// it; and after a reopen that got no answer from the agent (a timeout), it is
// the id that was asked for, because the agent may still have that
// conversation. A definite refusal replaces it with the new session's.
func (r *Runtime) SessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keep != "" {
		return r.keep
	}
	if r.session != "" {
		return r.session
	}
	return r.opts.ResumeSession
}

// open gets a session: the one asked to resume when the agent can reopen it,
// otherwise a new one. The error is the one fail should settle, and is never
// that of a session that could not be reopened.
//
// With session/load and session/resume both on offer, a load the agent refused
// with an error is followed by a resume. A load that got no answer (a timeout)
// is not: the agent may still be sending the replay, so the id is marked dead
// and nothing more for it is taken, and a new session is made.
func (r *Runtime) open(c *acp.Client, caps acp.AgentCapabilities, mcp []acp.MCPServer) (acp.SessionNew, Resume, error) {
	id := r.opts.ResumeSession
	if id == "" {
		created, err := c.NewSessionInfo(r.ctx, r.opts.Cwd, mcp)
		return created, ResumeNone, err
	}
	var (
		created acp.SessionNew
		err     = acp.ErrUnsupported
	)
	r.mu.Lock()
	r.session = id // updates for the session are taken from now
	r.mu.Unlock()
	if caps.LoadSession {
		// The transcript is the replay alone.
		r.br.BeginReplay()
		created, err = c.LoadSession(r.ctx, id, r.opts.Cwd, mcp)
		if err == nil {
			r.br.EndReplay()
			return created, ResumeLoaded, nil
		}
		r.br.DropReplay()
		if r.reopenEnds(c, err) {
			return acp.SessionNew{}, ResumeNone, err
		}
		if !answered(err) {
			r.markDead(id)
		}
	}
	if caps.Resume && (!caps.LoadSession || answered(err)) {
		created, err = c.ResumeSession(r.ctx, id, r.opts.Cwd, mcp)
		if err == nil {
			return created, ResumeResumed, nil
		}
		if r.reopenEnds(c, err) {
			return acp.SessionNew{}, ResumeNone, err
		}
		if !answered(err) {
			r.markDead(id)
		}
	}
	// Anything else, a stale or unknown id among it, means a new session. The
	// id stays in the layout when the agent never answered.
	r.mu.Lock()
	r.session = ""
	if !answered(err) {
		r.keep = id
	}
	r.mu.Unlock()
	created, err = c.NewSessionInfo(r.ctx, r.opts.Cwd, mcp)
	if err != nil {
		return created, ResumeNone, err
	}
	r.mu.Lock()
	delete(r.dead, created.SessionID) // the agent made it afresh
	r.mu.Unlock()
	r.br.Notice(ResumeFailedText)
	return created, ResumeFailed, nil
}

// answered says the agent itself refused the request, or has no such method:
// a definite answer, as opposed to a timeout or a read that failed.
func answered(err error) bool {
	var rpc *acp.RPCError
	return errors.Is(err, acp.ErrUnsupported) || errors.As(err, &rpc)
}

// reopenEnds says err ends the pane: a login the agent wants, its exit, a close.
func (r *Runtime) reopenEnds(c *acp.Client, err error) bool {
	return !errors.Is(err, acp.ErrUnsupported) && r.endsPane(c, err)
}

func (r *Runtime) markDead(id string) {
	r.mu.Lock()
	if r.dead == nil {
		r.dead = map[string]struct{}{}
	}
	r.dead[id] = struct{}{}
	r.mu.Unlock()
}

// fail settles the phase for an error from the agent: a login it wants, its
// own exit, a close, or anything else.
func (r *Runtime) fail(c *acp.Client, err error) {
	if r.ctx.Err() != nil {
		return
	}
	var auth *acp.AuthRequiredError
	var exit *acp.ExitError
	switch {
	case errors.As(err, &auth):
		s := &Setup{Kind: SetupLogin, Message: signIn, Login: clean(r.opts.Spec.ACP.Login, errCap)}
		for _, m := range auth.Methods {
			s.Methods = append(s.Methods, AuthMethod{ID: clean(m.ID, fieldCap), Name: clean(m.Name, fieldCap), Description: clean(m.Description, errCap)})
		}
		r.set(PhaseSetup, s.Message, s)
	case errors.As(err, &exit):
		r.set(PhaseExited, exitText(exit.Stderr), nil)
	case errors.Is(err, acp.ErrClosed):
		r.set(PhaseClosed, "", nil)
	default:
		select {
		case <-c.Done():
			r.set(PhaseExited, exitText(c.StderrTail()), nil)
		default:
			// err can carry an RPC error message written by the agent.
			r.set(PhaseFailed, unavailable, nil)
		}
	}
}

// endsPane reports whether an error from a prompt ends the pane: the agent
// asked for a login (ACP gives -32000 to "authentication required", which the
// client reads as acp.AuthRequiredError), exited, or the client was closed.
// Any other error, such as an RPC error for a rate limit, ends only the turn.
func (r *Runtime) endsPane(c *acp.Client, err error) bool {
	var exit *acp.ExitError
	if acp.IsAuthRequired(err) || errors.As(err, &exit) || errors.Is(err, acp.ErrClosed) {
		return true
	}
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}

// dropQueueLocked empties the queue and returns the tags of the prompts that
// were in it. The caller holds r.mu.
func (r *Runtime) dropQueueLocked() []int {
	var tags []int
	for _, q := range r.queue {
		if q.tag != 0 {
			tags = append(tags, q.tag)
		}
	}
	r.queue = nil
	return tags
}

// lose tells OnTurn that the prompts with these tags will not be sent.
func (r *Runtime) lose(tags []int) {
	for _, t := range tags {
		r.turnEvent(TurnEvent{What: TurnLost, Tag: t})
	}
}

func (r *Runtime) turnEvent(e TurnEvent) {
	if r.opts.OnTurn != nil && e.Tag != 0 {
		r.opts.OnTurn(e)
	}
}

// loop sends the queued prompts one at a time.
func (r *Runtime) loop() {
	c := r.client
	for {
		q, ok := r.pop()
		if !ok {
			select {
			case <-r.wake:
				continue
			case <-c.Done():
				r.set(PhaseExited, exitText(c.StderrTail()), nil)
				return
			case <-r.ctx.Done():
				return
			}
		}
		r.mu.Lock()
		r.inTurn, r.asked = true, false
		r.mu.Unlock()
		if q.mail != nil {
			r.br.MailReceived(*q.mail)
		} else {
			r.br.TurnStart(strings.Join(q.blocks, "\n"))
		}
		r.notify()
		r.turnEvent(TurnEvent{What: TurnBegan, Tag: q.tag})
		stop, err := c.PromptBlocks(r.ctx, r.session, q.blocks...)
		r.mu.Lock()
		asked := r.asked
		r.inTurn, r.asked = false, false
		r.mu.Unlock()
		if err != nil {
			r.turnEvent(TurnEvent{What: TurnLost, Tag: q.tag})
			if r.ctx.Err() != nil {
				return
			}
			if !r.endsPane(c, err) {
				// The agent refused this prompt (a rate limit, an internal error)
				// but is still there. The turn ends with a notice that says so and
				// the session stays ready for the next prompt, whose place in the
				// queue is kept.
				// What the agent said about it is not kept or shown: the text is
				// the agent's, and it is fixed here.
				r.br.TurnEnd("failed: " + agentError)
				r.mu.Lock()
				r.err = agentError
				r.mu.Unlock()
				r.notify()
				continue
			}
			r.br.TurnEnd("failed")
			r.fail(c, err)
			return
		}
		r.br.TurnEnd(stop)
		r.turnEvent(TurnEvent{What: TurnEnded, Tag: q.tag, Stop: stop, Asked: asked})
		r.notify()
	}
}

func (r *Runtime) pop() (queued, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != PhaseReady || len(r.queue) == 0 {
		return queued{}, false
	}
	t := r.queue[0]
	r.queue = r.queue[1:]
	return t, true
}

// watchTap notes in the recording the lines the client could not queue for it.
func (r *Runtime) watchTap(c *acp.Client, rec Recorder) {
	defer close(r.tapDone)
	t := time.NewTicker(tapPoll)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			r.noteGap(c, rec)
		case <-r.stopTap:
			return
		}
	}
}

func (r *Runtime) noteGap(c *acp.Client, rec Recorder) {
	total := c.TapDropped()
	if total <= r.tapSeen {
		return
	}
	rec.Gap(int(total - r.tapSeen))
	r.tapSeen = total
}

// teardown ends the client and the recording, once.
func (r *Runtime) teardown() {
	r.tearOnce.Do(func() {
		r.finish(r.finalPhase())
		close(r.stopTap)
		r.mu.Lock()
		c, rec := r.client, r.rec
		r.mu.Unlock()
		if c != nil {
			<-r.tapDone
			_ = c.Close()
			if rec != nil {
				r.noteGap(c, rec)
			}
		}
		if rec != nil {
			_ = rec.Close()
		}
		r.notify()
	})
}

// finalPhase is the phase to leave in when the loop ends without having set
// one: the run was cancelled from outside.
func (r *Runtime) finalPhase() Phase {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase == PhaseStarting || r.phase == PhaseReady {
		return PhaseClosed
	}
	return r.phase
}

// exitText is what a pane shows of an agent that ended. It is the same words
// whatever the agent wrote: stderr is the agent's, may name its own
// dependencies, paths or keys, and is not shown to the user.
func exitText(string) string { return unavailable }

// unavailable is all the user is told of an agent that did not start or ended.
const unavailable = agent.UnavailableText

// agentError is all the user is told of a prompt the agent refused while still
// running.
const agentError = "the agent reported an error"

// signIn is shown when the agent wants a login. It is our wording, not the agent's.
const signIn = "sign in to the agent"

// clean makes a string from the agent fit to show: invalid UTF-8, control
// characters and Unicode format characters (bidi overrides, zero-width marks)
// removed, a tab or newline made a space, cut at n runes. It is the same rule
// as agent's cleanLine, plus the whitespace.
func clean(s string, n int) string {
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(s, "") {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			b.WriteByte(' ')
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == 0x2028 || r == 0x2029:
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(out) > n {
		out = string([]rune(out)[:n]) + "…"
	}
	return out
}
