package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/team"
)

type wakeFakeSession struct {
	mu      sync.Mutex
	reading session.WakeReading
	user    time.Time
	status  session.Status
	exited  bool
	writes  []string
	sources []session.InputSource
	// onWrite runs after each write, for a test that has the user type then.
	onWrite func(n int)
	// failFrom makes every write from the nth on fail; 0 is never.
	failFrom  int
	flockdeck time.Time
}

var errWakeWrite = errors.New("write failed")

func (f *wakeFakeSession) LastFlockdeckInput() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flockdeck
}

func (f *wakeFakeSession) WakeReading() session.WakeReading {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reading
}
func (f *wakeFakeSession) LastUserInput() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.user
}
func (f *wakeFakeSession) Status() (session.Status, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, ""
}
func (f *wakeFakeSession) Exited() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exited
}
func (f *wakeFakeSession) WriteStringFrom(src session.InputSource, text string) error {
	f.mu.Lock()
	n := len(f.writes) + 1
	if f.failFrom > 0 && n >= f.failFrom {
		f.mu.Unlock()
		return errWakeWrite
	}
	f.writes = append(f.writes, text)
	f.sources = append(f.sources, src)
	hook := f.onWrite
	f.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return nil
}
func (f *wakeFakeSession) written() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes...)
}

type wakeFakeMail struct {
	mu     sync.Mutex
	unread int
	msgs   []team.Message
	woken  []int
}

func (m *wakeFakeMail) List(string, bool) []team.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]team.Message(nil), m.msgs...)
}
func (m *wakeFakeMail) NewestUnread(string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unread
}
func (m *wakeFakeMail) MarkWoken(_ string, ids ...int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.woken = append(m.woken, ids...)
}

type wakeFakeNotices struct{ at time.Time }

func (n wakeFakeNotices) LastNotice(string) time.Time { return n.at }

type wakeOff struct{}

func (wakeOff) WakeEnabled(string, string) bool { return false }
func (wakeOff) Invalidate()                     {}

type wakeRig struct {
	svc    *wakeService
	sess   *wakeFakeSession
	mail   *wakeFakeMail
	cand   wakeCandidate
	clock  time.Time
	events []team.Event
	mu     sync.Mutex
}

func newWakeRig(t *testing.T) *wakeRig {
	t.Helper()
	r := &wakeRig{clock: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	r.sess = &wakeFakeSession{status: session.StatusIdle}
	r.sess.reading = session.WakeReading{
		Verdict: agent.WakeOK, Prompt: "> ", Version: "1", Source: agent.RuleBuiltIn,
		Tail: session.ScreenTail{ChangedAt: r.clock.Add(-time.Minute)},
	}
	r.mail = &wakeFakeMail{unread: 7}
	r.cand = wakeCandidate{ID: "p1", Agent: "claude", Root: "/proj", Sess: r.sess}
	r.svc = newWakeService(func() []wakeCandidate { return []wakeCandidate{r.cand} },
		r.mail, wakeFakeNotices{}, func() WakeSettings { return wakeAllOn{} },
		func(e team.Event) { r.mu.Lock(); r.events = append(r.events, e); r.mu.Unlock() })
	r.svc.now = func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.clock }
	r.svc.sleep = func(time.Duration, <-chan struct{}) bool { return true }
	t.Cleanup(r.svc.Stop)
	return r
}

func (r *wakeRig) advance(d time.Duration) {
	r.mu.Lock()
	r.clock = r.clock.Add(d)
	r.mu.Unlock()
}

// try considers the pane and waits for any line to finish.
func (r *wakeRig) try() string {
	why := r.svc.consider(r.cand)
	r.svc.wg.Wait()
	return why
}

