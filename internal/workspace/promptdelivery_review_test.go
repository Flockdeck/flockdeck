package workspace

import (
	"errors"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/team"
)

// A runtime that keeps refusing a message ends it undeliverable after the cap,
// and is not asked again.
func TestASendTheRuntimeKeepsRefusingEndsUndeliverable(t *testing.T) {
	r := newPromptRig(t)
	m := r.send(t, "hello")
	other := r.send(t, "behind it")
	r.fake.err = errors.New("structured: the prompt is empty")
	for i := 0; i < team.MaxAttempts+3; i++ {
		r.svc.tick()
	}
	got := r.state(t, m.ID)
	if got.Delivery() != team.DeliveryUndeliverable || got.Attempts != team.MaxAttempts || got.StopReason != team.StopInterrupted {
		t.Fatalf("message = %+v", got)
	}
	if got := r.state(t, other.ID); got.Delivery() != team.DeliveryUndeliverable {
		t.Errorf("the message behind it: %q", got.Delivery())
	}
	// A refusal because the queue is full is not the message's fault.
	r2 := newPromptRig(t)
	m2 := r2.send(t, "hello")
	r2.fake.room = 0
	for i := 0; i < 5; i++ {
		r2.svc.tick()
	}
	if got := r2.state(t, m2.ID); got.Delivery() != team.DeliveryQueued || got.Attempts != 0 {
		t.Errorf("a full queue counted against the message: %+v", got)
	}
}

// A turn ended by a cancel the person asked for is handled, not sent again; the
// same stop reason with nobody asking is retried (see
// TestACancelledOrRefusedTurnIsRetriedUpToTheCap).
func TestAnAskedCancelHandlesTheMessageInTheRig(t *testing.T) {
	r := newPromptRig(t)
	m := r.send(t, "hello")
	r.svc.tick()
	r.turn(structured.TurnBegan, m.ID, "")
	r.svc.turn("p2", r.fake.gen, structured.TurnEvent{What: structured.TurnEnded, Tag: m.ID, Stop: "cancelled", Asked: true})
	got := r.state(t, m.ID)
	if got.Delivery() != team.DeliveryHandled || got.StopReason != "cancelled" {
		t.Errorf("message = %+v", got)
	}
}

// The real thing: the person cancels the turn that carries the message.
func TestThePersonCancellingTheTurnHandlesTheMessage(t *testing.T) {
	ws, lead, svc := promptWorkspace(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	m := sendMail(t, ws, id, "slow")
	tickUntil(t, svc, "the turn to carry the message", func() bool {
		return messageOf(ws, id, m.ID).Delivery() == team.DeliveryDelivered
	})
	if err := ws.StructuredRuntime(id).Cancel(); err != nil {
		t.Fatal(err)
	}
	tickUntil(t, svc, "the message to be handled", func() bool {
		return messageOf(ws, id, m.ID).Delivery() == team.DeliveryHandled
	})
	got := messageOf(ws, id, m.ID)
	if got.StopReason != "cancelled" || got.Attempts != 1 {
		t.Errorf("message = %+v", got)
	}
	statusWithin(t, ws, id, session.StatusIdle)
	svc.tick()
	if n, _ := prompts(ws, id); n != 2 {
		t.Errorf("%d prompts reached the agent, want the task and the message only", n)
	}
}

// Prompt-route mail to a pane that is not structured moves to the terminal
// route, where it counts as unread and the inbox can be read.
func TestPromptMailToATerminalPaneMovesToTheTerminalRoute(t *testing.T) {
	ws, lead, svc := promptWorkspace(t)
	m, err := ws.team.inbox.SendRoute(team.Sender{Name: "x"}, lead, "hello", team.RoutePrompt)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.team.inbox.List(lead, false)) != 0 || ws.Unread(lead) != 0 {
		t.Fatal("prompt mail counted as unread")
	}
	if !ws.paneIsTerminal(lead) {
		t.Fatal("the manager's terminal pane is not seen as terminal")
	}
	svc.tick()
	got := messageOf(ws, lead, m.ID)
	if got.Route != "" || got.Delivery() != team.DeliveryQueued || ws.Unread(lead) != 1 {
		t.Errorf("after the tick: %+v, unread %d", got, ws.Unread(lead))
	}
	if ws.team.inbox.NewestUnread(lead) != m.ID {
		t.Error("the rerouted message is not the newest unread")
	}
}

// A structured pane that is between runtimes, or whose agent failed to start,
// keeps its mail on the prompt route.
func TestAStructuredPaneWithoutARuntimeKeepsItsPromptMail(t *testing.T) {
	r := newPromptRig(t)
	m := r.send(t, "hello")
	r.noPane = true
	r.svc.terminal = func(string) bool { return false }
	r.svc.tick()
	if got := r.state(t, m.ID); got.Route != team.RoutePrompt {
		t.Errorf("rerouted: %+v", got)
	}
	r.svc.terminal = func(string) bool { return true }
	r.svc.tick()
	if got := r.state(t, m.ID); got.Route != "" {
		t.Errorf("not rerouted: %+v", got)
	}
}

// Mail sent to a terminal pane takes the terminal route from the start.
func TestMailToATerminalPaneIsNotOnThePromptRoute(t *testing.T) {
	ws, lead, _ := promptWorkspace(t)
	m := sendMail(t, ws, lead, "hello")
	if m.Route != "" || ws.Unread(lead) != 1 {
		t.Errorf("route %q, unread %d", m.Route, ws.Unread(lead))
	}
}
