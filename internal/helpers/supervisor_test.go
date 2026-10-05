package helpers

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// fastTimings are short enough that a test of a restart limit takes a second,
// and long enough that a slow machine starting a 10 MB test binary is not
// mistaken for a hang.
func fastTimings() *Timings {
	return &Timings{
		BannerWait: 20 * time.Second,
		ReadyEvery: 20 * time.Millisecond, ReadyWait: 20 * time.Second,
		HealthEvery: 50 * time.Millisecond, UnhealthyAfter: 3,
		StopGrace:   10 * time.Second,
		Backoff:     []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond},
		CrashWindow: time.Minute, MaxRestarts: 3,
		PortTries: 3,
	}
}

// testBreakCmd is what sends CTRL_BREAK in a test: this binary again, in the
// place of the flockdeck subcommand.
func testBreakCmd(pid int) *exec.Cmd {
	return exec.Command(os.Args[0], "-test.run=^TestCtrlBreakSender$", "--", strconv.Itoa(pid))
}

// fakeEntry is lens, started as the fake helper with extra arguments.
func fakeEntry(id string, extra ...string) Entry {
	e := lens
	e.ID, e.Name = id, id
	e.Args = append([]string{"-test.run=^TestFakeHelperProcess$", "--", "serve", "--host", "127.0.0.1", "--port", "{port}"}, extra...)
	return e
}

type supFixture struct {
	t     *testing.T
	store *Store
	sup   *Supervisor
	dir   string
	mu    sync.Mutex
	seen  []State
}