func TestWakeTypesTheFixedLineThenEnter(t *testing.T) {
	r := newWakeRig(t)
	if why := r.try(); why != "" {
		t.Fatalf("not woken: %s", why)
	}
	got := r.sess.written()
	want := []string{"Flockdeck: new mail, message 7. Run: flockdeck inbox read 7", "\r"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("typed %q, want %q", got, want)
	}
	for _, s := range r.sess.sources {
		if s != session.InputFlockdeck {
			t.Fatalf("a write was counted as the user's")
		}
	}
	if len(r.mail.woken) != 1 || r.mail.woken[0] != 7 {
		t.Fatalf("woken = %v", r.mail.woken)
	}
	if len(r.events) != 1 {
		t.Fatalf("events = %v", r.events)
	}
	e := r.events[0]
	if e.Kind != team.EventWake || e.Pane != "p1" || e.Agent != "claude" || e.RuleSource != agent.RuleBuiltIn || e.Line != want[0] {
		t.Fatalf("event = %+v", e)
	}
}

func TestWakeHoldsBackUnlessEveryConditionHolds(t *testing.T) {
	cases := map[string]func(r *wakeRig){
		"no mail":          func(r *wakeRig) { r.mail.unread = 0 },
		"exited":           func(r *wakeRig) { r.sess.exited = true },
		"working":          func(r *wakeRig) { r.sess.status = session.StatusWorking },
		"untested rule":    func(r *wakeRig) { r.sess.reading.Verdict = agent.WakeUnknown },
		"prompt not found": func(r *wakeRig) { r.sess.reading.Verdict = agent.WakeNo },
		"tail moved":       func(r *wakeRig) { r.sess.reading.Tail.ChangedAt = r.clock.Add(-time.Second) },
		"user typed":       func(r *wakeRig) { r.sess.user = r.clock.Add(-10 * time.Second) },
		"hook just told": func(r *wakeRig) {
			r.svc.notices = wakeFakeNotices{at: r.clock.Add(-10 * time.Second)}
		},
		"switched off": func(r *wakeRig) { r.svc.settings = func() WakeSettings { return wakeOff{} } },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			r := newWakeRig(t)
			set(r)
			if why := r.try(); why == "" {
				t.Fatalf("woke")
			}
			if n := len(r.sess.written()); n != 0 {
				t.Fatalf("typed %q", r.sess.written())
			}
			if len(r.mail.woken) != 0 || len(r.events) != 0 {
				t.Fatalf("recorded a wake that did not happen")
			}
		})
	}
}

func TestWakeLimits(t *testing.T) {
	t.Run("one line a minute per pane", func(t *testing.T) {
		r := newWakeRig(t)
		r.try()
		r.advance(30 * time.Second)
		if r.try() == "" {
			t.Fatal("second line inside a minute")
		}
		r.advance(31 * time.Second)
		if why := r.try(); why != "" {
			t.Fatalf("held back after a minute: %s", why)
		}
	})
	t.Run("streak stops at three until a read", func(t *testing.T) {
		r := newWakeRig(t)
		for i := 0; i < wakeStreakMax; i++ {
			if why := r.try(); why != "" {
				t.Fatalf("line %d: %s", i, why)
			}
			r.advance(2 * time.Minute)
		}
		if r.try() == "" {
			t.Fatal("fourth line with no read")
		}
		r.mail.msgs = []team.Message{{ReadAt: r.clock.Add(-time.Second)}}
		if why := r.try(); why != "" {
			t.Fatalf("after a read: %s", why)
		}
	})
	t.Run("project ceiling per hour", func(t *testing.T) {
		r := newWakeRig(t)
		r.svc.projectHour = 2
		for i := 0; i < 2; i++ {
			r.try()
			r.advance(2 * time.Minute)
			r.mail.msgs = []team.Message{{ReadAt: r.clock}}
		}
		if r.try() == "" {
			t.Fatal("third line in the hour")
		}
		r.advance(time.Hour)
		if why := r.try(); why != "" {
			t.Fatalf("after the hour: %s", why)
		}
	})
}

