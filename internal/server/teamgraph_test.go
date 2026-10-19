package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/team"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// The graph view of the Team subscription. These tests build a project from
// shell panes marked as agents, as the Team panel's tests do, and run under the
// package's testiso TestMain.

// graphEnv is a team: boss (manager) with helpers dev and eyes, and a helper
// under dev, plus a pane of the project that started nothing.
type graphEnv struct {
	*teamEnv
	nested, lone string
}

func newGraphEnv(t *testing.T) *graphEnv {
	t.Helper()
	// Proof that the tests run off the real config directory: testiso has pointed
	// it at a temporary one before any test started.
	cfg, err := os.UserConfigDir()
	if err != nil || !strings.HasPrefix(filepath.Clean(cfg), filepath.Clean(os.TempDir())) {
		t.Fatalf("the config directory %q is not under the temporary directory %q (%v)", cfg, os.TempDir(), err)
	}
	e := &graphEnv{teamEnv: newTeamEnv(t)}
	e.nested = agentPane(t, e.srv, e.ws, "nested", "")
	e.lone = agentPane(t, e.srv, e.ws, "lone", "")
	ask(e.srv, func() bool {
		e.ws.Pane(e.dev).Parent = e.boss
		e.ws.Pane(e.eyes).Parent = e.boss
		e.ws.Pane(e.nested).Parent = e.dev
		return true
	})
	return e
}

func (e *graphEnv) snapshotGraph(t *testing.T) graphMsg {
	t.Helper()
	e.srv.graphCommand(e.c, command{Cmd: "graphSnapshot", Root: e.root})
	var m graphMsg
	e.next(t, "graph", &m)
	return m
}

func (m graphMsg) node(id string) (graphNodeView, bool) {
	for _, n := range m.Nodes {
		if n.Pane == id {
			return n, true
		}
	}
	return graphNodeView{}, false
}

func TestGraphSnapshotHasNodesEdgesAndCapUse(t *testing.T) {
	e := newGraphEnv(t)
	hookSrv := e.ws.HookServer()
	api, token := hookSrv.BaseURL(), hookSrv.Token()
	if _, err := hooks.Msg(api, token, e.dev, "manager", "a question\nwith a second line"); err != nil {
		t.Fatal(err)
	}
	ask(e.srv, func() bool {
		e.ws.Pane(e.boss).Task = "Ship the thing"
		e.ws.Pane(e.dev).Sess.SetStatus(session.StatusWorking, "Edit main.go")
		return true
	})

	g := e.snapshotGraph(t)
	if len(g.Teams) != 1 || g.Teams[0].ID != e.boss || g.Teams[0].Manager != e.boss {
		t.Fatalf("teams = %+v", g.Teams)
	}
	tm := g.Teams[0]
	if tm.Goal != "Ship the thing" || len(tm.Members) != 4 {
		t.Errorf("goal %q members %v", tm.Goal, tm.Members)
	}
	depth := map[string]int{e.boss: 0, e.dev: 1, e.eyes: 1, e.nested: 2}
	for id, d := range depth {
		n, ok := g.node(id)
		if !ok || n.Depth != d || n.Team != e.boss {
			t.Errorf("node %s = %+v, want depth %d in the team", id, n, d)
		}
	}
	if n, _ := g.node(e.dev); n.Live != "Edit main.go" || n.Activity != "working" || n.Parent != e.boss || len(n.Children) != 1 {
		t.Errorf("dev = %+v", n)
	}
	if n, _ := g.node(e.boss); n.WaitingOn.Kind != team.WaitHelpers {
		t.Errorf("boss waits on %+v, want helpers", n.WaitingOn)
	}

	// Caps: the manager has three below it, dev has one, and no other pane has
	// children. The count is the workspace's own rule.
	caps := map[string]graphCap{}
	for _, c := range tm.Caps {
		caps[c.Pane] = c
	}
	if len(caps) != 2 || caps[e.boss].Used != 3 || caps[e.dev].Used != 1 || caps[e.boss].Max != workspace.MaxHelpers {
		t.Errorf("caps = %+v", tm.Caps)
	}

	// Edges: three spawn links, and the one message, cut to its first line.
	var spawn, msgs int
	for _, ed := range g.Edges {
		switch ed.Kind {
		case "spawn":
			spawn++
		case "message":
			msgs++
			if ed.From != e.dev || ed.To != e.boss || ed.Text != "a question" {
				t.Errorf("message edge = %+v", ed)
			}
		}
	}
	if spawn != 3 || msgs != 1 {
		t.Errorf("edges = %+v", g.Edges)
	}

	// Panes in no team: the lone agent and the shell the server started with.
	if !slices.Contains(g.Solo, e.lone) || slices.Contains(g.Solo, e.boss) {
		t.Errorf("solo = %v", g.Solo)
	}
}

func TestGraphPromptAndNeeds(t *testing.T) {
	e := newGraphEnv(t)
	ask(e.srv, func() bool {
		p := e.ws.Pane(e.eyes)
		p.Sess.SetStatusFull(session.StatusWaiting, "Bash", `{"command":"rm -rf build"}`)
		return true
	})
	g := e.snapshotGraph(t)
	n, _ := g.node(e.eyes)
	if n.WaitingOn.Kind != team.WaitUser || n.WaitingOn.Text != "rm -rf build" {
		t.Errorf("eyes waits on %+v", n.WaitingOn)
	}
	needs := g.Teams[0].Needs
	if len(needs) != 1 || needs[0].Kind != team.NeedPermission || needs[0].Pane != e.eyes {
		t.Errorf("needs = %+v", needs)
	}
}

