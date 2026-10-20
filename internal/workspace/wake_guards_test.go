package workspace

import (
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
)

// The Enter is not written when the write fails, and then the message is not
// marked woken.
func TestWakeEnterWriteFailureIsNotAWake(t *testing.T) {
	r := newWakeRig(t)
	r.sess.failFrom = 2
	if why := r.try(); why != "" {
		t.Fatal(why)
	}
	if len(r.mail.woken) != 0 {
		t.Fatalf("marked woken: %v", r.mail.woken)
	}
	if len(r.events) != 1 || !r.events[0].Unsubmitted {
		t.Fatalf("events = %+v", r.events)
	}
	// The line may be in the input box, so nothing more is typed until the user
	// has typed in the pane.
	r.advance(10 * time.Minute)
	if why := r.try(); why == "" {
		t.Fatal("typed again over an unsubmitted line")
	}
	r.sess.mu.Lock()
	r.sess.user = r.clock
	r.sess.mu.Unlock()
	r.advance(time.Minute)
	if why := r.try(); why != "" {
		t.Fatalf("after the user typed: %s", why)
	}
}

// A service that stops during the pause leaves the line typed but not
// submitted, and says so.
func TestWakeStoppedDuringTheSettleIsNotAWake(t *testing.T) {
	r := newWakeRig(t)
	r.svc.sleep = func(time.Duration, <-chan struct{}) bool { return false }
	if why := r.try(); why != "" {
		t.Fatal(why)
	}
	if got := r.sess.written(); len(got) != 1 {
		t.Fatalf("typed %q", got)
	}
	if len(r.mail.woken) != 0 || len(r.events) != 1 || !r.events[0].Unsubmitted {
		t.Fatalf("woken %v, events %+v", r.mail.woken, r.events)
	}
}

func TestWakeWaitsForFlockdeckOwnTyping(t *testing.T) {
	r := newWakeRig(t)
	r.sess.flockdeck = r.clock.Add(-time.Second)
	if r.try() == "" {
		t.Fatal("woke just after Flockdeck typed")
	}
	r.advance(10 * time.Second)
	if why := r.try(); why != "" {
		t.Fatalf("after the quiet: %s", why)
	}
}

func TestWakeDoesNotCountAnAttemptThatCouldNotClaimThePane(t *testing.T) {
	r := newWakeRig(t)
	claimed := false
	r.cand.Claim = func() bool { return claimed }
	r.cand.Release = func() {}
	if r.try() == "" {
		t.Fatal("typed without the claim")
	}
	if len(r.sess.written()) != 0 {
		t.Fatal("wrote")
	}
	claimed = true
	if why := r.try(); why != "" {
		t.Fatalf("a refused claim was counted: %s", why)
	}
}

// A graph message being sent with SendToPane holds the pane's send flag from
// its text to its Enter. The wake takes the same flag, so neither can land
// between the other's two writes.
func TestWakeAndSendToPaneExcludeEachOther(t *testing.T) {
	ws, p := newSendTestWorkspace(t)
	r := newWakeRig(t)
	r.cand.Claim = func() bool { return ws.claimSend(p) }
	r.cand.Release = func() { ws.releaseSend(p) }

	// A send in flight: the wake does not start.
	if !ws.claimSend(p) {
		t.Fatal("could not take the flag")
	}
	if r.try() == "" {
		t.Fatal("woke during a send")
	}
	if len(r.sess.written()) != 0 {
		t.Fatal("wrote during a send")
	}
	ws.releaseSend(p)

	// A wake in flight: a send is refused until it is done.
	inPause := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	// Registered after newWakeRig's Stop, so it runs first and a failed test
	// does not leave the line waiting for it.
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	r.svc.sleep = func(time.Duration, <-chan struct{}) bool {
		select {
		case <-inPause:
		default:
			close(inPause)
		}
		<-release
		return true
	}
	if why := r.svc.consider(r.cand); why != "" {
		t.Fatal(why)
	}
	<-inPause
	if _, err := ws.SendToPane(p.ID, "hello", true); err != ErrSendInFlight {
		t.Fatalf("SendToPane during a wake: %v, want ErrSendInFlight", err)
	}
	once.Do(func() { close(release) })
	r.svc.wg.Wait()
	if !ws.claimSend(p) {
		t.Fatal("the wake kept the flag")
	}
	ws.releaseSend(p)
}

