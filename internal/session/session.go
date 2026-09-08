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
	// sawInput records that the user has typed into this pane. Claude rings the
	// bell while starting up, so without this a freshly opened pane would
	// announce that it needs attention before anyone has spoken to it.
	sawInput   bool
	cols, rows int

	// history holds recent output for replay; subs are the live viewers.
	history *ring
	subs    map[int]chan []byte
	nextSub int

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
		cols:        cfg.Cols,
		rows:        cfg.Rows,
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
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.publish(chunk)
		}
		if err != nil {
			return
		}
	}
}

// publish records a chunk and delivers it to every viewer.
func (s *Session) publish(chunk []byte) {
	rang := s.bell.scan(chunk)

	s.mu.Lock()
	s.history.write(chunk)
	s.lastOutput = time.Now()
	// A pane that has produced output is up. Claude panes are corrected to
	// working/waiting by their lifecycle hooks; shells, and agents whose hooks
	// never arrive, stay readable rather than stuck on "starting".
	if s.status == StatusStarting {
		s.status = StatusIdle
		s.statusSince = time.Now()
	}
	if rang {
		s.bellAt = time.Now()
		// The bell is only a fallback. Claude rings it both when it wants input
		// and when a turn simply ends, so once lifecycle hooks are reporting
		// they are the sole source of truth; letting the bell win would flip
		// every completed turn to "waiting".
		if s.Kind == KindClaude && s.status != StatusExited && !s.hooksSeen && s.sawInput {
			// Claude rings again every time it nudges about the input it is
			// still waiting for, so the clock only starts on the transition:
			// restarting it on each bell is how a pane that has been blocked
			// for twenty minutes reports having just started waiting, which
			// is exactly the number being used to decide where to look.
			if s.status != StatusWaiting {
				s.statusSince = time.Now()
			}
			s.status = StatusWaiting
		}
	}
	var dead []int
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

	s.changed()
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
	s.changed()
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
	replay = s.history.bytes()
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
	if s.status == StatusExited {
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
	cols, rows = min(cols, maxCols), min(rows, maxRows)
	s.mu.Lock()
	if s.cols == cols && s.rows == rows {
		s.mu.Unlock()
		return
	}
	s.cols, s.rows = cols, rows
	s.mu.Unlock()

	_ = s.pty.Resize(cols, rows)
	s.changed()
}

// Size returns the current PTY dimensions.
func (s *Session) Size() (cols, rows int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cols, s.rows
}

// Close terminates the process and releases the PTY.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return s.pty.Close()
}
