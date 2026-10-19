package team

import (
	"reflect"
	"strings"
	"testing"
)

func pane(id, parent, role, activity string) GraphPane {
	return GraphPane{ID: id, Parent: parent, Role: role, Name: id, Activity: activity, Status: activity}
}

func TestBuildGraphNestedTeam(t *testing.T) {
	g := BuildGraph([]GraphPane{
		pane("m", "", "manager", "idle"),
		pane("p", "m", "planner", "idle"),
		pane("w", "p", "worker", "idle"),
		pane("h", "w", "", "idle"),
	}, nil)

	if len(g.Teams) != 1 {
		t.Fatalf("teams = %+v, want one", g.Teams)
	}
	if got, want := g.Teams[0].Members, []string{"m", "p", "w", "h"}; !reflect.DeepEqual(got, want) {
		t.Errorf("members = %v, want %v", got, want)
	}
	depths := map[string]int{"m": 0, "p": 1, "w": 2, "h": 3}
	for _, n := range g.Nodes {
		if n.Depth != depths[n.Pane] || n.Team != "m" {
			t.Errorf("node %s: depth %d team %q", n.Pane, n.Depth, n.Team)
		}
	}
	w, _ := g.Node("w")
	if w.Parent != "p" || !reflect.DeepEqual(w.Children, []string{"h"}) {
		t.Errorf("w parent %q children %v", w.Parent, w.Children)
	}
	if len(g.Solo) != 0 {
		t.Errorf("solo = %v, want none", g.Solo)
	}
}

func TestBuildGraphTwoTeamsAndSolo(t *testing.T) {
	g := BuildGraph([]GraphPane{
		pane("m1", "", "manager", "idle"),
		pane("a", "m1", "worker", "idle"),
		pane("m2", "", "manager", "idle"),
		pane("lone", "", "", "idle"),
		pane("sh", "", "", "idle"),
		pane("lead", "", "", "idle"),
		pane("b", "lead", "", "idle"),
	}, nil)

	var ids []string
	for _, tm := range g.Teams {
		ids = append(ids, tm.ID)
	}
	// A manager alone is still a team; a non-manager root needs a child.
	if want := []string{"m1", "m2", "lead"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("team ids = %v, want %v", ids, want)
	}
	if want := []string{"lone", "sh"}; !reflect.DeepEqual(g.Solo, want) {
		t.Errorf("solo = %v, want %v", g.Solo, want)
	}
	if a, _ := g.Node("a"); a.Team != "m1" {
		t.Errorf("a team = %q", a.Team)
	}
	if b, _ := g.Node("b"); b.Team != "lead" {
		t.Errorf("b team = %q", b.Team)
	}
	if l, _ := g.Node("lone"); l.Team != "" || l.Depth != 0 {
		t.Errorf("lone = %+v", l)
	}
}

func TestBuildGraphOrphan(t *testing.T) {
	g := BuildGraph([]GraphPane{
		pane("o", "gone", "worker", "idle"),
		pane("c", "o", "", "idle"),
		pane("x", "gone", "", "idle"),
	}, nil)

	o, _ := g.Node("o")
	if !o.Orphaned || o.Parent != "" || o.Team != "o" {
		t.Errorf("o = %+v, want an orphaned root of its own team", o)
	}
	if c, _ := g.Node("c"); c.Team != "o" || c.Depth != 1 || c.Orphaned {
		t.Errorf("c = %+v", c)
	}
	// An orphan with no children and no manager role is solo, still flagged.
	if x, _ := g.Node("x"); !x.Orphaned || x.Team != "" {
		t.Errorf("x = %+v", x)
	}
	if !reflect.DeepEqual(g.Solo, []string{"x"}) {
		t.Errorf("solo = %v", g.Solo)
	}
}

func TestBuildGraphParentLoop(t *testing.T) {
	g := BuildGraph([]GraphPane{
		pane("a", "b", "", "idle"),
		pane("b", "a", "", "idle"),
		pane("self", "self", "", "idle"),
	}, nil)
	for _, n := range g.Nodes {
		if !n.Orphaned || n.Team != "" || n.Depth != 0 {
			t.Errorf("node %s = %+v, want an orphaned solo root", n.Pane, n)
		}
	}
	if len(g.Solo) != 3 {
		t.Errorf("solo = %v", g.Solo)
	}
}

