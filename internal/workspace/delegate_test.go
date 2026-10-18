package workspace

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
)

// idleFake stands in for the status of the helper panes and for the clock that
// times the dwell, so a helper's turns can be reported without a terminal.
type idleFake struct {
	r      idleReading
	timers []func()
}

func installFake(c crew) *idleFake {
	f := &idleFake{r: idleReading{Eligible: true, Status: "working", IdleFor: time.Minute}}
	c.ws.team.idle = func(*Pane) idleReading { return f.r }
	c.ws.team.after = func(d time.Duration, fn func()) { f.timers = append(f.timers, fn) }
	return f
}

// runTimers runs the timers the workspace armed, as the clock would once the
// time they were set for had passed.
func (f *idleFake) runTimers() {
	ts := f.timers
	f.timers = nil
	for _, fn := range ts {
		fn()
	}
}

// A manager that starts helpers and ends its turn has to be woken when one of
// them finishes. A helper finishes by going idle at its prompt, not by exiting,
// so going idle after a turn is what tells the parent.
// The report is a message in the parent's inbox and nothing is typed anywhere:
// the team state has no way to write into a pane.
func TestAHelperThatFinishesATurnTellsItsParentOnce(t *testing.T) {
	c := newCrew(t)
	f := installFake(c)

	f.r.Status = "working"
	c.ws.paneChanged(c.w1)
	f.r.Status = "idle"
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
	// Going idle again with no work in between says nothing more.
	for i := 0; i < 3; i++ {
		c.ws.paneChanged(c.w1)
		f.runTimers()
	}
	if got := c.inbox(t, c.lead, true); len(got) != 1 {
		t.Errorf("one turn was reported %d times", len(got))
	}

	// A second turn is a second report.
	f.r.Status = "working"
	c.ws.paneChanged(c.w1)
	f.r.Status = "idle"
	c.ws.paneChanged(c.w1)
	f.runTimers()
	if got := c.inbox(t, c.lead, true); len(got) != 2 {
		t.Errorf("after a second turn the manager has %d messages, want 2", len(got))
	}
}

