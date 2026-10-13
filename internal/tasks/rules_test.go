package tasks

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/team"
)

// allowed is the table written out again by hand, as pairs, so a change to
// Transitions has to be made twice.
var allowed = map[string]bool{
	"planned>assigned": true, "planned>in-progress": true, "planned>blocked": true, "planned>cancelled": true,
	"assigned>planned": true, "assigned>in-progress": true, "assigned>blocked": true, "assigned>cancelled": true,
	"in-progress>in-review": true, "in-progress>done": true, "in-progress>blocked": true, "in-progress>cancelled": true,
	"in-review>in-progress": true, "in-review>done": true, "in-review>blocked": true, "in-review>cancelled": true,
	"blocked>planned": true, "blocked>assigned": true, "blocked>in-progress": true, "blocked>cancelled": true,
	"done>in-progress":  true,
	"cancelled>planned": true,
}

func TestEveryPairOfStatesIsAllowedOrRefused(t *testing.T) {
	var n int
	for _, from := range States {
		for _, to := range States {
			n++
			_, ok := Find(from, to)
			want := allowed[fmt.Sprintf("%s>%s", from, to)]
			if ok != want {
				t.Errorf("%s -> %s: table says %v, want %v", from, to, ok, want)
			}
			// What the manager can do is exactly what the table has, with a note
			// given and an owner on the task.
			task := Task{ID: "T1", State: from, OwnerPane: w1.Pane, OwnerRole: "worker", Creator: planner}
			err := CheckMove(task, to, mgr, true, "because")
			switch {
			case from == to:
				if err == nil {
					t.Errorf("%s -> itself was allowed", from)
				}
			case want && err != nil:
				t.Errorf("manager: %s -> %s refused: %v", from, to, err)
			case !want && err == nil:
				t.Errorf("manager: %s -> %s allowed", from, to)
			}
		}
	}
	if n != 49 {
		t.Fatalf("walked %d pairs", n)
	}
	if len(Transitions) != len(allowed) {
		t.Errorf("%d rules, %d expected pairs; a pair is listed twice or missing", len(Transitions), len(allowed))
	}
}

func TestEveryRuleNamesWhoMayAndNoPairIsRepeated(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Transitions {
		key := string(r.From) + ">" + string(r.To)
		if seen[key] {
			t.Errorf("%s is listed twice", key)
		}
		seen[key] = true
		if r.Who == 0 {
			t.Errorf("%s has nobody who may make it", key)
		}
		if r.Who&Manager == 0 {
			t.Errorf("%s cannot be made by the manager, who must always be able to untangle a board", key)
		}
	}
}

func TestReviewIsBinding(t *testing.T) {
	task := func(s State) Task {
		return Task{ID: "T1", State: s, OwnerPane: w1.Pane, OwnerRole: "worker", Creator: planner}
	}
	tester := Actor{Pane: "pt", Role: "tester", Name: "prober"}
	free := Actor{Pane: "free", Name: "no role"}
	// From review the manager, a reviewer or a tester closes it; the owner, who
	// is the one that asked for review, does not.
	for _, who := range []Actor{mgr, rev, tester} {
		if err := CheckMove(task(InReview), Done, who, true, ""); err != nil {
			t.Errorf("in-review -> done by %s: %v", who.Label(), err)
		}
	}
	for _, who := range []Actor{w1, w2, planner, free, {Role: "worker"}} {
		if err := CheckMove(task(InReview), Done, who, true, ""); err == nil {
			t.Errorf("in-review -> done by %s was allowed", who.Label())
		}
	}
	// From progress only the manager closes it; the owner moves it to review.
	if err := CheckMove(task(InProgress), Done, mgr, true, ""); err != nil {
		t.Errorf("in-progress -> done by the manager: %v", err)
	}
	for _, who := range []Actor{w1, w2, rev, tester, planner, free} {
		if err := CheckMove(task(InProgress), Done, who, true, ""); err == nil {
			t.Errorf("in-progress -> done by %s was allowed", who.Label())
		}
	}
	if err := CheckMove(task(InProgress), InReview, w1, true, ""); err != nil {
		t.Errorf("the owner could not send its work to review: %v", err)
	}
	for _, from := range []State{Planned, Assigned, Blocked, Cancelled} {
		if err := CheckMove(task(from), Done, mgr, true, "note"); err == nil {
			t.Errorf("%s -> done was allowed", from)
		}
	}
}