func TestBuildGraphEmpty(t *testing.T) {
	g := BuildGraph(nil, nil)
	if g.Teams == nil || g.Nodes == nil || g.Solo == nil {
		t.Errorf("nil slice in %+v; they should encode as []", g)
	}
}

func TestWaitingOn(t *testing.T) {
	long := strings.Repeat("é", 300)
	prompt := func(p GraphPane, text string) GraphPane {
		p.HasPrompt, p.PromptText = true, text
		return p
	}
	failed := func(p GraphPane) GraphPane { p.Failed, p.Exited = true, true; return p }
	exited := func(p GraphPane) GraphPane { p.Exited = true; return p }
	shell := func(p GraphPane) GraphPane { p.Shell = true; return p }

	tests := []struct {
		name  string
		panes []GraphPane
		tasks []GraphTask
		pane  string
		want  WaitingOn
		clipN int // when set, the text must be exactly this many runes
	}{
		{
			name:  "user permission",
			panes: []GraphPane{prompt(pane("a", "", "", "waiting"), "rm -rf build")},
			pane:  "a", want: WaitingOn{Kind: WaitUser, Text: "rm -rf build"},
		},
		{
			name:  "user blocked",
			panes: []GraphPane{prompt(pane("a", "", "", "blocked"), "Which branch?")},
			pane:  "a", want: WaitingOn{Kind: WaitUser, Text: "Which branch?"},
		},
		{
			name:  "user text clipped",
			panes: []GraphPane{prompt(pane("a", "", "", "waiting"), long)},
			pane:  "a", want: WaitingOn{Kind: WaitUser}, clipN: MaxWaitText,
		},
		{
			name:  "waiting without a prompt is not the user",
			panes: []GraphPane{pane("a", "", "", "waiting")},
			pane:  "a", want: WaitingOn{Kind: WaitNone},
		},
		{
			name:  "down exited",
			panes: []GraphPane{exited(pane("a", "", "", "exited"))},
			pane:  "a", want: WaitingOn{Kind: WaitDown, Text: "exited"},
		},
		{
			name:  "down failed",
			panes: []GraphPane{failed(pane("a", "", "", "failed"))},
			pane:  "a", want: WaitingOn{Kind: WaitDown, Text: "failed"},
		},
		{
			name:  "reviewer pane",
			panes: []GraphPane{pane("a", "", "worker", "idle"), {ID: "r", Name: "rev", Peer: "flockdeck-9"}},
			tasks: []GraphTask{{ID: "T1", State: "in-review", OwnerPane: "a", ReviewerPane: "r"}},
			pane:  "a", want: WaitingOn{Kind: WaitReviewer, Text: "T1 is in review by flockdeck-9", Ref: "T1"},
		},
		{
			name:  "reviewer role",
			panes: []GraphPane{pane("a", "", "worker", "idle")},
			tasks: []GraphTask{{ID: "T1", State: "in-review", OwnerPane: "a", ReviewerRole: "reviewer"}},
			pane:  "a", want: WaitingOn{Kind: WaitReviewer, Text: "T1 is in review by reviewer", Ref: "T1"},
		},
		{
			name:  "review by the owner itself is not waiting on a reviewer",
			panes: []GraphPane{pane("a", "", "worker", "idle")},
			tasks: []GraphTask{{ID: "T1", State: "in-review", OwnerPane: "a", ReviewerPane: "a"}},
			pane:  "a", want: WaitingOn{Kind: WaitNone},
		},
		{
			name:  "task blocked",
			panes: []GraphPane{pane("a", "", "worker", "idle")},
			tasks: []GraphTask{{ID: "T2", State: "blocked", OwnerPane: "a", LastNote: "needs the API key\nfrom ops"}},
			pane:  "a", want: WaitingOn{Kind: WaitTask, Text: "needs the API key from ops", Ref: "T2"},
		},
		{
			name:  "task note clipped",
			panes: []GraphPane{pane("a", "", "worker", "idle")},
			tasks: []GraphTask{{ID: "T2", State: "blocked", OwnerPane: "a", LastNote: long}},
			pane:  "a", want: WaitingOn{Kind: WaitTask, Ref: "T2"}, clipN: MaxWaitText,
		},
		{
			name:  "task owned by someone else",
			panes: []GraphPane{pane("a", "", "worker", "idle")},
			tasks: []GraphTask{{ID: "T2", State: "blocked", OwnerPane: "b"}},
			pane:  "a", want: WaitingOn{Kind: WaitNone},
		},
		{
			name:  "shell never waits on reviewer",
			panes: []GraphPane{shell(pane("a", "", "", "idle"))},
			tasks: []GraphTask{{ID: "T1", State: "in-review", OwnerPane: "a", ReviewerRole: "reviewer"}},
			pane:  "a", want: WaitingOn{Kind: WaitNone},
		},
		{
			name:  "shell never waits on task",
			panes: []GraphPane{shell(pane("a", "", "", "idle"))},
			tasks: []GraphTask{{ID: "T2", State: "blocked", OwnerPane: "a", LastNote: "x"}},
			pane:  "a", want: WaitingOn{Kind: WaitNone},
		},
		{
			name: "helpers working",
			panes: []GraphPane{
				pane("m", "", "manager", "idle"),
				pane("h1", "m", "", "idle"),
				pane("h2", "m", "", "working"),
				pane("h3", "m", "", "waiting"),
			},
			pane: "m", want: WaitingOn{Kind: WaitHelpers, Text: "2 helpers are busy", Ref: "h2"},
		},
		{
			name: "one helper",
			panes: []GraphPane{
				pane("m", "", "manager", "idle"),
				pane("h", "m", "", "working"),
			},
			pane: "m", want: WaitingOn{Kind: WaitHelpers, Text: "1 helper is busy", Ref: "h"},
		},
		{
			name: "only direct children count",
			panes: []GraphPane{
				pane("m", "", "manager", "idle"),
				pane("p", "m", "", "idle"),
				pane("h", "p", "", "working"),
			},
			pane: "m", want: WaitingOn{Kind: WaitNone},
		},
		{
			name: "user beats down",
			panes: []GraphPane{func() GraphPane {
				p := pane("a", "", "", "waiting")
				p.HasPrompt, p.PromptText, p.Exited = true, "ok?", true
				return p
			}()},
			pane: "a", want: WaitingOn{Kind: WaitUser, Text: "ok?"},
		},
		{
			name:  "down beats reviewer",
			panes: []GraphPane{exited(pane("a", "", "worker", "exited"))},
			tasks: []GraphTask{{ID: "T1", State: "in-review", OwnerPane: "a", ReviewerRole: "reviewer"}},
			pane:  "a", want: WaitingOn{Kind: WaitDown, Text: "exited"},
		},
		{
			name:  "reviewer beats task",
			panes: []GraphPane{pane("a", "", "worker", "idle")},
			tasks: []GraphTask{
				{ID: "T2", State: "blocked", OwnerPane: "a", LastNote: "stuck"},
				{ID: "T1", State: "in-review", OwnerPane: "a", ReviewerRole: "reviewer"},
			},
			pane: "a", want: WaitingOn{Kind: WaitReviewer, Text: "T1 is in review by reviewer", Ref: "T1"},
		},
		{
			name: "task beats helpers",
			panes: []GraphPane{
				pane("a", "", "worker", "idle"),
				pane("h", "a", "", "working"),
			},
			tasks: []GraphTask{{ID: "T2", State: "blocked", OwnerPane: "a", LastNote: "stuck"}},
			pane:  "a", want: WaitingOn{Kind: WaitTask, Text: "stuck", Ref: "T2"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := BuildGraph(tc.panes, tc.tasks)
			n, ok := g.Node(tc.pane)
			if !ok {
				t.Fatalf("no node for %s", tc.pane)
			}
			got := n.WaitingOn
			if tc.clipN > 0 {
				if c := len([]rune(got.Text)); c != tc.clipN {
					t.Errorf("text has %d runes, want %d", c, tc.clipN)
				}
				got.Text = ""
			}
			if got != tc.want {
				t.Errorf("waiting on = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestBuildGraphLeavesInputAlone(t *testing.T) {
	panes := []GraphPane{pane("m", "", "manager", "idle"), pane("a", "m", "", "working")}
	before := append([]GraphPane(nil), panes...)
	BuildGraph(panes, nil)
	if !reflect.DeepEqual(panes, before) {
		t.Errorf("panes changed: %+v", panes)
	}
}
