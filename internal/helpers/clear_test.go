package helpers

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// failingRemove makes removing a run.json fail, as an antivirus scanner that
// holds the file open does, until it is told to let go.
type failingRemove struct {
	mu   sync.Mutex
	fail bool
	n    int
}

func (f *failingRemove) set(v bool) { f.mu.Lock(); f.fail = v; f.mu.Unlock() }

func (f *failingRemove) install(t *testing.T) {
	f.fail = true
	h := func(path string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fail && strings.HasSuffix(path, "run.json") {
			f.n++
			return os.ErrPermission
		}
		return os.Remove(path)
	}
	removeHook.Store(&h)
	short := [2]time.Duration{3 * time.Second, 10 * time.Millisecond}
	clearTimings.Store(&short)
	t.Cleanup(func() { removeHook.Store(nil); clearTimings.Store(nil) })
}

// Whoever sees the helper stopped or failed must not find a record that still
// says it is starting: the record is removed before the state changes.
func TestTheRecordIsGoneBeforeTheStateSaysStoppedOrFailed(t *testing.T) {
	cases := map[string]func(t *testing.T) (id string, run func(f *supFixture)){}
	_ = cases

	check := func(t *testing.T, extra []string, wantState State, act func(f *supFixture)) {
		var mu sync.Mutex
		var seen []string
		var f *supFixture
		f = newSupFixture(t, []Entry{fakeEntry("lens", extra...)}, func(c *Config) {
			tm := *fastTimings()
			tm.MaxRestarts = 0
			c.Timings = &tm
			c.Notify = func(s Status) {
				if s.State != StateStopped && s.State != StateFailed {
					return
				}
				// Read here, in the notification that says it, not later.
				_, present := f.store.ReadRun("lens")
				mu.Lock()
				seen = append(seen, string(s.State)+" record present="+boolText(present))
				mu.Unlock()
			}
		})
		act(f)
		eventually(t, "the "+string(wantState)+" notification", 30*time.Second, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(seen) > 0
		})
		mu.Lock()
		defer mu.Unlock()
		for _, s := range seen {
			if strings.HasSuffix(s, "present=true") {
				t.Errorf("%s", s)
			}
		}
	}

	t.Run("a stop", func(t *testing.T) {
		check(t, nil, StateStopped, func(f *supFixture) {
			if st := f.startAndWait("lens"); st.State != StateRunning {
				t.Fatalf("status = %+v", st)
			}
			if err := f.sup.Stop("lens"); err != nil {
				t.Fatal(err)
			}
		})
	})
	t.Run("a crash that ends it", func(t *testing.T) {
		check(t, []string{"--mode", "crash", "--after", "50ms"}, StateFailed, func(f *supFixture) {
			if _, err := f.sup.Start("lens"); err != nil {
				t.Fatal(err)
			}
		})
	})
	t.Run("a wrong banner", func(t *testing.T) {
		check(t, []string{"--mode", "wrong-banner"}, StateFailed, func(f *supFixture) {
			if _, err := f.sup.Start("lens"); err != nil {
				t.Fatal(err)
			}
		})
	})
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// A record that cannot be removed does not block this process's own supervisor
// from starting the helper again, or the helper from being removed, and it is
// removed once the remove works.
func TestARecordThatCannotBeRemovedBlocksNothing(t *testing.T) {
	var rm failingRemove
	rm.install(t)
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	if st := f.startAndWait("lens"); st.State != StateRunning {
		t.Fatalf("status = %+v", st)
	}
	if err := f.sup.Stop("lens"); err != nil {
		t.Fatal(err)
	}
	if got := f.sup.Status("lens").State; got != StateStopped {
		t.Fatalf("state = %s", got)
	}
	r, left := f.store.ReadRun("lens")
	if !left || !r.Starting {
		t.Fatalf("the test did not leave a record behind: %+v, %v", r, left)
	}
	if rm.n == 0 {
		t.Fatal("the remove was never attempted")
	}
	if _, running := f.store.RunningPID("lens"); running {
		t.Fatal("a leftover record counts as a helper that is starting")
	}
	// Started again.
	if st := f.startAndWait("lens"); st.State != StateRunning {
		t.Fatalf("a start with a leftover record: %+v", st)
	}
	if err := f.sup.Stop("lens"); err != nil {
		t.Fatal(err)
	}
	// Removed while one is left.
	if err := f.store.Uninstall("lens", false); err != nil {
		t.Fatalf("an uninstall with a leftover record: %v", err)
	}
}

// Once the scanner lets go, the background retry removes the record, with no
// other help.
func TestARecordThatCannotBeRemovedIsRemovedWhenItCanBe(t *testing.T) {
	var rm failingRemove
	rm.install(t)
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	if st := f.startAndWait("lens"); st.State != StateRunning {
		t.Fatalf("status = %+v", st)
	}
	if err := f.sup.Stop("lens"); err != nil {
		t.Fatal(err)
	}
	if _, left := f.store.ReadRun("lens"); !left {
		t.Fatal("no record was left behind")
	}
	rm.set(false)
	eventually(t, "the leftover record to be removed", 10*time.Second, func() bool {
		_, ok := f.store.ReadRun("lens")
		return !ok
	})
}

// A record that is another process's is not made light of, though: it is
// someone else's helper, and only the leftover of this one is ignored.
func TestAnotherProcessesStartingRecordStillBlocks(t *testing.T) {
	f := newSupFixture(t, []Entry{fakeEntry("lens")})
	other, otherStarted := spawnStale(t, "sleep")
	if otherStarted.IsZero() {
		t.Skip("this platform cannot say when a process started")
	}
	if err := f.store.writeRun("lens", RunInfo{Starting: true, OwnerPID: other, OwnerStarted: otherStarted}); err != nil {
		t.Fatal(err)
	}
	if _, running := f.store.RunningPID("lens"); !running {
		t.Fatal("another live Flockdeck's start under way was ignored")
	}
}

// What a helper prints goes to the log as plain text.
func TestTheLogFileHoldsNoControlCharacters(t *testing.T) {
	done := t.TempDir() + "/flooded"
	f := newSupFixture(t, []Entry{fakeEntry("lens", "--mode", "flood", "--flood-done", done)})
	if st := f.startAndWait("lens"); st.State != StateRunning {
		t.Fatalf("status = %+v", st)
	}
	eventually(t, "the output", 60*time.Second, func() bool { _, err := os.Stat(done); return err == nil })
	if err := f.sup.Stop("lens"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(f.store.LogFile("lens"))
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range b {
		if c == 0x1b || (c < 0x20 && c != '\n') {
			t.Fatalf("a control character (%#x) is in the log at byte %d", c, i)
		}
	}
	if !strings.Contains(string(b), "red") {
		t.Fatal("the text was lost")
	}
}