func TestReopeningAndSendingBackNeedANote(t *testing.T) {
	for _, c := range []struct {
		from, to State
		by       Actor
	}{
		{Done, InProgress, mgr}, {Cancelled, Planned, mgr}, {InReview, InProgress, rev}, {InReview, InProgress, mgr}, {InReview, InProgress, w1},
	} {
		task := Task{ID: "T1", State: c.from, OwnerPane: w1.Pane, Creator: planner}
		err := CheckMove(task, c.to, c.by, true, "")
		if err == nil || !strings.Contains(err.Error(), "needs a note") {
			t.Errorf("%s -> %s with no note: %v", c.from, c.to, err)
		}
		if err := CheckMove(task, c.to, c.by, true, "  \n "); err == nil {
			t.Errorf("%s -> %s with a blank note was allowed", c.from, c.to)
		}
		if err := CheckMove(task, c.to, c.by, true, "found a bug"); err != nil {
			t.Errorf("%s -> %s with a note: %v", c.from, c.to, err)
		}
	}
	// A worker may not reopen a done task, and a reviewer may not either.
	done := Task{ID: "T1", State: Done, OwnerPane: w1.Pane, Creator: planner}
	for _, who := range []Actor{w1, rev, planner} {
		if err := CheckMove(done, InProgress, who, true, "again"); err == nil {
			t.Errorf("%s reopened a done task", who.Label())
		}
	}
}

func TestWhoMayMoveWhat(t *testing.T) {
	owned := Task{ID: "T1", State: Assigned, OwnerPane: w1.Pane, OwnerRole: "worker", Creator: planner}
	if err := CheckMove(owned, InProgress, w1, true, ""); err != nil {
		t.Errorf("the owner could not start its task: %v", err)
	}
	if err := CheckMove(owned, InProgress, w2, true, ""); err == nil {
		t.Error("another worker started the task")
	}
	if err := CheckMove(owned, InProgress, rev, true, ""); err == nil {
		t.Error("a reviewer started the task")
	}
	if err := CheckMove(owned, Cancelled, planner, true, ""); err != nil {
		t.Errorf("the creator could not cancel: %v", err)
	}
	if err := CheckMove(owned, Cancelled, w1, true, ""); err == nil {
		t.Error("the owner cancelled a task it did not create")
	}
	if err := CheckMove(owned, Planned, w1, true, ""); err == nil {
		t.Error("a worker unassigned itself")
	}
	// A task for a role, not yet taken, may be started by any pane in the role.
	forRole := Task{ID: "T2", State: Assigned, OwnerRole: "worker", Creator: planner}
	if err := CheckMove(forRole, InProgress, w2, true, ""); err != nil {
		t.Errorf("a worker could not take a task for its role: %v", err)
	}
	if err := CheckMove(forRole, InProgress, rev, true, ""); err == nil {
		t.Error("a reviewer took a task for workers")
	}
	// A pane with no role acts as the manager only while no manager pane is live.
	hand := Actor{Pane: "hand", Name: "by hand"}
	if err := CheckMove(owned, Planned, hand, false, ""); err != nil {
		t.Errorf("an agent with no role was refused with no manager running: %v", err)
	}
	if err := CheckMove(owned, Planned, hand, true, ""); err == nil {
		t.Error("an agent with no role had the manager's powers while a manager was running")
	}
	// "assigned" needs an owner, and the error says how to get one.
	bare := Task{ID: "T3", State: Planned}
	if err := CheckMove(bare, Assigned, mgr, true, ""); err == nil || !strings.Contains(err.Error(), "task assign") {
		t.Errorf("err = %v", err)
	}
	// The refusal says where a task can go from where it is.
	err := CheckMove(Task{ID: "T4", State: Planned}, InReview, mgr, true, "")
	if err == nil || !strings.Contains(err.Error(), "from planned it can go to assigned, in-progress, blocked, cancelled") {
		t.Errorf("err = %v", err)
	}
}

