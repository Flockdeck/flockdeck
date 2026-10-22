package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/structured"
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
		func() WakeSettings { return wakeTextOn{} },
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
	if want := wakeTextPrompt(m); got != want {
		t.Errorf("the agent was sent %q, want %q", got, want)
	}
	if !strings.Contains(got, body) {
		t.Error("the message text was not sent")
	}
	if p := ws.Pane(id); p.Sess != nil {
		t.Error("a session appeared")
	}

	ev := rig.wakes()
	if len(ev) != 1 {
		t.Fatalf("%d wake events", len(ev))
	}
	e := ev[0]
	if e.Kind != team.EventWake || e.ID != m.ID || e.Pane != id || e.Rule != "structured" || e.Line != wakeTextPrompt(m) || e.Unsubmitted {
		t.Errorf("event = %+v", e)
	}
	for _, msg := range ws.team.inbox.List(id, true) {
		if msg.ID == m.ID && msg.WokenAt.IsZero() {
			t.Error("the message was not marked woken")
		}
		if msg.ID == m.ID && msg.ReadAt.IsZero() {
			t.Error("the message was not marked read")
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

// stubStructured is a structured runtime that says what the test tells it to, to
// reach states the fake agent cannot be made to show.
type stubStructured struct {
	PaneRuntime
	status session.Status
	detail string
	err    error
	sent   []string
}

func (s *stubStructured) Transport() Transport             { return TransportStructured }
func (s *stubStructured) Status() (session.Status, string) { return s.status, s.detail }
func (s *stubStructured) Alive() bool                      { return true }
func (s *stubStructured) Activity() Activity               { return ActivityIdle }
func (s *stubStructured) Close() error                     { return nil }
func (s *stubStructured) Wake(m string) error              { s.sent = append(s.sent, m); return s.err }

// stubbedPane puts a stub runtime in the manager's pane. The terminal one comes
// back at the end of the test so that the workspace can close it.
func stubbedPane(t *testing.T, ws *Workspace, id string, s *stubStructured) {
	t.Helper()
	ws.SetRuntime(id, s)
	t.Cleanup(func() { ws.SetRuntime(id, nil) })
}

// A structured pane waiting on a request takes no text, whatever the tool is
// called: a terminal's AskUserQuestion takes typed text, and this does not.
func TestAStructuredPaneWaitingOnAnyToolTakesNoText(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	stub := &stubStructured{status: session.StatusWaiting, detail: "AskUserQuestion"}
	stubbedPane(t, ws, lead, stub)
	if _, err := ws.SendToPane(lead, "an answer", false); err != ErrSendPermission {
		t.Errorf("text to a waiting structured pane: %v", err)
	}
	if len(stub.sent) != 0 {
		t.Errorf("%q reached the agent", stub.sent)
	}
}

// The runtime's refusals are told in the send errors, and a refused send is not
// counted as queued.
func TestSendToAStructuredPaneTellsWhyItWasRefused(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	stub := &stubStructured{status: session.StatusIdle}
	stubbedPane(t, ws, lead, stub)
	for _, c := range []struct {
		from error
		want error
	}{{structured.ErrQueueFull, ErrSendQueueFull}, {structured.ErrNotRunning, ErrSendExited}} {
		stub.err = c.from
		res, err := ws.SendToPane(lead, "text", false)
		if err != c.want || res.Queued {
			t.Errorf("%v: got %+v, %v; want %v", c.from, res, err, c.want)
		}
	}
	stub.err = nil
	if _, err := ws.SendToPane(lead, "text", false); err != nil {
		t.Errorf("an accepted send: %v", err)
	}
}

// A prompt Flockdeck sends is the last input it made, which holds the wake back
// for the quiet period.
func TestASentPromptMovesTheStructuredPanesLastFlockdeckInput(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	sr, ok := ws.Runtime(ws.Pane(id)).(*structuredRuntime)
	if !ok {
		t.Fatal("not a structured runtime")
	}
	a := wakeStructured{sr}
	if !a.LastFlockdeckInput().IsZero() {
		t.Fatal("Flockdeck had sent nothing")
	}
	before := time.Now()
	if _, err := ws.SendToPane(id, "plain", false); err != nil {
		t.Fatal(err)
	}
	if at := a.LastFlockdeckInput(); at.Before(before) || time.Since(at) > time.Minute {
		t.Errorf("last Flockdeck input is %v", at)
	}
	if !a.LastUserInput().IsZero() {
		t.Error("the user typed nothing into a pane without a terminal")
	}
}

// A prompt that is refused is not a wake: the message stays unwoken, nothing is
// recorded, and the pane waits out the minute.
func TestARefusedWakePromptLeavesTheMessageUnwoken(t *testing.T) {
	r := newWakeRig(t)
	r.mail.msgs = []team.Message{{ID: 7, Text: "hello"}}
	r.svc.settings = func() WakeSettings { return wakeTextOn{} }
	r.cand.Deliver = func(string) error { return structured.ErrNotRunning }
	if why := r.try(); why != "" {
		t.Fatalf("not tried: %s", why)
	}
	if len(r.mail.woken) != 0 || len(r.events) != 0 {
		t.Errorf("woken %v, events %v", r.mail.woken, r.events)
	}
	if why := r.try(); why != "one line a minute" {
		t.Errorf("tried again at once: %q", why)
	}
}

// Each wake switch holds a structured pane back, and a damaged preferences file
// does too; with the switch back on the pane is woken.
func TestTheWakeSwitchesHoldBackAStructuredPane(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	t.Cleanup(store.ClearWakeDamaged)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	rig := newStructuredWakeRig(t, ws)
	rig.svc.settings = func() WakeSettings { return ws.wakeSettings }
	rig.advance(time.Hour)
	mailTo(t, ws, id, "mail")

	set := func(scope, who string, on bool) {
		t.Helper()
		p := store.Prefs{StructuredPanes: true}
		p.SetWake(store.WakeText, "opencode-acp", true)
		p.SetWake(scope, who, on)
		if err := store.SavePrefs(p); err != nil {
			t.Fatal(err)
		}
		ws.WakeSettingsChanged()
	}
	for _, c := range []struct{ name, scope, who string }{
		{"global", store.WakeGlobal, ""},
		{"agent type", store.WakeAgent, "opencode-acp"},
		{"pane", store.WakePane, id},
	} {
		set(c.scope, c.who, false)
		if why := rig.try(id); why != "switched off" {
			t.Errorf("%s off: %q", c.name, why)
		}
	}
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prefs.json"), []byte(`{"wake":{"off":false},"theme":5}x`), 0o600); err != nil {
		t.Fatal(err)
	}
	ws.WakeSettingsChanged()
	if why := rig.try(id); why != "switched off" {
		t.Errorf("damaged preferences: %q", why)
	}
	if n, _ := prompts(ws, id); n != 1 {
		t.Fatalf("%d prompts reached the agent while it was switched off", n)
	}

	store.ClearWakeDamaged()
	set(store.WakeGlobal, "", true)
	if why := rig.try(id); why != "" {
		t.Errorf("with everything on: %q", why)
	}
	waitForPrompts(t, ws, id, 2)
}
