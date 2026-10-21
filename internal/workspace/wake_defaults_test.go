package workspace

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/team"
)

// The defaults of the wake service are what the help page promises, so each is
// pinned here and a change to one is a change to the page.

// A new service waits a tenth of a second between the line and its Enter, and
// the limit for a project is 20 lines an hour.
func TestWakeServiceDefaults(t *testing.T) {
	svc := newWakeService(nil, nil, nil, nil, nil)
	if svc.settle != 100*time.Millisecond {
		t.Errorf("settle = %v, want 100ms", svc.settle)
	}
	if svc.settle != pasteSettle {
		t.Errorf("settle %v is not pasteSettle %v", svc.settle, pasteSettle)
	}
	if svc.projectHour != 20 {
		t.Errorf("projectHour = %d, want 20", svc.projectHour)
	}
	// These two compare the constants with their own values, so they are a
	// tripwire for a change to either and not a test of what it does:
	// TestWakeLimits pins the behaviour of the pane and streak limits.
	if wakePaneEvery != time.Minute || wakeStreakMax != 3 {
		t.Errorf("pane every %v, streak %d, want 1m and 3", wakePaneEvery, wakeStreakMax)
	}
}

// The Enter is written after the settle the service was made with, as a
// separate write.
func TestWakeWaitsTheDefaultSettleBeforeTheEnter(t *testing.T) {
	r := newWakeRig(t)
	var slept []time.Duration
	r.svc.sleep = func(d time.Duration, _ <-chan struct{}) bool {
		slept = append(slept, d)
		return true
	}
	if why := r.try(); why != "" {
		t.Fatal(why)
	}
	if len(slept) != 1 || slept[0] != 100*time.Millisecond {
		t.Errorf("slept %v between the line and its Enter, want one wait of 100ms", slept)
	}
	if w := r.sess.written(); len(w) != 2 || w[1] != "\r" {
		t.Errorf("writes = %q, want the line and then a separate Enter", w)
	}
}

// Twenty lines an hour for a project, with the default limit: the twenty-first
// is refused with the reason, and an hour later it is allowed again.
func TestWakeProjectLimitIsTwentyAnHour(t *testing.T) {
	r := newWakeRig(t)
	for i := 1; i <= 20; i++ {
		if why := r.try(); why != "" {
			t.Fatalf("line %d: %s", i, why)
		}
		r.advance(2 * time.Minute)
		r.mail.msgs = []team.Message{{ReadAt: r.clock}}
	}
	if why := r.try(); why != "project limit for the hour" {
		t.Fatalf("line 21 in the hour: %q", why)
	}
	r.advance(time.Hour)
	if why := r.try(); why != "" {
		t.Fatalf("after the hour: %s", why)
	}
}
