package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/help"
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

type oldPage struct {
	Panes []oldPane `json:"panes"`
	Tabs  []struct {
		Label     string `json:"label"`
		Attention bool   `json:"attention"`
		Name      string `json:"name"`
	} `json:"tabs"`
	Cards int    `json:"cards"`
	Title string `json:"title"`
	Rail  []struct {
		Waiting bool `json:"waiting"`
	} `json:"rail"`
}

// oldCase is the node case: the old page's own app.js, booted in the old UI's
// harness, given the state push of a fixture, and read for what it drew.
const oldCase = `"use strict";
const assert = require("assert");
const fs = require("fs");
const { boot } = require("./harness.js");
const h = boot();
h.hello();
h.recv(JSON.parse(fs.readFileSync(process.env.STATE, "utf8")));
const text = (n, sel) => { const e = n.querySelector(sel); return e ? e.textContent : ""; };
const out = {
  panes: h.doc.querySelectorAll("div.pane").map((w) => ({
    name: text(w, ".pane-name"), status: w.dataset.status, dot: w.querySelector("span.dot").className,
    role: text(w, ".pane-role"), unread: text(w, ".pane-unread"), agent: text(w, ".pane-agent"),
  })),
  tabs: h.$("tabs").children.map((t) => ({
    label: text(t, ".label"), attention: t.classList.contains("attention"), name: (t.querySelector("button.tab-btn") || t).getAttribute("aria-label") || "",
  })),
  cards: h.doc.querySelectorAll(".summary-card").length,
  title: h.doc.title,
  rail: h.$("rail-projects").children.map((t) => ({ waiting: t.querySelector(".rail-badge") ? t.querySelector(".rail-badge").classList.contains("waiting") : false })),
};
console.log("RESULT " + JSON.stringify(out));
`

// drawInTheOldPage runs the old page over one state push and returns what it drew.
func drawInTheOldPage(t *testing.T, state map[string]any) oldPage {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node is not on PATH, and CI is set")
		}
		t.Skip("node is not on PATH")
	}
	assets := filepath.Join("..", "..", "internal", "webui", "assets")
	dir := t.TempDir()
	copyFile := func(from, to string) {
		t.Helper()
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, to), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"app.js", "e2e.js", "index.html", "app.css"} {
		copyFile(filepath.Join(assets, name), name)
	}
	copyFile(filepath.Join("..", "..", "internal", "webui", "testdata", "harness.js"), "harness.js")
	keys, err := json.Marshal(help.Keys)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := help.Pages()
	if err != nil {
		t.Fatal(err)
	}
	helpJSON, err := json.Marshal(map[string]any{"pages": pages})
	if err != nil {
		t.Fatal(err)
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"keys.json": keys, "help.json": helpJSON, "state.json": stateJSON, "case.js": []byte(oldCase)} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join(dir, "case.js"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FLOCKDECK_ASSETS="+dir, "STATE="+filepath.Join(dir, "state.json"))
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the old page: %v\n%s", err, out)
	}
	i := bytes.LastIndex(out, []byte("RESULT "))
	if i < 0 {
		t.Fatalf("the old page printed no result:\n%s", out)
	}
	var page oldPage
	if err := json.Unmarshal(bytes.TrimSpace(out[i+len("RESULT "):]), &page); err != nil {
		t.Fatalf("the old page's result: %v\n%s", err, out)
	}
	return page
}

