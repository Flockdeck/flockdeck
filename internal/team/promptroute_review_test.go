package team

import "testing"

// Read returns a prompt message as it is: no mark, no event, no change.
func TestReadLeavesAPromptMessageAlone(t *testing.T) {
	s, _ := promptStore()
	var events []Event
	s.OnEvent(func(e Event) { events = append(events, e) })
	m := sendTo(t, s, "p2", "hi")
	s.Deliver("p2", m.ID)
	before := len(events)
	rev := s.Rev()
	got, err := s.Read("p2", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != m.ID || got.Read || !got.ReadAt.IsZero() || len(events) != before || s.Rev() != rev {
		t.Errorf("Read changed a prompt message: %+v, %d new events", got, len(events)-before)
	}
	if again := stateOf(t, s, "p2", m.ID); again.Read || !again.ReadAt.IsZero() {
		t.Errorf("stored message was stamped: %+v", again)
	}
}

// A prompt message is in no unread count and no hook or wake picks it, in any
// delivery state; a terminal message is unread until read.
func TestAPromptMessageIsNeverUnread(t *testing.T) {
	s, _ := promptStore()
	m := sendTo(t, s, "p2", "hi")
	if len(s.List("p2", false)) != 0 || s.NewestUnread("p2") != 0 || m.Unread() {
		t.Fatal("a queued prompt message is unread")
	}
	s.Deliver("p2", m.ID)
	if len(s.List("p2", false)) != 0 || s.NewestUnread("p2") != 0 {
		t.Error("a delivered prompt message is unread")
	}
	tm := sendTo(t, s, "term", "hi")
	if !tm.Unread() || s.NewestUnread("term") != tm.ID {
		t.Error("a terminal message is not unread")
	}
}

// A cancel the person asked for is handled, with the reason kept; one nobody
// asked for requeues. Other stop reasons are judged the same either way.
func TestACancelThePersonAskedForIsHandled(t *testing.T) {
	s, _ := promptStore()
	m := sendTo(t, s, "p2", "hi")
	s.Deliver("p2", m.ID)
	got, ok := s.EndTurnAsked("p2", m.ID, "cancelled")
	if !ok || got.Delivery() != DeliveryHandled || got.StopReason != StopCancelled {
		t.Fatalf("asked cancel: %+v, %v", got, ok)
	}
	r := sendTo(t, s, "p2", "again")
	s.Deliver("p2", r.ID)
	if got, _ := s.EndTurnAsked("p2", r.ID, "refusal"); got.Delivery() != DeliveryQueued || got.StopReason != StopRefusal {
		t.Errorf("an asked turn that was refused: %+v", got)
	}
	u := sendTo(t, s, "p2", "unprompted")
	s.Deliver("p2", u.ID)
	if got, _ := s.EndTurn("p2", u.ID, "cancelled"); got.Delivery() != DeliveryQueued {
		t.Errorf("an unprompted cancel: %+v", got)
	}
}

// A send that fails before any turn counts as an attempt and ends undeliverable.
func TestFailedSendsEndUndeliverable(t *testing.T) {
	s, _ := promptStore()
	m := sendTo(t, s, "p2", "hi")
	for i := 1; i <= MaxAttempts; i++ {
		got, ok := s.Fail("p2", m.ID)
		if !ok || got.Attempts != i || got.StopReason != StopInterrupted {
			t.Fatalf("failure %d: %+v, %v", i, got, ok)
		}
	}
	if got := stateOf(t, s, "p2", m.ID); got.Delivery() != DeliveryUndeliverable {
		t.Errorf("state = %q", got.Delivery())
	}
	if _, ok := s.Fail("p2", m.ID); ok {
		t.Error("an undeliverable message failed again")
	}
}

// Reroute turns the unfinished prompt mail of a pane into terminal mail and
// leaves finished mail alone.
func TestRerouteMovesUnfinishedPromptMailToTheTerminalRoute(t *testing.T) {
	s, _ := promptStore()
	queued := sendTo(t, s, "p2", "queued")
	flying := sendTo(t, s, "p2", "flying")
	done := sendTo(t, s, "p2", "done")
	s.Deliver("p2", flying.ID)
	s.Deliver("p2", done.ID)
	s.EndTurn("p2", done.ID, "end_turn")
	if n := s.Reroute("p2"); n != 2 {
		t.Fatalf("rerouted %d, want 2", n)
	}
	for _, id := range []int{queued.ID, flying.ID} {
		got := stateOf(t, s, "p2", id)
		if got.Route != "" || !got.Unread() || got.Delivery() != DeliveryQueued || got.Attempts != 0 {
			t.Errorf("message %d: %+v", id, got)
		}
	}
	if got := stateOf(t, s, "p2", done.ID); got.Route != RoutePrompt || got.Delivery() != DeliveryHandled {
		t.Errorf("handled message: %+v", got)
	}
	if len(s.List("p2", false)) != 2 || s.NewestUnread("p2") != flying.ID {
		t.Error("the rerouted mail is not unread terminal mail")
	}
	if len(s.Pending("p2")) != 0 || len(s.Panes()) != 0 {
		t.Error("the rerouted mail is still pending as prompts")
	}
	if n := s.Reroute("p2"); n != 0 {
		t.Errorf("second reroute moved %d", n)
	}
}