func TestRolelessPanesHaveManagerPowersOnlyWithoutALiveManager(t *testing.T) {
	hand := Actor{Pane: "hand", Name: "helper"}
	roster := []team.Member{
		{Pane: "pm", Role: "manager", Status: "working"},
		{Pane: "hand", Name: "helper", Status: "idle"},
	}
	done := Task{ID: "T1", State: Done, OwnerPane: w1.Pane, Creator: planner}
	review := Task{ID: "T2", State: InReview, OwnerPane: w1.Pane, Creator: planner}
	open := Task{ID: "T3", State: Planned, Creator: planner}
	powers := func(hasManager bool) (assign, reopen, cancel, closeIt, prune bool) {
		return CheckAssign(open, hand, hasManager) == nil,
			CheckMove(done, InProgress, hand, hasManager, "again") == nil,
			CheckMove(open, Cancelled, hand, hasManager, "") == nil,
			CheckMove(review, Done, hand, hasManager, "") == nil,
			CheckPrune(hand, hasManager) == nil
	}
	// A manager pane is live: the helper can do none of it.
	if !HasManager(roster) {
		t.Fatal("a live manager pane was not seen")
	}
	if a, r, c, d, p := powers(HasManager(roster)); a || r || c || d || p {
		t.Errorf("with a manager: assign %v, reopen %v, cancel %v, close %v, prune %v", a, r, c, d, p)
	}
	// The manager pane exits: the helper has the powers back.
	roster[0].Status = "exited"
	if HasManager(roster) {
		t.Fatal("an exited manager pane counted")
	}
	if a, r, c, d, p := powers(HasManager(roster)); !a || !r || !c || !d || !p {
		t.Errorf("without a manager: assign %v, reopen %v, cancel %v, close %v, prune %v", a, r, c, d, p)
	}
	// A new manager pane takes them away again.
	roster = append(roster, team.Member{Pane: "pm2", Role: "manager", Status: "idle"})
	if a, r, c, d, p := powers(HasManager(roster)); a || r || c || d || p {
		t.Errorf("with a second manager: assign %v, reopen %v, cancel %v, close %v, prune %v", a, r, c, d, p)
	}
	// A pane in the manager role has them whatever the roster says.
	if CheckAssign(open, mgr, false) != nil || CheckPrune(mgr, true) != nil {
		t.Error("the manager role lost its powers")
	}
	if HasManager(nil) {
		t.Error("an empty roster has a manager")
	}
}

func TestAssignAndEditPermissions(t *testing.T) {
	open := Task{ID: "T1", State: Planned, Creator: planner}
	if err := CheckAssign(open, mgr, true); err != nil {
		t.Error(err)
	}
	for _, who := range []Actor{w1, rev, planner} {
		if err := CheckAssign(open, who, true); err == nil {
			t.Errorf("%s assigned a task", who.Label())
		}
	}
	if err := CheckAssign(Task{ID: "T1", State: Done}, mgr, true); err == nil {
		t.Error("a done task was assigned")
	}
	owned := Task{ID: "T1", State: InProgress, OwnerPane: w1.Pane, Creator: planner}
	for _, who := range []Actor{w1, planner, mgr} {
		if err := CheckEdit(owned, who, true); err != nil {
			t.Errorf("%s could not edit: %v", who.Label(), err)
		}
	}
	for _, who := range []Actor{w2, rev} {
		if err := CheckEdit(owned, who, true); err == nil {
			t.Errorf("%s edited a task that is not theirs", who.Label())
		}
	}
	if err := CheckEdit(Task{ID: "T1", State: Cancelled}, mgr, true); err == nil {
		t.Error("a cancelled task was edited")
	}
}

