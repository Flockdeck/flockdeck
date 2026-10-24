package main

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// The activity ranks copied into the tool are the desk's: which activities count
// as waiting, which as working, and what a group of them adds up to.
func TestActivityRankIsWorkspaces(t *testing.T) {
	for _, a := range []workspace.Activity{
		workspace.ActivityNone, workspace.ActivityWaiting, workspace.ActivityBlocked, workspace.ActivityWorking,
		workspace.ActivityIdle, workspace.ActivityStarting, workspace.ActivityExited, workspace.ActivityFailed,
	} {
		var tally workspace.Tally
		tally.Add(a)
		want := 0
		if tally.Waiting > 0 {
			want = 2
		} else if tally.Working > 0 {
			want = 1
		}
		if got := activityRank(string(a)); got != want {
			t.Errorf("rank of %q is %d, workspace counts it as %d", a, got, want)
		}
		if got, want := aggregateActivity([]string{string(a)}), string(workspace.Aggregate(a)); got != want {
			t.Errorf("a group of %q is %q, workspace says %q", a, got, want)
		}
	}
	if got, want := aggregateActivity([]string{"working", "idle", "blocked", "exited"}), string(workspace.Aggregate("working", "idle", "blocked", "exited")); got != want {
		t.Errorf("a mixed group is %q, workspace says %q", got, want)
	}
}

// The tiling holds every pane once, and a tree of one pane is that pane.
func TestTileTreeHoldsEachPaneOnce(t *testing.T) {
	for n := 1; n <= 10; n++ {
		var ids []string
		for i := 0; i < n; i++ {
			ids = append(ids, string(rune('a'+i)))
		}
		var seen []string
		var walk func(node map[string]any)
		walk = func(node map[string]any) {
			if p, ok := node["pane"].(string); ok {
				seen = append(seen, p)
				return
			}
			kids, _ := node["children"].([]any)
			if len(kids) < 2 {
				t.Errorf("%d panes: a split of %d children", n, len(kids))
			}
			for _, k := range kids {
				walk(k.(map[string]any))
			}
		}
		walk(tileTree(ids))
		if strings.Join(seen, "") != strings.Join(ids, "") {
			t.Errorf("%d panes tiled as %v", n, seen)
		}
	}
}

// What the old page draws for a pane, read off its header.
type oldPane struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Dot    string `json:"dot"`
	Role   string `json:"role"`
	Unread string `json:"unread"`
	Agent  string `json:"agent"`
}

// copyOf is a fixture with its own nodes, to change.
func copyOf(t *testing.T, name string) *fixture {
	t.Helper()
	fx, err := loadFixture(fixtureDir(t), name)
	if err != nil {
		t.Fatal(err)
	}
	cp := *fx
	cp.nodes = append([]fixtureNode(nil), fx.nodes...)
	return &cp
}

// settled is a fixture with nothing working, which is when the old page folds a
// delegated tab into a summary card, unless a pane has a parent.
func settled(t *testing.T, name string) *fixture {
	fx := copyOf(t, name)
	for i := range fx.nodes {
		if activityRank(nodeActivity(fx.nodes[i])) == 1 {
			fx.nodes[i].Status, fx.nodes[i].Activity = "idle", "idle"
		}
	}
	return fx
}

// A parent is sent only while it is a pane of the push, as paneView does.
func TestParentIsSentOnlyWhileItIsAPane(t *testing.T) {
	fx := copyOf(t, "1")
	fx.nodes[1].Parent = "gone-pane"
	panes := fx.stateMsg()["panes"].(map[string]any)
	if _, ok := panes[fx.nodes[1].Pane].(map[string]any)["parent"]; ok {
		t.Errorf("a parent that is no pane was sent")
	}
	if p := panes[fx.nodes[2].Pane].(map[string]any)["parent"]; p != fx.nodes[2].Parent || p == "" {
		t.Errorf("a parent that is a pane was sent as %v, want %q", p, fx.nodes[2].Parent)
	}
}

