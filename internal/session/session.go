package session

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"

	"github.com/jmwri/flockdeck/internal/agent"
)

// Kind distinguishes an agent pane from a plain shell pane.
type Kind int

const (
	// KindAgent is a pane running a coding agent, described by an agent.Spec.
	KindAgent Kind = iota
	// KindShell is a pane running the user's shell.
	KindShell
)

// KindClaude is what an agent pane was called while the `claude` CLI was the
// only agent Flockdeck could run. It is the same kind under its older name, kept
// so that a pane is not read as a different sort of thing depending on which
// name the caller reached for.
const KindClaude = KindAgent

const (
	// replayBytes is how much recent output each pane keeps so a reconnecting
	// or reloading viewer can rebuild its screen.
	replayBytes = 512 << 10
	// subscriberQueue is how many output chunks may be outstanding for one
	// viewer before it is considered too slow to keep up.
	subscriberQueue = 512
	// subscriberBytes is how much output may be outstanding for one viewer,
	// which is the same question asked in the units that matter. A chunk is
	// whatever one read returned, up to the reader's whole buffer, so a queue
	// counted in chunks alone puts no bound at all on what a stalled viewer
	// holds: five hundred reads of thirty-two kilobytes is sixteen megabytes
	// for one pane, and a window that has been minimised or throttled stalls
	// every pane at once. Reaching this is also the point at which replaying
	// from history is cheaper than delivering the backlog.
	subscriberBytes = 2 << 20
	// drainGrace is how long the exit is held back so the reader can pick up
	// whatever the process printed on its way out. A PTY does not always
	// report end of output when the process it is attached to goes away, so
	// this is a grace period rather than something to wait on indefinitely.
	drainGrace = 500 * time.Millisecond

	// closeGrace bounds how long Close waits for a process it has killed to be
	// gone. Killing only starts the end of a process on Windows, and until it
	// is over the process still holds its working directory open.
	closeGrace = 3 * time.Second
	// quietBeforeIdle is how long a pane with no lifecycle hooks reporting for
	// it must print nothing before it is called idle again. It has to bridge
	// the pauses inside one piece of work -- a compiler between files, a test
	// runner between packages -- without leaving a pane that has genuinely
	// finished claiming to be busy.
	quietBeforeIdle = 3 * time.Second
	// bellGrace is how long after a pane starts its bells are treated as part
	// of starting up rather than a request for attention.
	bellGrace = 5 * time.Second
	// patternBytes is how much recent output an agent's own patterns are read
	// in. It is small on purpose: the patterns stand for what the agent is
	// doing now, and a question answered ten minutes ago is still somewhere in
	// the half megabyte of replay history, where it would go on reporting a
	// pane as blocked for the rest of its life.
	patternBytes = 512
	// maxCols and maxRows bound a resize. The dimensions are measured by the
	// browser and can be anything it cares to send, while a PTY allocates a
	// cell for every one of them, so a figure no display could produce is
	// clamped rather than honoured.
	maxCols = 2000
	maxRows = 2000
)

// Config describes a session to start.
type Config struct {
	ID   string // stable id; for agent panes this is also the conversation UUID
	Kind Kind
	// Spec is the agent running in the pane, and what the pane is understood
	// through after it starts: whether its status is reported to Flockdeck or has
	// to be read out of what it prints, and what to look for when it does.
	// A shell pane leaves it empty.
	Spec agent.Spec
	Name string // display name, usually the basename of Cwd
	Cwd  string
	Argv []string
	Env  []string
	Cols int
	Rows int
	// OnChange is installed as the session's OnChange before the process is
	// started. The reader starts with the process and can report a change
	// straight away, so assigning the field once Start has returned is a
	// write racing the reader's read of it.
	OnChange func()
}