func TestBoardOperationsApplyThePermissions(t *testing.T) {
	b, _ := testBoard(t)
	task := mustAdd(t, b, planner, "do it")
	if _, err := b.Assign(w1, true, task.ID, Assignee{Pane: w1.Pane, Role: "worker"}); err == nil {
		t.Error("a worker assigned a task to itself")
	}
	ch, err := b.Assign(mgr, true, task.ID, Assignee{Pane: w1.Pane, Role: "worker", Name: "dev-one"})
	if err != nil || ch.Task.State != Assigned || ch.Task.OwnerPane != w1.Pane || ch.Task.OwnerName != "dev-one" {
		t.Fatalf("assign = %+v, %v", ch, err)
	}
	if _, err := b.SetState(w2, true, task.ID, InProgress, ""); err == nil {
		t.Error("another worker started it")
	}
	if _, err := b.SetState(w1, true, task.ID, InProgress, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetBranch(w2, true, task.ID, "feat/x"); err == nil {
		t.Error("another worker set the branch")
	}
	if _, err := b.SetBranch(w1, true, task.ID, "feat/x"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetState(w1, true, task.ID, InReview, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetState(w1, true, task.ID, Done, ""); err == nil {
		t.Error("the owner closed a task that is in review")
	}
	if _, err := b.SetState(rev, true, task.ID, InProgress, ""); err == nil {
		t.Error("the reviewer sent it back without a note")
	}
	back, err := b.SetState(rev, true, task.ID, InProgress, "the parser drops the last line")
	if err != nil || back.Task.State != InProgress || len(back.Task.Notes) != 1 || back.Task.Notes[0].Author != rev {
		t.Fatalf("send back = %+v, %v", back, err)
	}
	// A failed move changes nothing on disk.
	before, _ := b.Get(task.ID)
	_, _ = b.SetState(w2, true, task.ID, Done, "")
	if after, _ := b.Get(task.ID); !after.Updated.Equal(before.Updated) || after.State != before.State {
		t.Errorf("a refused move changed the task: %+v -> %+v", before, after)
	}
	// Anyone on the team may add a note, to a task in any state.
	if _, err := b.SetState(mgr, true, task.ID, Cancelled, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddNote(w2, task.ID, "for the record"); err != nil {
		t.Errorf("note on a cancelled task: %v", err)
	}
}

func TestAPaneInTheOwnerRoleClaimsATaskByStartingIt(t *testing.T) {
	b, _ := testBoard(t)
	task := mustAdd(t, b, planner, "any worker")
	if _, err := b.Assign(mgr, true, task.ID, Assignee{Role: "worker"}); err != nil {
		t.Fatal(err)
	}
	ch, err := b.SetState(w2, true, task.ID, InProgress, "")
	if err != nil || !ch.Claimed || ch.Task.OwnerPane != w2.Pane || ch.Task.OwnerName != w2.Name {
		t.Fatalf("claim = %+v, %v", ch, err)
	}
	if _, err := b.SetState(w1, true, task.ID, InReview, ""); err == nil {
		t.Error("a second worker moved a task the first had taken")
	}
}

func TestStartingATaskThatHasAPaneOwnerDoesNotChangeTheOwner(t *testing.T) {
	b, _ := testBoard(t)
	task := mustAdd(t, b, planner, "for w1")
	_, _ = b.Assign(mgr, true, task.ID, Assignee{Pane: w1.Pane, Role: "worker", Name: "dev-one"})
	ch, err := b.SetState(mgr, true, task.ID, InProgress, "")
	if err != nil || ch.Claimed || ch.Task.OwnerPane != w1.Pane {
		t.Errorf("manager start = %+v, %v", ch, err)
	}
}
