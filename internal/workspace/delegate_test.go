package workspace

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/layout"
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

// tabOf returns the tab holding a pane.
func tabOf(w *Workspace, pane string) *Tab {
	for _, t := range w.Tabs {
		if t.Tree.Find(pane) != nil {
			return t
		}
	}
	return nil
}

// drawTab renders a tab's layout as text, one letter per pane, in a 40 by 8 box.
func drawTab(t *Tab, letters map[string]byte) string {
	t.Tree.Compute(layout.Rect{X: 0, Y: 0, W: 40, H: 8})
	cells := make([][]byte, 8)
	for y := range cells {
		cells[y] = []byte(strings.Repeat(".", 40))
	}
	for _, leaf := range t.Tree.Leaves() {
		r := leaf.Rect()
		for y := r.Y; y < r.Y+r.H && y < 8; y++ {
			for x := r.X; x < r.X+r.W && x < 40; x++ {
				cells[y][x] = letters[leaf.Pane]
			}
		}
	}
	var b strings.Builder
	for _, row := range cells {
		b.Write(row)
		b.WriteByte('\n')
	}
	return b.String()
}

// A manager keeps the panes for one piece of work in one tab by naming it, and
// starts a different piece of work in a tab of its own.
func TestSpawnWithATabNameGathersOnePieceOfWorkInOneTab(t *testing.T) {
	c := newCrew(t)
	start := func(role, tab string) string {
		id, err := c.ws.Spawn(c.lead, SpawnOptions{Task: "t", Role: role, Agent: "sleeper", Kind: session.KindClaude, SpawnedByAgent: true, TabName: tab})
		if err != nil {
			t.Fatalf("spawn %s in %q: %v", role, tab, err)
		}
		return id
	}
	before := len(c.ws.Tabs)

	plan := start("planner", "Login rate limit")
	work := start("worker", "login rate limit") // the title is matched without regard to case
	rev := start("reviewer", "Login rate limit")
	other := start("worker", "Billing export")
	if got := len(c.ws.Tabs); got != before+2 {
		t.Fatalf("%d tabs were made for two pieces of work, want 2", got-before)
	}

	feature := tabOf(c.ws, plan)
	if feature == nil || tabOf(c.ws, work) != feature || tabOf(c.ws, rev) != feature {
		t.Fatal("the planner, worker and reviewer for one feature are not in one tab")
	}
	if feature.Title != "Login rate limit" {
		t.Errorf("the tab is titled %q, want the name it was first given", feature.Title)
	}
	if !feature.Delegated || feature.AutoTitle {
		t.Errorf("the tab is %+v, want delegated and not retitled by a prompt", feature)
	}
	if got := len(feature.Tree.Panes()); got != 3 {
		t.Errorf("the feature tab holds %d panes, want 3", got)
	}
	bill := tabOf(c.ws, other)
	if bill == nil || bill == feature || bill.Title != "Billing export" || len(bill.Tree.Panes()) != 1 {
		t.Errorf("the second feature's tab = %+v", bill)
	}
	if tabOf(c.ws, c.lead) == feature || tabOf(c.ws, c.lead) == bill {
		t.Error("the manager's own pane was moved into a feature tab")
	}

	// Closing a task's helpers takes its tab with them.
	defer func() {
		for _, id := range []string{plan, work, rev} {
			c.ws.ClosePaneByID(id)
		}
		if tabOf(c.ws, other) == nil || c.ws.Tab(feature.ID) != nil {
			t.Error("closing the last pane of the feature did not remove its tab, or removed another")
		}
	}()

	// The layout is a grid with no pane hidden or overlapped.
	letters := map[string]byte{plan: 'P', work: 'W', rev: 'R'}
	drawn := drawTab(feature, letters)
	t.Logf("tab %q:\n%s", feature.Title, drawn)
	for _, l := range "PWR" {
		if !strings.ContainsRune(drawn, l) {
			t.Errorf("pane %c is not on screen in:\n%s", l, drawn)
		}
	}
	// The dots are the one-cell rules between panes: whole columns, never a gap in a pane.
	rows := strings.Split(strings.TrimSpace(drawn), "\n")
	for x := range rows[0] {
		dots := 0
		for _, row := range rows {
			if row[x] == '.' {
				dots++
			}
		}
		if dots != 0 && dots != len(rows) {
			t.Fatalf("column %d is partly empty:\n%s", x, drawn)
		}
	}
}

// A tab name is looked for in the project the caller is in only.
func TestSpawnWithATabNameDoesNotJoinAnotherProjectsTab(t *testing.T) {
	c := newCrew(t)
	first, err := c.ws.Spawn(c.lead, SpawnOptions{Task: "t", Role: "worker", Agent: "sleeper", Kind: session.KindClaude, SpawnedByAgent: true, TabName: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	tabOf(c.ws, first).Root = t.TempDir() // as though the tab belonged to another project
	second, err := c.ws.Spawn(c.lead, SpawnOptions{Task: "t", Role: "worker", Agent: "sleeper", Kind: session.KindClaude, SpawnedByAgent: true, TabName: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if tabOf(c.ws, first) == tabOf(c.ws, second) {
		t.Error("a helper joined a tab of another project")
	}
}
