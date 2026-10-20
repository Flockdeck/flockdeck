package workspace

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/team"
)

// These tests send to structured panes and wake them with mail. The fake agent
// answers every prompt; a prompt starting "slow" keeps a turn running until it
// is cancelled.

// structuredWakeRig is a wake service over the workspace's own panes, on a clock
// the test moves. The workspace's live service is stopped, so only this one
// acts.
type structuredWakeRig struct {
	ws     *Workspace
	svc    *wakeService
	mu     sync.Mutex
	offset time.Duration
	events []team.Event
}

func newStructuredWakeRig(t *testing.T, ws *Workspace) *structuredWakeRig {
	t.Helper()
	ws.stopWake()
	r := &structuredWakeRig{ws: ws}
	r.svc = newWakeService(ws.wakeCandidates, ws.team.inbox, ws.team.notifier,
		func() WakeSettings { return wakeAllOn{} },
		func(e team.Event) { r.mu.Lock(); r.events = append(r.events, e); r.mu.Unlock() })
	r.svc.now = func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return time.Now().Add(r.offset) }
	r.svc.sleep = func(time.Duration, <-chan struct{}) bool { return true }
	t.Cleanup(r.svc.Stop)
	return r
}

func (r *structuredWakeRig) advance(d time.Duration) {
	r.mu.Lock()
	r.offset += d
	r.mu.Unlock()
}

// try considers one pane, as a tick would, and waits for a line to finish.
func (r *structuredWakeRig) try(id string) string {
	for _, c := range r.ws.wakeCandidates() {
		if c.ID == id {
			why := r.svc.consider(c)
			r.svc.wg.Wait()
			return why
		}
	}
	return "not a candidate"
}

func (r *structuredWakeRig) wakes() []team.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]team.Event(nil), r.events...)
}

// prompts counts the prompts the agent has been sent, and the last one's text.
func prompts(ws *Workspace, id string) (int, string) {
	entries, _ := ws.StructuredEntries(id)
	n, last := 0, ""
	for _, e := range entries {
		if e.Kind == "prompt" {
			n++
			last = e.Markdown
			if last == "" {
				last = e.Text
			}
		}
	}
	return n, last
}

func waitForPrompts(t *testing.T, ws *Workspace, id string, want int) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		n, last := prompts(ws, id)
		if n >= want {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d prompts reached the agent, want %d", n, want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func mailTo(t *testing.T, ws *Workspace, id, text string) team.Message {
	t.Helper()
	m, err := ws.team.inbox.Send(team.Sender{Name: "peer"}, id, text)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A prompt sent to an idle pane goes to the agent at once, and to a working one
// is queued behind the turn and not sent into it.
func TestSendToAStructuredPaneIsQueuedBehindARunningTurn(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	res, err := ws.SendToPane(id, "plain again", false)
	if err != nil || res.Queued {
		t.Fatalf("to an idle pane: %+v, %v", res, err)
	}
	waitForPrompts(t, ws, id, 2)

	busy := spawnStructured(t, ws, lead, "slow")
	statusWithin(t, ws, busy, session.StatusWorking)
	res, err = ws.SendToPane(busy, "plain queued", false)
	if err != nil || !res.Queued {
		t.Fatalf("to a working pane: %+v, %v", res, err)
	}
	if n, _ := prompts(ws, busy); n != 1 {
		t.Fatalf("the queued prompt reached the agent during the turn: %d prompts", n)
	}
	if sn := ws.StructuredRuntime(busy).Snapshot(); sn.Status != "working" {
		t.Errorf("status after the queued send: %q", sn.Status)
	}
}

// A pane waiting on a permission request is refused text, a message from the
// graph included, and is not woken.
func TestAStructuredPaneWaitingOnAPermissionTakesNoTextAndNoWake(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "hold-perm")
	statusWithin(t, ws, id, session.StatusWaiting)
	if _, err := ws.SendToPane(id, "plain", false); err != ErrSendPermission {
		t.Errorf("text to a waiting pane: %v", err)
	}
	rig := newStructuredWakeRig(t, ws)
	mailTo(t, ws, id, "please look at this")
	rig.advance(time.Hour)
	if why := rig.try(id); why != "not idle" {
		t.Errorf("a waiting pane: %q", why)
	}
	if n, _ := prompts(ws, id); n != 1 {
		t.Errorf("%d prompts reached the agent", n)
	}
}