func TestWakeSkipsEnterWhenUserTypesDuringSettle(t *testing.T) {
	r := newWakeRig(t)
	r.svc.sleep = func(time.Duration, <-chan struct{}) bool {
		r.advance(time.Second)
		r.sess.mu.Lock()
		r.sess.user = r.clock
		r.sess.mu.Unlock()
		return true
	}
	if why := r.try(); why != "" {
		t.Fatal(why)
	}
	got := r.sess.written()
	if len(got) != 1 || strings.Contains(got[0], "\r") {
		t.Fatalf("typed %q", got)
	}
	if len(r.mail.woken) != 0 {
		t.Fatalf("marked woken with no Enter: %v", r.mail.woken)
	}
	if len(r.events) != 1 || !r.events[0].Unsubmitted {
		t.Fatalf("events = %+v, want one unsubmitted", r.events)
	}
}

func TestWakeDoesNotTypeWhenUserArrivesBeforeTheLine(t *testing.T) {
	r := newWakeRig(t)
	reads := 0
	r.svc.settings = func() WakeSettings {
		return wakeAllOn{}
	}
	// The user types between the judgement and the write.
	r.sess.mu.Lock()
	r.sess.reading.Tail.ChangedAt = r.clock.Add(-time.Minute)
	r.sess.mu.Unlock()
	orig := r.cand
	r.cand.Sess = &wakeUserArrives{wakeFakeSession: r.sess, after: &reads, clock: r}
	r.svc.consider(r.cand)
	r.svc.wg.Wait()
	if n := len(r.sess.written()); n != 0 {
		t.Fatalf("typed %q", r.sess.written())
	}
	r.cand = orig
	// The attempt is not counted against the pane.
	r.sess.mu.Lock()
	r.sess.user = time.Time{}
	r.sess.mu.Unlock()
	if why := r.try(); why != "" {
		t.Fatalf("limits counted an attempt that typed nothing: %s", why)
	}
}

// wakeUserArrives reports the user as having just typed from its second look at
// LastUserInput on.
type wakeUserArrives struct {
	*wakeFakeSession
	after *int
	clock *wakeRig
}

func (w *wakeUserArrives) LastUserInput() time.Time {
	*w.after++
	if *w.after >= 2 {
		return w.clock.svc.now()
	}
	return time.Time{}
}

func TestWakeStopWaitsAndStopsAreRepeatable(t *testing.T) {
	r := newWakeRig(t)
	r.svc.run()
	r.svc.Stop()
	r.svc.Stop()
}

func TestWakePrunesPanesThatAreGone(t *testing.T) {
	r := newWakeRig(t)
	r.try()
	r.svc.panes = func() []wakeCandidate { return nil }
	r.svc.tick()
	r.svc.mu.Lock()
	n := len(r.svc.state)
	r.svc.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d panes remembered", n)
	}
}

func TestCleanPromptLine(t *testing.T) {
	long := strings.Repeat("a", team.MaxPromptLine+50)
	if got := cleanPromptLine("> \x1b[31mhi\x07\n"); got != "> [31mhi" {
		t.Fatalf("got %q", got)
	}
	if got := cleanPromptLine(long); len(got) != team.MaxPromptLine {
		t.Fatalf("len %d", len(got))
	}
}

func TestPrefsWakeReadsSwitchesAndRefreshes(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	var prefs store.Prefs
	var err error
	reads := 0
	pw := &prefsWake{read: func() (store.Prefs, error) { reads++; return prefs, err }, now: func() time.Time { return now }}

	if !pw.WakeEnabled("claude", "p1") {
		t.Fatal("off with nothing switched off")
	}
	prefs.SetWake(store.WakePane, "p1", false)
	if !pw.WakeEnabled("claude", "p1") || reads != 1 {
		t.Fatalf("read again inside the interval (%d reads)", reads)
	}
	// A literal, so that lengthening the cache to an hour fails here.
	now = now.Add(1100 * time.Millisecond)
	if pw.WakeEnabled("claude", "p1") {
		t.Fatal("a switched-off pane is still woken 1.1 seconds after the switch")
	}
	if !pw.WakeEnabled("claude", "p2") {
		t.Fatal("another pane was switched off")
	}
	// A file that cannot be read turns wake off, not on.
	prefs = store.Prefs{}
	err = errors.New("unreadable")
	now = now.Add(1100 * time.Millisecond)
	if pw.WakeEnabled("claude", "p2") {
		t.Fatal("woke with preferences that could not be read")
	}
}

