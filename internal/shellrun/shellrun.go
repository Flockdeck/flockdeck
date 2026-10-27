// Package shellrun runs the commands a person types after "!" in a structured
// pane. They are the person's commands: they run in the pane's directory with
// the person's rights, never pass through the agent or its permission requests,
// and see none of Flockdeck's own variables or any stored key. Each run's output
// is kept in a bounded buffer (the newest bytes), a run has a time limit, and
// every run and what it started is ended when the Manager is closed, which is
// when the pane closes.
//
// The environment is an allowlist, so a variable nobody thought of is left out
// rather than passed on. Output is the program's, and may hold escape
// sequences, carriage returns, format characters and bytes that are not UTF-8;
// Text turns it into plain text that is safe to draw.
package shellrun

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/proctree"
	"github.com/jmwri/flockdeck/internal/sysproc"
)

const (
	// MaxCmd is the longest command accepted, in bytes.
	MaxCmd = 4000
	// OutputCap is how much of a run's output is kept: the newest bytes.
	OutputCap = 64 * 1024
	// MaxRuns is how many runs a Manager keeps. The oldest finished run makes
	// room for a new one.
	MaxRuns = 8
	// MaxRunning is how many runs may be going at once.
	MaxRunning = 4

	killGrace = 2 * time.Second
	notifyGap = 100 * time.Millisecond
)

// Timeout ends a run that is still going after this long. It is a variable for tests.
var Timeout = 10 * time.Minute

var (
	ErrEmpty    = errors.New("shellrun: nothing to run")
	ErrTooLong  = errors.New("shellrun: command too long")
	ErrClosed   = errors.New("shellrun: closed")
	ErrBusy     = errors.New("shellrun: too many commands running")
	ErrNoRun    = errors.New("shellrun: no such run")
	ErrRunning  = errors.New("shellrun: still running")
	ErrNotStart = errors.New("shellrun: could not start")
)

// State is where a run is.
type State string

const (
	Running State = "running"
	// Exited is a run that ended by itself; Code is its exit code.
	Exited State = "exited"
	// Stopped is a run the person stopped.
	Stopped State = "stopped"
	// TimedOut is a run ended for taking longer than Timeout.
	TimedOut State = "timeout"
	// Failed is a run that could not be started.
	Failed State = "failed"
)

// Run is a copy of one run's state. Output is the raw newest bytes; use Text.
type Run struct {
	ID      int
	Cmd     string
	State   State
	Code    int
	Started time.Time
	Ended   time.Time
	Output  []byte
	// Dropped is how many bytes of output are older than Output and were let go.
	Dropped int64
}

type run struct {
	Run
	cancel func(State)
}

// Manager runs and keeps the commands of one pane.
type Manager struct {
	dir    string
	env    []string
	notify func()

	mu      sync.Mutex
	runs    []*run
	next    int
	closed  bool
	pending bool
	wg      sync.WaitGroup
}

// New returns a Manager that runs commands in dir with environ filtered by
// Env. notify, if not nil, is called (never with the lock held, and at most
// every 100 ms) after a change.
func New(dir string, environ []string, notify func()) *Manager {
	return &Manager{dir: dir, env: Env(environ), notify: notify}
}

// Env is the environment a person's command gets from environ (nil means the
// process's own): only the variables a command needs to find its tools and a
// place to write. It holds nothing named FLOCKDECK_, no API key and no token.
func Env(environ []string) []string {
	if environ == nil {
		environ = os.Environ()
	}
	allowed := map[string]bool{}
	names := []string{"PATH", "LANG", "LC_ALL", "TZ"}
	if runtime.GOOS == "windows" {
		names = append(names, "SystemRoot", "SystemDrive", "windir", "ComSpec", "PATHEXT", "TEMP", "TMP",
			"USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "ProgramFiles", "ProgramFiles(x86)",
			"ProgramData", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "OS")
	} else {
		names = append(names, "HOME", "TMPDIR", "USER", "SHELL")
	}
	for _, n := range names {
		allowed[strings.ToUpper(n)] = true
	}
	var out []string
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" || !allowed[strings.ToUpper(name)] {
			continue
		}
		out = append(out, kv)
	}
	if runtime.GOOS == "windows" {
		out = append(out, "NoDefaultCurrentDirectoryInExePath=1")
	}
	return out
}

