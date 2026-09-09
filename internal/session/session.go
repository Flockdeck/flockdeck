package session

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"
)

// Kind distinguishes an agent pane from a plain shell pane.
type Kind int

const (
	// KindClaude is a pane running the `claude` CLI.
	KindClaude Kind = iota
	// KindShell is a pane running the user's shell.
	KindShell
)

const (
	// replayBytes is how much recent output each pane keeps so a reconnecting
	// or reloading viewer can rebuild its screen.
	replayBytes = 512 << 10
	// subscriberQueue is how many output chunks may be outstanding for one
	// viewer before it is considered too slow to keep up.
	subscriberQueue = 512
	// drainGrace is how long the exit is held back so the reader can pick up
	// whatever the process printed on its way out. A PTY does not always
	// report end of output when the process it is attached to goes away, so
	// this is a grace period rather than something to wait on indefinitely.
	drainGrace = 500 * time.Millisecond
	// quietBeforeIdle is how long a pane with no lifecycle hooks reporting for
	// it must print nothing before it is called idle again. It has to bridge
	// the pauses inside one piece of work -- a compiler between files, a test
	// runner between packages -- without leaving a pane that has genuinely
	// finished claiming to be busy.
	quietBeforeIdle = 3 * time.Second
	// bellGrace is how long after a pane starts its bells are treated as part
	// of starting up rather than a request for attention.
	bellGrace = 5 * time.Second
	// maxCols and maxRows bound a resize. The dimensions are measured by the
	// browser and can be anything it cares to send, while a PTY allocates a
	// cell for every one of them, so a figure no display could produce is
	// clamped rather than honoured.
	maxCols = 2000
	maxRows = 2000
)

// Config describes a session to start.
type Config struct {
	ID   string // stable id; for Claude panes this is also the --session-id UUID
	Kind Kind
	Name string // display name, usually the basename of Cwd
	Cwd  string
	Argv []string
	Env  []string
	Cols int
	Rows int
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
	// hooksSeen records that Claude lifecycle hooks have reported for this
	// session, which makes them authoritative over the terminal bell.
	hooksSeen bool
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

	// history holds recent output for replay; subs are the live viewers.
	history *ring
	subs    map[int]chan []byte
	nextSub int

	// resizeMu orders resizes, and keeps one from overlapping the release of
	// the PTY it would resize. It is separate from mu because applying a
	// resize is a call into the PTY, which must not be made while the reader
	// is blocked out of publishing.
	resizeMu sync.Mutex

	// pumped is closed once the PTY reader has seen the end of the output.
	pumped chan struct{}

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
		name:        cfg.Name,
		status:      StatusStarting,
		statusSince: time.Now(),
		startedAt:   time.Now(),
		cols:        cfg.Cols,
		rows:        cfg.Rows,
		idleAfter:   quietBeforeIdle,
		history:     newRing(replayBytes),
		subs:        map[int]chan []byte{},
		pumped:      make(chan struct{}),
	}

	cmd := p.Command(exe, cfg.Argv[1:]...)
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
	rang := s.bell.scan(chunk)

	// Output itself is not a change anyone outside this type can see: it
	// reaches viewers on their own subscriptions, and nothing in the interface
	// is drawn from the fact that bytes arrived. Reporting a change per chunk
	// is what keeps the whole workspace snapshot being rebuilt, encoded and
	// pushed to the browser for as long as any pane is streaming.
	notify := false

	s.mu.Lock()
	s.history.write(chunk)
	s.lastOutput = time.Now()
	// A pane nothing is reporting for is read from what it prints. Shell panes
	// never get lifecycle hooks at all, and a Claude pane has none until its
	// first event arrives, so without this a build running for a minute and a
	// prompt nobody has typed at look exactly alike from the tab bar.
	//
	// Waiting is left alone: it is the one status here worth surfacing, and it
	// is set from the bell below, which knows more than the fact that bytes
	// arrived.
	if !s.hooksSeen && s.status != StatusExited && s.status != StatusWaiting {
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
		// every completed turn to "waiting".
		// A pane is past its startup either because somebody typed into it or
		// because enough time has gone by. Waiting for the typing alone
		// silenced this for the panes it matters most for: an agent spawned
		// with its task on the command line is never typed at, and neither is
		// a pane restored from a saved layout until the user gets to it, so a
		// workspace of fifteen restored agents had no fallback at all if
		// their lifecycle hooks did not report.
		started := s.sawInput || time.Since(s.startedAt) > bellGrace
		if s.Kind == KindClaude && s.status != StatusExited && !s.hooksSeen && started {
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
	for id, ch := range s.subs {
		select {
		case ch <- chunk:
		default:
			// A viewer too slow to keep up would otherwise stall the process.
			// Drop it; the client reconnects and replays from history.
			dead = append(dead, id)
		}
	}
	for _, id := range dead {
		close(s.subs[id])
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
	for id, ch := range s.subs {
		close(ch)
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
	ch := make(chan []byte, subscriberQueue)

	s.mu.Lock()
	defer s.mu.Unlock()
	replay = s.history.replay()
	if s.status == StatusExited {
		close(ch)
		return -1, replay, ch
	}
	s.nextSub++
	id = s.nextSub
	s.subs[id] = ch
	return id, replay, ch
}

// Unsubscribe removes a viewer.
func (s *Session) Unsubscribe(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.subs[id]; ok {
		close(ch)
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
	s.sawInput = true
	s.mu.Unlock()
	return s.pty.Write(p)
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
	if s.status == StatusExited {
		s.mu.Unlock()
		return
	}
	if s.status != st {
		s.statusSince = time.Now()
	}
	s.status = st
	s.detail = detail
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
func (s *Session) Close() error {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return s.releasePTY()
}