// Session is a single pane: a process attached to a PTY.
//
// Terminal emulation happens in the browser, so this type does not interpret
// the byte stream. It owns the process, fans its output out to viewers, keeps
// enough history to redraw a reconnecting one, and tracks what the agent is
// doing.
type Session struct {
	ID   string
	Kind Kind
	Cwd  string

	pty pty.Pty
	cmd *pty.Cmd

	// OnChange is invoked (often from a reader goroutine) whenever the session
	// changes state. It must be cheap and must not call back into the session.
	// It is set through Config.OnChange: the reader is already running by the
	// time Start returns.
	OnChange func()

	mu          sync.RWMutex
	name        string
	status      Status
	detail      string // e.g. the tool currently running
	lastOutput  time.Time
	bellAt      time.Time
	statusSince time.Time
	exitErr     error
	closed      bool
	// hooksSeen records that lifecycle hooks have reported for this session,
	// which makes them authoritative over the terminal bell and over anything
	// else read out of the output.
	//
	// This is also where Caps.Hooks does its choosing. The hooks are only
	// installed for an agent whose Spec says it reports a lifecycle, so this can
	// only ever become true for one of those: for them the guesses below are a
	// bridge until the first event arrives, and for every other agent they are
	// the whole story, for as long as it runs.
	hooksSeen bool
	// toolQuestion records that the pane is waiting on a question put in the
	// middle of a tool call -- Claude's permission prompt -- which is the one
	// wait no hook reports the end of. See Write.
	toolQuestion bool
	// patterns are what to read out of an agent's output in place of the
	// lifecycle it does not report. They are taken off the Spec at launch
	// because the status machinery below runs on every chunk a pane prints and
	// must not go looking anything up to do it.
	patterns agent.Patterns
	// sawInput records that the user has typed into this pane, and startedAt
	// when it was launched. Claude rings the bell while starting up, so
	// without one of the two a freshly opened pane would announce that it
	// needs attention before anything has happened in it.
	sawInput   bool
	startedAt  time.Time
	cols, rows int
	// idleAfter is quietBeforeIdle, held per session so a test can shorten it.
	idleAfter time.Duration
	// settling records that a goroutine is already waiting to call this pane
	// idle again, so a pane printing steadily starts one rather than one per
	// chunk it prints.
	settling bool

	// usage is the last reading of what the pane's process tree costs, usageAt
	// is the process table it was taken from, and usageCPU is how much CPU each
	// process in the tree had used by then.
	usage    Usage
	usageAt  time.Time
	usageCPU map[int]time.Duration
	// cpuSeeded records that a CPU share has been measured at least once, so
	// the first measurable interval is reported rather than averaged against
	// the nothing before it.
	cpuSeeded bool

	// history holds recent output for replay; subs are the live viewers.
	history *ring
	// written is how many bytes the pane has printed, and modes what they
	// have done to the terminal's modes, both as of the last chunk in history.
	// They are copied off the reader's scanner here, under mu, so that
	// Subscribe can read them.
	written int64
	modes   termModes
	subs    map[int]*subscriber
	nextSub int

	// resizeMu orders resizes, and keeps one from overlapping the release of
	// the PTY it would resize. It is separate from mu because applying a
	// resize is a call into the PTY, which must not be made while the reader
	// is blocked out of publishing.
	resizeMu sync.Mutex

	// pumped is closed once the PTY reader has seen the end of the output.
	pumped chan struct{}
	// reaped is closed once the process has exited and been waited for.
	reaped chan struct{}

	// bell reads the bell and the terminal modes out of the output as it
	// streams. It is touched only in publish, under mu.
	bell bellScanner
}

