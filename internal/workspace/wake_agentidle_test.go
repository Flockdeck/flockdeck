package workspace

import (
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/team"
)

// agentWord is what the rig's agent says about itself, changeable by a test.
type agentWord struct {
	mu    sync.Mutex
	word  AgentWord
	ok    bool
	calls int
	// onCall runs with the number of the call, before it answers.
	onCall func(n int)
}

func (a *agentWord) get() (AgentWord, bool) {
	a.mu.Lock()
	a.calls++
	n, hook := a.calls, a.onCall
	a.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.word, a.ok
}

func (a *agentWord) set(w AgentWord, ok bool) {
	a.mu.Lock()
	a.word, a.ok = w, ok
	a.mu.Unlock()
}

func agentIdleRig(t *testing.T) (*wakeRig, *agentWord) {
	t.Helper()
	r := newWakeRig(t)
	r.sess.reading.Verdict = agent.WakeUnknown // the rule is untested
	w := &agentWord{ok: true, word: AgentWord{
		Idle: true, Since: r.clock.Add(-10 * time.Second), Pid: 4242, SessionID: "conv-1", From: "pid file",
	}}
	r.cand.AgentIdle = w.get
	return r, w
}

// An agent that says it has been idle for a few seconds is woken without a
// tested screen rule, and the record says why and from what.
func TestAnAgentThatSaysItIsIdleIsWokenWithoutAScreenRule(t *testing.T) {
	r, w := agentIdleRig(t)
	if why := r.try(); why != "" {
		t.Fatalf("not woken: %s", why)
	}
	if got := r.sess.written(); len(got) != 2 || got[1] != "\r" {
		t.Fatalf("typed %q, want the line and its Enter", got)
	}
	if len(r.events) != 1 || r.events[0].Kind != team.EventWake || r.events[0].RuleSource != wakeSessionFileRule {
		t.Fatalf("events = %+v, want one wake allowed by the session file", r.events)
	}
	e := r.events[0]
	if e.Pid != 4242 || e.SessionID != "conv-1" || !e.IdleSince.Equal(w.word.Since) || e.WordFrom != "pid file" {
		t.Errorf("the record does not say what the word was: %+v", e)
	}
	if len(r.mail.woken) != 1 || r.mail.woken[0] != 7 {
		t.Errorf("woken = %v", r.mail.woken)
	}
}

// Without the agent's word the untested rule still stops the wake, and every other
// condition and limit applies on top of it.
func TestTheAgentsWordDoesNotLiftTheOtherConditions(t *testing.T) {
	const notLong = "the agent has not said it is idle for long enough"
	cases := map[string]struct {
		set  func(r *wakeRig, w *agentWord)
		want string
	}{
		"no word from the agent": {func(r *wakeRig, w *agentWord) { w.set(AgentWord{}, false) }, "the rule does not allow it"},
		"agent is busy":          {func(r *wakeRig, w *agentWord) { w.set(AgentWord{Idle: false, Since: r.clock.Add(-time.Hour)}, true) }, notLong},
		"idle only just":         {func(r *wakeRig, w *agentWord) { w.set(AgentWord{Idle: true, Since: r.clock.Add(-time.Second)}, true) }, notLong},
		"flockdeck sees working": {func(r *wakeRig, w *agentWord) { r.sess.status = session.StatusWorking }, "not idle"},
		"flockdeck sees waiting": {func(r *wakeRig, w *agentWord) { r.sess.status = session.StatusWaiting }, "not idle"},
		"user typed":             {func(r *wakeRig, w *agentWord) { r.sess.user = r.clock.Add(-10 * time.Second) }, "the user is typing"},
		"output moved":           {func(r *wakeRig, w *agentWord) { r.sess.reading.Tail.ChangedAt = r.clock.Add(-time.Second) }, "output changed a moment ago"},
		"hook just told":         {func(r *wakeRig, w *agentWord) { r.svc.notices = wakeFakeNotices{at: r.clock.Add(-10 * time.Second)} }, "told by a hook a moment ago"},
		"switched off":           {func(r *wakeRig, w *agentWord) { r.svc.settings = func() WakeSettings { return wakeOff{} } }, "switched off"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r, w := agentIdleRig(t)
			c.set(r, w)
			if why := r.try(); why != c.want {
				t.Fatalf("considered: %q, want %q", why, c.want)
			}
			if len(r.sess.written()) != 0 {
				t.Errorf("typed %q", r.sess.written())
			}
		})
	}

	// The per-pane throttle: one line a minute.
	r, _ := agentIdleRig(t)
	if why := r.try(); why != "" {
		t.Fatalf("first: %s", why)
	}
	r.advance(10 * time.Second)
	if why := r.try(); why != "one line a minute" {
		t.Errorf("second within the minute: %q", why)
	}
}