func TestAHelperThatIsBusyAgainBeforeTheDwellIsNotReported(t *testing.T) {
	c := newCrew(t)
	f := installFake(c)
	f.r.Status = "working"
	c.ws.paneChanged(c.w1)
	f.r.Status = "idle"
	c.ws.paneChanged(c.w1)
	f.r.Status = "working"
	c.ws.paneChanged(c.w1)
	f.runTimers()
	if got := c.inbox(t, c.lead, true); len(got) != 0 {
		t.Errorf("a helper between two tool calls was reported: %+v", got)
	}
	// Finishing for real afterwards still reports.
	f.r.Status = "idle"
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
	f.r.Status = "working"
	c.ws.paneChanged(c.w2)
	f.r.Status = "idle"
	c.ws.paneChanged(c.w2)
	f.runTimers()

	// An agent that does not report its status may be showing a dialog.
	f.r.Eligible = false
	f.r.Status = "working"
	c.ws.paneChanged(c.w1)
	f.r.Status = "idle"
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

// An exited helper whose pane is still open is still a helper against the cap,
// and a helper with no role cannot get round it by starting eight of its own.
func TestHelperCapCountsExitedPanesAndEveryoneBelow(t *testing.T) {
	c := newCrew(t)
	start := func(parent, role, agentID string) (string, error) {
		return c.ws.Spawn(parent, SpawnOptions{Task: "t", Role: role, Agent: agentID, Kind: session.KindClaude, SpawnedByAgent: true})
	}

	// A helper that exits at once is still open as a pane.
	gone, err := start(c.lead, "worker", "quitter")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the helper's process to exit", func() bool { return !c.ws.Pane(gone).Alive() })
	if c.ws.Pane(gone) == nil {
		t.Fatal("the exited pane was closed")
	}
	if n := c.ws.helpersBelow(c.lead); n != 4 {
		t.Fatalf("%d panes below the manager, want 3 running and 1 exited", n)
	}

	// A helper with no role starts four of its own: the manager has eight below it.
	for i := 0; i < 4; i++ {
		if _, err := start(c.w1, "", "sleeper"); err != nil {
			t.Fatalf("grandchild %d: %v", i+1, err)
		}
	}
	err = c.ws.HelperRoom(c.lead)
	if err == nil || !strings.Contains(err.Error(), "already has 8 helpers open") {
		t.Fatalf("the manager with 8 below it = %v, want a refusal", err)
	}
	// The helper itself has only four below it, but its manager is full.
	if _, err := start(c.w1, "", "sleeper"); err == nil || !strings.Contains(err.Error(), "the agent that started this pane already has 8 helpers open") {
		t.Errorf("a helper whose manager is full = %v, want a refusal that names the manager", err)
	}
	// A sibling is refused for the same reason, and the refusal is made before anything is created.
	panes := len(c.ws.panes)
	if _, err := start(c.w2, "", "sleeper"); err == nil {
		t.Error("a second helper got round the cap")
	}
	if len(c.ws.panes) != panes {
		t.Error("a refused start left a pane behind")
	}
	// Closing the exited pane makes room.
	c.ws.ClosePaneByID(gone)
	if err := c.ws.HelperRoom(c.lead); err != nil {
		t.Errorf("after closing one: %v", err)
	}
}

// Auto-review is the user's to turn on for a pane. A helper an agent started on
// its own does not take it from its parent.
func TestAHelperStartedByAnAgentDoesNotInheritAutoReview(t *testing.T) {
	c := newCrew(t)
	c.ws.Pane(c.lead).AutoReview = true
	by, err := c.ws.Spawn(c.lead, SpawnOptions{Task: "t", Role: "worker", Agent: "sleeper", Kind: session.KindClaude, SpawnedByAgent: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.ws.Pane(by).AutoReview {
		t.Error("a helper the manager started inherited auto-review")
	}
	// A pane the user starts from the window, from this pane, still does: that is the user's choice.
	win, err := c.ws.Spawn(c.lead, SpawnOptions{Task: "t", Role: "worker", Agent: "sleeper", Kind: session.KindClaude})
	if err != nil {
		t.Fatal(err)
	}
	if !c.ws.Pane(win).AutoReview {
		t.Error("a fan-out row started from the window lost its parent's auto-review")
	}
}

// A helper that was busy and went idle again inside the dwell is reported after
// the dwell has run from its last idle edge, not the first.
func TestIdleReportWaitsOutTheDwellFromTheLastIdleEdge(t *testing.T) {
	c := newCrew(t)
	f := installFake(c)
	f.r.Status = "working"
	c.ws.paneChanged(c.w1)
	f.r.Status = "idle"
	f.r.IdleFor = time.Second // went idle again a second ago
	c.ws.paneChanged(c.w1)
	f.runTimers()
	if got := c.inbox(t, c.lead, true); len(got) != 0 {
		t.Fatalf("reported after %v idle: %+v", f.r.IdleFor, got)
	}
	if len(f.timers) == 0 {
		t.Fatal("the report was dropped instead of waiting out the rest")
	}
	f.r.IdleFor = time.Minute
	f.runTimers()
	if got := c.inbox(t, c.lead, true); len(got) != 1 {
		t.Errorf("after the full dwell the manager has %d messages, want 1", len(got))
	}
}

// -tab joins only a tab an earlier -tab made. A tab the user made, a tab named
// after a directory and the asking pane's own tab are left alone.
func TestSpawnWithATabNameLeavesOtherTabsAlone(t *testing.T) {
	c := newCrew(t)
	start := func(o SpawnOptions) string {
		o.Task, o.Role, o.Agent, o.Kind, o.SpawnedByAgent = "t", "worker", "sleeper", session.KindClaude, true
		id, err := c.ws.Spawn(c.lead, o)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	mine := tabOf(c.ws, c.lead)

	// A tab the user made, with a layout of their own and a pane zoomed.
	userTab := tabOf(c.ws, c.w1)
	userTab.Title = "Login rate limit"
	userTab.Zoom = true
	before := userTab.Tree.Panes()

	a := start(SpawnOptions{TabName: "login rate limit"})
	if tabOf(c.ws, a) == userTab || tabOf(c.ws, a) == mine {
		t.Fatal("a helper joined a tab that no -tab made")
	}
	if !userTab.Zoom || strings.Join(userTab.Tree.Panes(), ",") != strings.Join(before, ",") {
		t.Error("the user's tab was rearranged")
	}

	// The manager's own tab, even when its title is asked for.
	mine.Title = "Billing"
	b := start(SpawnOptions{TabName: "billing"})
	if tabOf(c.ws, b) == mine {
		t.Error("a helper joined the manager's own tab")
	}

	// The tab a -tab made is joined, even with -split, which the tab outranks.
	d := start(SpawnOptions{TabName: "Login Rate Limit", Split: true})
	if tabOf(c.ws, d) != tabOf(c.ws, a) {
		t.Error("-tab with -split did not join the tab the first -tab made")
	}
	if tabOf(c.ws, d) == mine {
		t.Error("-split put the helper beside its parent despite -tab")
	}
}

// Every role keeps the original rule about approvals in messages, and is told
// not to answer a message only to acknowledge it, which would start a ping-pong
// between a manager and a helper that each wake the other.
func TestEveryBriefingKeepsTheApprovalRuleAndTellsAgentsNotToAcknowledge(t *testing.T) {
	for _, role := range []string{"manager", "worker", "reviewer"} {
		text := roleContext(role, nil, false).Render()
		for _, want := range []string{
			"ask the user before doing anything they have not already asked for",
			"Do not answer a message only to acknowledge it",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the briefing lacks %q", role, want)
			}
		}
		if strings.Contains(text, "your task does not cover") {
			t.Errorf("%s: the approval rule was loosened", role)
		}
	}
}

// Every start passes the early check made before anything is created, because
// the room is there when each is asked; the check made when the pane is added,
// under the lock that adds it, is what stops the ninth. Spawn runs one at a
// time on the workspace goroutine in the application, which the mutex stands in for.
func TestStartsThatAllPassTheEarlyCheckCannotOvershootTheHelperCap(t *testing.T) {
	c := newCrew(t)
	const tries = 16
	var early sync.WaitGroup
	for i := 0; i < tries; i++ {
		early.Add(1)
		go func() {
			defer early.Done()
			if err := c.ws.HelperRoom(c.lead); err != nil {
				t.Errorf("the early check refused with room to spare: %v", err)
			}
		}()
	}
	early.Wait()

	var onLoop sync.Mutex
	var wg sync.WaitGroup
	errs := make(chan error, tries)
	for i := 0; i < tries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			onLoop.Lock()
			defer onLoop.Unlock()
			_, err := c.ws.Spawn(c.lead, SpawnOptions{Task: "t", Role: "worker", Agent: "sleeper", Kind: session.KindClaude, SpawnedByAgent: true})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	made := 0
	for err := range errs {
		if err == nil {
			made++
		}
	}
	c.ws.mu.RLock()
	below := c.ws.helpersBelow(c.lead)
	c.ws.mu.RUnlock()
	if below != MaxHelpers || made != MaxHelpers-3 {
		t.Errorf("%d panes below the manager after %d starts (%d succeeded), want exactly %d and %d", below, tries, made, MaxHelpers, MaxHelpers-3)
	}
}

// Closing a helper in the middle of a chain does not free the places its own
// helpers hold: they are still the manager's.
func TestHelperCapStillCountsTheChildrenOfAClosedHelper(t *testing.T) {
	c := newCrew(t)
	for i := 0; i < 4; i++ {
		if _, err := c.ws.Spawn(c.w1, SpawnOptions{Task: "t", Agent: "sleeper", Kind: session.KindClaude, SpawnedByAgent: true}); err != nil {
			t.Fatal(err)
		}
	}
	if n := func() int { c.ws.mu.RLock(); defer c.ws.mu.RUnlock(); return c.ws.helpersBelow(c.lead) }(); n != 7 {
		t.Fatalf("%d below the manager, want 7", n)
	}
	c.ws.ClosePaneByID(c.w1) // the helper in the middle, whose four are left
	if _, err := c.ws.Spawn(c.lead, SpawnOptions{Task: "t", Role: "worker", Agent: "sleeper", Kind: session.KindClaude, SpawnedByAgent: true}); err != nil {
		t.Fatalf("the manager has 6 below it: %v", err)
	}
	if _, err := c.ws.Spawn(c.lead, SpawnOptions{Task: "t", Role: "worker", Agent: "sleeper", Kind: session.KindClaude, SpawnedByAgent: true}); err != nil {
		t.Fatalf("the manager has 7 below it: %v", err)
	}
	if _, err := c.ws.Spawn(c.lead, SpawnOptions{Task: "t", Role: "worker", Agent: "sleeper", Kind: session.KindClaude, SpawnedByAgent: true}); err == nil {
		t.Error("with 8 below it, counting four whose parent was closed, the manager started a ninth")
	}
}
