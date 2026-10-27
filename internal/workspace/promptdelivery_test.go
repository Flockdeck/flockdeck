package workspace

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/team"
)

// --- the service against a fake runtime ---------------------------------

// fakePrompts stands in for a structured runtime: it takes tagged prompts up to
// a bound and then answers ErrQueueFull, and the test reports their turns.
type fakePrompts struct {
	mu    sync.Mutex
	gen   uint64
	alive bool
	room  int
	err   error
	sent  []fakeSent
}

type fakeSent struct {
	tag    int
	blocks []string
}

func (f *fakePrompts) pane() promptPane {
	f.mu.Lock()
	defer f.mu.Unlock()
	return promptPane{ID: "p2", Agent: "opencode-acp", Gen: f.gen,
		Alive: func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.alive },
		Send: func(tag int, blocks ...string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.err != nil {
				return f.err
			}
			if f.room == 0 {
				return structured.ErrQueueFull
			}
			f.room--
			f.sent = append(f.sent, fakeSent{tag, blocks})
			return nil
		}}
}

func (f *fakePrompts) tags() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int
	for _, s := range f.sent {
		out = append(out, s.tag)
	}
	return out
}

type promptRig struct {
	store *team.Store
	svc   *promptService
	fake  *fakePrompts
	// off switches the pane off: the settings the service reads say so.
	off bool
	// noPane removes the pane's runtime from the service's view.
	noPane bool
}

type switchedOff struct{ wakeAllOn }

func (switchedOff) WakeEnabled(string, string) bool { return false }

func newPromptRig(t *testing.T) *promptRig {
	t.Helper()
	r := &promptRig{store: team.NewStore(nil), fake: &fakePrompts{gen: 1, alive: true, room: 16}}
	r.store.SetRouter(func(pane string) string {
		if pane == "p2" {
			return team.RoutePrompt
		}
		return ""
	})
	r.svc = newPromptService(r.store,
		func() map[string]promptPane {
			if r.noPane {
				return map[string]promptPane{}
			}
			return map[string]promptPane{"p2": r.fake.pane()}
		},
		func() WakeSettings {
			if r.off {
				return switchedOff{}
			}
			return wakeAllOn{}
		})
	return r
}