// Start spawns the configured process attached to a new PTY and begins
// forwarding its output.
func Start(cfg Config) (*Session, error) {
	if cfg.Cols <= 0 {
		cfg.Cols = 80
	}
	if cfg.Rows <= 0 {
		cfg.Rows = 24
	}
	// The size a pane starts at comes from the same place a resize does -- the
	// browser's own measurement, kept in the saved layout -- so it needs the
	// same bound.
	cfg.Cols, cfg.Rows = clampSize(cfg.Cols, cfg.Rows)
	if len(cfg.Argv) == 0 {
		return nil, fmt.Errorf("session %s: no command to run", cfg.ID)
	}

	// go-pty resolves the executable relative to Cmd.Dir rather than searching
	// PATH, so resolve it ourselves before handing it over.
	exe, err := exec.LookPath(cfg.Argv[0])
	if err != nil {
		return nil, fmt.Errorf("locate %s: %w", cfg.Argv[0], err)
	}

	p, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("open pty: %w", err)
	}
	if err := p.Resize(cfg.Cols, cfg.Rows); err != nil {
		p.Close()
		return nil, fmt.Errorf("resize pty: %w", err)
	}

	s := &Session{
		ID:          cfg.ID,
		Kind:        cfg.Kind,
		Cwd:         cfg.Cwd,
		pty:         p,
		OnChange:    cfg.OnChange,
		name:        cfg.Name,
		status:      StatusStarting,
		statusSince: time.Now(),
		startedAt:   time.Now(),
		cols:        cfg.Cols,
		rows:        cfg.Rows,
		patterns:    foldPatterns(cfg.Spec.Patterns),
		idleAfter:   quietBeforeIdle,
		history:     newRing(replayBytes),
		subs:        map[int]*subscriber{},
		pumped:      make(chan struct{}),
		reaped:      make(chan struct{}),
	}

	cmd := command(p, exe, cfg.Argv[1:])
	cmd.Dir = cfg.Cwd
	cmd.Env = cfg.Env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	if err := cmd.Start(); err != nil {
		p.Close()
		return nil, fmt.Errorf("start %s: %w", cfg.Argv[0], err)
	}
	s.cmd = cmd

	go s.pumpOutput()
	go s.wait()

	return s, nil
}

// subscriber is one viewer's queue of output.
type subscriber struct {
	ch chan []byte
	// sizes holds the lengths of the chunks put into ch, oldest first, and
	// queued their sum. A channel is first in first out, so whatever is still
	// in it is the last len(ch) of what was put there, which is what makes the
	// outstanding bytes exactly knowable without the viewer reporting back.
	sizes  []int
	queued int
}

// settle forgets the chunks the viewer has taken since the last look.
func (v *subscriber) settle() {
	taken := len(v.sizes) - len(v.ch)
	if taken <= 0 {
		return
	}
	for _, n := range v.sizes[:taken] {
		v.queued -= n
	}
	// Copied down rather than resliced forwards, so the array is reused
	// instead of growing away from its start for the life of the viewer.
	v.sizes = append(v.sizes[:0], v.sizes[taken:]...)
}