// For each fixture the old page shows the situation as it does for a live team:
// the panes that wait are the fixture's, they are marked on the pane and its dot,
// the team's tab and the project carry the waiting mark, and nothing is folded
// into a summary card.
func TestEachFixtureInTheOldPage(t *testing.T) {
	for _, name := range allFixtures(t) {
		t.Run(name, func(t *testing.T) {
			fx, err := loadFixture(fixtureDir(t), name)
			if err != nil {
				t.Fatal(err)
			}
			page := drawInTheOldPage(t, fx.stateMsg())

			// The fixture's waiting set: the panes the graph lists a permission or a
			// question for, which are also the panes whose activity is waiting.
			// Fixture 04 lists three needs and has two waiting panes: its returned task is
			// on a pane that is working, and a returned task is not a permission or a
			// question, so it is not in this set.
			want := map[string]bool{}
			for _, n := range fx.needs {
				if n.Kind == "permission" || n.Kind == "ask" {
					want[nodeNamed(t, fx, n.Pane)] = true
				}
			}
			byActivity := map[string]bool{}
			for _, n := range fx.nodes {
				if activityRank(nodeActivity(n)) == 2 {
					byActivity[n.Name] = true
				}
			}
			if !sameSet(want, byActivity) {
				t.Fatalf("the fixture's needs name %v and its waiting panes are %v; this test would prove nothing", keys(want), keys(byActivity))
			}
			if len(want) == 0 {
				t.Fatal("the fixture has no waiting pane")
			}

			if len(page.Panes) != len(fx.nodes) {
				t.Fatalf("the old page drew %d panes for %d nodes", len(page.Panes), len(fx.nodes))
			}
			got := map[string]bool{}
			byName := map[string]oldPane{}
			for _, p := range page.Panes {
				byName[p.Name] = p
				if p.Status == "waiting" {
					got[p.Name] = true
				}
			}
			if !sameSet(got, want) {
				t.Errorf("the old page marks %v as waiting, the fixture has %v", keys(got), keys(want))
			}
			for _, n := range fx.nodes {
				p, ok := byName[n.Name]
				if !ok {
					t.Errorf("no pane drawn for %s", n.Name)
					continue
				}
				if p.Status != n.Status {
					t.Errorf("%s: drawn as %q, the fixture says %q", n.Name, p.Status, n.Status)
				}
				if !strings.Contains(p.Dot, nodeActivity(n)) {
					t.Errorf("%s: the dot is %q, not %q", n.Name, p.Dot, nodeActivity(n))
				}
				if n.Role != "" && p.Role == "" {
					t.Errorf("%s: the role %q is not drawn", n.Name, n.Role)
				}
				if n.Unread > 0 && p.Unread == "" {
					t.Errorf("%s: %d unread, none drawn", n.Name, n.Unread)
				}
				if want := wantAgent(n); p.Agent != want {
					t.Errorf("%s: the agent is drawn as %q, want %q", n.Name, p.Agent, want)
				}
			}

			if len(page.Tabs) != 1 {
				t.Fatalf("%d tabs, want the team's one", len(page.Tabs))
			}
			if !page.Tabs[0].Attention {
				t.Errorf("the team's tab is not marked, with %d waiting", len(want))
			}
			if !strings.Contains(page.Tabs[0].Name, "waiting on you") {
				t.Errorf("the tab does not say an agent is waiting: %q", page.Tabs[0].Name)
			}
			if page.Cards != 0 {
				t.Errorf("%d summary cards: the team is not finished", page.Cards)
			}
			if wantTitle := fmt.Sprintf("%d waiting", len(want)); !strings.Contains(page.Title, wantTitle) {
				t.Errorf("the window title is %q, want it to say %q", page.Title, wantTitle)
			}
			if len(page.Rail) != 1 || !page.Rail[0].Waiting {
				t.Errorf("the project's rail tile is not marked waiting: %v", page.Rail)
			}
		})
	}
}

func nodeNamed(t *testing.T, fx *fixture, pane string) string {
	t.Helper()
	for _, n := range fx.nodes {
		if n.Pane == pane {
			return n.Name
		}
	}
	t.Fatalf("no node for pane %s", pane)
	return ""
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func wantAgent(n fixtureNode) string {
	if n.Agent != "" {
		return n.Agent
	}
	return "claude"
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

// The two marks that keep a team's tab from becoming a summary card, read in the
// old page: every helper has its parent, and the tab is delegated. With the
// team settled and the parents in, no card; with the parents taken out the page
// folds the tab, which shows the tab is marked delegated.
func TestParentAndDelegatedAreWhatKeepTheTeamFromFolding(t *testing.T) {
	for _, name := range allFixtures(t) {
		t.Run(name, func(t *testing.T) {
			with := settled(t, name)
			if got := drawInTheOldPage(t, with.stateMsg()).Cards; got != 0 {
				t.Errorf("a settled team with its parents is %d cards, want none", got)
			}
			without := settled(t, name)
			for i := range without.nodes {
				without.nodes[i].Parent = ""
			}
			if got := drawInTheOldPage(t, without.stateMsg()).Cards; got != 1 {
				t.Errorf("a settled delegated tab with no parents is %d cards, want the one", got)
			}
		})
	}
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