// Attempts that type nothing do not use up the project's hour, so another pane
// of the same project can still be woken.
func TestWakeFailedAttemptsDoNotBurnTheProjectQuota(t *testing.T) {
	r := newWakeRig(t)
	broken := &wakeFakeSession{status: session.StatusIdle, reading: r.sess.reading, failFrom: 1}
	bad := wakeCandidate{ID: "bad", Agent: "claude", Root: "/proj", Sess: broken}
	for i := 0; i < 25; i++ {
		r.svc.consider(bad)
		r.svc.wg.Wait()
		r.advance(61 * time.Second)
		// The reading must stay quiet as the clock moves.
		broken.mu.Lock()
		broken.reading.Tail.ChangedAt = r.clock.Add(-time.Minute)
		broken.mu.Unlock()
	}
	r.sess.mu.Lock()
	r.sess.reading.Tail.ChangedAt = r.clock.Add(-time.Minute)
	r.sess.mu.Unlock()
	if why := r.try(); why != "" {
		t.Fatalf("a healthy pane in the same project: %s", why)
	}
}

func TestWakeFailedWriteBacksTheSamePaneOff(t *testing.T) {
	r := newWakeRig(t)
	r.sess.failFrom = 1
	r.try()
	r.advance(10 * time.Second)
	if why := r.svc.consider(r.cand); why != "one line a minute" {
		t.Fatalf("second try: %q", why)
	}
	r.svc.wg.Wait()
}

// Two considers in a row, with the first still typing, start one line.
func TestWakeOverlappingConsidersTypeOnce(t *testing.T) {
	r := newWakeRig(t)
	inPause := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	// Registered after newWakeRig's Stop, so it runs first and a failed test
	// does not leave the line waiting for it.
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	r.svc.sleep = func(time.Duration, <-chan struct{}) bool {
		select {
		case <-inPause:
		default:
			close(inPause)
		}
		<-release
		return true
	}
	if why := r.svc.consider(r.cand); why != "" {
		t.Fatal(why)
	}
	<-inPause
	if why := r.svc.consider(r.cand); why != "a line is being typed" {
		t.Fatalf("overlapping consider: %q", why)
	}
	once.Do(func() { close(release) })
	r.svc.wg.Wait()
	if got := r.sess.written(); len(got) != 2 {
		t.Fatalf("typed %q", got)
	}
}

// The pane is read again just before typing: one that changed between the
// judgement and the write is left alone.
func TestWakeReadsThePaneAgainBeforeTyping(t *testing.T) {
	r := newWakeRig(t)
	reads := 0
	r.cand.Sess = &wakeReadChanges{wakeFakeSession: r.sess, reads: &reads}
	r.svc.consider(r.cand)
	r.svc.wg.Wait()
	if n := len(r.sess.written()); n != 0 {
		t.Fatalf("typed %q into a pane that changed", r.sess.written())
	}
}

// wakeReadChanges reports a prompt on the first reading and a draft on the next.
type wakeReadChanges struct {
	*wakeFakeSession
	reads *int
}

func (w *wakeReadChanges) WakeReading() session.WakeReading {
	r := w.wakeFakeSession.WakeReading()
	*w.reads++
	if *w.reads >= 2 {
		r.Verdict = agent.WakeNo
	}
	return r
}

// A real session, with no wake rule, is never typed into, whatever it shows.
func TestWakeNeverTypesIntoARealSessionWithoutATestedRule(t *testing.T) {
	_, p := newSendTestWorkspace(t)
	r := newWakeRig(t)
	r.cand.Sess = p.Sess
	if why := r.svc.consider(r.cand); why == "" {
		t.Fatal("woke a pane whose agent has no rule")
	}
	r.svc.wg.Wait()
	if len(r.mail.woken) != 0 || len(r.events) != 0 {
		t.Fatal("recorded a wake")
	}
}

// The built-in rules are all untested until a recording is made, so the
// catalog's own claude rule gives the service nothing to act on.
func TestWakeBuiltInRulesAreUntested(t *testing.T) {
	for _, sp := range agent.Builtins() {
		if sp.Wake != nil && sp.Wake.Tested {
			t.Errorf("%s: built-in wake rule is marked tested", sp.ID)
		}
	}
}
