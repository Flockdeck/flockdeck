package tasks

import (
	"path/filepath"
	"strings"
	"testing"
)

// The person at the window is an actor with the manager's powers and no pane. The
// rules table is the one every other actor is held to; these tests walk it with
// the user in it.

func TestTheUserMayMakeExactlyTheMovesTheManagerMay(t *testing.T) {
	user := UserActor()
	for _, hasManager := range []bool{true, false} {
		for _, from := range States {
			task := Task{ID: "T1", State: from, OwnerPane: w1.Pane, OwnerRole: "worker", Creator: planner}
			moves := Moves(task, user, hasManager)
			got := map[State]bool{}
			for _, m := range moves {
				got[m.To] = true
				if r, _ := Find(from, m.To); m.NeedNote != r.NeedNote {
					t.Errorf("%s -> %s: NeedNote = %v, the table says %v", from, m.To, m.NeedNote, r.NeedNote)
				}
			}
			for _, to := range States {
				_, inTable := Find(from, to)
				// Every rule in the table can be made by the manager, so the user may
				// make every rule in it, with a note given where one is needed.
				if got[to] != inTable {
					t.Errorf("hasManager=%v %s -> %s: offered %v, table has it %v", hasManager, from, to, got[to], inTable)
				}
				err := CheckMove(task, to, user, hasManager, "because")
				if (err == nil) != inTable {
					t.Errorf("%s -> %s: CheckMove = %v, table has it %v", from, to, err, inTable)
				}
				// The same pair for the manager pane gives the same answer.
				if merr := CheckMove(task, to, mgr, true, "because"); (merr == nil) != (err == nil) {
					t.Errorf("%s -> %s: user %v, manager %v", from, to, err, merr)
				}
			}
		}
	}
}

func TestMovesOfferOnlyWhatAnActorMayMake(t *testing.T) {
	task := Task{ID: "T1", State: InReview, OwnerPane: w1.Pane, OwnerRole: "worker", Creator: planner}
	names := func(ms []Allowed) string {
		var out []string
		for _, m := range ms {
			s := string(m.To)
			if m.NeedNote {
				s += "+note"
			}
			out = append(out, s)
		}
		return strings.Join(out, ",")
	}
	// The owner may send it back or block it, and cannot close it: review is
	// binding.
	if got := names(Moves(task, w1, true)); got != "in-progress+note,blocked" {
		t.Errorf("owner in review: %s", got)
	}
	if got := names(Moves(task, rev, true)); got != "in-progress+note,done" {
		t.Errorf("reviewer in review: %s", got)
	}
	if got := names(Moves(task, UserActor(), true)); got != "in-progress+note,done,blocked,cancelled" {
		t.Errorf("user in review: %s", got)
	}
	// Assigned needs an owner, so a task with none is not offered it.
	unowned := Task{ID: "T2", State: Blocked, Creator: planner}
	if got := names(Moves(unowned, UserActor(), true)); got != "planned,in-progress,cancelled" {
		t.Errorf("user on an unowned blocked task: %s", got)
	}
	// A pane that has no part in a task is offered nothing.
	if got := Moves(task, w2, true); len(got) != 0 {
		t.Errorf("a stranger is offered %v", got)
	}
}

func TestTheUserIsNeitherOwnerNorCreatorAndHasNoPane(t *testing.T) {
	user := UserActor()
	if user.Pane != "" || user.Role != "" || !user.User {
		t.Fatalf("user = %+v", user)
	}
	task := Task{ID: "T1", State: Planned, OwnerRole: "worker", Creator: user}
	if IsOwner(task, user) || IsCreator(task, user) {
		t.Error("the user is the owner or creator of a task it made")
	}
	if got := Capacities(task, user, true); got != Manager {
		t.Errorf("capacities = %v, want only the manager's", got)
	}
	if user.Label() != "the user (at the window)" {
		t.Errorf("label = %q", user.Label())
	}
	// An actor with the flag unset and no role is the manager only while no pane is.
	free := Actor{Pane: "x", Name: "free"}
	if IsManager(free, true) || !IsManager(free, false) {
		t.Error("a pane with no role changed meaning")
	}
}

