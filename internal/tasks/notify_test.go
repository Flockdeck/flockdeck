package tasks

import (
	"sort"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/team"
)

func roster() []team.Member {
	return []team.Member{
		{Pane: "pm", Name: "boss", Role: "manager", Status: "working"},
		{Pane: "pp", Name: "plan", Role: "planner", Status: "idle"},
		{Pane: "w1", Name: "dev-one", Role: "worker", Status: "working"},
		{Pane: "w2", Name: "dev-two", Role: "worker", Status: "idle"},
		{Pane: "pr", Name: "eyes", Role: "reviewer", Status: "idle"},
		{Pane: "pr2", Name: "eyes-two", Role: "reviewer", Status: "idle"},
		{Pane: "gone", Name: "old", Role: "reviewer", Status: "exited"},
	}
}

func who(ns []Notice) string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Pane)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func task(state State) Task {
	return Task{ID: "T3", Title: "fix the parser", State: state, OwnerPane: "w1", OwnerRole: "worker", Creator: Actor{Pane: "pp", Role: "planner", Name: "plan"}, Branch: "feat/parser"}
}

func TestAssignTellsTheNewOwnerAndNobodyElse(t *testing.T) {
	ns := Notices(Change{Kind: Assign, Task: task(Assigned)}, mgr, roster())
	if who(ns) != "w1" || ns[0].Text != "task T3 assigned to you: fix the parser" {
		t.Errorf("notices = %+v", ns)
	}
	// A task for a role goes to its live panes, the exited one and the actor left out.
	forRole := task(Assigned)
	forRole.OwnerPane = ""
	forRole.OwnerRole = "reviewer"
	ns = Notices(Change{Kind: Assign, Task: forRole}, mgr, roster())
	if who(ns) != "pr,pr2" || !strings.Contains(ns[0].Text, "assigned to your role (reviewer)") {
		t.Errorf("role notices = %+v", ns)
	}
	// Giving a task to yourself tells nobody.
	self := task(Assigned)
	self.OwnerPane = "pm"
	if ns := Notices(Change{Kind: Assign, Task: self}, mgr, roster()); len(ns) != 0 {
		t.Errorf("notices = %+v", ns)
	}
	// A pane that is not on the roster, as one that was closed, is not told.
	gone := task(Assigned)
	gone.OwnerPane = "closed-pane"
	if ns := Notices(Change{Kind: Assign, Task: gone}, mgr, roster()); len(ns) != 0 {
		t.Errorf("notices for a closed pane = %+v", ns)
	}
	exited := task(Assigned)
	exited.OwnerPane = "gone"
	if ns := Notices(Change{Kind: Assign, Task: exited}, mgr, roster()); len(ns) != 0 {
		t.Errorf("notices for an exited pane = %+v", ns)
	}
}

func TestReviewGoesToTheReviewersOrElseTheCreator(t *testing.T) {
	ch := Change{Kind: Move, From: InProgress, Task: task(InReview)}
	ns := Notices(ch, w1, roster())
	if who(ns) != "pr,pr2" {
		t.Errorf("with reviewers: %s", who(ns))
	}
	if !strings.Contains(ns[0].Text, "task T3 is ready for review: fix the parser (branch feat/parser)") {
		t.Errorf("text = %q", ns[0].Text)
	}
	noReviewers := []team.Member{roster()[0], roster()[1], roster()[2], roster()[6]}
	if ns := Notices(ch, w1, noReviewers); who(ns) != "pp" {
		t.Errorf("without reviewers: %s", who(ns))
	}
	// The only live reviewer being the one who moved it counts as none.
	onlyMe := []team.Member{roster()[1], roster()[2], roster()[4]}
	if ns := Notices(ch, rev, onlyMe); who(ns) != "pp" {
		t.Errorf("reviewer moved it: %s", who(ns))
	}
	// A creator who moves their own task to review is not told.
	own := task(InReview)
	own.Creator = w1
	if ns := Notices(Change{Kind: Move, Task: own}, w1, noReviewers); len(ns) != 0 {
		t.Errorf("creator told of their own move: %+v", ns)
	}
	// A creator who has closed their pane is not told, and nothing is sent in its place.
	closedCreator := task(InReview)
	closedCreator.Creator = Actor{Pane: "closed", Role: "planner"}
	if ns := Notices(Change{Kind: Move, Task: closedCreator}, w1, noReviewers); len(ns) != 0 {
		t.Errorf("a closed creator was told: %+v", ns)
	}
}

func TestBlockedAndDoneTellTheCreator(t *testing.T) {
	blocked := Notices(Change{Kind: Move, Task: task(Blocked), Note: "waiting on the schema"}, w1, roster())
	if who(blocked) != "pp" || !strings.Contains(blocked[0].Text, "task T3 is blocked, by dev-one (worker): fix the parser (note: waiting on the schema)") {
		t.Errorf("blocked = %+v", blocked)
	}
	done := Notices(Change{Kind: Move, Task: task(Done)}, mgr, roster())
	if who(done) != "pp" || done[0].Text != "task T3 is done: fix the parser" {
		t.Errorf("done = %+v", done)
	}
	// The creator moving it is told nothing.
	if ns := Notices(Change{Kind: Move, Task: task(Done)}, planner, roster()); len(ns) != 0 {
		t.Errorf("notices = %+v", ns)
	}
}