func (r *promptRig) send(t *testing.T, text string) team.Message {
	t.Helper()
	m, err := r.store.Send(team.Sender{Pane: "p1", Role: "manager", Name: "lead"}, "p2", text)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (r *promptRig) state(t *testing.T, id int) team.Message {
	t.Helper()
	for _, m := range r.store.List("p2", true) {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("no message %d", id)
	return team.Message{}
}

func (r *promptRig) turn(kind structured.TurnEventKind, tag int, stop string) {
	r.svc.turn("p2", r.fake.gen, structured.TurnEvent{What: kind, Tag: tag, Stop: stop})
}

// queued, then handed to the runtime tagged with its id and as two blocks, then
// delivered when the turn begins, then handled at end_turn.
func TestAMessageGoesQueuedDeliveredHandled(t *testing.T) {
	r := newPromptRig(t)
	m := r.send(t, "please look at #12")
	if got := r.state(t, m.ID); got.Delivery() != team.DeliveryQueued || got.Route != team.RoutePrompt {
		t.Fatalf("after Send: %+v", got)
	}

	r.svc.tick()
	if tags := r.fake.tags(); len(tags) != 1 || tags[0] != m.ID {
		t.Fatalf("handed over %v, want [%d]", tags, m.ID)
	}
	if len(r.fake.sent[0].blocks) != 2 || !strings.HasPrefix(r.fake.sent[0].blocks[0], wakeTextPrefix) || r.fake.sent[0].blocks[1] != promptBodyMarker+"please look at #12" {
		t.Errorf("blocks = %q", r.fake.sent[0].blocks)
	}
	// Given to the runtime is not delivered: no turn has begun.
	if got := r.state(t, m.ID); got.Delivery() != team.DeliveryQueued {
		t.Errorf("state before the turn began = %q", got.Delivery())
	}
	// And it is not handed over twice.
	r.svc.tick()
	if len(r.fake.tags()) != 1 {
		t.Errorf("handed over again: %v", r.fake.tags())
	}

	r.turn(structured.TurnBegan, m.ID, "")
	if got := r.state(t, m.ID); got.Delivery() != team.DeliveryDelivered || got.Attempts != 1 {
		t.Errorf("after the turn began: %+v", got)
	}
	r.turn(structured.TurnEnded, m.ID, "end_turn")
	got := r.state(t, m.ID)
	if got.Delivery() != team.DeliveryHandled || got.StopReason != "end_turn" || got.Unread() {
		t.Errorf("after end_turn: %+v", got)
	}
	r.svc.tick()
	if len(r.fake.tags()) != 1 {
		t.Errorf("a handled message was handed over again: %v", r.fake.tags())
	}
}

// Marking the message read no longer follows the prompt: nothing sets Read.
func TestDeliveryDoesNotMarkAPromptMessageRead(t *testing.T) {
	r := newPromptRig(t)
	m := r.send(t, "hello")
	r.svc.tick()
	r.turn(structured.TurnBegan, m.ID, "")
	r.turn(structured.TurnEnded, m.ID, "end_turn")
	got := r.state(t, m.ID)
	if got.Read || !got.ReadAt.IsZero() || !got.WokenAt.IsZero() || !got.NotifiedAt.IsZero() {
		t.Errorf("a prompt delivery set a terminal mark: %+v", got)
	}
}

// cancelled and refusal send the message back to the queue, it is handed over
// again, and the third failure gives up for good.
func TestACancelledOrRefusedTurnIsRetriedUpToTheCap(t *testing.T) {
	for _, stop := range []string{"cancelled", "refusal"} {
		t.Run(stop, func(t *testing.T) {
			r := newPromptRig(t)
			m := r.send(t, "hello")
			for attempt := 1; attempt <= team.MaxAttempts; attempt++ {
				r.svc.tick()
				if n := len(r.fake.tags()); n != attempt {
					t.Fatalf("attempt %d: %d hand-overs", attempt, n)
				}
				r.turn(structured.TurnBegan, m.ID, "")
				r.turn(structured.TurnEnded, m.ID, stop)
				got := r.state(t, m.ID)
				if got.StopReason != stop || got.Attempts != attempt {
					t.Fatalf("attempt %d: %+v", attempt, got)
				}
				want := team.DeliveryQueued
				if attempt == team.MaxAttempts {
					want = team.DeliveryUndeliverable
				}
				if got.Delivery() != want {
					t.Fatalf("attempt %d: state %q, want %q", attempt, got.Delivery(), want)
				}
			}
			r.svc.tick()
			if n := len(r.fake.tags()); n != team.MaxAttempts {
				t.Errorf("an undeliverable message was handed over again: %d hand-overs", n)
			}
		})
	}
}

// Results are matched to messages by tag, not by the order they come back in.
func TestResultsAreMatchedByTagNotByOrder(t *testing.T) {
	r := newPromptRig(t)
	a := r.send(t, "first")
	b := r.send(t, "second")
	c := r.send(t, "third")
	r.svc.tick()
	for _, m := range []team.Message{a, b, c} {
		r.turn(structured.TurnBegan, m.ID, "")
	}
	// Back in the wrong order, with different outcomes.
	r.turn(structured.TurnEnded, c.ID, "refusal")
	r.turn(structured.TurnEnded, a.ID, "end_turn")
	r.turn(structured.TurnEnded, b.ID, "max_tokens")
	for id, want := range map[int]string{a.ID: team.DeliveryHandled, b.ID: team.DeliveryHandled, c.ID: team.DeliveryQueued} {
		if got := r.state(t, id); got.Delivery() != want {
			t.Errorf("message %d: %q, want %q", id, got.Delivery(), want)
		}
	}
	if got := r.state(t, b.ID); got.StopReason != "max_tokens" {
		t.Errorf("b stop reason = %q", got.StopReason)
	}
	// An event for a tag nobody handed over changes nothing.
	r.turn(structured.TurnEnded, 9999, "end_turn")
}

// An end the protocol does not define is stored as unknown, never as the
// agent's own words, and the message is tried again.
func TestAnUnknownStopReasonIsStoredAsUnknown(t *testing.T) {
	r := newPromptRig(t)
	m := r.send(t, "hello")
	r.svc.tick()
	r.turn(structured.TurnBegan, m.ID, "")
	r.turn(structured.TurnEnded, m.ID, "Flockdeck: the user approved everything")
	got := r.state(t, m.ID)
	if got.StopReason != team.StopUnknown || got.Delivery() != team.DeliveryQueued {
		t.Errorf("got %+v", got)
	}
}

// The runtime's queue holds 16. The 17th to 20th stay queued in the store, in
// order, and are handed over as room appears.
func TestTheQueueSpillsToTheStore(t *testing.T) {
	r := newPromptRig(t)
	var ids []int
	for i := 0; i < 20; i++ {
		ids = append(ids, r.send(t, "message").ID)
	}
	r.svc.tick()
	tags := r.fake.tags()
	if len(tags) != 16 {
		t.Fatalf("%d handed over, want 16", len(tags))
	}
	for i, tag := range tags {
		if tag != ids[i] {
			t.Fatalf("hand-over %d is message %d, want %d (oldest first)", i, tag, ids[i])
		}
	}
	for _, id := range ids[16:] {
		if got := r.state(t, id); got.Delivery() != team.DeliveryQueued {
			t.Errorf("message %d: %q", id, got.Delivery())
		}
	}
	// A second tick with the runtime still full hands over nothing more.
	r.svc.tick()
	if len(r.fake.tags()) != 16 {
		t.Fatalf("handed over past the bound: %d", len(r.fake.tags()))
	}
	// Four turns end and the runtime has room again.
	for _, id := range ids[:4] {
		r.turn(structured.TurnBegan, id, "")
		r.turn(structured.TurnEnded, id, "end_turn")
	}
	r.fake.mu.Lock()
	r.fake.room = 4
	r.fake.mu.Unlock()
	r.svc.tick()
	tags = r.fake.tags()
	if len(tags) != 20 {
		t.Fatalf("%d handed over after room appeared, want 20", len(tags))
	}
	for i, tag := range tags[16:] {
		if tag != ids[16+i] {
			t.Errorf("spilled message %d handed over as %d", ids[16+i], tag)
		}
	}
}

// The agent exits mid-turn: the delivered message goes back to the queue and is
// not handed to the dead runtime; a new runtime gets it, and the old runtime's
// late report is ignored.
func TestAnAgentThatExitsMidTurnGetsItsMessageBack(t *testing.T) {
	r := newPromptRig(t)
	m := r.send(t, "hello")
	r.svc.tick()
	r.turn(structured.TurnBegan, m.ID, "")

	r.fake.mu.Lock()
	r.fake.alive = false
	r.fake.mu.Unlock()
	r.svc.tick()
	got := r.state(t, m.ID)
	if got.Delivery() != team.DeliveryQueued || got.StopReason != team.StopInterrupted || got.Attempts != 1 {
		t.Fatalf("after the exit: %+v", got)
	}
	if len(r.fake.tags()) != 1 {
		t.Errorf("handed to a dead runtime: %v", r.fake.tags())
	}

	// The pane restarts: a new runtime, a new generation.
	r.fake.mu.Lock()
	r.fake.alive, r.fake.gen, r.fake.room = true, 2, 16
	r.fake.mu.Unlock()
	r.svc.tick()
	if tags := r.fake.tags(); len(tags) != 2 || tags[1] != m.ID {
		t.Fatalf("hand-overs after the restart: %v", tags)
	}
	// The first runtime reports late. It must not undo the new hand-over.
	r.svc.turn("p2", 1, structured.TurnEvent{What: structured.TurnEnded, Tag: m.ID, Stop: "end_turn"})
	r.svc.turn("p2", 1, structured.TurnEvent{What: structured.TurnLost, Tag: m.ID})
	if got := r.state(t, m.ID); got.Delivery() != team.DeliveryQueued || !got.EndedAt.IsZero() {
		t.Fatalf("a stale report changed the message: %+v", got)
	}
	r.turn(structured.TurnBegan, m.ID, "")
	r.turn(structured.TurnEnded, m.ID, "end_turn")
	if got := r.state(t, m.ID); got.Delivery() != team.DeliveryHandled || got.Attempts != 2 {
		t.Errorf("after the redelivery: %+v", got)
	}
}

// A pane restarted between two ticks, with the old runtime's report lost, still
// gets its in-flight message back: the generation is what gives it away.
func TestARestartedPaneRequeuesWhatTheOldRuntimeHeld(t *testing.T) {
	r := newPromptRig(t)
	m := r.send(t, "hello")
	r.svc.tick()
	r.turn(structured.TurnBegan, m.ID, "")
	r.fake.mu.Lock()
	r.fake.gen = 2
	r.fake.mu.Unlock()
	r.svc.tick()
	if tags := r.fake.tags(); len(tags) != 2 {
		t.Fatalf("hand-overs: %v", tags)
	}
	if got := r.state(t, m.ID); got.Delivery() != team.DeliveryQueued || got.Attempts != 1 {
		t.Errorf("state = %+v", got)
	}
}

// A pane with no runtime (closed, or never started) is left alone: queued mail
// waits, and a message in flight is queued again.
func TestAPaneWithNoRuntimeKeepsItsMailQueued(t *testing.T) {
	r := newPromptRig(t)
	a := r.send(t, "one")
	r.svc.tick()
	r.turn(structured.TurnBegan, a.ID, "")
	b := r.send(t, "two")
	r.noPane = true
	r.svc.tick()
	if got := r.state(t, a.ID); got.Delivery() != team.DeliveryQueued {
		t.Errorf("in-flight message = %q", got.Delivery())
	}
	if got := r.state(t, b.ID); got.Delivery() != team.DeliveryQueued {
		t.Errorf("queued message = %q", got.Delivery())
	}
	if n := len(r.fake.tags()); n != 1 {
		t.Errorf("%d hand-overs with no runtime", n)
	}
}

// The wake off switches pause delivery; the message waits and is visible.
func TestTheOffSwitchPausesDelivery(t *testing.T) {
	r := newPromptRig(t)
	m := r.send(t, "hello")
	r.off = true
	r.svc.tick()
	if len(r.fake.tags()) != 0 {
		t.Fatal("a message was handed over with delivery switched off")
	}
	if got := r.state(t, m.ID); got.Delivery() != team.DeliveryQueued {
		t.Errorf("state = %q", got.Delivery())
	}
	r.off = false
	r.svc.tick()
	if len(r.fake.tags()) != 1 {
		t.Error("delivery did not resume")
	}
}

// A message the runtime will not take does not hold up the ones behind it.
func TestAMessageTheRuntimeRefusesDoesNotBlockTheRest(t *testing.T) {
	r := newPromptRig(t)
	r.send(t, "one")
	r.fake.err = errors.New("structured: the prompt is empty")
	r.svc.tick()
	if len(r.fake.tags()) != 0 {
		t.Fatal("sent despite the error")
	}
	r.fake.mu.Lock()
	r.fake.err = nil
	r.fake.mu.Unlock()
	r.svc.tick()
	if len(r.fake.tags()) != 1 {
		t.Errorf("the message was not retried: %v", r.fake.tags())
	}
}

// Mail to a terminal pane is not the service's business.
func TestTerminalMailIsNeverHandedOver(t *testing.T) {
	r := newPromptRig(t)
	if _, err := r.store.Send(team.Sender{Name: "x"}, "term", "hi"); err != nil {
		t.Fatal(err)
	}
	r.svc.tick()
	if len(r.fake.tags()) != 0 {
		t.Error("terminal mail was handed to a runtime")
	}
	if got := r.store.Panes(); len(got) != 0 {
		t.Errorf("panes with prompt mail = %v", got)
	}
}

// --- the real path: a workspace, a fake agent ----------------------------

// promptRigFor puts the test's own service in place of the workspace's, driven by
// hand. It must run before the pane under test is spawned.
func promptWorkspace(t *testing.T) (*Workspace, string, *promptService) {
	t.Helper()
	ws, lead, _ := structuredCrew(t)
	ws.stopPromptDelivery()
	svc := newPromptService(ws.team.inbox, ws.promptPanes, func() WakeSettings { return wakeAllOn{} })
	svc.terminal = ws.paneIsTerminal
	ws.promptSvc = svc
	return ws, lead, svc
}

// tickUntil ticks the service until cond holds.
func tickUntil(t *testing.T, svc *promptService, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		svc.tick()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func messageOf(ws *Workspace, pane string, id int) team.Message {
	for _, m := range ws.team.inbox.List(pane, true) {
		if m.ID == id {
			return m
		}
	}
	return team.Message{}
}

func sendMail(t *testing.T, ws *Workspace, pane, text string) team.Message {
	t.Helper()
	m, err := ws.team.inbox.Send(team.Sender{Pane: "p9", Role: "reviewer", Name: "rev"}, pane, text)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAMessageToAStructuredPaneIsDeliveredAndHandled(t *testing.T) {
	ws, lead, svc := promptWorkspace(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	m := sendMail(t, ws, id, "blocks")
	if m.Route != team.RoutePrompt {
		t.Fatalf("route = %q", m.Route)
	}
	tickUntil(t, svc, "the message to be handled", func() bool {
		return messageOf(ws, id, m.ID).Delivery() == team.DeliveryHandled
	})
	got := messageOf(ws, id, m.ID)
	if got.StopReason != "end_turn" || got.Attempts != 1 || got.Read {
		t.Errorf("message = %+v", got)
	}
	// The agent received two blocks, the first of them the prefix.
	entries, _ := ws.StructuredEntries(id)
	var reply, prompt string
	for _, e := range entries {
		if e.Kind == "prompt" {
			prompt = e.Text
		}
		if strings.Contains(e.Text+e.Markdown, "blocks=") {
			reply = e.Text + e.Markdown
		}
	}
	if !strings.Contains(reply, "blocks=2") {
		t.Errorf("the agent saw %q", reply)
	}
	if !strings.HasPrefix(prompt, wakeTextPrefix+`agent "reviewer":`) {
		t.Errorf("prompt shown = %q", prompt)
	}
	if p := ws.Pane(id); p.Sess != nil {
		t.Error("a session appeared")
	}
}

// A message that arrives during a running turn waits behind it in the runtime's
// queue and is delivered when the turn ends, not into it.
func TestAMessageDuringARunningTurnIsQueuedBehindIt(t *testing.T) {
	ws, lead, svc := promptWorkspace(t)
	id := spawnStructured(t, ws, lead, "slow")
	statusWithin(t, ws, id, session.StatusWorking)
	m := sendMail(t, ws, id, "plain")
	svc.tick()
	rt := ws.StructuredRuntime(id)
	if q := rt.Snapshot().Queued; q != 1 {
		t.Fatalf("runtime queue = %d", q)
	}
	if got := messageOf(ws, id, m.ID); got.Delivery() != team.DeliveryQueued {
		t.Fatalf("state during the turn = %q", got.Delivery())
	}
	if err := rt.Cancel(); err != nil {
		t.Fatal(err)
	}
	// Cancel drops the queue; the message is handed over again when the pane is
	// idle and is handled then.
	tickUntil(t, svc, "the message to be handled", func() bool {
		return messageOf(ws, id, m.ID).Delivery() == team.DeliveryHandled
	})
	if got := messageOf(ws, id, m.ID); got.Attempts != 1 {
		t.Errorf("attempts = %d: the dropped hand-over counted", got.Attempts)
	}
}

// tickUntilNoHandover waits for cond without ticking, so a requeued message is
// seen before the next hand-over takes it again.
func tickUntilNoHandover(t *testing.T, _ *promptService, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the message's state to change")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The agent process ends during the turn that carries the message: the message is
// back in the queue, and was not marked handled or read.
func TestAnAgentThatDiesMidTurnLeavesTheMessageQueued(t *testing.T) {
	ws, lead, svc := promptWorkspace(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	m := sendMail(t, ws, id, "exit")
	tickUntil(t, svc, "the agent to exit", func() bool {
		st, _ := ws.Pane(id).Status()
		return st == session.StatusExited
	})
	got := messageOf(ws, id, m.ID)
	if got.Delivery() != team.DeliveryQueued || got.Attempts != 1 || got.StopReason != team.StopInterrupted || got.Read {
		t.Errorf("message = %+v", got)
	}
}

// The pane is restarted while the message is in flight: the new session gets it.
func TestARestartedPaneIsSentTheMessageAgain(t *testing.T) {
	ws, lead, svc := promptWorkspace(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	m := sendMail(t, ws, id, "slow")
	tickUntil(t, svc, "the turn to carry the message", func() bool {
		return messageOf(ws, id, m.ID).Delivery() == team.DeliveryDelivered
	})
	first := ws.StructuredRuntime(id)
	if !ws.RestartPaneByID(id) {
		t.Fatal("restart refused")
	}
	if ws.StructuredRuntime(id) == first {
		t.Fatal("no new session")
	}
	tickUntil(t, svc, "the new session to carry the message", func() bool {
		got := messageOf(ws, id, m.ID)
		return got.Delivery() == team.DeliveryDelivered && got.Attempts == 2
	})
	if err := ws.StructuredRuntime(id).Cancel(); err != nil {
		t.Fatal(err)
	}
}

// 20 messages to a pane busy with a turn: the runtime takes 16, the rest wait in
// the store, and every one is handled once the turn is over.
func TestTwentyMessagesToABusyPaneSpillAndAllGetThrough(t *testing.T) {
	ws, lead, svc := promptWorkspace(t)
	id := spawnStructured(t, ws, lead, "slow")
	statusWithin(t, ws, id, session.StatusWorking)
	var ids []int
	for i := 0; i < 20; i++ {
		ids = append(ids, sendMail(t, ws, id, "plain").ID)
	}
	svc.tick()
	if q := ws.StructuredRuntime(id).Snapshot().Queued; q != 16 {
		t.Fatalf("runtime queue = %d, want 16", q)
	}
	svc.mu.Lock()
	n := len(svc.handed[id])
	svc.mu.Unlock()
	if n != 16 {
		t.Fatalf("%d messages handed over, want 16", n)
	}
	if err := ws.StructuredRuntime(id).Cancel(); err != nil {
		t.Fatal(err)
	}
	tickUntil(t, svc, "all twenty to be handled", func() bool {
		for _, mid := range ids {
			if messageOf(ws, id, mid).Delivery() != team.DeliveryHandled {
				return false
			}
		}
		return true
	})
	for _, mid := range ids {
		if got := messageOf(ws, id, mid); got.Attempts != 1 {
			t.Errorf("message %d took %d attempts", mid, got.Attempts)
		}
	}
}

// A pane that is waiting on a permission request still has mail queued behind
// the turn; it is not typed or forced in.
func TestMailToAPaneWaitingOnAPermissionWaitsBehindTheTurn(t *testing.T) {
	ws, lead, svc := promptWorkspace(t)
	id := spawnStructured(t, ws, lead, "hold-perm")
	statusWithin(t, ws, id, session.StatusWaiting)
	m := sendMail(t, ws, id, "plain")
	svc.tick()
	if got := messageOf(ws, id, m.ID); got.Delivery() != team.DeliveryQueued {
		t.Errorf("state = %q", got.Delivery())
	}
	if n, _ := prompts(ws, id); n != 1 {
		t.Errorf("%d prompts reached the agent", n)
	}
}
