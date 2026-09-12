package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// revealAgents is an agent every machine that runs these tests has: `go`,
// given the task as a command it does not know, which says so and exits. The
// pane it ran in stays, which is all a test of where the pane went needs.
const revealAgents = `{"version": 1, "agents": [{"id": "gocli", "name": "Go", "exe": "go",
	"args": [{"value": "{{prompt}}"}]}]}`

// place is the tab on screen and the pane focused in it.
type place struct{ tab, pane string }

// wherePlaced reads where the window is, on the workspace goroutine.
func wherePlaced(t *testing.T, srv *Server, ws *workspace.Workspace) place {
	t.Helper()
	p, ok := ask(srv, func() place {
		tab := ws.CurrentTab()
		if tab == nil {
			return place{}
		}
		return place{tab.ID, tab.Focus}
	})
	if !ok {
		t.Fatal("server closed")
	}
	return p
}

// runFanout starts a fan-out and waits for the notice it closes on.
func runFanout(t *testing.T, srv *Server, req fanoutRequest) {
	t.Helper()
	c := &controlClient{out: make(chan []byte, 32)}
	srv.fanout(c, req)
	var said []string
	deadline := time.After(15 * time.Second)
	for {
		select {
		case raw := <-c.out:
			var msg noticeMsg
			if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != "notice" {
				continue
			}
			said = append(said, msg.Text)
			if strings.HasPrefix(msg.Text, "started ") {
				if msg.Error {
					t.Fatalf("the fan-out did not start every agent: %q", said)
				}
				return
			}
			if msg.Error {
				t.Fatalf("the fan-out failed: %q", said)
			}
		case <-deadline:
			t.Fatalf("the fan-out never finished; it said %q", said)
		}
	}
}

// fanoutServer is a test server that can start the agent in revealAgents.
func fanoutServer(t *testing.T) (*Server, *workspace.Workspace) {
	t.Helper()
	srv, ws := newTestServer(t)
	writeAgents(t, revealAgents)
	ws.ReloadAgents()
	return srv, ws
}

// TestFanoutSelectsTheTabItOpened covers a fan-out into a tab of its own. The
// tab was added at the end of the tab bar and left unselected, so the person
// who had just started a dozen agents had to go and find them.
func TestFanoutSelectsTheTabItOpened(t *testing.T) {
	srv, ws := fanoutServer(t)
	lead := wherePlaced(t, srv, ws)

	runFanout(t, srv, fanoutRequest{Tasks: []string{"first-task", "second-task"}, Agent: "gocli"})

	got := wherePlaced(t, srv, ws)
	if got.tab == lead.tab {
		t.Fatal("the fan-out's tab was not selected; the window stayed on the tab it was started from")
	}
	panes, _ := ask(srv, func() []string { return ws.Tab(got.tab).Tree.Panes() })
	if len(panes) != 2 {
		t.Fatalf("the selected tab holds %d panes; want the fan-out's two", len(panes))
	}
	first, _ := ask(srv, func() string { return ws.Pane(got.pane).Task })
	if first != "first-task" {
		t.Errorf("the focused pane is running %q; want the first task", first)
	}
}

// TestFanoutIntoThisTabFocusesTheFirstNewPane covers a fan-out split into the
// tab it was started from. The tab is already on screen, so what shows the
// agents is the focus, which stayed on the pane the plan was read from.
func TestFanoutIntoThisTabFocusesTheFirstNewPane(t *testing.T) {
	srv, ws := fanoutServer(t)
	lead := wherePlaced(t, srv, ws)

	runFanout(t, srv, fanoutRequest{Tasks: []string{"first-task", "second-task"}, Agent: "gocli", Split: true})

	got := wherePlaced(t, srv, ws)
	if got.tab != lead.tab {
		t.Fatalf("a fan-out into this tab moved the window to tab %s", got.tab)
	}
	if got.pane == lead.pane {
		t.Fatal("the focus stayed on the pane the fan-out was started from")
	}
	first, _ := ask(srv, func() string { return ws.Pane(got.pane).Task })
	if first != "first-task" {
		t.Errorf("the focused pane is running %q; want the first task", first)
	}
}

// TestFanoutLeavesAWindowThatHasMovedOn covers somebody who went to another tab
// while the fan-out was still cutting worktrees. Taking the window back would
// send whatever they were typing there into an agent they had not chosen.
func TestFanoutLeavesAWindowThatHasMovedOn(t *testing.T) {
	srv, ws := newTestServer(t)
	lead := wherePlaced(t, srv, ws)
	other, _ := ask(srv, func() string {
		id := ws.NewTabWith(workspace.Choice{Kind: session.KindShell}, "", "other").ID
		child, err := ws.Spawn(lead.pane, workspace.SpawnOptions{Task: "elsewhere", Kind: session.KindShell})
		if err != nil {
			t.Error(err)
			return id
		}
		// Started while the window was on the lead's tab, and found only
		// once the person had moved to another.
		srv.revealFirstChild(child, lead.tab)
		return id
	})

	if got := wherePlaced(t, srv, ws); got.tab != other {
		t.Errorf("the fan-out took the window to tab %s; want it left on %s, where the person went", got.tab, other)
	}
}