// pumpOutput reads process output, records it for replay and fans it out.
func (s *Session) pumpOutput() {
	defer close(s.pumped)
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.publish(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// publish records a chunk and delivers it to every viewer.
//
// The chunk is borrowed for the duration of the call: it goes into the replay
// buffer by copy, and a copy is taken for viewers only when there are any. A
// pane whose output nobody is watching -- every pane on a tab that is not on
// screen -- then reads without allocating at all, which is most of them once
// a handful of agents are running.
func (s *Session) publish(chunk []byte) {
	// Output itself is not a change anyone outside this type can see: it
	// reaches viewers on their own subscriptions, and nothing in the interface
	// is drawn from the fact that bytes arrived. Reporting a change per chunk
	// is what keeps the whole workspace snapshot being rebuilt, encoded and
	// pushed to the browser for as long as any pane is streaming.
	notify := false

	s.mu.Lock()
	// The scanner carries state from one chunk to the next, so it is read and
	// written under the lock like everything else here. Only the reader calls
	// this in the running program, but nothing about the method says so, and a
	// second caller -- a test feeding a live pane, say -- raced it.
	rang := s.bell.scan(chunk)
	s.history.write(chunk)
	s.written += int64(len(chunk))
	s.modes = s.bell.modes
	s.lastOutput = time.Now()
	// A pane nothing is reporting for is read from what it prints, in three
	// ways that know progressively more. An agent's own patterns are the
	// sharpest: its Spec says what the lines it prints when it is blocked on
	// you, or back at its prompt, look like, which are exactly the two events
	// its lifecycle would have reported if it had one. A hook event outranks
	// all three, which is what !hooksSeen says throughout.
	inferred, patterned := StatusIdle, false
	if !s.hooksSeen && s.status != StatusExited {
		inferred, patterned = s.patternStatus()
	}
	switch {
	case patterned:
		if inferred != s.status {
			s.status = inferred
			s.statusSince = s.lastOutput
			notify = true
		}
	// Failing that, the fact that bytes arrived at all. Shell panes never get
	// lifecycle hooks, an agent whose Spec claims none never will, and one that
	// does has none until its first event arrives, so without this a build
	// running for a minute and a prompt nobody has typed at look exactly alike
	// from the tab bar.
	//
	// Waiting is left alone: it is the one status here worth surfacing, and the
	// bell and the patterns both know more than the fact that bytes arrived.
	case !s.hooksSeen && s.status != StatusExited && s.status != StatusWaiting:
		if s.status != StatusWorking {
			s.status = StatusWorking
			s.statusSince = s.lastOutput
			notify = true
		}
		if !s.settling {
			s.settling = true
			go s.settleIdle()
		}
	}
	if rang {
		s.bellAt = time.Now()
		// The bell is only a fallback. Claude rings it both when it wants input
		// and when a turn simply ends, so once lifecycle hooks are reporting
		// they are the sole source of truth; letting the bell win would flip
		// every completed turn to "waiting". An agent's own patterns outrank it
		// for the same reason: they can tell a question from the end of a turn
		// and the bell cannot, so a chunk they have spoken for is left to them.
		//
		// A pane is past its startup either because somebody typed into it or
		// because enough time has gone by. Waiting for the typing alone
		// silenced this for the panes it matters most for: an agent spawned
		// with its task on the command line is never typed at, and neither is
		// a pane restored from a saved layout until the user gets to it, so a
		// workspace of fifteen restored agents had no fallback at all if
		// their lifecycle hooks did not report.
		//
		// A shell is left out of it: it bells for a finished build and for a
		// command it did not recognise, neither of which is a question, and a
		// shell pane is never reported as wanting you.
		started := s.sawInput || time.Since(s.startedAt) > bellGrace
		if s.Kind == KindAgent && s.status != StatusExited && !s.hooksSeen && !patterned && started {
			// Claude rings again every time it nudges about the input it is
			// still waiting for, so the clock only starts on the transition:
			// restarting it on each bell is how a pane that has been blocked
			// for twenty minutes reports having just started waiting, which
			// is exactly the number being used to decide where to look.
			if s.status != StatusWaiting {
				s.statusSince = time.Now()
				notify = true
			}
			s.status = StatusWaiting
		}
	}
	var dead []int
	if len(s.subs) > 0 {
		owned := make([]byte, len(chunk))
		copy(owned, chunk)
		chunk = owned
	}
	for id, v := range s.subs {
		v.settle()
		// A viewer too slow to keep up would otherwise stall the process, or
		// hold the backlog for as long as it took to catch up. Drop it; the
		// client reconnects and replays from history, which is bounded.
		if v.queued+len(chunk) > subscriberBytes {
			dead = append(dead, id)
			continue
		}
		select {
		case v.ch <- chunk:
			v.sizes = append(v.sizes, len(chunk))
			v.queued += len(chunk)
		default:
			dead = append(dead, id)
		}
	}
	for _, id := range dead {
		close(s.subs[id].ch)
		delete(s.subs, id)
	}
	s.mu.Unlock()

	if notify {
		s.changed()
	}
}

// settleIdle returns an inferred-working pane to idle once it has been quiet
// for long enough, and gives up as soon as anything better informed -- a
// lifecycle hook, the bell, the process exiting -- has spoken for it.
func (s *Session) settleIdle() {
	for {
		s.mu.Lock()
		if s.hooksSeen || s.status != StatusWorking {
			s.settling = false
			s.mu.Unlock()
			return
		}
		if quiet := time.Since(s.lastOutput); quiet < s.idleAfter {
			s.mu.Unlock()
			time.Sleep(s.idleAfter - quiet)
			continue
		}
		s.status = StatusIdle
		s.statusSince = time.Now()
		s.settling = false
		s.mu.Unlock()
		s.changed()
		return
	}
}

// wait reaps the process and records its exit status.
func (s *Session) wait() {
	err := s.cmd.Wait()
	close(s.reaped)

	// The process is gone, but what it printed on the way out -- a shell's
	// goodbye, a crash message, the last frame Claude drew -- may still be
	// sitting in the PTY. Closing the subscriptions now would cut every live
	// viewer off before that arrived, leaving the pane showing something
	// other than the last thing that happened in it.
	select {
	case <-s.pumped:
	case <-time.After(drainGrace):
	}

	s.mu.Lock()
	s.exitErr = err
	s.status = StatusExited
	s.statusSince = time.Now()
	for id, v := range s.subs {
		close(v.ch)
		delete(s.subs, id)
	}
	s.mu.Unlock()

	// The process is gone and no further output can reach a viewer, so let the
	// pseudo-terminal go. A pane whose process ends on its own is left on
	// screen showing that it has, and nothing else closes it: without this the
	// reader stays blocked on a handle nothing will ever write to again, which
	// is not a hypothetical but the normal case on Windows, where a ConPTY
	// does not report the end of its output when the process attached to it
	// exits. The status is recorded first so that a write racing the exit gets
	// the message naming the pane rather than a closed handle.
	_ = s.releasePTY()

	s.changed()
}

// releasePTY closes the pseudo-terminal, once, whichever of the exit and an
// explicit close reaches it first.
func (s *Session) releasePTY() error {
	s.resizeMu.Lock()
	defer s.resizeMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	return s.pty.Close()
}

func (s *Session) changed() {
	s.mu.RLock()
	cb := s.OnChange
	s.mu.RUnlock()
	if cb != nil {
		cb()
	}
}

// Subscribe registers a viewer. It returns the output produced so far, so the
// viewer can rebuild its screen, and a channel of subsequent output. The
// channel is closed when the process exits or the viewer falls too far behind.
//
// Unsubscribe must be called with the returned id when the viewer goes away.
func (s *Session) Subscribe() (id int, replay []byte, out <-chan []byte) {
	id, replay, _, _, out = s.SubscribeFrom(0, -1)
	return id, replay, out
}

// SubscribeFrom is Subscribe for a viewer that already holds the start of this
// session's output: the first off bytes of the run named by epoch. While the
// rest is still held, replay is only what the viewer is missing and resumed is
// true, so it can carry on with the screen and the scrollback it has. Otherwise
// replay is the usual one, for a terminal started afresh. The viewer counts
// the bytes of replay and of everything after it on from start, and that count
// is the off it gives when it next subscribes.
func (s *Session) SubscribeFrom(epoch, off int64) (id int, replay []byte, start int64, resumed bool, out <-chan []byte) {
	ch := make(chan []byte, subscriberQueue)

	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch == s.Epoch() && off >= 0 {
		replay, resumed = s.history.since(s.written, off)
	}
	if resumed {
		// Nothing is put back ahead of a resume. The viewer already has every
		// mode switched before where it stopped, and switching the alternate
		// screen on again would clear the one it is showing.
		start = off
	} else {
		replay = s.history.replay()
		start = s.written - int64(len(replay))
		// Whatever the output switched before the replay begins has to be
		// switched again ahead of it, or the window rebuilding its screen does
		// not know. The viewer counts those bytes like the rest, so where it
		// counts from moves back by as many.
		if restore := s.modes.restore(start); len(restore) > 0 {
			replay = append(restore, replay...)
			start -= int64(len(restore))
		}
	}
	if s.status == StatusExited {
		close(ch)
		return -1, replay, start, resumed, ch
	}
	s.nextSub++
	id = s.nextSub
	s.subs[id] = &subscriber{ch: ch}
	return id, replay, start, resumed, ch
}

// Epoch names this run of the pane's process. A restarted pane is a new
// session with output of its own, so a place in the last one's means nothing
// in it. When it started is what tells the two apart, and unlike a counter it
// still does across Flockdeck itself being restarted.
func (s *Session) Epoch() int64 { return s.startedAt.UnixMicro() }

// AltScreen reports whether the pane's program has switched to the alternate
// screen -- a full-screen program: vim, htop, an agent's own full-screen view
// -- and not back. The replay of one is its screen as it was drawn, a piece at
// a time, so a window starting afresh on it has to have it redrawn.
func (s *Session) AltScreen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range trackedModes {
		switch t.mode {
		case 1049, 1047, 47:
			if s.modes.changed&(1<<i) != 0 {
				return true
			}
		}
	}
	return false
}

// BracketedPaste reports whether the pane's program has asked for bracketed
// paste (mode 2004) and not switched it back off: whether text it is sent
// between ESC[200~ and ESC[201~ is taken as one paste, line breaks and all,
// rather than typed a key at a time with each break pressing Enter.
func (s *Session) BracketedPaste() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range trackedModes {
		if t.mode == 2004 {
			return s.modes.changed&(1<<i) != 0
		}
	}
	return false
}