// A pane with mail is woken when it is idle, not while a turn runs, and once
// the turn has ended. The prompt is the fixed line and the id, the message is
// marked woken, and the event names the rule and carries no message text.
func TestAStructuredPaneIsWokenWhenItGoesIdle(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "slow")
	statusWithin(t, ws, id, session.StatusWorking)
	rig := newStructuredWakeRig(t, ws)
	const body = "the secret body of the message"
	m := mailTo(t, ws, id, body)

	if why := rig.try(id); why != "not idle" {
		t.Fatalf("while a turn runs: %q", why)
	}
	if n, _ := prompts(ws, id); n != 1 {
		t.Fatalf("%d prompts during the turn", n)
	}

	if err := ws.StructuredRuntime(id).Cancel(); err != nil {
		t.Fatal(err)
	}
	statusWithin(t, ws, id, session.StatusIdle)
	// The pane has not been idle for the quiet period yet.
	if why := rig.try(id); why != "output changed a moment ago" {
		t.Fatalf("just after the turn: %q", why)
	}
	rig.advance(time.Minute)
	if why := rig.try(id); why != "" {
		t.Fatalf("after the turn: %q", why)
	}
	got := waitForPrompts(t, ws, id, 2)
	if want := wakeLine(m.ID); got != want {
		t.Errorf("the agent was sent %q, want %q", got, want)
	}
	if strings.Contains(got, body) {
		t.Error("the message text was sent")
	}
	if p := ws.Pane(id); p.Sess != nil {
		t.Error("a session appeared")
	}

	ev := rig.wakes()
	if len(ev) != 1 {
		t.Fatalf("%d wake events", len(ev))
	}
	e := ev[0]
	if e.Kind != team.EventWake || e.ID != m.ID || e.Pane != id || e.Rule != "structured" || e.Line != wakeLine(m.ID) || e.Unsubmitted {
		t.Errorf("event = %+v", e)
	}
	raw, _ := json.Marshal(e)
	if strings.Contains(string(raw), body) {
		t.Error("the event carries the message text")
	}
	for _, msg := range ws.team.inbox.List(id, true) {
		if msg.ID == m.ID && msg.WokenAt.IsZero() {
			t.Error("the message was not marked woken")
		}
	}
}

// The limits of the typed wake apply: one a minute for a pane, and three in a
// row without a read.
func TestAStructuredPaneWakeKeepsTheLimits(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	rig := newStructuredWakeRig(t, ws)
	rig.advance(time.Hour)

	for i := 1; i <= wakeStreakMax; i++ {
		mailTo(t, ws, id, "mail")
		if why := rig.try(id); why != "" {
			t.Fatalf("wake %d: %q", i, why)
		}
		waitForPrompts(t, ws, id, 1+i)
		statusWithin(t, ws, id, session.StatusIdle)
		if i == 1 {
			mailTo(t, ws, id, "more mail")
			if why := rig.try(id); why != "one line a minute" {
				t.Fatalf("a second wake inside the minute: %q", why)
			}
		}
		rig.advance(2 * wakePaneEvery)
	}
	mailTo(t, ws, id, "yet more")
	if why := rig.try(id); why != "waiting for the agent to read" {
		t.Errorf("a fourth wake with nothing read: %q", why)
	}
	if n, _ := prompts(ws, id); n != 1+wakeStreakMax {
		t.Errorf("%d prompts reached the agent", n)
	}
}

// A wake does not go to a pane whose agent has ended, and a send to one says so.
func TestAStructuredPaneThatEndedIsNotWokenOrSentTo(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	rig := newStructuredWakeRig(t, ws)
	mailTo(t, ws, id, "mail")
	rig.advance(time.Hour)
	if err := ws.StructuredRuntime(id).Close(); err != nil {
		t.Fatal(err)
	}
	statusWithin(t, ws, id, session.StatusExited)
	if why := rig.try(id); why != "not a candidate" && why != "no running process" {
		t.Errorf("an ended pane: %q", why)
	}
	if _, err := ws.SendToPane(id, "plain", false); err != ErrSendExited {
		t.Errorf("a send to an ended pane: %v", err)
	}
}
