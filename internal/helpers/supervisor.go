package helpers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/sysproc"
)

// State is where a helper is in its life. The words are what the interface
// shows, next to a glyph.
type State string

const (
	StateStopped      State = "stopped"
	StateStarting     State = "starting"
	StateRunning      State = "running"
	StateStopping     State = "stopping"
	StateUnresponsive State = "unresponsive"
	StateFailed       State = "failed"
)

// Status is a snapshot of one helper.
type Status struct {
	ID    string `json:"id"`
	State State  `json:"state"`
	Port  int    `json:"port,omitempty"`
	// URL is where Open goes, once the helper is running.
	URL string `json:"url,omitempty"`
	PID int    `json:"pid,omitempty"`
	// Err says why a helper is Failed or was restarted, with the exit status.
	Err string `json:"error,omitempty"`
	// Log is the last lines the helper wrote, kept for a failure.
	Log      []string `json:"log,omitempty"`
	Restarts int      `json:"restarts,omitempty"`
	// Owner says whether the operating system confirmed that the helper's own
	// process holds its port: OwnerVerified, or OwnerUnverified where the
	// platform cannot say (macOS). Empty until the helper is running.
	Owner string `json:"owner,omitempty"`
}

// Timings are the supervisor's clocks. Tests shorten them.
type Timings struct {
	// BannerWait is how long the helper has to print its first line.
	BannerWait time.Duration
	// ReadyEvery and ReadyWait poll the ready path after the banner.
	ReadyEvery, ReadyWait time.Duration
	// HealthEvery polls the health path of a running helper; UnhealthyAfter
	// consecutive failures make it unresponsive.
	HealthEvery    time.Duration
	UnhealthyAfter int
	// StopGrace is how long a helper is given to exit after the interrupt.
	StopGrace time.Duration
	// Backoff are the delays before the first, second and third restart.
	Backoff []time.Duration
	// CrashWindow and MaxRestarts: more than MaxRestarts crashes inside the
	// window ends in Failed.
	CrashWindow time.Duration
	MaxRestarts int
	// PortTries is how many ports a start tries when the helper exits before
	// it is ready, which is what losing the race for a port looks like.
	PortTries int
}

// DefaultTimings are the design's values.
func DefaultTimings() Timings {
	return Timings{
		BannerWait: 20 * time.Second,
		ReadyEvery: 250 * time.Millisecond, ReadyWait: 30 * time.Second,
		HealthEvery: 10 * time.Second, UnhealthyAfter: 3,
		StopGrace:   30 * time.Second,
		Backoff:     []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
		CrashWindow: 5 * time.Minute, MaxRestarts: 3,
		PortTries: 3,
	}
}

// Config configures a Supervisor.
type Config struct {
	Store *Store
	// Lookup finds a helper's entry. Nil means the compiled-in catalogue.
	Lookup func(id string) (Entry, bool)
	// IDs are the helpers the reaper looks for. Nil means the catalogue.
	IDs []string
	// Env is the parent environment the allowlist is applied to. Nil means
	// os.Environ.
	Env func() []string
	// Timings are the clocks. The zero value means DefaultTimings.
	Timings *Timings
	// Notify is told every time a helper's status changes. It is called from
	// the supervisor's own goroutines, never with a lock held.
	Notify func(Status)
	// BreakCmd builds the command that sends CTRL_BREAK to a Windows helper's
	// console from outside Flockdeck's own console state. Nil means this
	// executable's hidden "helpers ctrl-break" subcommand.
	BreakCmd func(pid int) *exec.Cmd
	// GOOS names the platform's binary name. Empty means this one.
	GOOS string
	// PickPort chooses the port for a start. Nil means ChoosePort. Tests use it
	// to lose the race for a port on purpose.
	PickPort func(preferred int) (int, error)
	// InstallBusy, when set, says an install of a helper is under way in this
	// process, and a start is refused while it is.
	InstallBusy func(id string) bool
	// OtherInstance, when set, reports that another Flockdeck is running, in
	// which case the startup reaper leaves everything alone: a record that
	// names a live process may be that instance's helper.
	OtherInstance func() bool
}

