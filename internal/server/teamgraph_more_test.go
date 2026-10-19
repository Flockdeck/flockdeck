package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/team"
)

// More of the graph view: what the bounds cut, what the counts show, the parts
// that come from the board, and when the graph is rebuilt.

func (e *graphEnv) taskOp(t *testing.T, pane string, req hooks.TaskRequest) {
	t.Helper()
	hookSrv := e.ws.HookServer()
	if _, err := hooks.Task(hookSrv.BaseURL(), hookSrv.Token(), pane, req); err != nil {
		t.Fatalf("task %+v: %v", req, err)
	}
}

func TestGraphBoundsLiveLineAndMessageEdges(t *testing.T) {
	e := newGraphEnv(t)
	hookSrv := e.ws.HookServer()
	api, token := hookSrv.BaseURL(), hookSrv.Token()
	ask(e.srv, func() bool {
		e.ws.Pane(e.dev).Sess.SetStatus(session.StatusWorking, strings.Repeat("é", 400))
		return true
	})
	for i := 0; i < 130; i++ {
		// An inbox holds 100 unread messages, so the sends are split between two.
		to := e.boss
		if i%2 == 1 {
			to = e.eyes
		}
		if _, err := hooks.Msg(api, token, e.dev, to, fmt.Sprintf("message %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	g := e.snapshotGraph(t)
	if n, _ := g.node(e.dev); len([]rune(n.Live)) != graphLiveCut {
		t.Errorf("live line has %d characters, want %d", len([]rune(n.Live)), graphLiveCut)
	}
	var msgs int
	var newest int
	for _, ed := range g.Edges {
		if ed.Kind == "message" {
			msgs++
			newest = max(newest, ed.ID)
		}
	}
	if msgs != maxGraphEdges {
		t.Errorf("%d message edges, want %d", msgs, maxGraphEdges)
	}
	if first := firstMessageEdge(g); first.Text != "message 129" {
		t.Errorf("the first message edge is %q, want the newest", first.Text)
	}
}

func firstMessageEdge(g graphMsg) graphEdge {
	for _, ed := range g.Edges {
		if ed.Kind == "message" {
			return ed
		}
	}
	return graphEdge{}
}

func TestGraphCountsChipAndSpendFlag(t *testing.T) {
	e := newGraphEnv(t)
	hookSrv := e.ws.HookServer()
	if _, err := hooks.Msg(hookSrv.BaseURL(), hookSrv.Token(), e.dev, "manager", "one"); err != nil {
		t.Fatal(err)
	}
	e.taskOp(t, e.boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "Build it"})
	e.taskOp(t, e.boss, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: e.dev})
	e.taskOp(t, e.dev, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"})
	ask(e.srv, func() bool { e.ws.Pane(e.dev).AutoReview = true; return true })

	g := e.snapshotGraph(t)
	if n, _ := g.node(e.boss); n.Unread != 1 {
		t.Errorf("boss unread = %d, want 1", n.Unread)
	}
	n, _ := g.node(e.dev)
	if !n.AutoReview {
		t.Error("dev's auto-review is not shown")
	}
	if n.Task == nil || n.Task.ID != "T1" || n.Task.Title != "Build it" || n.Task.State != "in-progress" {
		t.Errorf("dev's task chip = %+v", n.Task)
	}
	// Agents that have run and reported no priced figure make the total a floor.
	if tm := g.Teams[0]; !tm.SpendPartial || tm.SpendUSD != 0 {
		t.Errorf("spend = %v partial %v", tm.SpendUSD, tm.SpendPartial)
	}

	// Agents that have not started report none by nature, and are not counted.
	ask(e.srv, func() bool {
		for _, id := range []string{e.boss, e.dev, e.eyes, e.nested} {
			e.ws.Pane(id).Sess.SetStatus(session.StatusStarting, "")
		}
		return true
	})
	if tm := e.snapshotGraph(t).Teams[0]; tm.SpendPartial {
		t.Error("a team of panes that have not started is flagged partial")
	}
}

func TestGraphReviewerRole(t *testing.T) {
	m := func(pane, role string, exited bool) team.Member {
		st := "idle"
		if exited {
			st = "exited"
		}
		return team.Member{Pane: pane, Role: role, Status: st}
	}
	cases := []struct {
		name    string
		members []team.Member
		owner   string
		want    string
	}{
		{"none", []team.Member{m("a", "worker", false)}, "a", ""},
		{"a reviewer", []team.Member{m("a", "worker", false), m("r", "reviewer", false)}, "a", "reviewer"},
		{"a tester only", []team.Member{m("a", "worker", false), m("t", "tester", false)}, "a", "tester"},
		{"reviewer is preferred", []team.Member{m("t", "tester", false), m("r", "reviewer", false)}, "a", "reviewer"},
		{"an exited reviewer is not told", []team.Member{m("r", "reviewer", true), m("t", "tester", false)}, "a", "tester"},
		{"the owner does not review itself", []team.Member{m("r", "reviewer", false)}, "r", ""},
	}
	for _, c := range cases {
		if got := graphReviewerRole(c.members, c.owner); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A task in review waits on the reviewer role of a live pane, and with none it
// still shows as waiting on a reviewer.
func TestGraphReviewFillsTheRole(t *testing.T) {
	e := newGraphEnv(t)
	e.taskOp(t, e.boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "Build it"})
	e.taskOp(t, e.boss, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: e.dev})
	e.taskOp(t, e.dev, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"})
	e.taskOp(t, e.dev, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-review"})
	g := e.snapshotGraph(t)
	if n, _ := g.node(e.dev); n.WaitingOn.Kind != team.WaitReviewer || n.WaitingOn.Text != "T1 is in review by reviewer" {
		t.Errorf("with a reviewer pane: %+v", n.WaitingOn)
	}
	ask(e.srv, func() bool { e.ws.Pane(e.eyes).Role = "worker"; return true })
	g = e.snapshotGraph(t)
	if n, _ := g.node(e.dev); n.WaitingOn.Kind != team.WaitReviewer || !strings.Contains(n.WaitingOn.Text, "nobody") {
		t.Errorf("with none: %+v", n.WaitingOn)
	}
}

// A task sent back from review is listed with the note that came with the move,
// and not with a later note that happens to follow it. This also pins the
// wording of the board's history entry, which the graph reads.
func TestGraphReturnedTaskNote(t *testing.T) {
	e := newGraphEnv(t)
	e.taskOp(t, e.boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "Build it"})
	e.taskOp(t, e.boss, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: e.dev})
	e.taskOp(t, e.dev, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"})
	e.taskOp(t, e.dev, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-review"})
	e.taskOp(t, e.eyes, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress", Note: "tests are missing"})
	e.taskOp(t, e.dev, hooks.TaskRequest{Op: hooks.TaskNote, ID: "T1", Text: "working on it"})

	find := func(g graphMsg) team.NeedsItem {
		for _, it := range g.Teams[0].Needs {
			if it.Kind == team.NeedReturned {
				return it
			}
		}
		t.Fatalf("no returned item in %+v", g.Teams[0].Needs)
		return team.NeedsItem{}
	}
	if it := find(e.snapshotGraph(t)); it.Task != "T1" || it.Pane != e.dev || it.Text != "tests are missing" {
		t.Errorf("returned item = %+v", it)
	}

	// A second task sent back with no note does not pick up a later note.
	e.taskOp(t, e.boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "Other"})
	e.taskOp(t, e.boss, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T2", Target: e.dev})
	e.taskOp(t, e.dev, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T2", State: "in-progress"})
	e.taskOp(t, e.dev, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T2", State: "in-review"})
	e.taskOp(t, e.boss, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T2", State: "in-progress", Note: "x"})
	g := e.snapshotGraph(t)
	for _, it := range g.Teams[0].Needs {
		if it.Task == "T2" && it.Text != "x" {
			t.Errorf("T2 item text = %q", it.Text)
		}
	}
}

func TestGraphPaneThatCouldNotStartIsDown(t *testing.T) {
	e := newGraphEnv(t)
	ask(e.srv, func() bool { e.ws.Pane(e.nested).Err = errors.New("claude was not found"); return true })
	g := e.snapshotGraph(t)
	n, _ := g.node(e.nested)
	if n.WaitingOn.Kind != team.WaitDown || n.WaitingOn.Text != "failed: claude was not found" {
		t.Errorf("waiting on = %+v", n.WaitingOn)
	}
	var found bool
	for _, it := range g.Teams[0].Needs {
		found = found || it.Kind == team.NeedDown && it.Pane == e.nested && it.Text == "failed: claude was not found"
	}
	if !found {
		t.Errorf("needs = %+v", g.Teams[0].Needs)
	}
}

// An idle project is not read again on every tick: the board and the messages
// are rebuilt only when they, or the agents, change.
func TestGraphBoardAndMessagesAreNotRebuiltWhenNothingChanged(t *testing.T) {
	e := newGraphEnv(t)
	builds := func() int {
		n, _ := ask(e.srv, func() int { return e.srv.graphBuilds })
		return n
	}
	push := func() {
		ask(e.srv, func() bool {
			_, _, err := e.srv.graphData(e.root)
			return err == nil
		})
	}
	push()
	first := builds()
	for i := 0; i < 5; i++ {
		push()
	}
	// Pane activity does not count as a change to the board or the inboxes.
	ask(e.srv, func() bool { e.ws.Pane(e.dev).Sess.SetStatus(session.StatusWorking, "x"); return true })
	push()
	if got := builds(); got != first {
		t.Errorf("the board part was built %d times, want %d", got, first)
	}
	e.taskOp(t, e.boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "New"})
	push()
	if got := builds(); got != first+1 {
		t.Errorf("after a task was added it was built %d times, want %d", got, first+1)
	}
	hookSrv := e.ws.HookServer()
	if _, err := hooks.Msg(hookSrv.BaseURL(), hookSrv.Token(), e.dev, "manager", "hi"); err != nil {
		t.Fatal(err)
	}
	push()
	if got := builds(); got != first+2 {
		t.Errorf("after a message it was built %d times, want %d", got, first+2)
	}
}

func TestGraphWatchReplacesTheProject(t *testing.T) {
	e := newGraphEnv(t)
	e.withOtherProject(t)
	c := e.watcher(t, 16)
	e.srv.graphCommand(c, command{Cmd: "graphWatch", Root: e.root})
	e.srv.graphCommand(c, command{Cmd: "graphWatch", Root: e.otherRoot})
	root, _ := ask(e.srv, func() string { return c.graphRoot })
	if root == e.root || root == "" {
		t.Fatalf("watching %q, want the other project", root)
	}
	var last graphMsg
	for len(c.out) > 0 {
		raw := <-c.out
		if typeOf(raw) == "graph" {
			if err := json.Unmarshal(raw, &last); err != nil {
				t.Fatal(err)
			}
		}
	}
	if last.Root != root {
		t.Errorf("the last graph is of %q, want %q", last.Root, root)
	}
	if _, ok := last.node(e.otherAgent); !ok {
		t.Error("the other project's pane is not in its graph")
	}
	if _, ok := last.node(e.boss); ok {
		t.Error("a pane of the first project is in the other's graph")
	}
}

// Watching and then changing within the interval is one push for the watch and
// one held push, not two at once.
func TestGraphWatchCountsAsAPush(t *testing.T) {
	e := newGraphEnv(t)
	c := e.watcher(t, 64)
	e.srv.graphCommand(c, command{Cmd: "graphWatch", Root: e.root})
	ask(e.srv, func() bool { e.ws.Pane(e.dev).Sess.SetStatus(session.StatusWorking, "now"); return true })
	e.srv.Wake()
	time.Sleep(100 * time.Millisecond)
	if n := e.graphPushes(c); n != 1 {
		t.Errorf("%d graphs within the interval, want only the one for the watch", n)
	}
	time.Sleep(teamInterval + 200*time.Millisecond)
	if n := e.graphPushes(c); n != 1 {
		t.Errorf("%d graphs after the interval, want the held one", n)
	}
}

// What depends on the clock alone is looked at on a slow tick, with nothing
// else waking the server.
func TestGraphIsLookedAtOnATick(t *testing.T) {
	old := graphTickEvery
	graphTickEvery = 150 * time.Millisecond
	t.Cleanup(func() { graphTickEvery = old })
	e := newGraphEnv(t)
	c := e.watcher(t, 64)
	e.srv.graphCommand(c, command{Cmd: "graphWatch", Root: e.root})
	e.graphPushes(c)
	// A change that wakes nothing: no Wake, no push of the state.
	ask(e.srv, func() bool { e.ws.Pane(e.dev).Sess.SetStatus(session.StatusWorking, "quietly"); return true })
	deadline := time.After(5 * time.Second)
	for {
		select {
		case raw := <-c.out:
			if typeOf(raw) != "graph" {
				continue
			}
			var m graphMsg
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			if n, _ := m.node(e.dev); n.Live == "quietly" {
				return
			}
		case <-deadline:
			t.Fatal("the tick did not push the change")
		}
	}
}
