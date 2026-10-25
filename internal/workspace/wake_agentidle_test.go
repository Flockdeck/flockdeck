package workspace

import (
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/team"
)

// agentSaysIdle makes the rig's pane one whose agent records its own state, with
// the screen rule untested (as Claude Code's is), and returns a handle to change
// what the agent says.
type agentWord struct {
	mu    sync.Mutex
	since time.Time
	idle  bool
	ok    bool
}

func (a *agentWord) get() (time.Time, bool, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.since, a.idle, a.ok
}

func (a *agentWord) set(since time.Time, idle, ok bool) {
	a.mu.Lock()
	a.since, a.idle, a.ok = since, idle, ok
	a.mu.Unlock()
}

func agentIdleRig(t *testing.T) (*wakeRig, *agentWord) {
	t.Helper()
	r := newWakeRig(t)
	r.sess.reading.Verdict = agent.WakeUnknown // the rule is untested
	w := &agentWord{since: r.clock.Add(-10 * time.Second), idle: true, ok: true}
	r.cand.AgentIdle = w.get
	return r, w
}

// An agent that says it has been idle for a few seconds is woken without a
// tested screen rule, and the record says why it was allowed.
func TestAnAgentThatSaysItIsIdleIsWokenWithoutAScreenRule(t *testing.T) {
	r, _ := agentIdleRig(t)
	if why := r.try(); why != "" {
		t.Fatalf("not woken: %s", why)
	}
	if got := r.sess.written(); len(got) != 2 || got[1] != "\r" {
		t.Fatalf("typed %q, want the line and its Enter", got)
	}
	if len(r.events) != 1 || r.events[0].Kind != team.EventWake || r.events[0].RuleSource != wakeSessionFileRule {
		t.Fatalf("events = %+v, want one wake allowed by the session file", r.events)
	}
	if len(r.mail.woken) != 1 || r.mail.woken[0] != 7 {
		t.Errorf("woken = %v", r.mail.woken)
	}
}

// Without the agent's word the untested rule still stops the wake, and every other
// condition and limit applies on top of it.
func TestTheAgentsWordDoesNotLiftTheOtherConditions(t *testing.T) {
	cases := map[string]struct {
		set  func(r *wakeRig, w *agentWord)
		want string
	}{
		"no word from the agent": {func(r *wakeRig, w *agentWord) { w.set(time.Time{}, false, false) }, "the rule does not allow it"},
		"agent is busy":          {func(r *wakeRig, w *agentWord) { w.set(r.clock.Add(-time.Hour), false, true) }, "the agent has not said it is idle for long enough"},
		"idle only just":         {func(r *wakeRig, w *agentWord) { w.set(r.clock.Add(-time.Second), true, true) }, "the agent has not said it is idle for long enough"},
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

// If the agent goes busy between the decision and the line, nothing is typed.
func TestAWakeIsDroppedIfTheAgentGoesBusyBeforeTheLine(t *testing.T) {
	r, w := agentIdleRig(t)
	calls := 0
	r.cand.AgentIdle = func() (time.Time, bool, bool) {
		calls++
		if calls > 1 { // the decision was the first look; the second is just before the line
			return r.clock.Add(-time.Second), false, true
		}
		return w.get()
	}
	r.try()
	if n := len(r.sess.written()); n != 0 {
		t.Fatalf("typed %q after the agent went busy", r.sess.written())
	}
	if len(r.mail.woken) != 0 {
		t.Errorf("woken = %v", r.mail.woken)
	}
}