// Supervisor starts, watches and stops helpers. It owns one instance per
// helper: a second start returns the first.
type Supervisor struct {
	cfg    Config
	t      Timings
	lookup func(string) (Entry, bool)
	goos   string

	mu       sync.Mutex
	insts    map[string]*instance
	activity sync.Map // id to bool, see isActive

	// ownerPID and ownerStarted identify this Flockdeck in every run.json it
	// writes.
	ownerPID     int
	ownerStarted time.Time
}

// NewSupervisor makes a supervisor. It starts nothing.
func NewSupervisor(cfg Config) *Supervisor {
	s := &Supervisor{cfg: cfg, insts: map[string]*instance{}, lookup: cfg.Lookup, goos: cfg.GOOS}
	if s.lookup == nil {
		s.lookup = Lookup
	}
	if s.goos == "" {
		s.goos = runtime.GOOS
	}
	s.ownerPID = os.Getpid()
	if cfg.Store != nil {
		cfg.Store.bindOwner(s.ownerPID, s.isActive)
	}
	s.ownerStarted, _ = store.ProcessStartedAt(s.ownerPID)
	if cfg.Timings != nil {
		s.t = *cfg.Timings
	} else {
		s.t = DefaultTimings()
	}
	return s
}

// instance is one helper's lifecycle.
type instance struct {
	id    string
	entry Entry

	// Guarded by Supervisor.mu.
	state    State
	port     int
	pid      int
	err      string
	restarts int
	owner    string
	log      *lineRing

	stopReq  chan struct{}
	stopOnce sync.Once
	// done is closed when the lifecycle ends, in Stopped or Failed.
	done chan struct{}
	// stopGrace overrides the timing's grace, in nanoseconds, when positive.
	stopGrace atomic.Int64
	logw      *RotatingWriter
}

// ring keeps the last lines a helper wrote.
type lineRing struct {
	mu    sync.Mutex
	lines []string
}

func (r *lineRing) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
	if len(r.lines) > 20 {
		r.lines = r.lines[len(r.lines)-20:]
	}
}

func (r *lineRing) tail() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

func (s *Supervisor) statusLocked(in *instance) Status {
	st := Status{ID: in.id, State: in.state, Port: in.port, PID: in.pid, Err: in.err, Restarts: in.restarts, Owner: in.owner}
	if in.state == StateRunning || in.state == StateUnresponsive || in.state == StateStopping {
		if in.port > 0 {
			st.URL = fmt.Sprintf("http://127.0.0.1:%d%s", in.port, in.entry.UIPath)
		}
	}
	if in.state == StateFailed || in.err != "" {
		st.Log = in.log.tail()
	}
	return st
}

// set changes the instance's state and tells the listener, outside the lock.
func (s *Supervisor) set(in *instance, mutate func()) {
	s.mu.Lock()
	mutate()
	st := s.statusLocked(in)
	s.noteActivity(in)
	s.mu.Unlock()
	if s.cfg.Notify != nil {
		s.cfg.Notify(st)
	}
}

// noteActivity records, for isActive, whether the instance has a process or a
// start under way. The caller holds s.mu.
func (s *Supervisor) noteActivity(in *instance) {
	s.activity.Store(in.id, in.state != StateStopped && in.state != StateFailed)
}

// isActive is Active without s.mu. The store asks it from inside RunningPID,
// which Start calls while holding s.mu, so it must not take that lock.
func (s *Supervisor) isActive(id string) bool {
	v, ok := s.activity.Load(id)
	active, _ := v.(bool)
	return ok && active
}

// Status is a helper's current state. A helper that was never started is
// Stopped.
func (s *Supervisor) Status(id string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in, ok := s.insts[id]; ok {
		return s.statusLocked(in)
	}
	return Status{ID: id, State: StateStopped}
}

// Active reports whether a helper has a process this supervisor is
// responsible for, in any state but Stopped and Failed.
func (s *Supervisor) Active(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.insts[id]
	return ok && in.state != StateStopped && in.state != StateFailed
}