func TestANoteTellsTheOwnerAndTheCreatorExceptTheAuthor(t *testing.T) {
	ch := Change{Kind: NoteKind, Task: task(InProgress), Note: "see the failing case in parser_test.go"}
	if got := who(Notices(ch, rev, roster())); got != "pp,w1" {
		t.Errorf("note by a reviewer told %s, want the owner and the creator", got)
	}
	if got := who(Notices(ch, w1, roster())); got != "pp" {
		t.Errorf("note by the owner told %s, want the creator", got)
	}
	if got := who(Notices(ch, planner, roster())); got != "w1" {
		t.Errorf("note by the creator told %s, want the owner", got)
	}
	// An owner that is a role, with no pane yet, is every live pane in it.
	forRole := task(Assigned)
	forRole.OwnerPane = ""
	got := who(Notices(Change{Kind: NoteKind, Task: forRole, Note: "x"}, rev, roster()))
	if got != "pp,w1,w2" {
		t.Errorf("note on a task for a role told %s", got)
	}
	if ns := Notices(ch, rev, roster()); !strings.HasPrefix(ns[0].Text, "note on task T3 from eyes (reviewer): see the failing case") {
		t.Errorf("text = %q", ns[0].Text)
	}
}

func TestAMoveWithANoteAlsoReachesTheOwnerOnce(t *testing.T) {
	back := Change{Kind: Move, From: InReview, Task: task(InProgress), Note: "the parser drops the last line"}
	ns := Notices(back, rev, roster())
	if who(ns) != "pp,w1" {
		t.Errorf("send back told %s", who(ns))
	}
	for _, n := range ns {
		if !strings.Contains(n.Text, "is now in-progress, by eyes (reviewer)") || !strings.Contains(n.Text, "the parser drops the last line") {
			t.Errorf("text = %q", n.Text)
		}
	}
	// One message to a pane for one change, however many rules reach it.
	blocked := Change{Kind: Move, Task: task(Blocked), Note: "why"}
	ns = Notices(blocked, w1, roster())
	count := map[string]int{}
	for _, n := range ns {
		count[n.Pane]++
	}
	for p, c := range count {
		if c != 1 {
			t.Errorf("pane %s was told %d times", p, c)
		}
	}
	// Cancelling tells the owner, who may be working on it.
	cancelled := Notices(Change{Kind: Move, Task: task(Cancelled)}, mgr, roster())
	if who(cancelled) != "w1" || !strings.Contains(cancelled[0].Text, "was cancelled by boss (manager)") {
		t.Errorf("cancelled = %+v", cancelled)
	}
}

func TestOtherChangesTellNobody(t *testing.T) {
	for _, k := range []Kind{Added, Branch, Baton} {
		if ns := Notices(Change{Kind: k, Task: task(Planned)}, mgr, roster()); len(ns) != 0 {
			t.Errorf("%s told %+v", k, ns)
		}
	}
	// A move to in-progress with no note is silent.
	if ns := Notices(Change{Kind: Move, Task: task(InProgress)}, w1, roster()); len(ns) != 0 {
		t.Errorf("a start told %+v", ns)
	}
}

func TestNoticeTextCarriesNoControlCharacters(t *testing.T) {
	evil := task(Blocked)
	evil.Title, _ = CleanTitle("fix\x1b[31m the\nparser\r\n")
	actor := Actor{Pane: "w1", Role: "worker", Name: "dev\x1b]0;x\x07\nsecond line"}
	ns := Notices(Change{Kind: Move, Task: evil, Note: "line one\nline two\x1b[2J"}, actor, roster())
	if len(ns) == 0 {
		t.Fatal("no notices")
	}
	for _, n := range ns {
		for _, r := range n.Text {
			if r < ' ' || r == 0x7f {
				t.Errorf("notice has control character %q: %q", r, n.Text)
			}
		}
	}
}

func TestResolveAssignee(t *testing.T) {
	ms := roster()
	ms[2].Peer = "alpha"
	roles := []string{"manager", "planner", "worker", "reviewer", "tester", "docs"}
	cases := []struct {
		target string
		want   Assignee
		err    string
	}{
		{"w2", Assignee{Pane: "w2", Role: "worker", Name: "dev-two"}, ""},
		{"alpha", Assignee{Pane: "w1", Role: "worker", Name: "alpha"}, ""},
		{"ALPHA", Assignee{Pane: "w1", Role: "worker", Name: "alpha"}, ""},
		{"dev-two", Assignee{Pane: "w2", Role: "worker", Name: "dev-two"}, ""},
		{"boss", Assignee{Pane: "pm", Role: "manager", Name: "boss"}, ""},
		{"tester", Assignee{Role: "tester"}, ""},
		{"role:worker", Assignee{Role: "worker"}, ""},
		{"worker", Assignee{Role: "worker"}, ""},
		{"", Assignee{}, "say who"},
		{"nobody", Assignee{}, "no pane, name or role matches"},
		{"role:nobody", Assignee{}, "no role called"},
		{"role:", Assignee{}, "needs a role id"},
		{"old", Assignee{}, "has exited"},
		{"gone", Assignee{}, "has exited"},
	}
	for _, c := range cases {
		got, err := ResolveAssignee(ms, roles, c.target)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err = %v, want %q", c.target, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: got %+v, %v; want %+v", c.target, got, err, c.want)
		}
	}
	// A name that two panes share is refused with the panes listed.
	dup := append(roster(), team.Member{Pane: "w3", Name: "dev-one", Role: "worker", Status: "idle"})
	if _, err := ResolveAssignee(dup, roles, "dev-one"); err == nil || !strings.Contains(err.Error(), "name of 2 panes") || !strings.Contains(err.Error(), "w3") {
		t.Errorf("err = %v", err)
	}
	// A pane's own name matching a role id does not shadow the pane.
	named := []team.Member{{Pane: "x1", Name: "worker", Role: "docs", Status: "idle"}}
	if got, _ := ResolveAssignee(named, roles, "worker"); got.Pane != "x1" {
		t.Errorf("got %+v, want the pane called worker", got)
	}
}
