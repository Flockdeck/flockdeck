package team

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func needsFor(panes []GraphPane, tasks []GraphTask, cf []PaneConflict, mv []TaskMove) map[string][]NeedsItem {
	return NeedsYou(BuildGraph(panes, tasks), panes, tasks, cf, mv, t0)
}

func kinds(items []NeedsItem) []string {
	var k []string
	for _, it := range items {
		k = append(k, it.Kind+":"+it.Pane)
	}
	return k
}

func team2() []GraphPane {
	return []GraphPane{
		pane("m", "", "manager", "idle"),
		pane("a", "m", "worker", "idle"),
		pane("b", "m", "worker", "idle"),
	}
}

func TestNeedsPromptKinds(t *testing.T) {
	ps := team2()
	ps[1].Activity, ps[1].HasPrompt, ps[1].PromptText, ps[1].PromptKind = "waiting", true, "Which branch?", "ask"
	ps[1].StatusSince = t0.Add(-time.Minute)
	ps[2].Activity, ps[2].HasPrompt, ps[2].PromptText = "blocked", true, "rm -rf build"
	ps[2].StatusSince = t0.Add(-time.Hour)

	got := needsFor(ps, nil, nil, nil)["m"]
	if want := []string{"permission:b", "ask:a"}; !reflect.DeepEqual(kinds(got), want) {
		t.Fatalf("items = %v, want %v (longest waiting first)", kinds(got), want)
	}
	if got[0].Text != "rm -rf build" || got[1].Text != "Which branch?" {
		t.Errorf("texts = %q, %q", got[0].Text, got[1].Text)
	}
}

func TestNeedsReturned(t *testing.T) {
	ps := team2()
	tasks := []GraphTask{
		{ID: "T1", State: "in-progress", OwnerPane: "a"},
		{ID: "T2", State: "in-progress", OwnerPane: "b"},
		{ID: "T3", State: "in-review", OwnerPane: "b"},
		{ID: "T4", State: "in-progress", OwnerPane: "b"},
	}
	mv := []TaskMove{
		{Task: "T1", From: "in-review", To: "in-progress", Time: t0.Add(-time.Hour), Note: "tests missing"},
		{Task: "T1", From: "in-review", To: "in-progress", Time: t0.Add(-2 * time.Hour), Note: "older"},
		{Task: "T2", From: "in-review", To: "in-progress", Time: t0.Add(-25 * time.Hour), Note: "too old"},
		{Task: "T3", From: "in-review", To: "in-progress", Time: t0.Add(-time.Hour), Note: "back in review since"},
		{Task: "T4", From: "blocked", To: "in-progress", Time: t0.Add(-time.Hour), Note: "not from review"},
	}
	got := needsFor(ps, tasks, nil, mv)["m"]
	if len(got) != 1 || got[0].ID != "returned:T1" || got[0].Pane != "a" || got[0].Task != "T1" || got[0].Text != "tests missing" {
		t.Errorf("items = %+v", got)
	}
}

func TestNeedsConflict(t *testing.T) {
	ps := team2()
	paths := []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go"}
	got := needsFor(ps, nil, []PaneConflict{{A: "b", B: "a", Paths: paths}}, nil)["m"]
	if len(got) != 1 || got[0].Kind != NeedConflict || got[0].Other != "a" {
		t.Fatalf("items = %+v", got)
	}
	if want := "a.go, b.go, c.go, d.go, e.go and 2 more"; got[0].Text != want {
		t.Errorf("text = %q, want %q", got[0].Text, want)
	}
	// The id does not depend on which side the radar listed first.
	swapped := needsFor(ps, nil, []PaneConflict{{A: "a", B: "b", Paths: paths}}, nil)["m"]
	if swapped[0].ID != got[0].ID {
		t.Errorf("ids differ: %q and %q", swapped[0].ID, got[0].ID)
	}
}

func TestNeedsDown(t *testing.T) {
	ps := team2()
	ps[1].Exited, ps[1].ExitCode, ps[1].Activity = true, 2, "exited"
	ps[2].Exited, ps[2].ExitCode, ps[2].Activity = true, 0, "exited"
	ps = append(ps, pane("c", "m", "worker", "failed"))
	ps[3].Failed, ps[3].Exited = true, true

	got := needsFor(ps, nil, nil, nil)["m"]
	if want := []string{"down:a", "down:c"}; !reflect.DeepEqual(kinds(got), want) {
		t.Fatalf("items = %v, want %v (a clean exit is not listed)", kinds(got), want)
	}
	if got[0].Text != "exited with code 2" || got[1].Text != "failed" {
		t.Errorf("texts = %q, %q", got[0].Text, got[1].Text)
	}
}

