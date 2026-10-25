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
	out := map[string][]NeedsItem{}
	for k, v := range NeedsYou(BuildGraph(panes, tasks), panes, tasks, cf, mv, t0) {
		out[k] = v.Items
	}
	return out
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
	ps[1].Activity, ps[1].HasPrompt, ps[1].PromptText, ps[1].PromptKind = "waiting", true, "Which branch?", PromptAsk
	ps[1].StatusSince = t0.Add(-time.Minute)
	ps[2].Activity, ps[2].HasPrompt, ps[2].PromptText, ps[2].PromptKind = "blocked", true, "rm -rf build", PromptPermission
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

func TestNeedsConflictText(t *testing.T) {
	ps := team2()
	paths := []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go"}
	got := needsFor(ps, nil, []PaneConflict{{A: "b", B: "a", Paths: paths}}, nil)["m"]
	if len(got) != 1 || got[0].Kind != NeedConflict || got[0].Pane != "a" || got[0].Other != "b" {
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
	ps[1].Unread, ps[1].UnreadMail, ps[1].UnreadSince = 2, 2, t0.Add(-40*time.Minute)
	ps[2].Unread, ps[2].UnreadMail, ps[2].UnreadSince = 1, 1, t0.Add(-20*time.Minute)
	ps = append(ps, pane("c", "m", "worker", "working"))
	ps[3].Unread, ps[3].UnreadMail, ps[3].UnreadSince = 1, 1, t0.Add(-time.Hour)

	got := needsFor(ps, nil, nil, nil)["m"]
	if len(got) != 1 || got[0].ID != "stale-unread:a" || got[0].Text != "2 unread" {
		t.Errorf("items = %+v, want only the idle pane unread for over half an hour", got)
	}
}

func TestStaleUnreadOfSeveralPanesIsOneItem(t *testing.T) {
	ps := []GraphPane{pane("m", "", "manager", "idle")}
	for i, mins := range []int{50, 120, 90} {
		p := pane(fmt.Sprintf("w%d", i), "m", "", "idle")
		p.Unread, p.UnreadMail, p.UnreadSince = 10, 10, t0.Add(-time.Duration(mins)*time.Minute)
		ps = append(ps, p)
	}
	got := needsFor(ps, nil, nil, nil)["m"]
	if len(got) != 1 || got[0].ID != "stale-unread:w1" || got[0].Text != "3 agents, 30 unread" {
		t.Errorf("items = %+v, want one item for the oldest pane counting all three", got)
	}
}

func TestMailOfFlockdeckAloneIsNotStale(t *testing.T) {
	ps := team2()
	ps[1].Unread, ps[1].UnreadSince = 5, t0.Add(-time.Hour) // UnreadMail is 0: all five are notices
	if got := needsFor(ps, nil, nil, nil); len(got) != 0 {
		t.Errorf("got %+v", got)
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
	ps[1].Unread, ps[1].UnreadMail, ps[1].UnreadSince = 1, 1, t0.Add(-time.Hour)
	ps[2].Failed, ps[2].Exited = true, true
	ps[3].HasPrompt, ps[3].PromptKind, ps[3].StatusSince = true, PromptAsk, t0.Add(-time.Minute)
	ps[4].HasPrompt, ps[4].PromptKind, ps[4].StatusSince = true, PromptPermission, t0.Add(-time.Hour)
	tasks := []GraphTask{{ID: "T9", State: "in-progress", OwnerPane: "ret"}}
	mv := []TaskMove{{Task: "T9", From: "in-review", To: "in-progress", Time: t0.Add(-time.Hour)}}
	cf := []PaneConflict{{A: "x", B: "ret", Paths: []string{"p"}}}

	got := kinds(needsFor(ps, tasks, cf, mv)["m"])
	want := []string{"permission:perm", "ask:ask", "returned:ret", "conflict:ret", "down:down", "stale-unread:unread"}
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
	second := NeedsYou(BuildGraph(ps, nil), ps, nil, nil, nil, t0.Add(time.Hour))["m"].Items
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

func rawNeeds(panes []GraphPane, tasks []GraphTask, cf []PaneConflict, mv []TaskMove) map[string]TeamNeeds {
	return NeedsYou(BuildGraph(panes, tasks), panes, tasks, cf, mv, t0)
}

func TestNeedsConflictInBothTeams(t *testing.T) {
	ps := []GraphPane{
		pane("m1", "", "manager", "idle"), pane("a", "m1", "", "idle"),
		pane("m2", "", "manager", "idle"), pane("b", "m2", "", "idle"),
		pane("solo", "", "", "idle"),
	}
	cf := []PaneConflict{{A: "b", B: "a", Paths: []string{"x.go"}}, {A: "a", B: "b", Paths: []string{"x.go"}}}
	got := needsFor(ps, nil, cf, nil)
	if len(got["m1"]) != 1 || len(got["m2"]) != 1 || got["m1"][0].ID != got["m2"][0].ID {
		t.Fatalf("got %+v, want one item with one id in both teams (and the repeated pair listed once)", got)
	}
	if it := got["m1"][0]; it.Pane != "a" || it.Other != "b" {
		t.Errorf("pair = %s, %s, want a, b", it.Pane, it.Other)
	}

	// With a solo pane the item is in its team and the solo list.
	got = needsFor(ps, nil, []PaneConflict{{A: "solo", B: "a", Paths: []string{"y"}}}, nil)
	if len(got["m1"]) != 1 || len(got[""]) != 1 || len(got["m2"]) != 0 {
		t.Errorf("got %+v", got)
	}
	// Both panes in one team: listed once.
	ps2 := team2()
	got = needsFor(ps2, nil, []PaneConflict{{A: "a", B: "b"}}, nil)
	if len(got["m"]) != 1 {
		t.Errorf("same-team conflict listed %d times", len(got["m"]))
	}
}

func TestNeedsConflictPaneMissing(t *testing.T) {
	ps := team2()
	got := needsFor(ps, nil, []PaneConflict{{A: "gone", B: "a", Paths: []string{"x"}}}, nil)
	if len(got["m"]) != 1 || got["m"][0].Other != "gone" && got["m"][0].Pane != "gone" {
		t.Errorf("got %+v, want the conflict kept in a's team", got)
	}
	if got := needsFor(ps, nil, []PaneConflict{{A: "gone", B: "also"}}, nil); len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}

func TestNeedsConflictTextKeepsCount(t *testing.T) {
	long := strings.Repeat("d/", 100)
	paths := []string{long, long, long, long, long, "x", "y"}
	got := needsFor(team2(), nil, []PaneConflict{{A: "a", B: "b", Paths: paths}}, nil)["m"]
	if !strings.HasSuffix(got[0].Text, " and 2 more") {
		t.Errorf("text = %q", got[0].Text)
	}
}

func TestNeedsDownRules(t *testing.T) {
	ps := team2()
	ps[1].Exited, ps[1].ExitCode, ps[1].ClosedByUser = true, 137, true
	ps[2].Exited, ps[2].ExitCode, ps[2].Shell = true, 1, true
	ps = append(ps, pane("sh", "m", "", "failed"), pane("dead", "m", "", "waiting"))
	ps[3].Shell, ps[3].Failed, ps[3].Exited = true, true, true
	ps[4].Exited, ps[4].ExitCode, ps[4].HasPrompt, ps[4].PromptText = true, 3, true, "ok?"

	got := needsFor(ps, nil, nil, nil)["m"]
	// a was closed by the person, b is a shell that exited non-zero: neither
	// is listed. A failed shell is. A dead pane shows as down, not as a prompt.
	if want := []string{"down:dead", "down:sh"}; !reflect.DeepEqual(kinds(got), want) {
		t.Errorf("items = %v, want %v", kinds(got), want)
	}
}

func TestNeedsPromptKindFallback(t *testing.T) {
	ps := team2()
	for i := 1; i <= 2; i++ {
		ps[i].Activity, ps[i].HasPrompt = "waiting", true
	}
	ps[2].PromptKind = PromptPermission
	got := needsFor(ps, nil, nil, nil)["m"]
	k := map[string]string{}
	for _, it := range got {
		k[it.Pane] = it.Kind
	}
	if k["a"] != NeedAsk || k["b"] != NeedPermission {
		t.Errorf("kinds = %v, want empty to read as ask", k)
	}
}

func TestNeedsReturnedClearsOnceOwnerWorks(t *testing.T) {
	tasks := []GraphTask{{ID: "T1", State: "in-progress", OwnerPane: "a"}}
	mv := []TaskMove{{Task: "T1", From: "in-review", To: "in-progress", Time: t0.Add(-time.Hour), Note: "fix the findings"}}
	for name, set := range map[string]func(p *GraphPane){
		"idle since the return":                 func(p *GraphPane) {},
		"echo of the return note":               func(p *GraphPane) { p.LastOutput = t0.Add(-time.Hour + time.Second) },
		"output long after, still idle":         func(p *GraphPane) { p.LastOutput = t0.Add(-time.Minute) },
		"working since before the return":       func(p *GraphPane) { p.Activity, p.StatusSince = "working", t0.Add(-2*time.Hour) },
		"working from the moment of the return": func(p *GraphPane) { p.Activity, p.StatusSince = "working", t0.Add(-time.Hour) },
		"working with no status time":           func(p *GraphPane) { p.Activity = "working" },
	} {
		ps := team2()
		set(&ps[1])
		if got := needsFor(ps, tasks, nil, mv)["m"]; len(got) != 1 || got[0].Kind != NeedReturned {
			t.Errorf("%s: items = %+v, want the returned item", name, got)
		}
	}
	// The appraisable shape: the worker is working after the return.
	ps := team2()
	ps[1].Activity, ps[1].StatusSince, ps[1].LastOutput = "working", t0.Add(-30*time.Minute), t0.Add(-time.Second)
	if got := needsFor(ps, tasks, nil, mv)["m"]; len(got) != 0 {
		t.Errorf("working since the return: items = %+v, want none", got)
	}
	// A duplicate pane id does not let a later copy decide.
	dup := append(team2(), ps[1])
	dup[3].ID = "a"
	if got := needsFor(dup, tasks, nil, mv)["m"]; len(got) != 1 {
		t.Errorf("duplicate id: items = %+v, want the first pane's view (idle): one item", got)
	}
}

func TestNeedsReturnedEdges(t *testing.T) {
	ps := team2()
	tasks := []GraphTask{
		{ID: "T1", State: "in-progress", OwnerPane: "a"},
		{ID: "T2", State: "in-progress", OwnerPane: "gone"},
		{ID: "T3", State: "in-progress"},
	}
	mv := []TaskMove{
		{Task: "T1", From: "in-review", To: "in-progress", Time: t0.Add(time.Minute), Note: "future"},
		{Task: "T2", From: "in-review", To: "in-progress", Time: t0.Add(-time.Minute)},
		{Task: "T3", From: "in-review", To: "in-progress", Time: t0.Add(-time.Minute)},
	}
	if got := needsFor(ps, tasks, nil, mv); len(got) != 0 {
		t.Errorf("got %+v, want nothing: a future move, an owner not in the snapshot and no owner", got)
	}
}

func TestNeedsReturnedAndConflictIDsStable(t *testing.T) {
	ps := team2()
	tasks := []GraphTask{{ID: "T1", State: "in-progress", OwnerPane: "a"}}
	mv := []TaskMove{{Task: "T1", From: "in-review", To: "in-progress", Time: t0.Add(-time.Hour)}}
	cf := []PaneConflict{{A: "a", B: "b", Paths: []string{"p"}}}
	one := needsFor(ps, tasks, cf, mv)["m"]
	cf = []PaneConflict{{A: "b", B: "a", Paths: []string{"q", "p"}}}
	mv[0].Note = "again"
	two := needsFor(ps, tasks, cf, mv)["m"]
	if len(one) != 2 || len(two) != 2 {
		t.Fatalf("items %+v, %+v", one, two)
	}
	for i := range one {
		if one[i].ID != two[i].ID {
			t.Errorf("id %q became %q", one[i].ID, two[i].ID)
		}
	}
}

func TestNeedsZeroTimeLast(t *testing.T) {
	ps := team2()
	ps[1].Activity, ps[1].HasPrompt = "waiting", true // StatusSince is zero
	ps[2].Activity, ps[2].HasPrompt, ps[2].StatusSince = "waiting", true, t0.Add(-time.Second)
	got := needsFor(ps, nil, nil, nil)["m"]
	if want := []string{"ask:b", "ask:a"}; !reflect.DeepEqual(kinds(got), want) {
		t.Errorf("order = %v, want %v", kinds(got), want)
	}
}

func TestNeedsBoundMixed(t *testing.T) {
	ps := []GraphPane{pane("m", "", "manager", "idle")}
	for i := 0; i < 60; i++ {
		p := pane(fmt.Sprintf("u%02d", i), "m", "", "failed")
		p.Failed = true
		ps = append(ps, p)
	}
	for i := 0; i < 3; i++ {
		p := pane(fmt.Sprintf("q%d", i), "m", "", "waiting")
		p.HasPrompt = true
		ps = append(ps, p)
	}
	got := rawNeeds(ps, nil, nil, nil)["m"]
	if len(got.Items) != MaxNeeds || got.Hidden != 13 {
		t.Fatalf("items %d hidden %d, want %d and 13", len(got.Items), got.Hidden, MaxNeeds)
	}
	for i := 0; i < 3; i++ {
		if got.Items[i].Kind != NeedAsk {
			t.Errorf("item %d is %s, want the prompts kept first", i, got.Items[i].Kind)
		}
	}
}

func TestNeedsSoloBounded(t *testing.T) {
	var ps []GraphPane
	for i := 0; i < 55; i++ {
		p := pane(fmt.Sprintf("s%02d", i), "", "", "failed")
		p.Failed = true
		ps = append(ps, p)
	}
	got := rawNeeds(ps, nil, nil, nil)[""]
	if len(got.Items) != MaxNeeds || got.Hidden != 5 {
		t.Errorf("items %d hidden %d", len(got.Items), got.Hidden)
	}
}

func TestNeedsDuplicatePaneIDs(t *testing.T) {
	ps := team2()
	ps[1].Failed = true
	ps = append(ps, ps[1])
	if got := needsFor(ps, nil, nil, nil)["m"]; len(got) != 1 {
		t.Errorf("items = %+v", got)
	}
}

func TestNeedsStaleUnreadNeedsTime(t *testing.T) {
	ps := team2()
	ps[1].Unread, ps[1].UnreadMail = 3, 3 // no UnreadSince
	if got := needsFor(ps, nil, nil, nil); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestFailureReasonIsCarried(t *testing.T) {
	ps := team2()
	ps[1].Failed, ps[1].Exited, ps[1].FailureReason = true, true, "exit code 2"
	g := BuildGraph(ps, nil)
	if n, _ := g.Node("a"); n.WaitingOn.Text != "failed: exit code 2" {
		t.Errorf("waiting on = %+v", n.WaitingOn)
	}
	got := needsFor(ps, nil, nil, nil)["m"]
	if len(got) != 1 || got[0].Text != "failed: exit code 2" {
		t.Errorf("items = %+v", got)
	}
}

func TestNeedsSetup(t *testing.T) {
	ps := []GraphPane{
		pane("m", "", "manager", "idle"),
		pane("unread", "m", "", "idle"),
		pane("down", "m", "", "failed"),
		pane("setup", "m", "", "blocked"),
	}
	ps[1].Unread, ps[1].UnreadMail, ps[1].UnreadSince = 1, 1, t0.Add(-time.Hour)
	ps[2].Failed, ps[2].Exited = true, true
	ps[3].SetupText = strings.Repeat("Install Node. ", 40)
	ps[3].StatusSince = t0.Add(-time.Minute)

	got := needsFor(ps, nil, nil, nil)["m"]
	if want := []string{"down:down", "setup:setup", "stale-unread:unread"}; !reflect.DeepEqual(kinds(got), want) {
		t.Fatalf("order = %v, want %v", kinds(got), want)
	}
	s := got[1]
	if s.ID != "setup:setup" || !s.Time.Equal(t0.Add(-time.Minute)) {
		t.Errorf("item = %+v", s)
	}
	if n := len([]rune(s.Text)); n == 0 || n > MaxWaitText {
		t.Errorf("text is %d characters, want 1 to %d", n, MaxWaitText)
	}

	// The same pane gives the same id on the next read, and the item is gone
	// once the pane no longer needs setup.
	if again := needsFor(ps, nil, nil, nil)["m"]; again[1].ID != s.ID {
		t.Errorf("id changed: %q then %q", s.ID, again[1].ID)
	}
	ps[3].SetupText, ps[3].Activity = "", "working"
	if left := kinds(needsFor(ps, nil, nil, nil)["m"]); !reflect.DeepEqual(left, []string{"down:down", "stale-unread:unread"}) {
		t.Errorf("after the start = %v", left)
	}
}

func TestASetupPaneWaitsOnTheUser(t *testing.T) {
	ps := team2()
	ps[1].Activity, ps[1].SetupText = "blocked", "Install Node 20 or later. Then start the pane again."
	for _, n := range BuildGraph(ps, nil).Nodes {
		if n.Pane == "a" && (n.WaitingOn.Kind != WaitUser || n.WaitingOn.Text != ps[1].SetupText) {
			t.Errorf("waiting on = %+v", n.WaitingOn)
		}
	}
}

func TestNeedsStuck(t *testing.T) {
	ps := team2()
	ps = append(ps, pane("c", "m", "worker", "working"), pane("d", "m", "worker", "working"), pane("e", "m", "worker", "working"), pane("f", "m", "worker", "waiting"))
	ps[3].LastOutput = t0.Add(-11 * time.Minute) // quiet for over ten minutes
	ps[4].LastOutput = t0.Add(-9 * time.Minute)  // not yet
	// ps[5] has no output time, as a structured pane has none: never stuck.
	ps[6].LastOutput = t0.Add(-time.Hour) // not working, so it is a wait the person already sees
	ps[1].LastOutput = t0.Add(-time.Hour) // idle: finished, not stuck
	got := needsFor(ps, nil, nil, nil)["m"]
	if len(got) != 1 || got[0].ID != "stuck:c" || got[0].Text != "no output for 11m" {
		t.Errorf("items = %+v, want only the working pane quiet for over ten minutes", got)
	}
	if got[0].Time != ps[3].LastOutput {
		t.Errorf("time = %v, want when it last wrote", got[0].Time)
	}
	// A stuck pane sorts after the items that ask something of the person.
	ps[2].Unread, ps[2].UnreadMail, ps[2].UnreadSince = 1, 1, t0.Add(-time.Hour)
	if k := kinds(needsFor(ps, nil, nil, nil)["m"]); !reflect.DeepEqual(k, []string{"stale-unread:b", "stuck:c"}) {
		t.Errorf("order = %v", k)
	}
}

func TestQuietFor(t *testing.T) {
	for d, want := range map[time.Duration]string{11 * time.Minute: "11m", 60 * time.Minute: "1h", 125 * time.Minute: "2h 5m"} {
		if got := quietFor(d); got != want {
			t.Errorf("quietFor(%v) = %q, want %q", d, got, want)
		}
	}
}