func TestChangesByTheUserAreTold(t *testing.T) {
	user := UserActor()
	// Done: the creator pane and the manager pane hear of it, and the user, who has
	// no pane, is not among them.
	done := task(Done)
	ns := Notices(Change{Kind: Move, Task: done, From: InReview}, user, roster())
	if who(ns) != "pm,pp" || !strings.Contains(ns[0].Text, "is done, by the user (at the window)") && !strings.Contains(ns[1].Text, "is done, by the user (at the window)") {
		t.Errorf("done: %+v", ns)
	}
	blocked := task(Blocked)
	ns = Notices(Change{Kind: Move, Task: blocked, From: InProgress, Note: "waiting on a key"}, user, roster())
	if who(ns) != "pm,pp,w1" || !strings.Contains(ns[0].Text, "blocked, by the user (at the window)") {
		t.Errorf("blocked: %+v", ns)
	}
	ns = Notices(Change{Kind: NoteKind, Task: task(InReview), Note: "check the error path"}, user, roster())
	if who(ns) != "pm,pp,w1" || !strings.Contains(ns[0].Text, "note on task T3 from the user (at the window): check the error path") {
		t.Errorf("note: %+v", ns)
	}
	// Review with a reviewer running goes to the reviewers.
	ns = Notices(Change{Kind: Move, Task: task(InReview), From: InProgress}, user, roster())
	if who(ns) != "pr,pr2" {
		t.Errorf("review: %+v", ns)
	}
}

func TestHistoryRecordsWhoDidWhatAndKeepsTheLastEntries(t *testing.T) {
	b, _ := testBoard(t)
	user := UserActor()
	task := mustAdd(t, b, user, "plan it")
	if len(task.History) != 1 || task.History[0].What != "added the task" || !task.History[0].Actor.User {
		t.Fatalf("history = %+v", task.History)
	}
	if _, err := b.Assign(user, true, "T1", Assignee{Pane: w1.Pane, Role: "worker", Name: "dev-one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetBranch(user, true, "T1", "feat/x"); err != nil {
		t.Fatal(err)
	}
	// MaxHistory rounds of moves overflow the history, and the first
	// entries go.
	for i := 0; i < MaxHistory; i++ {
		to := InProgress
		if i%2 == 1 {
			to = Blocked
		}
		if _, err := b.SetState(user, true, "T1", to, ""); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}
	got, err := b.Get("T1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.History) != MaxHistory {
		t.Fatalf("history holds %d entries, want %d", len(got.History), MaxHistory)
	}
	if got.History[0].What == "added the task" {
		t.Error("the oldest entry was kept")
	}
	last := got.History[len(got.History)-1]
	if !strings.HasPrefix(last.What, "moved the task from ") {
		t.Errorf("last = %+v", last)
	}
	// A note adds to the thread and not to the history.
	n := len(got.History)
	if _, err := b.AddNote(user, "T1", "hello"); err != nil {
		t.Fatal(err)
	}
	if again, _ := b.Get("T1"); len(again.History) != n || len(again.Notes) != 1 {
		t.Errorf("after a note: %d history, %d notes", len(again.History), len(again.Notes))
	}
}

func TestAReadOfAnUnchangedBoardDoesNotParseItAgain(t *testing.T) {
	b, _ := testBoard(t)
	mustAdd(t, b, mgr, "one")
	list := func() int {
		l, _, err := b.List(Filter{All: true})
		if err != nil {
			t.Fatal(err)
		}
		return len(l)
	}
	list() // the first read after a write parses the file
	loads := b.Loads()
	for i := 0; i < 50; i++ {
		if list() != 1 {
			t.Fatal("wrong list")
		}
	}
	if b.Loads() != loads {
		t.Errorf("the file was parsed %d more times with nothing written", b.Loads()-loads)
	}
	// A write through the board shows at once.
	mustAdd(t, b, mgr, "two")
	if list() != 2 {
		t.Error("a task added through the board was not read")
	}
	// A write by another process shows too, by the file's stamp.
	other := Open(filepath.Dir(filepath.Dir(filepath.Dir(b.Path))))
	if _, err := other.Add(mgr, NewTask{Title: "three, from elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if list() != 3 {
		t.Error("a task added by another process was not read")
	}
	// A change a reader got is not changed by the next write: the cache is not
	// handed out to be mutated.
	got, _, _ := b.List(Filter{All: true})
	mustAdd(t, b, mgr, "four")
	if len(got) != 3 {
		t.Errorf("a list already returned changed to %d tasks", len(got))
	}
}
