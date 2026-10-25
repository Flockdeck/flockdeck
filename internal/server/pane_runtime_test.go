package server

import (
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/team"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// fakeRuntime is a pane that is not a terminal: it answers every question the
// graph and the pane view ask from its own fields, and has no session behind it
// for them to read.
type fakeRuntime struct {
	status  session.Status
	detail  string
	input   string
	options []workspace.PermissionOption
	reply   string
	ran     bool
	started time.Time
	// tasks is the background work it says is running.
	tasks int
	// sess is the process the pane was made with, which closing it still ends.
	sess *session.Session
	// setup is what a pane that cannot start says it needs; the zero value is a
	// pane that needs nothing.
	setup workspace.SetupNeeded
}

func (f fakeRuntime) Transport() workspace.Transport { return workspace.TransportStructured }
func (f fakeRuntime) Caps() workspace.PaneCaps {
	return workspace.PaneCaps{Structured: true, AnswerPermission: true}
}
func (f fakeRuntime) Status() (session.Status, string) { return f.status, f.detail }
func (f fakeRuntime) StatusSince() time.Time           { return f.started }
func (f fakeRuntime) StartedAt() time.Time             { return f.started }
func (f fakeRuntime) HasRun() bool                     { return f.ran }
func (f fakeRuntime) Activity() workspace.Activity {
	if f.status == session.StatusWaiting {
		return workspace.ActivityWaiting
	}
	if f.status == session.StatusBlocked {
		return workspace.ActivityBlocked
	}
	if f.status == session.StatusIdle {
		return workspace.ActivityIdle
	}
	return workspace.ActivityWorking
}
func (f fakeRuntime) Setup() (workspace.SetupNeeded, bool) { return f.setup, f.setup.Text != "" }
func (f fakeRuntime) Waiting() workspace.WaitingOn {
	return workspace.WaitingOn{Detail: f.detail, ToolInput: f.input}
}
func (f fakeRuntime) PermissionOptions(string) []workspace.PermissionOption { return f.options }
func (f fakeRuntime) Background() workspace.BackgroundWork {
	return workspace.BackgroundWork{Tasks: f.tasks, Verified: f.tasks}
}
func (f fakeRuntime) LastReply() (workspace.LastReply, bool) {
	return workspace.LastReply{Text: f.reply, Kind: "text"}, f.reply != ""
}
func (f fakeRuntime) Wake(string) error { return nil }
func (f fakeRuntime) Close() error      { return f.sess.Close() }
func (f fakeRuntime) Record(bool) error { return nil }
func (f fakeRuntime) Conversation() workspace.ConversationSource {
	return workspace.ConversationSource{}
}

// detach takes a pane's session out from under it, as a pane that failed to
// start has none, and gives the pane the runtime instead. The runtime closes
// the session when the pane closes, so the test leaves no process behind.
func (e *graphEnv) detach(id string, f fakeRuntime) {
	ask(e.srv, func() bool {
		p := e.ws.Pane(id)
		if p.Sess != nil {
			f.sess, p.Sess = p.Sess, nil
		} else if prev, ok := e.ws.Runtime(p).(fakeRuntime); ok {
			f.sess = prev.sess
		}
		e.ws.SetRuntime(id, f)
		return true
	})
}

// A pane of another transport shows in the graph, the needs list, the pane view
// and the agents list from what its runtime says. It has no session at all, so
// nothing a consumer reads can come from one.
func TestAPaneOfAnotherTransportIsReadThroughItsRuntime(t *testing.T) {
	e := newGraphEnv(t)
	always := []workspace.PermissionOption{
		{ID: "a", Name: "Allow", Kind: optionAllowOnce},
		{ID: "b", Name: "Always allow", Kind: optionAllowAlways},
	}
	start := time.Now().Add(-time.Minute).Truncate(time.Second)
	e.detach(e.dev, fakeRuntime{status: session.StatusWaiting, detail: "Bash",
		input: `{"command":"make test"}`, options: always, ran: true, started: start, tasks: 2})
	e.detach(e.eyes, fakeRuntime{status: session.StatusWorking, reply: "Reading the diff", ran: true, started: start})
	e.detach(e.boss, fakeRuntime{status: session.StatusIdle, reply: "Done, merged it", ran: true, started: start})

	g := e.snapshotGraph(t)
	dev, ok := g.node(e.dev)
	if !ok {
		t.Fatal("no node for the structured pane")
	}
	if dev.Kind != graphKindStructured || dev.Status != "waiting" || dev.Activity != "waiting" {
		t.Errorf("kind %q, status %q, activity %q", dev.Kind, dev.Status, dev.Activity)
	}
	if dev.Since == "" {
		t.Error("no status time from the runtime")
	}
	eyes, _ := g.node(e.eyes)
	// A working pane's last reply is the previous turn, so it is not shown as
	// what the pane is doing; an idle pane keeps it.
	if eyes.Status != "working" || eyes.Live != "" {
		t.Errorf("status %q, live %q; want a working pane with no live line", eyes.Status, eyes.Live)
	}
	if boss, _ := g.node(e.boss); boss.Activity != "idle" || boss.Live != "Done, merged it" {
		t.Errorf("the idle pane has activity %q, live %q", boss.Activity, boss.Live)
	}
	if !hasNeed(g, e.dev) {
		t.Errorf("the waiting pane is not in the needs list: %+v", g.Teams)
	}

	snap, _ := ask(e.srv, func() stateMsg { return e.srv.snapshot() })
	dv := snap.Panes[e.dev]
	if dv.Permission == nil || dv.Permission.Summary == "" || len(dv.Permission.Options) != 2 || dv.Permission.Options[1].Kind != optionAllowAlways {
		t.Errorf("permission = %+v, want the runtime's options", dv.Permission)
	}
	if dv.StatusSince != start.Format(time.RFC3339) || dv.Background != 2 {
		t.Errorf("status since %q, background %d", dv.StatusSince, dv.Background)
	}
	if last := snap.Panes[e.eyes].Last; last == nil || last.Text != "Reading the diff" {
		t.Errorf("last reply = %+v", last)
	}

	ask(e.srv, func() bool { e.srv.sendAgents(e.c); return true })
	var list agentsMsg
	e.next(t, "agents", &list)
	var item *agentView
	for i := range list.Items {
		if list.Items[i].PaneID == e.dev {
			item = &list.Items[i]
		}
	}
	if item == nil || item.Waiting == "" || item.For == "" || item.Background != 2 {
		t.Errorf("agents item = %+v", item)
	}
}

// The team starts when the manager's runtime says, and its spend is a floor
// only for a pane whose runtime says it has run.
func TestTeamStartAndSpendFloorComeFromTheRuntimes(t *testing.T) {
	e := newGraphEnv(t)
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, id := range []string{e.boss, e.dev, e.eyes, e.nested} {
		e.detach(id, fakeRuntime{status: session.StatusWorking, started: start})
	}
	g := e.snapshotGraph(t)
	if len(g.Teams) != 1 {
		t.Fatalf("teams = %+v", g.Teams)
	}
	if got := g.Teams[0].Started; got != start.Format(time.RFC3339) {
		t.Errorf("started = %q, want the runtime's %q", got, start.Format(time.RFC3339))
	}
	if g.Teams[0].SpendPartial {
		t.Error("no pane has run, so there is no figure to be missing")
	}

	e.detach(e.eyes, fakeRuntime{status: session.StatusWorking, started: start, ran: true})
	if g := e.snapshotGraph(t); !g.Teams[0].SpendPartial {
		t.Error("a pane that has run and has no figure makes the total a floor")
	}
}

func hasNeed(g graphMsg, pane string) bool {
	for _, tm := range g.Teams {
		for _, n := range tm.Needs {
			if n.Pane == pane {
				return true
			}
		}
	}
	return false
}

// A pane that cannot start for want of Node, a sign-in or the agent's program is
// listed once, with the fix as its text, after a pane that is down and before
// the ones with unread mail. Its id is the same on every read, and it goes when
// the pane starts.
func TestAPaneThatNeedsSetupIsOneNeedsItem(t *testing.T) {
	e := newGraphEnv(t)
	start := time.Now().Add(-time.Minute).Truncate(time.Second)
	causes := map[string]workspace.SetupNeeded{
		e.dev:    {Kind: "node", Text: "Install Node 20 or later from nodejs.org. Then start the pane again."},
		e.eyes:   {Kind: "login", Text: "Sign in to claude with its own tool. Flockdeck does not sign in for you. Then start the pane again."},
		e.nested: {Kind: "install", Text: strings.Repeat("Install the agent. ", 40)},
	}
	for id, su := range causes {
		e.detach(id, fakeRuntime{status: session.StatusBlocked, detail: "needs setup", started: start, setup: su})
	}

	g := e.snapshotGraph(t)
	var setups []team.NeedsItem
	for _, n := range g.Teams[0].Needs {
		if n.Kind == team.NeedSetup {
			setups = append(setups, n)
		}
	}
	if len(setups) != len(causes) {
		t.Fatalf("setup items = %+v, want one for each of %d panes", setups, len(causes))
	}
	for _, n := range setups {
		if n.ID != "setup:"+n.Pane || n.Text == "" || len([]rune(n.Text)) > team.MaxWaitText {
			t.Errorf("item = %+v", n)
		}
		if want := causes[n.Pane].Text; len([]rune(want)) <= team.MaxWaitText && n.Text != want {
			t.Errorf("text for %s = %q, want %q", n.Pane, n.Text, want)
		}
	}
	again := e.snapshotGraph(t)
	for i, n := range again.Teams[0].Needs {
		if n.ID != g.Teams[0].Needs[i].ID {
			t.Errorf("item %d id %q then %q", i, g.Teams[0].Needs[i].ID, n.ID)
		}
	}

	// Started: the pane works, and its item is gone.
	e.detach(e.dev, fakeRuntime{status: session.StatusWorking, started: start})
	for _, n := range e.snapshotGraph(t).Teams[0].Needs {
		if n.ID == "setup:"+e.dev {
			t.Errorf("the item stayed after the pane started: %+v", n)
		}
	}
}