// A key pressed in the pane after the agent went idle may be half a message or a
// picker opened by hand, so nothing is typed; one from before it is nothing of
// the kind. This holds however long ago the key was, which the 30 s user guard
// alone does not.
func TestAKeyPressedSinceTheAgentWentIdleHoldsTheWakeBack(t *testing.T) {
	const typed = "someone has typed in the pane since the agent went idle"
	r, w := agentIdleRig(t)
	w.set(AgentWord{Idle: true, Since: r.clock.Add(-120 * time.Second), Pid: 4242, SessionID: "conv-1", From: "pid file"}, true)
	r.sess.user = r.clock.Add(-60 * time.Second) // 60 s ago, after the agent went idle 120 s ago
	if why := r.try(); why != typed {
		t.Fatalf("a key after the idle began: %q, want %q", why, typed)
	}
	if len(r.sess.written()) != 0 {
		t.Errorf("typed %q after a keystroke", r.sess.written())
	}

	r, w = agentIdleRig(t)
	w.set(AgentWord{Idle: true, Since: r.clock.Add(-30 * time.Second), Pid: 4242, SessionID: "conv-1", From: "pid file"}, true)
	r.sess.user = r.clock.Add(-60 * time.Second) // 60 s ago, before the agent went idle 30 s ago
	if why := r.try(); why != "" {
		t.Fatalf("a key from before the idle began held it back: %q", why)
	}
}

// The same test is made again in the moment before the line.
func TestAKeyDuringTheDecisionStopsTheLine(t *testing.T) {
	r, w := agentIdleRig(t)
	w.onCall = func(n int) {
		if n == 2 { // the look just before the line
			r.sess.mu.Lock()
			r.sess.user = r.clock.Add(-5 * time.Second)
			r.sess.mu.Unlock()
		}
	}
	r.try()
	if n := len(r.sess.written()); n != 0 {
		t.Fatalf("typed %q after a key was pressed", r.sess.written())
	}
}

// What counts as the person typing, and what does not: bytes from the window or a
// device do, and text Flockdeck typed on its own account does not.
func TestOnlyThePersonsKeysCountAsTyping(t *testing.T) {
	s := idleShellSession(t)
	if !s.LastUserInput().IsZero() {
		t.Fatal("a new pane has user input")
	}
	_ = s.WriteStringFrom(session.InputFlockdeck, "hello")
	if !s.LastUserInput().IsZero() {
		t.Error("text Flockdeck typed (a message-box send, a wake) counted as the person")
	}
	_, _ = s.Write([]byte("x"))
	if s.LastUserInput().IsZero() {
		t.Error("bytes from the window did not count as the person")
	}
}

// If the agent goes busy between the decision and the line, nothing is typed.
func TestAWakeIsDroppedIfTheAgentGoesBusyBeforeTheLine(t *testing.T) {
	r, w := agentIdleRig(t)
	w.onCall = func(n int) {
		if n == 2 {
			w.mu.Lock()
			w.word.Idle = false
			w.mu.Unlock()
		}
	}
	r.try()
	if n := len(r.sess.written()); n != 0 {
		t.Fatalf("typed %q after the agent went busy", r.sess.written())
	}
	if len(r.mail.woken) != 0 {
		t.Errorf("woken = %v", r.mail.woken)
	}
}

// Between the line and its Enter the agent may go busy, a dialog may open or
// another record may stand in: no Enter is sent, the line is recorded as
// unsubmitted, and the pane is not typed into again until the person has.
func TestNoEnterIsSentIfThePaneChangesAfterTheLine(t *testing.T) {
	cases := map[string]func(r *wakeRig, w *agentWord){
		"agent went busy": func(r *wakeRig, w *agentWord) { w.mu.Lock(); w.word.Idle = false; w.mu.Unlock() },
		"another idle record": func(r *wakeRig, w *agentWord) {
			w.mu.Lock()
			w.word.Since = w.word.Since.Add(time.Second)
			w.mu.Unlock()
		},
		"another process": func(r *wakeRig, w *agentWord) { w.mu.Lock(); w.word.Pid = 9; w.mu.Unlock() },
		"flockdeck sees work": func(r *wakeRig, w *agentWord) {
			r.sess.mu.Lock()
			r.sess.status = session.StatusWorking
			r.sess.mu.Unlock()
		},
		"a dialog is drawn": func(r *wakeRig, w *agentWord) {
			r.sess.mu.Lock()
			r.sess.reading.Forbidden = true
			r.sess.mu.Unlock()
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r, w := agentIdleRig(t)
			r.sess.onWrite = func(n int) {
				if n == 1 { // the line has been typed
					change(r, w)
				}
			}
			r.try()
			got := r.sess.written()
			if len(got) != 1 || got[0] == "\r" {
				t.Fatalf("typed %q, want the line and no Enter", got)
			}
			if len(r.mail.woken) != 0 {
				t.Errorf("woken = %v for a line that was not submitted", r.mail.woken)
			}
			if len(r.events) != 1 || !r.events[0].Unsubmitted {
				t.Errorf("events = %+v, want one unsubmitted wake", r.events)
			}
		})
	}
}

// The line is not typed into a screen that shows a question, whatever the agent's
// record says.
func TestAForbiddenScreenStopsTheLine(t *testing.T) {
	r, _ := agentIdleRig(t)
	r.sess.reading.Forbidden = true
	r.try()
	if n := len(r.sess.written()); n != 0 {
		t.Fatalf("typed %q into a screen the rule forbids", r.sess.written())
	}
}