// Start starts a helper and returns at once with its status, which is
// Starting. A helper that is already starting or running is returned as it is.
// Wait for it to settle with Wait.
func (s *Supervisor) Start(id string) (Status, error) {
	e, ok := s.lookup(id)
	if !ok || !validID(id) {
		return Status{}, fmt.Errorf("%q is not a helper Flockdeck knows", id)
	}
	st := s.cfg.Store
	if _, ok := st.Current(id); !ok {
		return Status{}, fmt.Errorf("%s is not installed; run: flockdeck helpers install %s", e.Name, id)
	}
	if s.cfg.InstallBusy != nil && s.cfg.InstallBusy(id) {
		return Status{}, fmt.Errorf("%s is being installed; wait for that to finish", e.Name)
	}
	if err := st.VerifyInstall(e, s.goos); err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	if in, ok := s.insts[id]; ok {
		switch in.state {
		case StateStarting, StateRunning, StateUnresponsive:
			status := s.statusLocked(in)
			s.mu.Unlock()
			return status, nil
		case StateStopping:
			s.mu.Unlock()
			return Status{}, fmt.Errorf("%s is stopping; try again in a moment", e.Name)
		}
	}
	// Another Flockdeck may already be running it. The record names the
	// process, and a live one that is the same process wins.
	if pid, running := st.RunningPID(id); running {
		s.mu.Unlock()
		return Status{}, fmt.Errorf("%s is already running (process %d), started by another Flockdeck", e.Name, pid)
	}
	// The record that says a start is under way goes down before anything that
	// takes time, with the port the last run used so a bookmark keeps working.
	prev, _ := st.ReadRun(id)
	if prev.Port == 0 {
		prev.Port = st.lastPort(id)
	}
	in := &instance{
		id: id, entry: e, state: StateStarting, log: &lineRing{},
		stopReq: make(chan struct{}), done: make(chan struct{}),
		logw: NewRotatingWriter(st.logFile(id)),
	}
	s.insts[id] = in
	// Active before the record exists: a record that names this process while
	// nothing here is active for the helper is read as a leftover, and must not be
	// seen as one in the moment between being written and being claimed.
	s.noteActivity(in)
	s.writeStarting(id, prev.Port)
	status := s.statusLocked(in)
	s.mu.Unlock()
	if s.cfg.Notify != nil {
		s.cfg.Notify(status)
	}
	go s.run(in)
	return status, nil
}

// writeStarting writes the record of a start under way.
func (s *Supervisor) writeStarting(id string, port int) {
	_ = s.cfg.Store.writeRun(id, RunInfo{Starting: true, Port: port, OwnerPID: s.ownerPID, OwnerStarted: s.ownerStarted, StartedAt: time.Now().UTC()})
}

// Wait blocks until a helper is no longer Starting, or ctx ends, and returns
// its status then.
func (s *Supervisor) Wait(ctx context.Context, id string) Status {
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if st := s.Status(id); st.State != StateStarting {
			return st
		}
		select {
		case <-ctx.Done():
			return s.Status(id)
		case <-tick.C:
		}
	}
}

// Stop stops a helper and returns once its process is gone. It asks politely
// first and kills after the grace period. A helper that is not running is
// already stopped, and that is not an error.
func (s *Supervisor) Stop(id string) error {
	s.mu.Lock()
	in, ok := s.insts[id]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	select {
	case <-in.done:
		return nil
	default:
	}
	in.stopOnce.Do(func() { close(in.stopReq) })
	<-in.done
	return nil
}

// StopAll stops every helper at once, giving each at most grace to exit
// before it is killed. It is what quitting Flockdeck does.
func (s *Supervisor) StopAll(grace time.Duration) {
	s.mu.Lock()
	var all []*instance
	for _, in := range s.insts {
		all = append(all, in)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, in := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if grace > 0 {
				in.stopGrace.Store(int64(grace))
			}
			_ = s.Stop(in.id)
		}()
	}
	wg.Wait()
}

func (s *Supervisor) graceFor(in *instance) time.Duration {
	if g := time.Duration(in.stopGrace.Load()); g > 0 {
		return g
	}
	return s.t.StopGrace
}

// outcome is how one run of the child ended.
type outcome struct {
	// stopped: the user asked for it, and it is over.
	stopped bool
	// ready: the helper had passed its ready check before it ended.
	ready bool
	// fatal: something is wrong that retrying will not fix, so it is Failed.
	fatal string
	// exit is how the process ended, when it did.
	exit string
}

