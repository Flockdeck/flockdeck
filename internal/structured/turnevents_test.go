package structured

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/bridge"
)

// turnLog collects the events OnTurn is told.
type turnLog struct {
	mu     sync.Mutex
	events []TurnEvent
}

func (l *turnLog) add(e TurnEvent) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

func (l *turnLog) all() []TurnEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

// waitFor polls until the log holds an event of the kind for the tag.
func (l *turnLog) waitFor(t *testing.T, kind TurnEventKind, tag int) TurnEvent {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		for _, e := range l.all() {
			if e.What == kind && e.Tag == tag {
				return e
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s event for tag %d; log %+v", kind, tag, l.all())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func taggedOptions(t *testing.T, l *turnLog) Options {
	o := fakeOptions(t)
	o.OnTurn = l.add
	return o
}

// A tagged prompt is reported as began and then ended with the agent's stop
// reason, in that order, and an untagged one is not reported at all.
func TestATaggedPromptReportsItsTurn(t *testing.T) {
	var l turnLog
	r := begin(t, taggedOptions(t, &l))
	if err := r.Send("plain"); err != nil {
		t.Fatal(err)
	}
	if err := r.SendTagged(7, "Flockdeck message from agent \"w\":", "plain"); err != nil {
		t.Fatal(err)
	}
	end := l.waitFor(t, TurnEnded, 7)
	if end.Stop != "end_turn" {
		t.Errorf("stop = %q", end.Stop)
	}
	got := l.all()
	if len(got) != 2 || got[0] != (TurnEvent{What: TurnBegan, Tag: 7}) || got[1].What != TurnEnded {
		t.Errorf("events = %+v", got)
	}
	// The agent was given the prompt in two blocks, and the conversation shows
	// them as one prompt.
	s := until(t, r, "both turns", func(s Snapshot) bool { return idle(s) && len(prompts(s)) == 2 })
	if p := prompts(s)[1]; p != "Flockdeck message from agent \"w\":\nplain" {
		t.Errorf("prompt shown = %q", p)
	}
}

// The stop reason is the agent's, not a guess: a refusal and a cancel come back
// as they were sent.
func TestATaggedPromptReportsARefusal(t *testing.T) {
	var l turnLog
	r := begin(t, taggedOptions(t, &l))
	if err := r.SendTagged(3, "refuse"); err != nil {
		t.Fatal(err)
	}
	if e := l.waitFor(t, TurnEnded, 3); e.Stop != "refusal" {
		t.Errorf("stop = %q", e.Stop)
	}
}

// Cancel ends the running turn with a stop reason and drops the tagged prompts
// behind it, each reported as lost: nothing is left reported as pending.
func TestCancelReportsTheDroppedTaggedPrompts(t *testing.T) {
	var l turnLog
	r := begin(t, taggedOptions(t, &l))
	if err := r.SendTagged(1, "slow"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the turn to run", func(s Snapshot) bool {
		return s.Status == bridge.StatusWorking && strings.Contains(replies(s), "working...")
	})
	if err := r.SendTagged(2, "plain"); err != nil {
		t.Fatal(err)
	}
	if err := r.SendTagged(3, "plain"); err != nil {
		t.Fatal(err)
	}
	if err := r.Cancel(); err != nil {
		t.Fatal(err)
	}
	if e := l.waitFor(t, TurnEnded, 1); e.Stop != "cancelled" {
		t.Errorf("stop = %q", e.Stop)
	}
	l.waitFor(t, TurnLost, 2)
	l.waitFor(t, TurnLost, 3)
	for _, e := range l.all() {
		if (e.Tag == 2 || e.Tag == 3) && e.What == TurnBegan {
			t.Errorf("a dropped prompt began: %+v", e)
		}
	}
}

// An agent that exits during the turn leaves the prompt lost, not ended.
func TestAnAgentExitingMidTurnLosesTheTaggedPrompt(t *testing.T) {
	var l turnLog
	r := begin(t, taggedOptions(t, &l))
	if err := r.SendTagged(5, "exit"); err != nil {
		t.Fatal(err)
	}
	l.waitFor(t, TurnLost, 5)
	for _, e := range l.all() {
		if e.Tag == 5 && e.What == TurnEnded {
			t.Errorf("a turn that never ended was reported ended: %+v", e)
		}
	}
}

// Closing the runtime reports every tagged prompt still waiting as lost.
func TestCloseLosesTheWaitingTaggedPrompts(t *testing.T) {
	var l turnLog
	r := begin(t, taggedOptions(t, &l))
	if err := r.SendTagged(1, "slow"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the turn to run", func(s Snapshot) bool { return strings.Contains(replies(s), "working...") })
	if err := r.SendTagged(2, "plain"); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	l.waitFor(t, TurnLost, 1)
	l.waitFor(t, TurnLost, 2)
}

func TestATagMustBePositiveAndABlockSetNotEmpty(t *testing.T) {
	r := begin(t, fakeOptions(t))
	if err := r.SendTagged(0, "plain"); err == nil {
		t.Error("tag 0 was accepted")
	}
	if err := r.SendTagged(-1, "plain"); err == nil {
		t.Error("a negative tag was accepted")
	}
	if err := r.SendTagged(1); err == nil {
		t.Error("no blocks were accepted")
	}
	if err := r.SendTagged(1, " ", "\n"); err == nil {
		t.Error("blank blocks were accepted")
	}
}

// The queue bound counts tagged prompts with the rest.
func TestTaggedPromptsFillTheQueueAndStopAtItsBound(t *testing.T) {
	var l turnLog
	r := begin(t, taggedOptions(t, &l))
	if err := r.SendTagged(1000, "slow"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the turn to run", func(s Snapshot) bool { return strings.Contains(replies(s), "working...") })
	for i := 1; i <= maxQueue; i++ {
		if err := r.SendTagged(i, "plain"); err != nil {
			t.Fatalf("prompt %d: %v", i, err)
		}
	}
	if err := r.SendTagged(99, "plain"); !errors.Is(err, ErrQueueFull) {
		t.Errorf("past the bound: %v", err)
	}
}

// A turn ended by Cancel is reported as asked; one that ends by itself is not.
func TestAskedMarksOnlyATurnCancelWasCalledDuring(t *testing.T) {
	var l turnLog
	r := begin(t, taggedOptions(t, &l))
	if err := r.SendTagged(1, "plain"); err != nil {
		t.Fatal(err)
	}
	if e := l.waitFor(t, TurnEnded, 1); e.Asked {
		t.Errorf("a turn nobody cancelled is asked: %+v", e)
	}
	if err := r.SendTagged(2, "slow"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the turn to run", func(s Snapshot) bool { return strings.Contains(replies(s), "working...") })
	if err := r.Cancel(); err != nil {
		t.Fatal(err)
	}
	e := l.waitFor(t, TurnEnded, 2)
	if !e.Asked || e.Stop != "cancelled" {
		t.Errorf("event = %+v", e)
	}
}