func TestGraphProjectWithNoTeam(t *testing.T) {
	srv, ws := newTestServer(t)
	a := agentPane(t, srv, ws, "a", "worker")
	c := &controlClient{out: make(chan []byte, 8)}
	srv.graphCommand(c, command{Cmd: "graphSnapshot", Root: ws.ActiveRoot()})
	var m graphMsg
	if err := json.Unmarshal(<-c.out, &m); err != nil {
		t.Fatal(err)
	}
	if m.Teams == nil || len(m.Teams) != 0 {
		t.Errorf("teams = %#v, want an empty list", m.Teams)
	}
	if !slices.Contains(m.Solo, a) {
		t.Errorf("solo = %v", m.Solo)
	}
	raw, _ := json.Marshal(m)
	if !strings.Contains(string(raw), `"teams":[]`) {
		t.Errorf("teams did not encode as []: %s", raw)
	}
}

func TestGraphIsRefusedThroughTheRelay(t *testing.T) {
	e := newGraphEnv(t)
	c := &controlClient{out: make(chan []byte, 8), remote: true}
	for _, cmd := range []string{"graphSnapshot", "graphWatch", "graphUnwatch"} {
		e.srv.graphCommand(c, command{Cmd: cmd, Root: e.root, Req: "r"})
		var res teamResultMsg
		if err := json.Unmarshal(<-c.out, &res); err != nil || res.OK || res.Error != teamDeskOnly {
			t.Errorf("%s = %+v (%v)", cmd, res, err)
		}
		for len(c.out) > 0 { // the notice that goes with the refusal
			<-c.out
		}
	}
	if c.graphRoot != "" {
		t.Error("a relay window was subscribed")
	}
}

func (e *graphEnv) graphPushes(c *controlClient) (n int) {
	for {
		select {
		case raw := <-c.out:
			if typeOf(raw) == "graph" {
				n++
			}
		default:
			return n
		}
	}
}

func TestGraphIsPushedOnAStatusChange(t *testing.T) {
	e := newGraphEnv(t)
	c := e.watcher(t, 256)
	e.srv.graphCommand(c, command{Cmd: "graphWatch", Root: e.root})
	ask(e.srv, func() bool { return true })
	if n := e.graphPushes(c); n != 1 {
		t.Fatalf("%d graphs on subscribing, want 1", n)
	}

	ask(e.srv, func() bool { e.ws.Pane(e.dev).Sess.SetStatus(session.StatusWorking, "x"); return true })
	e.srv.Wake()
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
			if n, _ := m.node(e.dev); n.Activity == "working" {
				return
			}
		case <-deadline:
			t.Fatal("no graph with the new status")
		}
	}
}

func TestGraphPushesAreThrottledAndUnwatchStopsThem(t *testing.T) {
	e := newGraphEnv(t)
	c := e.watcher(t, 1024)
	e.srv.graphCommand(c, command{Cmd: "graphWatch", Root: e.root})
	ask(e.srv, func() bool { return true })
	e.graphPushes(c)

	// A flood of changes for one second, far faster than the interval.
	start := time.Now()
	for i := 0; time.Since(start) < time.Second; i++ {
		text := strings.Repeat("x", i%7+1)
		ask(e.srv, func() bool { e.ws.Pane(e.dev).Sess.SetStatus(session.StatusWorking, text); return true })
		e.srv.Wake()
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(teamInterval + 200*time.Millisecond) // the last held push
	elapsed := time.Since(start)
	n := e.graphPushes(c)
	limit := int(elapsed/teamInterval) + 1
	if n < 1 || n > limit {
		t.Errorf("%d pushes in %v, want between 1 and %d (twice a second at most)", n, elapsed, limit)
	}

	e.srv.graphCommand(c, command{Cmd: "graphUnwatch"})
	ask(e.srv, func() bool { e.ws.Pane(e.dev).Sess.SetStatus(session.StatusIdle, "after"); return true })
	e.srv.Wake()
	time.Sleep(teamInterval + 300*time.Millisecond)
	if n := e.graphPushes(c); n != 0 {
		t.Errorf("%d graphs after unwatching", n)
	}
}

func TestGraphAndTeamSubscriptionsAreIndependent(t *testing.T) {
	e := newGraphEnv(t)
	c := e.watcher(t, 64)
	e.srv.graphCommand(c, command{Cmd: "graphWatch", Root: e.root})
	ask(e.srv, func() bool { return c.teamRoot == "" && c.graphRoot != "" })
	e.srv.teamCommand(c, command{Cmd: "teamWatch", Root: e.root})
	e.srv.graphCommand(c, command{Cmd: "graphUnwatch"})
	if ok, _ := ask(e.srv, func() bool { return c.teamRoot != "" && c.graphRoot == "" }); !ok {
		t.Error("unwatching the graph changed the Team subscription")
	}
}