// run is the lifecycle: start, watch, and restart within the limits.
func (s *Supervisor) run(in *instance) {
	defer close(in.done)
	defer in.logw.Close()

	preferred := 0
	if r, ok := s.cfg.Store.ReadRun(in.id); ok {
		preferred = r.Port
	}
	var crashes []time.Time
	for {
		var out outcome
		portsTried := 0
		for portsTried < s.t.PortTries {
			portsTried++
			pick := ChoosePort
			if s.cfg.PickPort != nil {
				pick = s.cfg.PickPort
			}
			port, err := pick(preferred)
			preferred = 0
			if err != nil {
				out = outcome{fatal: err.Error()}
				break
			}
			out = s.runOnce(in, port)
			if out.stopped || out.fatal != "" || out.ready {
				break
			}
			// Exited before it was ready: another port, at once.
			s.set(in, func() { in.err = "exited before it was ready (" + out.exit + "); trying another port" })
		}
		switch {
		case out.stopped:
			s.cfg.Store.clearRun(in.id)
			s.set(in, func() { in.state, in.port, in.pid, in.err = StateStopped, 0, 0, "" })
			return
		case out.fatal != "":
			s.fail(in, out.fatal)
			return
		case !out.ready:
			s.fail(in, fmt.Sprintf("it exited before it was ready on %d different ports (%s)", portsTried, out.exit))
			return
		}

		// It was running and has ended on its own: a crash.
		now := time.Now()
		crashes = append(crashes, now)
		kept := crashes[:0]
		for _, c := range crashes {
			if now.Sub(c) <= s.t.CrashWindow {
				kept = append(kept, c)
			}
		}
		crashes = kept
		if len(crashes) > s.t.MaxRestarts {
			s.fail(in, fmt.Sprintf("it stopped %d times in %s (last: %s)", len(crashes), s.t.CrashWindow, out.exit))
			return
		}
		delay := s.t.Backoff[min(len(crashes)-1, len(s.t.Backoff)-1)]
		s.set(in, func() {
			in.state, in.pid, in.err = StateStarting, 0, "stopped unexpectedly ("+out.exit+"); restarting"
			in.restarts = len(crashes)
		})
		select {
		case <-time.After(delay):
		case <-in.stopReq:
			s.cfg.Store.clearRun(in.id)
			s.set(in, func() { in.state, in.port, in.pid, in.err = StateStopped, 0, 0, "" })
			return
		}
	}
}

func (s *Supervisor) fail(in *instance, why string) {
	// The record goes before the state changes, so that nobody who sees the
	// helper stopped or failed finds a record still saying it is starting.
	s.cfg.Store.clearRun(in.id)
	s.set(in, func() { in.state, in.pid, in.err = StateFailed, 0, why })
}

// probe asks the helper's own port for a path.
func (s *Supervisor) probe(port int, path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		return false
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode == http.StatusOK
}

// exitText describes how a process ended.
func exitText(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}