func TestNeedsStaleUnread(t *testing.T) {
	ps := team2()
	ps[1].Unread, ps[1].UnreadSince = 2, t0.Add(-6*time.Minute)
	ps[2].Unread, ps[2].UnreadSince = 1, t0.Add(-4*time.Minute)
	ps = append(ps, pane("c", "m", "worker", "working"))
	ps[3].Unread, ps[3].UnreadSince = 1, t0.Add(-time.Hour)

	got := needsFor(ps, nil, nil, nil)["m"]
	if len(got) != 1 || got[0].ID != "stale-unread:a" || got[0].Text != "2 unread" {
		t.Errorf("items = %+v, want only the idle pane unread for over five minutes", got)
	}
}

func TestNeedsOrder(t *testing.T) {
	ps := []GraphPane{
		pane("m", "", "manager", "idle"),
		pane("unread", "m", "", "idle"),
		pane("down", "m", "", "failed"),
		pane("ask", "m", "", "waiting"),
		pane("perm", "m", "", "waiting"),
		pane("ret", "m", "", "idle"),
		pane("x", "m", "", "idle"),
	}
	ps[1].Unread, ps[1].UnreadSince = 1, t0.Add(-time.Hour)
	ps[2].Failed, ps[2].Exited = true, true
	ps[3].HasPrompt, ps[3].PromptKind, ps[3].StatusSince = true, "ask", t0.Add(-time.Minute)
	ps[4].HasPrompt, ps[4].StatusSince = true, t0.Add(-time.Hour)
	tasks := []GraphTask{{ID: "T9", State: "in-progress", OwnerPane: "ret"}}
	mv := []TaskMove{{Task: "T9", From: "in-review", To: "in-progress", Time: t0.Add(-time.Hour)}}
	cf := []PaneConflict{{A: "x", B: "ret", Paths: []string{"p"}}}

	got := kinds(needsFor(ps, tasks, cf, mv)["m"])
	want := []string{"permission:perm", "ask:ask", "returned:ret", "conflict:x", "down:down", "stale-unread:unread"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestNeedsIDsStable(t *testing.T) {
	ps := team2()
	ps[1].Activity, ps[1].HasPrompt, ps[1].PromptText = "waiting", true, "ok?"
	ps[2].Failed, ps[2].Exited = true, true
	first := needsFor(ps, nil, nil, nil)["m"]

	// Later, with the wait longer and the text changed, the ids hold.
	ps[1].PromptText = "ok? really?"
	second := NeedsYou(BuildGraph(ps, nil), ps, nil, nil, nil, t0.Add(time.Hour))["m"]
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("items = %+v, %+v", first, second)
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Errorf("item %d id %q became %q", i, first[i].ID, second[i].ID)
		}
	}
}

func TestNeedsBounded(t *testing.T) {
	ps := []GraphPane{pane("m", "", "manager", "idle")}
	for i := 0; i < 80; i++ {
		p := pane(fmt.Sprintf("p%02d", i), "m", "", "waiting")
		p.HasPrompt, p.PromptText = true, strings.Repeat("x", 500)
		p.StatusSince = t0.Add(-time.Duration(i) * time.Minute)
		ps = append(ps, p)
	}
	got := needsFor(ps, nil, nil, nil)["m"]
	if len(got) != MaxNeeds {
		t.Fatalf("len = %d, want %d", len(got), MaxNeeds)
	}
	// The ones kept are the longest waiting.
	if got[0].Pane != "p79" || len([]rune(got[0].Text)) != MaxWaitText {
		t.Errorf("first = %s with %d runes", got[0].Pane, len([]rune(got[0].Text)))
	}
}

func TestNeedsPerTeamAndSolo(t *testing.T) {
	ps := []GraphPane{
		pane("m1", "", "manager", "idle"),
		pane("a", "m1", "", "failed"),
		pane("m2", "", "manager", "idle"),
		pane("solo", "", "", "failed"),
	}
	ps[1].Failed, ps[3].Failed = true, true
	got := needsFor(ps, nil, nil, nil)
	if len(got["m1"]) != 1 || len(got[""]) != 1 || len(got["m2"]) != 0 {
		t.Errorf("got %+v", got)
	}
}