// Start begins running cmdline and returns its id. It returns at once.
func (m *Manager) Start(cmdline string) (int, error) {
	if strings.TrimSpace(cmdline) == "" {
		return 0, ErrEmpty
	}
	if len(cmdline) > MaxCmd {
		return 0, ErrTooLong
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0, ErrClosed
	}
	running := 0
	for _, r := range m.runs {
		if r.State == Running {
			running++
		}
	}
	if running >= MaxRunning {
		m.mu.Unlock()
		return 0, ErrBusy
	}
	if len(m.runs) >= MaxRuns {
		drop := -1
		for i, r := range m.runs {
			if r.State != Running {
				drop = i
				break
			}
		}
		if drop < 0 {
			m.mu.Unlock()
			return 0, ErrBusy
		}
		m.runs = append(m.runs[:drop], m.runs[drop+1:]...)
	}
	m.next++
	r := &run{Run: Run{ID: m.next, Cmd: cmdline, State: Running, Started: time.Now()}}
	m.runs = append(m.runs, r)

	ctx, cancelCtx := context.WithTimeout(context.Background(), Timeout)
	cmd := shellCommand(ctx, cmdline)
	cmd.Dir = m.dir
	cmd.Env = m.env
	cmd.Stdin = nil
	w := &sink{m: m, r: r}
	cmd.Stdout, cmd.Stderr = w, w
	cmd.WaitDelay = time.Second
	sysproc.NoWindow(cmd)
	tree, err := proctree.Start(cmd)
	if err != nil {
		cancelCtx()
		r.State, r.Ended = Failed, time.Now()
		m.mu.Unlock()
		m.changed()
		return r.ID, ErrNotStart
	}
	var why State
	r.cancel = func(s State) {
		if why == "" {
			why = s
		}
		cancelCtx()
	}
	m.wg.Add(1)
	m.mu.Unlock()

	go func() {
		defer m.wg.Done()
		reaped := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			err := cmd.Wait()
			close(reaped)
			done <- err
		}()
		var werr error
		select {
		case werr = <-done:
		case <-ctx.Done():
			// The context's own kill ends the shell only, and Wait cannot return
			// while a child holds the output pipe: end the tree now.
			tree.End(cmd.Process, nil, killGrace)
			werr = <-done
		}
		tree.End(cmd.Process, reaped, killGrace)
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancelCtx()
		m.mu.Lock()
		switch {
		case why != "":
			r.State = why
		case timedOut:
			r.State = TimedOut
		default:
			r.State = Exited
		}
		if werr != nil {
			var ee *exec.ExitError
			if errors.As(werr, &ee) {
				r.Code = ee.ExitCode()
			} else {
				r.Code = -1
			}
		}
		r.Ended = time.Now()
		r.cancel = nil
		m.mu.Unlock()
		m.changed()
	}()
	m.changed()
	return r.ID, nil
}

// Stop ends the run and what it started. It returns at once; the run is
// Stopped when it has ended. A run that has ended is left as it is.
func (m *Manager) Stop(id int) error {
	m.mu.Lock()
	r := m.find(id)
	if r == nil {
		m.mu.Unlock()
		return ErrNoRun
	}
	if r.State == Running && r.cancel != nil {
		r.cancel(Stopped)
	}
	m.mu.Unlock()
	return nil
}

// Remove forgets a run that has ended.
func (m *Manager) Remove(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.runs {
		if r.ID != id {
			continue
		}
		if r.State == Running {
			return ErrRunning
		}
		m.runs = append(m.runs[:i], m.runs[i+1:]...)
		return nil
	}
	return ErrNoRun
}

// Get returns a copy of one run.
func (m *Manager) Get(id int) (Run, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.find(id); r != nil {
		return copyOf(r), true
	}
	return Run{}, false
}

// List returns a copy of every run, the oldest first.
func (m *Manager) List() []Run {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Run, 0, len(m.runs))
	for _, r := range m.runs {
		out = append(out, copyOf(r))
	}
	return out
}

func copyOf(r *run) Run {
	c := r.Run
	c.Output = append([]byte(nil), r.Output...)
	return c
}

