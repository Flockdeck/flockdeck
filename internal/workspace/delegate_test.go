package workspace

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

// runTimers runs the timers the workspace armed, as the clock would once the
// time they were set for had passed.
func (f *fakeDelivery) runTimers() {
	ts := f.timers
	f.timers = nil
	for _, fn := range ts {
		fn()
	}
}

// A manager that starts helpers and ends its turn has to be woken when one of
// them finishes. A helper finishes by going idle at its prompt, not by exiting,
// so going idle after a turn is what tells the parent.
func TestAHelperThatFinishesATurnTellsItsParentOnce(t *testing.T) {
	c := newCrew(t)
	f := installFake(c)

	f.state.Status = "working"
	c.ws.paneChanged(c.w1)
	f.state.Status = "idle"
	c.ws.paneChanged(c.w1)
	if got := c.inbox(t, c.lead, true); len(got) != 0 {
		t.Fatalf("the parent was told before the helper had stayed idle: %+v", got)
	}
	f.runTimers()

	msgs := c.inbox(t, c.lead, false)
	if len(msgs) != 1 || !msgs[0].System() || msgs[0].To != c.lead {
		t.Fatalf("the manager was told %+v, want one message from flockdeck", msgs)
	}
	for _, want := range []string{"worker", "finished its turn", "idle", c.w1, "flockdeck inbox"} {
		if !strings.Contains(msgs[0].Text, want) {
			t.Errorf("text %q lacks %q", msgs[0].Text, want)
		}
	}
	// The notice that wakes an idle manager is the usual one-line one.
	var typed bool
	for _, line := range f.typed.all() {
		typed = typed || strings.HasPrefix(line, c.lead+": [flockdeck] 1 new message")
	}
	if !typed {
		t.Errorf("the idle manager was not sent a notice: %q", f.typed.all())
	}

	// Going idle again with no work in between says nothing more.
	for i := 0; i < 3; i++ {
		c.ws.paneChanged(c.w1)
		f.runTimers()
	}
	if got := c.inbox(t, c.lead, true); len(got) != 1 {
		t.Errorf("one turn was reported %d times", len(got))
	}

	// A second turn is a second report.
	f.state.Status = "working"
	c.ws.paneChanged(c.w1)
	f.state.Status = "idle"
	c.ws.paneChanged(c.w1)
	f.runTimers()
	if got := c.inbox(t, c.lead, true); len(got) != 2 {
		t.Errorf("after a second turn the manager has %d messages, want 2", len(got))
	}
}

func TestAHelperThatIsBusyAgainBeforeTheDwellIsNotReported(t *testing.T) {
	c := newCrew(t)
	f := installFake(c)
	f.state.Status = "working"
	c.ws.paneChanged(c.w1)
	f.state.Status = "idle"
	c.ws.paneChanged(c.w1)
	f.state.Status = "working"
	c.ws.paneChanged(c.w1)
	f.runTimers()
	if got := c.inbox(t, c.lead, true); len(got) != 0 {
		t.Errorf("a helper between two tool calls was reported: %+v", got)
	}
	// Finishing for real afterwards still reports.
	f.state.Status = "idle"
	c.ws.paneChanged(c.w1)
	f.runTimers()
	if got := c.inbox(t, c.lead, true); len(got) != 1 {
		t.Errorf("the finished helper was reported %d times, want 1", len(got))
	}
}

func TestOnlyAHelperWithAParentWhoseAgentReportsItsStatusIsReported(t *testing.T) {
	c := newCrew(t)
	f := installFake(c)

	// The user's own pane has no parent to tell.
	c.ws.Pane(c.w2).Parent = ""
	f.state.Status = "working"
	c.ws.paneChanged(c.w2)
	f.state.Status = "idle"
	c.ws.paneChanged(c.w2)
	f.runTimers()

	// An agent that does not report its status may be showing a dialog.
	f.state.Eligible = false
	f.state.Status = "working"
	c.ws.paneChanged(c.w1)
	f.state.Status = "idle"
	c.ws.paneChanged(c.w1)
	f.runTimers()

	if got := c.inbox(t, c.lead, true); len(got) != 0 {
		t.Errorf("the manager was told %+v", got)
	}
}

// A pane that started a plan of helpers on its own cannot start more than
// MaxHelpers at once, and closing one makes room.
func TestAnAgentMayHaveOnlyMaxHelpersOpen(t *testing.T) {
	c := newCrew(t)
	spawn := func() (string, error) {
		return c.ws.Spawn(c.lead, SpawnOptions{Task: "more", Role: "worker", Agent: "sleeper", Kind: session.KindClaude, SpawnedByAgent: true})
	}
	// The crew already holds three.
	var last string
	for i := 3; i < MaxHelpers; i++ {
		id, err := spawn()
		if err != nil {
			t.Fatalf("helper %d: %v", i+1, err)
		}
		last = id
	}
	_, err := spawn()
	if err == nil || !strings.Contains(err.Error(), "already has 8 helpers open") || !strings.Contains(err.Error(), "flockdeck close") {
		t.Fatalf("the ninth helper = %v, want a refusal that says how to make room", err)
	}
	// A pane started by hand, with no parent, is not counted against the manager.
	if _, err := c.ws.Spawn(c.lead, SpawnOptions{Task: "by the user", Role: "worker", Agent: "sleeper", Kind: session.KindClaude}); err != nil {
		t.Errorf("a start from the window was refused: %v", err)
	}
	c.ws.ClosePaneByID(last)
	if _, err := spawn(); err != nil {
		t.Errorf("after a close the next start = %v", err)
	}
}
