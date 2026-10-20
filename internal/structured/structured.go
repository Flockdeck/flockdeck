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
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/acp/manifest"
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
	// PhaseSetup: the agent cannot run until the person does something: sign in
	// to it, install Node, or install the agent. Snapshot.Setup says what. The state is final: the
	// runtime does not retry after the person has signed in, and the pane must
	// be started again.
	PhaseSetup Phase = "needs setup"
	// PhaseExited: the agent's process ended. Snapshot.Err says how.
	PhaseExited Phase = "exited"
	// PhaseFailed: the agent could not be started or did not complete setup.
	PhaseFailed Phase = "failed"
	// PhaseClosed: Close was called.
	PhaseClosed Phase = "closed"
)

// The kinds of setup a pane can be waiting on.
const (
	SetupLogin = "login"
	SetupNode  = "node"
	// SetupInstall is an agent whose program is not on PATH.
	SetupInstall = "install"
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
	return append(acp.MinimalEnv(), r.opts.Spec.ACPPassEnv(r.opts.Environ)...)
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

	// Records and Meta start a stream recording of the traffic. Nil Records
	// means none, which is what a pane with recording off passes.
	Records *record.Manager
	Meta    record.Meta
	// Recorder is a recording that is already open. It is used in place of
	// Records, and closed by Close.
	Recorder Recorder

	// Command replaces the program the manifest names, and skips the Node
	// check. Probe replaces how PATH and Node are looked at.
	Command *Command
	Probe   *manifest.Probe

	// Bridge tunes the transcript bridge; the zero value works.
	Bridge bridge.Config
	// Client lets a test shorten the client's waits.
	Client func(*acp.Config)
}

// AuthMethod is a way of signing in that the agent listed.
type AuthMethod struct {
	ID          string
	Name        string
	Description string
}

// Setup is what the person has to do before the agent can run.
type Setup struct {
	Kind string // SetupLogin, SetupNode or SetupInstall
	// Message is what the agent or the check said.
	Message string
	// Methods are the ways of signing in the agent offered.
	Methods []AuthMethod
	// Login is what to do: the catalog entry's own advice for a login, Fix for
	// Node, and the install text for a program that is not there.
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
	Entries  []transcript.Entry
	// Queued is how many prompts wait behind the running turn.
	Queued int
	// Err is the last thing that went wrong, cleaned. After an exit it holds
	// the last three lines the agent wrote to stderr, joined with " / ". It is
	// kept after later turns succeed, so it says what last went wrong and not
	// that something is wrong now: a UI shows it with the phase and status,
	// never alone as the current state.
	Err   string
	Setup *Setup
	// Agent is the name the agent gave itself.
	Agent string
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
	queue   []string
	closed  bool
	client  *acp.Client
	session string
	waiters []*waiter
	subs    map[int]chan struct{}
	nextSub int
	rec     Recorder
	tapSeen int64

	tearOnce sync.Once
	stopTap  chan struct{}
	tapDone  chan struct{}
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
func (r *Runtime) Send(text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("structured: the prompt is empty")
	}
	if len(text) > maxPrompt {
		return fmt.Errorf("structured: the prompt is %d bytes; the most is %d", len(text), maxPrompt)
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
	r.queue = append(r.queue, text)
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
	r.queue = nil
	r.mu.Unlock()
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

// Snapshot returns the state now.
func (r *Runtime) Snapshot() Snapshot {
	r.mu.Lock()
	s := Snapshot{
		Phase:  r.phase,
		Queued: len(r.queue),
		Err:    r.err,
		Agent:  r.agentNm,
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
	r.queue = nil
	waiters := r.waiters
	r.waiters = nil
	r.mu.Unlock()
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
	r.queue = nil
	r.mu.Unlock()
	r.notify()
}

// handler is the bridge's handler with a signal after each update.
type handler struct {
	acp.Handler
	r *Runtime
}

func (h handler) Update(u acp.Update) {
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
	if a.NeedsNode {
		if err := manifest.CheckNode(r.ctx, a, probe); err != nil {
			var ne *manifest.NodeRequiredError
			if errors.As(err, &ne) {
				return Command{}, &Setup{Kind: SetupNode, Message: clean(ne.Error(), errCap), Login: clean(ne.Fix, errCap)}, err
			}
			return Command{}, nil, err
		}
	}
	look := exec.LookPath
	if probe.LookPath != nil {
		look = probe.LookPath
	}
	path, err := look(a.Command)
	if err != nil {
		msg := fmt.Sprintf("%s was not found on PATH", a.Command)
		install := r.opts.Spec.Install
		if install == "" {
			install = a.Install
		}
		full := msg
		if install != "" {
			full += ". " + install
		}
		return Command{}, &Setup{Kind: SetupInstall, Message: clean(msg, errCap), Login: clean(install, errCap)}, errors.New(full)
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return Command{Path: path, Args: append([]string(nil), a.Args...), env: r.env()}, nil, nil
}

// launch gets the agent to a session. It reports whether there is one.
func (r *Runtime) launch() bool {
	cmd, setup, err := r.resolve()
	if err != nil {
		if setup != nil {
			r.set(PhaseSetup, setup.Message, setup)
		} else {
			r.set(PhaseFailed, clean(err.Error(), errCap), nil)
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
		r.set(PhaseFailed, clean(err.Error(), errCap), nil)
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
	var mcp []acp.MCPServer
	if r.opts.Spec.ACP.MCP {
		mcp = r.opts.MCPServers
	}
	sid, err := c.NewSession(r.ctx, r.opts.Cwd, mcp)
	if err != nil {
		r.fail(c, err)
		return false
	}
	r.mu.Lock()
	r.session = sid
	if r.phase == PhaseStarting {
		r.phase = PhaseReady
	}
	r.mu.Unlock()
	r.notify()
	return true
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
		s := &Setup{Kind: SetupLogin, Message: clean(auth.Message, errCap), Login: clean(r.opts.Spec.ACP.Login, errCap)}
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
			r.set(PhaseFailed, clean(err.Error(), errCap), nil)
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

// loop sends the queued prompts one at a time.
func (r *Runtime) loop() {
	c := r.client
	for {
		text, ok := r.pop()
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
		r.br.TurnStart(text)
		r.notify()
		stop, err := c.Prompt(r.ctx, r.session, text)
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			if !r.endsPane(c, err) {
				// The agent refused this prompt (a rate limit, an internal error)
				// but is still there. The turn ends with a notice that says so and
				// the session stays ready for the next prompt, whose place in the
				// queue is kept.
				reason := clean(err.Error(), fieldCap)
				r.br.TurnEnd("failed: " + reason)
				r.mu.Lock()
				r.err = reason
				r.mu.Unlock()
				r.notify()
				continue
			}
			r.br.TurnEnd("failed")
			r.fail(c, err)
			return
		}
		r.br.TurnEnd(stop)
		r.notify()
	}
}

func (r *Runtime) pop() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != PhaseReady || len(r.queue) == 0 {
		return "", false
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

// exitText is what a pane shows of an agent that ended: the last three lines
// it wrote to stderr, cleaned, joined with " / ", and the end of them if they
// are longer than the cap. stderr is the agent's, so nothing in it is trusted.
func exitText(stderr string) string {
	var lines []string
	for _, l := range strings.Split(stderr, "\n") {
		if t := clean(l, errCap); t != "" {
			lines = append(lines, t)
		}
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	text := strings.Join(lines, " / ")
	if r := []rune(text); len(r) > errCap {
		text = "…" + string(r[len(r)-errCap:])
	}
	if text == "" {
		return "the agent exited"
	}
	return "the agent exited: " + text
}

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