func (m *Manager) find(id int) *run {
	for _, r := range m.runs {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// Close ends every run and what each started, waits for them to be gone, and
// refuses new ones. It is safe to call twice.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	for _, r := range m.runs {
		if r.State == Running && r.cancel != nil {
			r.cancel(Stopped)
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// changed tells the notify function, coalescing calls within notifyGap.
func (m *Manager) changed() {
	if m.notify == nil {
		return
	}
	m.mu.Lock()
	if m.pending {
		m.mu.Unlock()
		return
	}
	m.pending = true
	m.mu.Unlock()
	time.AfterFunc(notifyGap, func() {
		m.mu.Lock()
		m.pending = false
		m.mu.Unlock()
		m.notify()
	})
}

// sink keeps the newest OutputCap bytes of a run.
type sink struct {
	m *Manager
	r *run
}

func (s *sink) Write(p []byte) (int, error) {
	n := len(p)
	s.m.mu.Lock()
	r := s.r
	if len(p) >= OutputCap {
		r.Dropped += int64(len(r.Output) + len(p) - OutputCap)
		r.Output = append(r.Output[:0], p[len(p)-OutputCap:]...)
	} else {
		if over := len(r.Output) + len(p) - OutputCap; over > 0 {
			r.Dropped += int64(over)
			r.Output = append(r.Output[:0], r.Output[over:]...)
		}
		r.Output = append(r.Output, p...)
	}
	s.m.mu.Unlock()
	s.m.changed()
	return n, nil
}

// Text turns a program's output into plain text: UTF-8 is repaired, escape
// sequences (colours, cursor moves, window titles and hyperlinks) are removed,
// a carriage return overwrites the line so far as a terminal would show it, and
// control characters other than line break and tab, format characters (the
// bidirectional overrides, zero-width joiners) and the line and paragraph
// separators are dropped. What comes back holds only text that is safe to draw
// and to send on as a prompt.
func Text(b []byte) string {
	s := strings.ToValidUTF8(string(b), "\ufffd")
	var out strings.Builder
	var line []rune
	flush := func() {
		out.WriteString(string(line))
		line = line[:0]
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == 0x1b:
			i = skipEscape(rs, i)
		case r == 0x9b: // C1 CSI
			i = skipCSI(rs, i)
		case r == 0x9d: // C1 OSC
			i = skipString(rs, i)
		case r == '\r':
			if i+1 < len(rs) && rs[i+1] == '\n' {
				continue
			}
			line = line[:0]
		case r == '\n':
			line = append(line, '\n')
			flush()
		case r == '\t':
			line = append(line, '\t')
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == 0x2028, r == 0x2029:
		default:
			line = append(line, r)
		}
	}
	flush()
	return out.String()
}

// skipEscape returns the index of the last rune of the escape sequence that
// starts at rs[i].
func skipEscape(rs []rune, i int) int {
	if i+1 >= len(rs) {
		return i
	}
	switch rs[i+1] {
	case '[':
		return skipCSI(rs, i+1)
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: end at BEL or ST
		return skipString(rs, i+1)
	}
	// A two-character sequence, with any intermediate bytes before the final one.
	j := i + 1
	for j < len(rs) && rs[j] >= 0x20 && rs[j] <= 0x2f {
		j++
	}
	if j >= len(rs) {
		return len(rs) - 1
	}
	return j
}

// maxSeq bounds how far a sequence may run before it is taken to be text, so
// an introducer with no end does not swallow everything after it.
const maxSeq = 4096

func skipCSI(rs []rune, i int) int {
	for j := i + 1; j < len(rs) && j-i < maxSeq; j++ {
		if rs[j] >= 0x40 && rs[j] <= 0x7e {
			return j
		}
	}
	return i
}

func skipString(rs []rune, i int) int {
	for j := i + 1; j < len(rs) && j-i < maxSeq; j++ {
		if rs[j] == 0x07 || rs[j] == 0x9c {
			return j
		}
		if rs[j] == 0x1b && j+1 < len(rs) && rs[j+1] == '\\' {
			return j + 1
		}
	}
	return i
}

// Clip cuts s to at most max bytes, from the start when tail is false and from
// the end when it is true, between characters. It says whether it cut.
func Clip(s string, max int, tail bool) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	if tail {
		s = s[len(s)-max:]
		for len(s) > 0 && !utf8.RuneStart(s[0]) {
			s = s[1:]
		}
		return s, true
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s, true
}