// install puts the test binary in place as the installed helper.
func installFake(t *testing.T, st *Store, e Entry, goos string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := st.versionDir(e.ID, "0.0.1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, e.BinaryName(goos))
	if err := os.Link(exe, dst); err != nil {
		src, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, src, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := fileSHA256(dst)
	if err != nil {
		t.Fatal(err)
	}
	info := `{"version":"0.0.1","binarySha256":"` + sum + `","signed":true}`
	if err := os.WriteFile(filepath.Join(dir, "install.json"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.currentFile(e.ID), []byte(`{"version":"0.0.1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newSupFixture(t *testing.T, entries []Entry, mutate ...func(*Config)) *supFixture {
	t.Helper()
	f := &supFixture{t: t, dir: t.TempDir()}
	f.store = &Store{Root: filepath.Join(f.dir, "apps")}
	table := map[string]Entry{}
	var ids []string
	for _, e := range entries {
		table[e.ID] = e
		ids = append(ids, e.ID)
		installFake(t, f.store, e, runtime.GOOS)
	}
	cfg := Config{
		Store:    f.store,
		Lookup:   func(id string) (Entry, bool) { e, ok := table[id]; return e, ok },
		IDs:      ids,
		Timings:  fastTimings(),
		BreakCmd: testBreakCmd,
		Notify: func(s Status) {
			f.mu.Lock()
			if len(f.seen) == 0 || f.seen[len(f.seen)-1] != s.State {
				f.seen = append(f.seen, s.State)
			}
			f.mu.Unlock()
		},
	}
	for _, m := range mutate {
		m(&cfg)
	}
	f.sup = NewSupervisor(cfg)
	t.Cleanup(func() { f.sup.StopAll(2 * time.Second) })
	return f
}

func (f *supFixture) states() []State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]State(nil), f.seen...)
}

func (f *supFixture) startAndWait(id string) Status {
	f.t.Helper()
	if _, err := f.sup.Start(id); err != nil {
		f.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return f.sup.Wait(ctx, id)
}

// eventually polls until cond holds.
func eventually(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func countLines(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(b)))
}

func processGone(pid int) bool { return !store.ProcessAlive(pid) }

func TestStartReachesRunningAndStopsGracefully(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	e := fakeEntry("lens", "--marker", marker)
	f := newSupFixture(t, []Entry{e})
	st := f.startAndWait("lens")
	if st.State != StateRunning {
		t.Fatalf("state = %s (%s), log %v", st.State, st.Err, st.Log)
	}
	if st.Port < 1024 || st.PID == 0 || st.URL != "http://127.0.0.1:"+strconv.Itoa(st.Port)+"/" {
		t.Fatalf("status = %+v", st)
	}
	// The page answers where the status says.
	resp, err := http.Get(st.URL[:len(st.URL)-1] + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	run, ok := f.store.ReadRun("lens")
	if !ok || run.PID != st.PID || run.Port != st.Port {
		t.Fatalf("run.json = %+v, %v", run, ok)
	}

	if err := f.sup.Stop("lens"); err != nil {
		t.Fatal(err)
	}
	if got := f.sup.Status("lens").State; got != StateStopped {
		t.Fatalf("state after stop = %s", got)
	}
	if b, _ := os.ReadFile(marker); string(b) != "graceful" {
		t.Fatalf("the helper was not asked to stop gracefully (marker %q)", b)
	}
	if !processGone(st.PID) {
		t.Fatal("the process is still running")
	}
	if _, ok := f.store.ReadRun("lens"); ok {
		t.Fatal("run.json was left after a stop")
	}
	want := []State{StateStarting, StateRunning, StateStopping, StateStopped}
	if got := f.states(); strings.Join(statesText(got), ",") != strings.Join(statesText(want), ",") {
		t.Fatalf("states = %v, want %v", got, want)
	}
	// Its output went to the log, banner first.
	log := TailLines(f.store.LogFile("lens"), 5)
	if len(log) < 2 || !strings.HasPrefix(log[0], "lens 0.0.0-test at ") {
		t.Fatalf("log = %q", log)
	}
}

func statesText(s []State) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = string(v)
	}
	return out
}

func TestHelperGetsTheEnvironmentItShould(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "env.txt")
	e := fakeEntry("lens", "--dump-env", dump)
	t.Setenv("FLOCKDECK_TOKEN", "secret-token")
	t.Setenv("FLOCKDECK_API", "http://127.0.0.1:1")
	t.Setenv("PERCH_TOKEN", "old-secret")
	t.Setenv("MY_API_KEY", "secret-key")
	t.Setenv("GITHUB_TOKEN", "secret-gh")
	f := newSupFixture(t, []Entry{e})
	if st := f.startAndWait("lens"); st.State != StateRunning {
		t.Fatalf("state = %s (%s)", st.State, st.Err)
	}
	b, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	env := string(b)
	for _, bad := range []string{"secret-token", "secret-key", "secret-gh", "old-secret", "FLOCKDECK_", "PERCH_", "_API_KEY", "GITHUB_TOKEN"} {
		if strings.Contains(env, bad) {
			t.Errorf("the child's environment contains %q", bad)
		}
	}
	st := f.sup.Status("lens")
	for _, want := range []string{
		"PORT=" + strconv.Itoa(st.Port), "HOST=127.0.0.1", "ALLOWED_HOSTS=127.0.0.1", "LOG_FILE=-",
		"DATA_DIR=" + f.store.DataDir("lens"),
	} {
		if !strings.Contains(env, want) {
			t.Errorf("the child's environment is missing %q", want)
		}
	}
	if fi, err := os.Stat(f.store.DataDir("lens")); err != nil || !fi.IsDir() {
		t.Errorf("the data folder was not made: %v", err)
	}
}

func TestWrongBannerIsRejectedAndKilled(t *testing.T) {
	for _, mode := range []string{"wrong-banner", "wrong-port"} {
		t.Run(mode, func(t *testing.T) {
			f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", mode)})
			st := f.startAndWait("lens")
			if st.State != StateFailed || !strings.Contains(st.Err, "first line") {
				t.Fatalf("status = %+v", st)
			}
			pid := f.sup.Status("lens").PID
			_ = pid
			run, _ := f.store.ReadRun("lens")
			if run.PID != 0 {
				t.Fatalf("run.json left behind: %+v", run)
			}
			// The probe was never made: nothing about the page was trusted.
			if st.URL != "" {
				t.Fatalf("a failed helper has a URL: %q", st.URL)
			}
		})
	}
}

func TestWrongBannerProcessIsDead(t *testing.T) {
	var mu sync.Mutex
	var pid int
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "wrong-banner")}, func(c *Config) {
		c.Notify = func(s Status) {
			mu.Lock()
			if s.PID != 0 {
				pid = s.PID
			}
			mu.Unlock()
		}
	})
	if st := f.startAndWait("lens"); st.State != StateFailed {
		t.Fatalf("status = %+v", st)
	}
	mu.Lock()
	got := pid
	mu.Unlock()
	if got == 0 {
		t.Fatal("the process id was never reported")
	}
	eventually(t, "the wrong program to be killed", 10*time.Second, func() bool { return processGone(got) })
}

func TestSilentHelperIsKilled(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "silent")}, func(c *Config) {
		tm := *fastTimings()
		tm.BannerWait = 300 * time.Millisecond
		c.Timings = &tm
	})
	st := f.startAndWait("lens")
	if st.State != StateFailed || !strings.Contains(st.Err, "printed nothing") {
		t.Fatalf("status = %+v", st)
	}
}

func TestNeverReadyFails(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "never-ready")}, func(c *Config) {
		tm := *fastTimings()
		tm.ReadyWait = 400 * time.Millisecond
		c.Timings = &tm
	})
	st := f.startAndWait("lens")
	if st.State != StateFailed || !strings.Contains(st.Err, "/readyz") {
		t.Fatalf("status = %+v", st)
	}
}

func TestEarlyExitRetriesOnAnotherPort(t *testing.T) {
	state := filepath.Join(t.TempDir(), "ports")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "exit-early-once", "--state", state)})
	st := f.startAndWait("lens")
	if st.State != StateRunning {
		t.Fatalf("status = %+v", st)
	}
	if n := countLines(state); n != 2 {
		t.Fatalf("the helper ran %d times, want 2", n)
	}
}

func TestLosingTheRaceForAPortIsRetried(t *testing.T) {
	// Something takes the port between the choice and the helper's bind. The
	// helper fails to bind, exits before its banner, and a new port is tried.
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	takenPort := taken.Addr().(*net.TCPAddr).Port
	var mu sync.Mutex
	var offered []int
	f := newSupFixture(t, []Entry{fakeEntry("lens")}, func(c *Config) {
		c.PickPort = func(preferred int) (int, error) {
			mu.Lock()
			defer mu.Unlock()
			if len(offered) == 0 {
				offered = append(offered, takenPort)
				return takenPort, nil
			}
			p, err := ChoosePort(0)
			offered = append(offered, p)
			return p, err
		}
	})
	st := f.startAndWait("lens")
	if st.State != StateRunning || st.Port == takenPort {
		t.Fatalf("status = %+v (the taken port was %d)", st, takenPort)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(offered) != 2 {
		t.Fatalf("ports offered = %v", offered)
	}
}

func TestEarlyExitEveryTimeFails(t *testing.T) {
	state := filepath.Join(t.TempDir(), "ports")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "exit-early", "--state", state)})
	st := f.startAndWait("lens")
	if st.State != StateFailed || !strings.Contains(st.Err, "3 different ports") {
		t.Fatalf("status = %+v", st)
	}
	if n := countLines(state); n != 3 {
		t.Fatalf("ran %d times, want 3", n)
	}
}

func TestCrashRestartsThenFailsAtTheLimit(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runs")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "crash", "--after", "100ms", "--state", state)})
	if _, err := f.sup.Start("lens"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the helper to be marked failed", 60*time.Second, func() bool {
		return f.sup.Status("lens").State == StateFailed
	})
	st := f.sup.Status("lens")
	// The first run and three restarts; the fourth crash ends it.
	if n := countLines(state); n != 4 {
		t.Fatalf("the helper ran %d times, want 4", n)
	}
	if !strings.Contains(st.Err, "4 times") || !strings.Contains(st.Err, "exit status 3") {
		t.Fatalf("err = %q", st.Err)
	}
	if len(st.Log) == 0 || !strings.Contains(strings.Join(st.Log, "\n"), "crashing") {
		t.Fatalf("the failure carries no log tail: %v", st.Log)
	}
	// It stays failed.
	time.Sleep(200 * time.Millisecond)
	if n := countLines(state); n != 4 {
		t.Fatalf("a failed helper was restarted (%d runs)", n)
	}
	// And a person can start it again.
	if _, err := f.sup.Start("lens"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the second start to run", 30*time.Second, func() bool { return countLines(state) > 4 })
}

func TestCrashOnceThenRecovers(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runs")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "crash-once", "--after", "100ms", "--state", state)})
	if _, err := f.sup.Start("lens"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a restart that is running", 60*time.Second, func() bool {
		st := f.sup.Status("lens")
		return countLines(state) == 2 && st.State == StateRunning && st.Restarts == 1
	})
}

func TestRestartDelaysAreOneTwoFour(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runs")
	var starts []time.Time
	var mu sync.Mutex
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "crash", "--after", "100ms", "--state", state)}, func(c *Config) {
		tm := *fastTimings()
		tm.Backoff = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
		c.Timings = &tm
		last := 0
		c.Notify = func(s Status) {
			mu.Lock()
			defer mu.Unlock()
			if s.State == StateStarting && strings.Contains(s.Err, "restarting") && s.Restarts != last {
				last = s.Restarts
				starts = append(starts, time.Now())
			}
		}
	})
	if _, err := f.sup.Start("lens"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "failure", 60*time.Second, func() bool { return f.sup.Status("lens").State == StateFailed })
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 3 {
		t.Fatalf("saw %d restarts, want 3", len(starts))
	}
	// Each restart is announced when its delay begins, so the gap between
	// announcements is the delay plus a run, and has to grow.
	if g1, g2 := starts[1].Sub(starts[0]), starts[2].Sub(starts[1]); g2 <= g1 {
		t.Errorf("the delays did not grow: %v then %v", g1, g2)
	}
}

func TestUserStopDoesNotRestart(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runs")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--state", state)})
	if st := f.startAndWait("lens"); st.State != StateRunning {
		t.Fatalf("status = %+v", st)
	}
	if err := f.sup.Stop("lens"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := countLines(state); n != 1 {
		t.Fatalf("the helper ran %d times after a stop", n)
	}
	if got := f.sup.Status("lens").State; got != StateStopped {
		t.Fatalf("state = %s", got)
	}
	// Stopping again is not an error.
	if err := f.sup.Stop("lens"); err != nil {
		t.Fatal(err)
	}
	if err := f.sup.Stop("never-started"); err != nil {
		t.Fatal(err)
	}
}

func TestStopEscalatesToKill(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "ignore-int", "--marker", marker)}, func(c *Config) {
		tm := *fastTimings()
		tm.StopGrace = 400 * time.Millisecond
		c.Timings = &tm
	})
	st := f.startAndWait("lens")
	if st.State != StateRunning {
		t.Fatalf("status = %+v", st)
	}
	start := time.Now()
	if err := f.sup.Stop("lens"); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if took < 350*time.Millisecond {
		t.Fatalf("stopped in %v: it was killed before the grace ran out", took)
	}
	if took > 15*time.Second {
		t.Fatalf("stopping took %v", took)
	}
	if !processGone(st.PID) {
		t.Fatal("the helper that ignored the interrupt is still running")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the helper handled the interrupt it was meant to ignore")
	}
	if f.sup.Status("lens").State != StateStopped {
		t.Fatalf("state = %s", f.sup.Status("lens").State)
	}
}

func TestOneInstancePerHelper(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runs")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--state", state)})
	first, err := f.sup.Start("lens")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.sup.Start("lens")
	if err != nil {
		t.Fatal(err)
	}
	if second.State != StateStarting && second.State != StateRunning {
		t.Fatalf("second start = %+v (first %+v)", second, first)
	}
	running := f.startAndWait("lens")
	again, _ := f.sup.Start("lens")
	if again.PID != running.PID || again.Port != running.Port || again.URL != running.URL {
		t.Fatalf("a third start gave another process: %+v vs %+v", again, running)
	}
	if n := countLines(state); n != 1 {
		t.Fatalf("%d processes were started", n)
	}

	// A second Flockdeck sharing the state directory is refused, by run.json.
	other := NewSupervisor(Config{Store: f.store, Lookup: f.sup.cfg.Lookup, Timings: fastTimings(), BreakCmd: testBreakCmd})
	_, err = other.Start("lens")
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartRefusals(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	if _, err := f.sup.Start("nope"); err == nil {
		t.Error("started a helper that is not in the catalogue")
	}
	if _, err := f.sup.Start("../lens"); err == nil {
		t.Error("started a helper with a path for a name")
	}
	if err := os.Remove(f.store.currentFile("lens")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sup.Start("lens"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("err = %v", err)
	}
}

func TestUnresponsiveHelperIsRestarted(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runs")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "unhealthy", "--after", "200ms", "--state", state)}, func(c *Config) {
		tm := *fastTimings()
		tm.HealthEvery = 40 * time.Millisecond
		tm.UnhealthyAfter = 3
		c.Timings = &tm
	})
	if _, err := f.sup.Start("lens"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "unresponsive then a restart", 60*time.Second, func() bool {
		saw := false
		for _, s := range f.states() {
			saw = saw || s == StateUnresponsive
		}
		return saw && countLines(state) >= 2
	})
}

func TestStopAllStopsEveryHelper(t *testing.T) {
	m1, m2 := filepath.Join(t.TempDir(), "m1"), filepath.Join(t.TempDir(), "m2")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--marker", m1), fakeEntry("other", "--marker", m2)})
	a := f.startAndWait("lens")
	b := f.startAndWait("other")
	if a.State != StateRunning || b.State != StateRunning {
		t.Fatalf("states = %s, %s", a.State, b.State)
	}
	f.sup.StopAll(5 * time.Second)
	for _, id := range []string{"lens", "other"} {
		if st := f.sup.Status(id); st.State != StateStopped {
			t.Errorf("%s is %s", id, st.State)
		}
	}
	if !processGone(a.PID) || !processGone(b.PID) {
		t.Fatal("a helper outlived StopAll")
	}
	// A helper that ignores the interrupt is killed at the grace given.
	f2 := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "ignore-int")})
	c := f2.startAndWait("lens")
	start := time.Now()
	f2.sup.StopAll(300 * time.Millisecond)
	if time.Since(start) > 10*time.Second || !processGone(c.PID) {
		t.Fatalf("StopAll took %v, alive=%v", time.Since(start), !processGone(c.PID))
	}
}

// What a launcher started is ended when the launcher crashes.
func TestCrashedLauncherTakesItsChildrenWithIt(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	state := filepath.Join(t.TempDir(), "runs")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "grandchild-crash", "--after", "300ms", "--child-pid", pidFile, "--state", state)}, func(c *Config) {
		tm := *fastTimings()
		tm.MaxRestarts = 0
		c.Timings = &tm
	})
	if _, err := f.sup.Start("lens"); err != nil {
		t.Fatal(err)
	}
	var child int
	eventually(t, "the grandchild to start", 30*time.Second, func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		return child > 0
	})
	eventually(t, "the helper to fail", 30*time.Second, func() bool { return f.sup.Status("lens").State == StateFailed })
	eventually(t, "the grandchild to be ended", 15*time.Second, func() bool { return processGone(child) })
}

// A stop waits for what the launcher started to finish draining.
func TestStopWaitsForTheChildrenToDrain(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "drained")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "grandchild-drain", "--marker", marker, "--drain", "700ms")})
	if st := f.startAndWait("lens"); st.State != StateRunning {
		t.Fatalf("status = %+v", st)
	}
	// The child needs a moment to be listening for the interrupt.
	time.Sleep(500 * time.Millisecond)
	if err := f.sup.Stop("lens"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(marker); string(b) != "graceful" {
		t.Fatalf("the child was killed before it had drained (marker %q)", b)
	}
}

// reaper -------------------------------------------------------------------

// spawnStale starts a fake helper outside any supervisor, as one a crashed
// Flockdeck left behind, in a group of its own so a group signal reaches it.
func spawnStale(t *testing.T, mode string) (pid int, started time.Time) {
	t.Helper()
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "-test.run=^TestFakeHelperProcess$", "--", "serve", "--mode", mode, "--port", strconv.Itoa(freePort(t)))
	configureProc(cmd)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	started, _ = store.ProcessStartedAt(cmd.Process.Pid)
	return cmd.Process.Pid, started
}

func TestReaperEndsAStaleHelper(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	pid, started := spawnStale(t, "sleep")
	if started.IsZero() {
		t.Skip("this platform cannot say when a process started")
	}
	if err := f.store.writeRun("lens", RunInfo{PID: pid, Started: started, Port: 1}); err != nil {
		t.Fatal(err)
	}
	if n := f.sup.ReapStale(); n != 1 {
		t.Fatalf("reaped %d, want 1", n)
	}
	eventually(t, "the stale process to end", 15*time.Second, func() bool { return processGone(pid) })
	if _, ok := f.store.ReadRun("lens"); ok {
		t.Fatal("run.json survived the reap")
	}
}

func TestReaperLeavesARecycledPidAlone(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	pid, started := spawnStale(t, "sleep")
	if started.IsZero() {
		t.Skip("this platform cannot say when a process started")
	}
	// The record says the process started an hour ago: this pid is a different
	// process that was given the number since.
	if err := f.store.writeRun("lens", RunInfo{PID: pid, Started: started.Add(-time.Hour), Port: 1}); err != nil {
		t.Fatal(err)
	}
	if n := f.sup.ReapStale(); n != 0 {
		t.Fatalf("reaped %d, want 0", n)
	}
	time.Sleep(200 * time.Millisecond)
	if processGone(pid) {
		t.Fatal("a process that was not the helper was killed")
	}
	if _, ok := f.store.ReadRun("lens"); ok {
		t.Fatal("the stale record was kept")
	}
}

func TestReaperIgnoresADeadPid(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	if err := f.store.writeRun("lens", RunInfo{PID: 2147483000, Started: time.Now().Add(-time.Hour), Port: 1}); err != nil {
		t.Fatal(err)
	}
	if n := f.sup.ReapStale(); n != 0 {
		t.Fatalf("reaped %d", n)
	}
	if _, ok := f.store.ReadRun("lens"); ok {
		t.Fatal("the record for a dead process was kept")
	}
}

func TestReaperWaitsWhileAnotherFlockdeckRuns(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")}, func(c *Config) { c.OtherInstance = func() bool { return true } })
	pid, started := spawnStale(t, "sleep")
	if started.IsZero() {
		t.Skip("this platform cannot say when a process started")
	}
	if err := f.store.writeRun("lens", RunInfo{PID: pid, Started: started, Port: 1}); err != nil {
		t.Fatal(err)
	}
	if n := f.sup.ReapStale(); n != 0 {
		t.Fatalf("reaped %d with another instance running", n)
	}
	if processGone(pid) {
		t.Fatal("another instance's helper was killed")
	}
	if _, ok := f.store.ReadRun("lens"); !ok {
		t.Fatal("another instance's record was removed")
	}
}

func TestReaperSkipsWhatThisSupervisorRuns(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	st := f.startAndWait("lens")
	if st.State != StateRunning {
		t.Fatalf("status = %+v", st)
	}
	if n := f.sup.ReapStale(); n != 0 {
		t.Fatalf("the reaper ended %d of this supervisor's own helpers", n)
	}
	if processGone(st.PID) {
		t.Fatal("the running helper was killed")
	}
}

// With no start time on record, a Unix process has to show the helper's folder
// in its command line before it is ended.
func TestReaperWithoutAStartTimeChecksTheCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a record without a start time is not acted on on Windows")
	}
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	bin, _ := f.store.BinaryPath(fakeEntry("lens"), runtime.GOOS)
	cmd := exec.Command(bin, "-test.run=^TestFakeHelperProcess$", "--", "serve", "--mode", "sleep", "--port", strconv.Itoa(freePort(t)))
	configureProc(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	if err := f.store.writeRun("lens", RunInfo{PID: cmd.Process.Pid, Port: 1}); err != nil {
		t.Fatal(err)
	}
	if n := f.sup.ReapStale(); n != 1 {
		t.Fatalf("reaped %d, want 1", n)
	}

	// An unrelated process under the same kind of record is left alone.
	other, _ := spawnStale(t, "sleep")
	if err := f.store.writeRun("lens", RunInfo{PID: other, Port: 1}); err != nil {
		t.Fatal(err)
	}
	if n := f.sup.ReapStale(); n != 0 {
		t.Fatalf("reaped %d unrelated processes", n)
	}
	if processGone(other) {
		t.Fatal("an unrelated process was killed")
	}
}

// A program changed since it was installed is not started, and the refusal
// says what to do. The file is replaced rather than written into, since the
// test copy is a hard link to the test binary.
func TestStartRefusesAChangedProgram(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	bin, _ := f.store.BinaryPath(fakeEntry("lens"), runtime.GOOS)
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("something else"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := f.sup.Start("lens")
	if err == nil || !strings.Contains(err.Error(), "changed since it was installed") || !strings.Contains(err.Error(), "reinstall") {
		t.Fatalf("err = %v", err)
	}
	if st := f.sup.Status("lens"); st.State != StateStopped {
		t.Fatalf("state = %s", st.State)
	}
}

// The check is made again for every run, restarts included: a program swapped
// while the helper was running is not what a restart starts.
func TestARestartRefusesAProgramChangedWhileRunning(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runs")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "crash-once", "--after", "100ms", "--state", state)})
	bin, _ := f.store.BinaryPath(fakeEntry("lens"), runtime.GOOS)
	if _, err := f.sup.Start("lens"); err != nil {
		t.Fatal(err)
	}
	// Replace the file as soon as the first run is up. On Windows a running
	// program cannot be deleted, but it can be renamed away.
	eventually(t, "the first run", 30*time.Second, func() bool { return f.sup.Status("lens").State == StateRunning })
	moved := bin + ".old"
	if err := os.Rename(bin, moved); err != nil {
		t.Skipf("cannot move a running program here: %v", err)
	}
	if err := os.WriteFile(bin, []byte("something else"), 0o755); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the restart to be refused", 30*time.Second, func() bool {
		st := f.sup.Status("lens")
		return st.State == StateFailed && strings.Contains(st.Err, "changed since it was installed")
	})
	if n := countLines(state); n != 1 {
		t.Fatalf("the helper ran %d times", n)
	}
}

// A helper that writes a 2 MiB line and then a great deal more must not be
// left blocked on a pipe nobody reads, and what is kept for the status stays
// small and free of escape sequences.
func TestAHelperThatFloodsItsOutputKeepsRunning(t *testing.T) {
	done := filepath.Join(t.TempDir(), "flooded")
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "flood", "--flood-done", done)})
	st := f.startAndWait("lens")
	if st.State != StateRunning {
		t.Fatalf("status = %+v", st)
	}
	eventually(t, "the helper to finish writing its output", 60*time.Second, func() bool {
		_, err := os.Stat(done)
		return err == nil
	})
	// Still answering.
	if !f.sup.probe(st.Port, "/healthz") {
		t.Fatal("the helper stopped answering")
	}
	// What the status carries is small and printable.
	f.sup.mu.Lock()
	in := f.sup.insts["lens"]
	f.sup.mu.Unlock()
	log := in.log.tail()
	if len(log) == 0 || len(log) > 20 {
		t.Fatalf("%d log lines", len(log))
	}
	total := 0
	for _, l := range log {
		total += len(l)
		if len(l) > maxRingLine+len(" [cut off]") {
			t.Errorf("a status line is %d bytes", len(l))
		}
		if strings.ContainsRune(l, 0x1b) {
			t.Errorf("an escape sequence reached the status: %q", l)
		}
	}
	if total > 20*(maxRingLine+16) {
		t.Errorf("the status log is %d bytes", total)
	}
	// The log file has the lines, each cut to a bounded size.
	fi, err := os.Stat(f.store.LogFile("lens"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() < 1<<20 {
		t.Errorf("the log file is only %d bytes", fi.Size())
	}
}

// A redirect from the helper's port is not an answer from the helper, and the
// probe does not go where it points.
func TestProbeNeverFollowsARedirect(t *testing.T) {
	reached := 0
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached++ }))
	defer elsewhere.Close()
	squatter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/readyz", http.StatusFound)
	}))
	defer squatter.Close()
	port, _ := strconv.Atoi(squatter.URL[strings.LastIndex(squatter.URL, ":")+1:])
	s := NewSupervisor(Config{Store: &Store{Root: t.TempDir()}})
	if s.probe(port, "/readyz") {
		t.Fatal("a redirect counted as ready")
	}
	if reached != 0 {
		t.Fatal("the probe followed the redirect")
	}
	tr, ok := probeClient.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Fatal("the probe may use a proxy")
	}
	if probeClient.Timeout == 0 || probeClient.Timeout > 5*time.Second {
		t.Fatalf("the probe timeout is %v", probeClient.Timeout)
	}
}

// A bookmark to a helper keeps working after it is stopped and started, not only
// after a crash and a restart: run.json goes when it stops, so the port is kept
// beside it.
func TestTheLastPortIsReusedAfterAStopAndAStart(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	first := f.startAndWait("lens")
	if first.State != StateRunning {
		t.Fatalf("status = %+v", first)
	}
	if err := f.sup.Stop("lens"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.store.ReadRun("lens"); ok {
		t.Fatal("run.json survived the stop, which the test relies on it not doing")
	}
	second := f.startAndWait("lens")
	if second.State != StateRunning || second.Port != first.Port {
		t.Fatalf("the port changed from %d to %d across a stop and a start", first.Port, second.Port)
	}
}

func TestTheLastPortFileIsReadSafely(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(s.appDir("lens"), 0o755); err != nil {
		t.Fatal(err)
	}
	for content, want := range map[string]int{"8123": 8123, " 8123\n": 8123, "": 0, "abc": 0, "80": 0, "70000": 0, "-5": 0, "8123x": 0} {
		if err := os.WriteFile(s.lastPortFile("lens"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := s.lastPort("lens"); got != want {
			t.Errorf("%q: %d, want %d", content, got, want)
		}
	}
}