// The state push read as data: what is sent for each pane and in the totals is
// what workspace says of the same activities.
func TestStateMsgFollowsPaneViewAndTally(t *testing.T) {
	for _, name := range allFixtures(t) {
		t.Run(name, func(t *testing.T) {
			fx := copyOf(t, name)
			// One shell, and one agent whose activity is not its status (an idle
			// agent with background work reads as working).
			var shell, bg int
			for i, n := range fx.nodes {
				if n.Status == "idle" && n.Parent != "" && bg == 0 && !n.Shell {
					bg = i
				}
				if n.Status == "exited" && shell == 0 {
					shell = i
				}
			}
			fx.nodes[shell].Shell = true
			fx.nodes[bg].Activity = "working"
			fx.nodes[bg].Agent = "codex"
			fx.nodes[0].Agent = "" // the default is claude

			msg := fx.stateMsg()
			panes := msg["panes"].(map[string]any)
			var tally workspace.Tally
			var acts []workspace.Activity
			for i, n := range fx.nodes {
				p := panes[n.Pane].(map[string]any)
				wantKind := "agent"
				if i == shell {
					wantKind = "shell"
				}
				if p["kind"] != wantKind {
					t.Errorf("%s: kind %v, want %s", n.Name, p["kind"], wantKind)
				}
				if p["agent"] != wantAgent(n) {
					t.Errorf("%s: agent %v, want %s", n.Name, p["agent"], wantAgent(n))
				}
				wantAct := n.Status
				if n.Activity != "" {
					wantAct = n.Activity
				}
				if p["activity"] != wantAct || p["status"] != n.Status {
					t.Errorf("%s: status %v activity %v, want %s and %s", n.Name, p["status"], p["activity"], n.Status, wantAct)
				}
				if _, has := p["statusSince"]; has != (n.Since != "") {
					t.Errorf("%s: statusSince sent %v, the node has %q", n.Name, has, n.Since)
				}
				tally.Add(workspace.Activity(wantAct))
				acts = append(acts, workspace.Activity(wantAct))
			}
			proj := msg["projects"].([]any)[0].(map[string]any)
			tab := msg["tabs"].([]any)[0].(map[string]any)
			if proj["waiting"] != tally.Waiting || proj["working"] != tally.Working || msg["waiting"] != tally.Waiting || msg["working"] != tally.Working {
				t.Errorf("counts project %v/%v total %v/%v, workspace counts %d/%d", proj["waiting"], proj["working"], msg["waiting"], msg["working"], tally.Waiting, tally.Working)
			}
			want := string(workspace.Aggregate(acts...))
			if got, _ := tab["activity"].(string); got != want {
				t.Errorf("tab activity %q, workspace says %q", got, want)
			}
			if got, _ := proj["activity"].(string); got != want {
				t.Errorf("project activity %q, workspace says %q", got, want)
			}
			if tab["attention"] != workspace.Aggregate(acts...).NeedsAttention() || tab["delegated"] != true {
				t.Errorf("tab attention %v delegated %v", tab["attention"], tab["delegated"])
			}
		})
	}
}

// The tiling is the manager first and then rows of three.
func TestTilingIsRowsOfThree(t *testing.T) {
	fx := copyOf(t, "1")
	root := fx.stateMsg()["tabs"].([]any)[0].(map[string]any)["root"].(map[string]any)
	rows := root["children"].([]any)
	first := rows[0].(map[string]any)["children"].([]any)
	if len(first) != tileColumns || tileColumns != 3 {
		t.Errorf("the first row holds %d panes, tileColumns is %d, want 3", len(first), tileColumns)
	}
	if first[0].(map[string]any)["pane"] != fx.nodes[0].Pane {
		t.Errorf("the first tile is %v, want the manager", first[0])
	}
}

// A pane with a need but a status that is not waiting has no view: snapshot
// attaches the views only to a waiting pane. The graph lists a need for a pane
// that is, say, working only if the desk's graph and its push were read at
// different moments, and the push is then the truth about the pane.
func TestViewsOnlyForAWaitingPane(t *testing.T) {
	fx := copyOf(t, "1")
	for i, n := range fx.nodes {
		if n.Status == "waiting" {
			fx.nodes[i].Status, fx.nodes[i].Activity = "working", "working"
		}
	}
	for id, p := range fx.stateMsg()["panes"].(map[string]any) {
		pv := p.(map[string]any)
		if pv["permission"] != nil || pv["ask"] != nil {
			t.Errorf("pane %s is not waiting and has a view", id)
		}
	}
}

func wantAgent(n fixtureNode) string {
	if n.Agent != "" {
		return n.Agent
	}
	return "claude"
}