// runOnce starts the child on port and watches it until it ends.
func (s *Supervisor) runOnce(in *instance, port int) (out outcome) {
	st, e := s.cfg.Store, in.entry
	bin, err := st.BinaryPath(e, s.goos)
	if err != nil {
		return outcome{fatal: err.Error()}
	}
	// Again on every run, restarts included: the files can change between.
	if err := st.VerifyInstall(e, s.goos); err != nil {
		return outcome{fatal: err.Error()}
	}
	vars := EnvVars{Port: port, Host: "127.0.0.1", DataDir: st.DataDir(in.id), AllowedHosts: "127.0.0.1"}
	args, err := ExpandArgs(e.Args, vars)
	if err != nil {
		return outcome{fatal: err.Error()}
	}
	parent := os.Environ
	if s.cfg.Env != nil {
		parent = s.cfg.Env
	}
	env, err := BuildEnv(e, parent(), vars)
	if err != nil {
		return outcome{fatal: err.Error()}
	}
	if err := os.MkdirAll(vars.DataDir, 0o700); err != nil {
		return outcome{fatal: err.Error()}
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = vars.DataDir
	cmd.Env = env
	// Standard output and standard error are separate pipes: the first line of
	// standard output is the banner, and a warning on standard error before it
	// is not.
	outR, outW, err := os.Pipe()
	if err != nil {
		return outcome{fatal: err.Error()}
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return outcome{fatal: err.Error()}
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	configureProc(cmd)
	sysproc.NoWindow(cmd)
	if err := cmd.Start(); err != nil {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		return outcome{fatal: "could not start it: " + err.Error()}
	}
	outW.Close()
	errW.Close()
	pg := attachProc(cmd, s.cfg.BreakCmd)
	started, _ := store.ProcessStartedAt(cmd.Process.Pid)
	_ = st.writeRun(in.id, RunInfo{PID: cmd.Process.Pid, Started: started, Port: port, StartedAt: time.Now().UTC(),
		OwnerPID: s.ownerPID, OwnerStarted: s.ownerStarted})
	s.set(in, func() { in.port, in.pid, in.owner = port, cmd.Process.Pid, "" })

	banner := make(chan string, 1)
	var readers sync.WaitGroup
	pump := func(r *os.File, first chan<- string) {
		defer readers.Done()
		readLines(r, func(line string) {
			if first != nil {
				first <- line
				first = nil
			}
			in.log.add(ringLine(line))
			// The log file holds plain text too: a person reads it in a terminal.
			_, _ = in.logw.Write([]byte(printable(line) + "\n"))
		})
	}
	readers.Add(2)
	go pump(outR, banner)
	go pump(errR, nil)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		// The readers end when every holder of the pipes has gone. Give them a
		// moment, then close our ends so a stray grandchild cannot hold this up.
		done := make(chan struct{})
		go func() { readers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			outR.Close()
			errR.Close()
			<-done
		}
		outR.Close()
		errR.Close()
		// Between runs the start is still under way.
		s.writeStarting(in.id, port)
	}()

	// ended takes the process's exit and ends whatever it started, which a
	// helper that crashed is not allowed to leave behind.
	var exit atomic.Pointer[string]
	ended := func(err error) string {
		text := exitText(err)
		exit.Store(&text)
		pg.release()
		return text
	}
	waitExit := func() string {
		if p := exit.Load(); p != nil {
			return *p
		}
		return ended(<-exited)
	}
	// stop is the polite stop, then the kill, for any point in the run. A
	// helper that is stopping drains its work after its launcher has gone, so
	// what it started is given the rest of the grace before it is killed.
	stop := func() {
		s.set(in, func() { in.state = StateStopping })
		deadline := time.Now().Add(s.graceFor(in))
		if pg.interrupt() == nil {
			select {
			case err := <-exited:
				text := exitText(err)
				exit.Store(&text)
				pg.drain(deadline)
				pg.release()
				return
			case <-time.After(time.Until(deadline)):
			}
		}
		pg.kill()
		waitExit()
	}
	killNow := func() {
		pg.kill()
		waitExit()
	}

	// 1. The first line, which says what answered.
	var line string
	select {
	case line = <-banner:
	case err := <-exited:
		return outcome{exit: ended(err)}
	case <-in.stopReq:
		stop()
		return outcome{stopped: true}
	case <-time.After(s.t.BannerWait):
		killNow()
		return outcome{fatal: fmt.Sprintf("it printed nothing in %s, so Flockdeck cannot tell what it is", s.t.BannerWait)}
	}
	m := e.Banner.FindStringSubmatch(line)
	if m == nil || len(m) < 2 || m[1] != strconv.Itoa(port) {
		killNow()
		return outcome{fatal: fmt.Sprintf("its first line was %q, not what %s prints for port %d, so it was stopped", truncate(line, 120), e.Name, port)}
	}

	// 2. Ready.
	deadline := time.After(s.t.ReadyWait)
	tick := time.NewTicker(s.t.ReadyEvery)
	defer tick.Stop()
ready:
	for {
		select {
		case err := <-exited:
			return outcome{exit: ended(err)}
		case <-in.stopReq:
			stop()
			return outcome{stopped: true}
		case <-deadline:
			killNow()
			return outcome{fatal: fmt.Sprintf("it did not answer %s in %s", e.Ready, s.t.ReadyWait)}
		case <-tick.C:
			if s.probe(port, e.Ready) {
				break ready
			}
		}
	}
	// The probe above was answered by whatever holds the port. Ask the system
	// whether that is the helper, since a program that took the port in the gap
	// after it was chosen can answer for a helper that never bound it.
	owner := OwnerUnverified
	res, why := s.portOwner(port, pg)
	if res == ownerMismatch {
		// Once more after a moment, since a socket can be seen mid-change. Only a
		// verdict that holds is acted on.
		time.Sleep(s.t.ReadyEvery)
		res, why = s.portOwner(port, pg)
	}
	switch res {
	case ownerMismatch:
		// Not fatal: the port is contested, which is what losing the race for it
		// looks like, so this is stopped and tried again on another port, up to
		// the same three tries as a helper that exits before it is ready.
		killNow()
		return outcome{exit: fmt.Sprintf("port %d is held by something other than %s (%s)", port, e.Name, why)}
	case ownerVerified:
		owner = OwnerVerified
	}
	s.cfg.Store.saveLastPort(in.id, port)
	s.set(in, func() { in.state, in.err, in.owner = StateRunning, "", owner })

	// 3. Health, until it ends.
	health := time.NewTicker(s.t.HealthEvery)
	defer health.Stop()
	failures := 0
	for {
		select {
		case err := <-exited:
			return outcome{ready: true, exit: ended(err)}
		case <-in.stopReq:
			stop()
			return outcome{stopped: true}
		case <-health.C:
			if s.probe(port, e.Health) {
				failures = 0
				continue
			}
			failures++
			if failures >= s.t.UnhealthyAfter {
				s.set(in, func() { in.state = StateUnresponsive })
				killNow()
				return outcome{ready: true, exit: "it stopped answering " + e.Health}
			}
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Reaper ------------------------------------------------------------------

// ReapStale ends helpers that a previous Flockdeck left running: on Windows
// the job object ends them with their parent, but on Linux and macOS a helper
// outlives a Flockdeck that crashed. run.json holds the pid and the process's
// start time, and a process is only ended if it still matches, so a pid the
// system has given to something else is never touched. It returns how many
// processes it ended.
func (s *Supervisor) ReapStale() int {
	if s.cfg.OtherInstance != nil && s.cfg.OtherInstance() {
		return 0
	}
	ids := s.cfg.IDs
	if ids == nil {
		for _, e := range Catalogue() {
			ids = append(ids, e.ID)
		}
	}
	killedTotal := 0
	for _, id := range ids {
		if !validID(id) || s.Active(id) {
			continue
		}
		r, ok := s.cfg.Store.ReadRun(id)
		if !ok {
			continue
		}
		// A helper whose owner is a Flockdeck that is still running belongs to
		// it, whatever else is true of the record. OtherInstance cannot say so for
		// a Flockdeck that has not been recorded as the instance.
		if r.OwnerPID != os.Getpid() && ownerAlive(r) {
			continue
		}
		if r.PID == 0 {
			// A start under way whose owner has gone: nothing to end.
			s.cfg.Store.clearRun(id)
			continue
		}
		same := func(p sysproc.StaleProc) bool {
			rec := RunInfo{PID: p.PID, Started: p.Started}
			if !p.Started.IsZero() {
				return sameProcess(rec, false)
			}
			// No start time was recorded, or the system cannot give one. The
			// process has to say it is this helper's program.
			return store.ProcessAlive(p.PID) && commandMentions(p.PID, s.cfg.Store.appDir(id))
		}
		killed, _ := sysproc.Reap([]sysproc.StaleProc{{ID: id, PID: r.PID, Started: r.Started}}, same, terminateGroup)
		killedTotal += len(killed)
		s.cfg.Store.clearRun(id)
	}
	return killedTotal
}

// terminateGroup ends a helper process this run did not start: the interrupt
// first, a short wait, then the kill.
func terminateGroup(pid int) error {
	return terminateStale(pid, 10*time.Second)
}

// portOwner asks which processes hold the port. A helper's processes that
// cannot be listed completely give "unknown", never a verdict.
func (s *Supervisor) portOwner(port int, pg *procGroup) (ownerResult, string) {
	pids, ok := pg.members()
	if !ok {
		return ownerUnknown, "the helper's processes could not be listed"
	}
	return checkListenerOwner(port, pids)
}
