package tasks

import (
	"fmt"
	"strings"

	"github.com/jmwri/flockdeck/internal/team"
)

// This file is the rule table: which moves between states exist, and who may
// make each. Nothing else in the package decides either, and the tests walk
// every pair of states against it.

// Who is a set of the capacities in which an actor may do something to a task.
type Who uint8

// The capacities.
const (
	// Manager is an actor in the manager role. An agent with no role has no
	// limits in Flockdeck's role model, so it is treated as the manager would be,
	// but only while its project has no live pane in the manager role. The user at
	// the window is always one.
	Manager Who = 1 << iota
	// Owner is the pane the task is assigned to, or any pane in the role the
	// task is assigned to when no pane has taken it yet.
	Owner
	// Creator is the pane that added the task.
	Creator
	// Reviewer is an actor in the reviewer or tester role.
	Reviewer
)

// capacityNames are the words for each capacity in a sentence, in the order they
// are listed.
var capacityNames = []struct {
	w          Who
	need, have string
}{
	{Manager, "a manager", "a manager"},
	{Owner, "the owner", "the owner"},
	{Creator, "the creator", "the creator"},
	{Reviewer, "a reviewer or a tester", "a reviewer or tester"},
}

// list joins words with a comma and then the last with word.
func list(parts []string, word string) string {
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " " + word + " " + parts[len(parts)-1]
}

// String is the capacities as a phrase for what a move needs: "a manager, the
// owner or the creator".
func (w Who) String() string {
	var parts []string
	for _, c := range capacityNames {
		if w&c.w != 0 {
			parts = append(parts, c.need)
		}
	}
	return list(parts, "or")
}

// held says what the actor is to the task: "you are the owner and the creator
// of this task", or that the actor has no part in it.
func (w Who) held() string {
	var parts []string
	for _, c := range capacityNames {
		if w&c.w != 0 {
			parts = append(parts, c.have)
		}
	}
	if len(parts) == 0 {
		return "you have no part in this task"
	}
	return "you are " + list(parts, "and") + " of this task"
}

// refusal is the one wording of a move the actor may not make: what the move
// needs, then what the actor is.
func refusal(t Task, to State, need, have Who) error {
	return fmt.Errorf("moving %s from %s to %s is for %s; %s", t.ID, t.State, to, need, have.held())
}

// Rule is one allowed move from a state to another.
type Rule struct {
	From, To State
	// Who may make the move. An actor in any of these capacities may.
	Who Who
	// NeedNote says the move must carry a note that says why. It is the case for
	// every move that undoes progress.
	NeedNote bool
}

// Transitions is every move there is. A pair that is not here is refused.
var Transitions = []Rule{
	{Planned, Assigned, Manager, false},
	{Planned, InProgress, Manager | Owner, false},
	{Planned, Blocked, Manager | Owner, false},
	{Planned, Cancelled, Manager | Creator, false},

	{Assigned, Planned, Manager, false},
	{Assigned, InProgress, Manager | Owner, false},
	{Assigned, Blocked, Manager | Owner, false},
	{Assigned, Cancelled, Manager | Creator, false},

	{InProgress, InReview, Manager | Owner, false},
	// The owner hands its work over for review and does not close it itself.
	{InProgress, Done, Manager, false},
	{InProgress, Blocked, Manager | Owner, false},
	{InProgress, Cancelled, Manager | Creator, false},

	// Sending a task back from review is for whoever reviewed it, and says why.
	{InReview, InProgress, Manager | Owner | Reviewer, true},
	// Review is binding: the owner cannot close a task that is in review.
	{InReview, Done, Manager | Reviewer, false},
	{InReview, Blocked, Manager | Owner, false},
	{InReview, Cancelled, Manager | Creator, false},

	{Blocked, Planned, Manager, false},
	{Blocked, Assigned, Manager, false},
	{Blocked, InProgress, Manager | Owner, false},
	{Blocked, Cancelled, Manager | Creator, false},

	// Reopening a finished task is the manager's, and says why.
	{Done, InProgress, Manager, true},
	{Cancelled, Planned, Manager, true},
}

// Find returns the rule for a move, if there is one.
func Find(from, to State) (Rule, bool) {
	for _, r := range Transitions {
		if r.From == from && r.To == to {
			return r, true
		}
	}
	return Rule{}, false
}

// Next lists the states a task in from may move to.
func Next(from State) []State {
	var out []State
	for _, r := range Transitions {
		if r.From == from {
			out = append(out, r.To)
		}
	}
	return out
}