// Unsubscribe removes a viewer.
func (s *Session) Unsubscribe(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.subs[id]; ok {
		close(v.ch)
		delete(s.subs, id)
	}
}

// Write sends input to the process. Writing to a pane whose process has ended
// reports that, rather than the closed-handle error the PTY would give, which
// says nothing about which pane or why.
func (s *Session) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	if s.status == StatusExited || s.closed {
		s.mu.Unlock()
		return 0, fmt.Errorf("pane %s has exited; nothing is listening for input", s.ID)
	}
	// A terminal reports some things on its own, to a program that asked for
	// them -- focus coming and going, the mouse wheel turning -- and they
	// arrive here exactly like typing does. Clicking into a pane, the window
	// coming to the front, or scrolling back to read the question is not an
	// answer to it.
	typed := !terminalReport(p)
	if typed {
		s.sawInput = true
	}
	// Typing is the answer to whatever the pane was blocked on, and where the
	// bell is what put it there, typing is the only thing that can take it
	// back out: the bell rings again on the next question, not on this one
	// being answered, and the guess made from output deliberately leaves
	// "waiting" alone. Without this a pane whose lifecycle hooks are not
	// reporting stays in the count of agents needing you from the first
	// question it ever asks until it exits.
	//
	// Where hooks report, a hook's wait outranks the keyboard, except for a
	// question put in the middle of a tool call: Claude's permission prompt.
	// Nothing reports its answer -- after approving, Claude says nothing until
	// the tool has finished, however long it runs -- so the pane stayed amber,
	// and in the count of agents needing you, through the whole of the command
	// it had just been allowed to run. Enter is what settles it; the arrow keys
	// only move between the choices.
	answered := typed && s.status == StatusWaiting &&
		(!s.hooksSeen || (s.toolQuestion && bytes.IndexByte(p, '\r') >= 0))
	if answered {
		s.status = StatusWorking
		s.statusSince = time.Now()
		s.toolQuestion = false
		if !s.hooksSeen && !s.settling {
			s.settling = true
			go s.settleIdle()
		}
	}
	s.mu.Unlock()
	if answered {
		s.changed()
	}
	return s.pty.Write(p)
}