func TestWakeSettingsChangedDropsTheCache(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	var prefs store.Prefs
	pw := &prefsWake{read: func() (store.Prefs, error) { return prefs, nil }, now: func() time.Time { return now }}
	w := &Workspace{wakeSettings: pw}
	if !pw.WakeEnabled("claude", "p1") {
		t.Fatal("off with nothing switched off")
	}
	prefs.SetWake(store.WakeGlobal, "", false)
	if !pw.WakeEnabled("claude", "p1") {
		t.Fatal("the cache was not used")
	}
	w.WakeSettingsChanged()
	if pw.WakeEnabled("claude", "p1") {
		t.Fatal("a switch just set is not in force")
	}
}

// flipSettings answers from a script: calls before flipAt see wake on, and
// calls from flipAt on see it off.
type flipSettings struct {
	mu     sync.Mutex
	calls  int
	flipAt int
}

func (f *flipSettings) WakeEnabled(string, string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.calls < f.flipAt
}
func (f *flipSettings) Invalidate() {}

// A switch turned off after consider has passed does not get a line typed.
func TestWakeReadsTheSwitchesAgainBeforeTheLine(t *testing.T) {
	r := newWakeRig(t)
	// Call 1 is consider's; call 2 is the one just before the line.
	f := &flipSettings{flipAt: 2}
	r.svc.settings = func() WakeSettings { return f }
	r.try()
	if n := len(r.sess.written()); n != 0 {
		t.Fatalf("typed %q after the switch went off", r.sess.written())
	}
	if len(r.mail.woken) != 0 || len(r.events) != 0 {
		t.Fatal("recorded a wake that did not happen")
	}
}

// A switch turned off while the line settles keeps the Enter back. The line
// stays typed, which the service records as unsubmitted.
func TestWakeReadsTheSwitchesAgainBeforeTheEnter(t *testing.T) {
	r := newWakeRig(t)
	f := &flipSettings{flipAt: 3}
	r.svc.settings = func() WakeSettings { return f }
	r.try()
	got := r.sess.written()
	if len(got) != 1 || strings.Contains(got[0], "\r") {
		t.Fatalf("typed %q, want the line alone", got)
	}
	if len(r.mail.woken) != 0 {
		t.Fatalf("marked woken with no Enter: %v", r.mail.woken)
	}
	if len(r.events) != 1 || !r.events[0].Unsubmitted {
		t.Fatalf("events = %+v, want one unsubmitted", r.events)
	}
}

// A damaged preferences file turns wake off, and stays off after the file has
// been moved aside, which is what ReadPrefs does with it.
func TestPrefsWakeIsOffForADamagedFileEvenAfterItIsMovedAside(t *testing.T) {
	isolateConfig(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.ClearWakeDamaged)
	path := filepath.Join(dir, "prefs.json")
	if err := os.WriteFile(path, []byte(`{"wake":{"off":true},"theme":5}x`), 0o600); err != nil {
		t.Fatal(err)
	}
	pw := newPrefsWake()
	if pw.WakeEnabled("claude", "p1") {
		t.Fatal("wake on with a damaged file")
	}
	// The rest of the program reads the preferences, and the file is moved aside.
	store.ReadPrefs()
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the damaged file was not moved aside")
	}
	pw.Invalidate()
	if pw.WakeEnabled("claude", "p1") {
		t.Fatal("wake came back on once the damaged file was moved aside")
	}
	store.ClearWakeDamaged()
	pw.Invalidate()
	if !pw.WakeEnabled("claude", "p1") {
		t.Fatal("wake still off after the user set a switch")
	}
}