// Allowed is a move the actor may make now: the state to go to, and whether the
// move must carry a note.
type Allowed struct {
	To       State
	NeedNote bool
}

// Moves lists the moves of a task that CheckMove would let the actor make, in
// the order of the rule table, so a window offers only those. It asks CheckMove
// with a note in hand and reports NeedNote itself, so the rules are decided in
// one place.
func Moves(t Task, a Actor, hasManager bool) []Allowed {
	var out []Allowed
	for _, r := range Transitions {
		if r.From != t.State {
			continue
		}
		if CheckMove(t, r.To, a, hasManager, "x") == nil {
			out = append(out, Allowed{To: r.To, NeedNote: r.NeedNote})
		}
	}
	return out
}

// HasManager reports that the roster has a live pane in the manager role.
func HasManager(members []team.Member) bool {
	for _, m := range members {
		if m.Role == "manager" && !m.Exited() {
			return true
		}
	}
	return false
}

// IsManager reports that the actor has the manager's powers: it is the user at
// the window, it is in the manager role, or it has no role and the project has
// no live manager pane (hasManager says whether it has one).
func IsManager(a Actor, hasManager bool) bool {
	return a.User || a.Role == "manager" || a.Role == "" && !hasManager
}

// IsOwner reports that the actor is the owner of the task: the pane it is
// assigned to, or a pane in the role it is assigned to while no pane has it.
func IsOwner(t Task, a Actor) bool {
	if a.Pane == "" {
		return false
	}
	if t.OwnerPane != "" {
		return t.OwnerPane == a.Pane
	}
	return t.OwnerRole != "" && t.OwnerRole == a.Role
}

// IsCreator reports that the actor added the task.
func IsCreator(t Task, a Actor) bool { return a.Pane != "" && t.Creator.Pane == a.Pane }

// Capacities is the set of capacities an actor has toward a task.
func Capacities(t Task, a Actor, hasManager bool) Who {
	var w Who
	if IsManager(a, hasManager) {
		w |= Manager
	}
	if IsOwner(t, a) {
		w |= Owner
	}
	if IsCreator(t, a) {
		w |= Creator
	}
	if a.Role == "reviewer" || a.Role == "tester" {
		w |= Reviewer
	}
	return w
}

// CheckMove says whether the actor may move the task to a state, with the note
// they wrote for it (empty for none).
func CheckMove(t Task, to State, a Actor, hasManager bool, note string) error {
	if t.State == to {
		return fmt.Errorf("%s is already %s", t.ID, to)
	}
	r, ok := Find(t.State, to)
	if !ok {
		next := Next(t.State)
		names := make([]string, len(next))
		for i, s := range next {
			names[i] = string(s)
		}
		return fmt.Errorf("%s is %s and cannot go straight to %s; from %s it can go to %s",
			t.ID, t.State, to, t.State, strings.Join(names, ", "))
	}
	if have := Capacities(t, a, hasManager); have&r.Who == 0 {
		return refusal(t, to, r.Who, have)
	}
	if r.NeedNote && strings.TrimSpace(note) == "" {
		return fmt.Errorf("moving %s from %s to %s needs a note that says why: add -note <text>", t.ID, t.State, to)
	}
	if to == Assigned && !t.Owned() {
		return fmt.Errorf("%s has no owner; `flockdeck task assign %s <role|name|pane>` assigns it", t.ID, t.ID)
	}
	return nil
}

// CheckPrune says whether the actor may remove old finished tasks: the manager.
func CheckPrune(a Actor, hasManager bool) error {
	if !IsManager(a, hasManager) {
		return fmt.Errorf("only the manager role prunes the board")
	}
	return nil
}

// CheckAssign says whether the actor may give the task an owner. Only the
// manager does, and only while the task is open.
func CheckAssign(t Task, a Actor, hasManager bool) error {
	if !t.State.Open() {
		return fmt.Errorf("%s is %s; reopen it before assigning it", t.ID, t.State)
	}
	if !IsManager(a, hasManager) {
		return fmt.Errorf("only the manager role assigns tasks; ask the manager to assign %s", t.ID)
	}
	return nil
}

// CheckEdit says whether the actor may change the branch or baton of a task: its
// owner, its creator or the manager, while it is open.
func CheckEdit(t Task, a Actor, hasManager bool) error {
	if !t.State.Open() {
		return fmt.Errorf("%s is %s; reopen it before changing it", t.ID, t.State)
	}
	if Capacities(t, a, hasManager)&(Manager|Owner|Creator) == 0 {
		return fmt.Errorf("only the owner, the creator or the manager role may change %s", t.ID)
	}
	return nil
}