// terminalReport reports whether input is nothing but reports a terminal makes
// on its own account, none of which any key produces: focus in and out (CSI I,
// CSI O), and the wheel turning or the pointer moving, in the SGR (CSI <) and
// X10 (CSI M) mouse encodings. A click is not one of them: in a program drawn
// for the mouse, clicking an option is how a question gets answered.
func terminalReport(p []byte) bool {
	// passive is a mouse report's button code saying the wheel or a movement.
	passive := func(button int) bool { return button&(64|32) != 0 }
	for len(p) > 0 {
		if len(p) < 3 || p[0] != 0x1b || p[1] != '[' {
			return false
		}
		switch p[2] {
		case 'I', 'O':
			p = p[3:]
		case 'M':
			if len(p) < 6 || !passive(int(p[3])-32) {
				return false
			}
			p = p[6:]
		case '<':
			end := bytes.IndexAny(p[3:], "Mm")
			if end < 0 {
				return false
			}
			button, _, _ := bytes.Cut(p[3:3+end], []byte{';'})
			if n, err := strconv.Atoi(string(button)); err != nil || !passive(n) {
				return false
			}
			p = p[3+end+1:]
		default:
			return false
		}
	}
	return true
}

// WriteString sends text to the process.
func (s *Session) WriteString(text string) error {
	_, err := s.Write([]byte(text))
	return err
}

// SetStatus records an authoritative status transition, normally from a
// lifecycle hook. detail is an optional short label such as the running tool.
func (s *Session) SetStatus(st Status, detail string) {
	s.mu.Lock()
	// A waiting status that names nothing is about whatever the pane was
	// running when it arrived: Claude asks permission for a tool in a
	// Notification that follows the PreToolUse naming it, and puts no name in
	// it. Keeping the tool's is what lets the pane say what it is asking. A
	// second nudge about a wait already showing is about the same wait, and
	// keeps what the first said it was about -- a question from
	// AskUserQuestion was otherwise renamed to nothing by the nudge about it.
	question := false
	if st == StatusWaiting && detail == "" {
		switch s.status {
		case StatusWorking:
			detail, question = s.detail, s.detail != ""
		case StatusWaiting:
			detail, question = s.detail, s.toolQuestion
		}
	}
	// An exited pane stays exited, and an event that changes nothing is not
	// reported: every report rebuilds and sends the whole workspace, and
	// Claude repeats itself -- the same nudge about the same unanswered
	// question, a tool it has already said it is running.
	if s.status == StatusExited || (s.hooksSeen && s.status == st && s.detail == detail) {
		s.mu.Unlock()
		return
	}
	if s.status != st {
		s.statusSince = time.Now()
	}
	s.status = st
	s.detail = detail
	s.toolQuestion = question
	s.hooksSeen = true
	s.mu.Unlock()
	s.changed()
}

// Status returns the current status and its detail label.
func (s *Session) Status() (Status, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status, s.detail
}

// StatusSince reports when the current status began, which is how long an
// agent has been waiting on you.
func (s *Session) StatusSince() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statusSince
}

// Name returns the pane's display name.
func (s *Session) Name() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.name
}

// SetName sets the pane's display name.
func (s *Session) SetName(n string) {
	s.mu.Lock()
	s.name = n
	s.mu.Unlock()
	s.changed()
}

// ExitErr returns the process exit error once the session has exited.
func (s *Session) ExitErr() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.exitErr
}

// Exited reports whether the process has terminated.
func (s *Session) Exited() bool {
	st, _ := s.Status()
	return st == StatusExited
}

// Resize resizes the PTY, clamped to something a terminal could plausibly be.
// Unchanged dimensions are skipped, since a resize forces applications to
// redraw.
func (s *Session) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	cols, rows = clampSize(cols, rows)

	// Recording the size and applying it have to happen as one step. The
	// browser measures a pane on every layout change, so two resizes are
	// routinely in flight at once -- one from the terminal socket, one from
	// the layout -- and if the second overtakes the first inside the PTY call
	// the pane is left the size of the resize that lost, while the session and
	// the saved layout both report the size of the one that won. Nothing
	// corrects that until somebody drags the divider again.
	s.resizeMu.Lock()
	defer s.resizeMu.Unlock()

	s.mu.Lock()
	// Resizing a pseudo-terminal that has been released is not a no-op that
	// returns an error. On Windows the handle is a pointer into the console
	// host, closing it frees what it points at, and go-pty leaves the field
	// holding the stale value, so the resize reaches ResizePseudoConsole with
	// a pointer to memory that has been given back. That is a crash of the
	// whole application, not of one pane, and there is nothing above this that
	// could catch it. Holding resizeMu across the release as well is what
	// makes the check mean something: a resize cannot already be inside the
	// PTY when it is closed, and cannot start afterwards.
	if s.closed || (s.cols == cols && s.rows == rows) {
		s.mu.Unlock()
		return
	}
	s.cols, s.rows = cols, rows
	s.mu.Unlock()

	_ = s.pty.Resize(cols, rows)
	s.changed()
}

// clampSize bounds terminal dimensions to something a display could produce.
func clampSize(cols, rows int) (int, int) {
	return min(cols, maxCols), min(rows, maxRows)
}

// Size returns the current PTY dimensions.
func (s *Session) Size() (cols, rows int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cols, s.rows
}

// Close terminates the process and releases the PTY. It is safe to call on a
// pane that has already exited, which releases the PTY on its own.
//
// It returns once the process is gone, or after closeGrace. On Windows killing
// a process only begins its end, and until that is over it holds its working
// directory open: removing a pane's folder straight after closing the pane --
// a worktree, a temporary checkout -- failed as "being used by another
// process" in four runs out of ten.
func (s *Session) Close() error {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		if s.reaped != nil {
			select {
			case <-s.reaped:
			case <-time.After(closeGrace):
			}
		}
	}
	return s.releasePTY()
}
