// Package tasks is the shared task board of a project: the tasks the panes of
// its team plan, assign, work on and review, the rules for who may move a task
// where, and the file that keeps them.
//
// It holds no panes or sessions. The workspace tells it who is acting and who
// is on the roster, and sends the messages that Notices describes, so
// everything here can be tested without a terminal.
//
// Who is acting is the pane id the caller sent, which the application checks is
// an open agent pane, and the role that pane was started with. Neither is
// authenticated beyond that: any process in a pane can send its pane id, so a
// permission here keeps agents to their jobs and is no defence against one that
// is set on getting round it.
package tasks

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/team"
)

// Limits on what a task holds. They bound the file and what one task can put
// into another agent's context.
const (
	// MaxTitle is the most a title may have, in characters.
	MaxTitle = 200
	// MaxBody is the most a task's body may have, in bytes.
	MaxBody = 16 * 1024
	// MaxNotes is how many notes a task keeps. The oldest is dropped to make
	// room for a new one.
	MaxNotes = 100
	// MaxNote is the most one note may have, in bytes.
	MaxNote = 4 * 1024
	// MaxBranch is the most a branch name may have, in bytes.
	MaxBranch = 200
	// MaxTasks is how many tasks a board holds. A board that is full refuses
	// new tasks until some are removed from the file by hand.
	MaxTasks = 2000
)

// State is where a task is in its life.
type State string

// The states of a task.
const (
	Planned    State = "planned"
	Assigned   State = "assigned"
	InProgress State = "in-progress"
	InReview   State = "in-review"
	Done       State = "done"
	Blocked    State = "blocked"
	Cancelled  State = "cancelled"
)

// States lists every state, in the order a task usually passes through them.
var States = []State{Planned, Assigned, InProgress, InReview, Done, Blocked, Cancelled}

// Open reports that the task is not finished: it is neither done nor cancelled.
func (s State) Open() bool { return s != Done && s != Cancelled }

// ParseState reads a state as a person writes it: in any case, with a hyphen,
// underscore or space between the words of "in-progress" and "in-review", or
// none.
func ParseState(s string) (State, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	v = strings.NewReplacer("_", "-", " ", "-").Replace(v)
	switch v {
	case "inprogress":
		v = "in-progress"
	case "inreview":
		v = "in-review"
	case "canceled":
		v = "cancelled"
	}
	for _, st := range States {
		if string(st) == v {
			return st, nil
		}
	}
	names := make([]string, len(States))
	for i, st := range States {
		names[i] = string(st)
	}
	return "", fmt.Errorf("%q is not a task state; the states are %s", s, strings.Join(names, ", "))
}

// Actor says who did something to a task: a pane, the role it was started as and
// the name it was known by then.
type Actor struct {
	Pane string `json:"pane,omitempty"`
	Role string `json:"role,omitempty"`
	Name string `json:"name,omitempty"`
}

// Label is the actor as one phrase, made of its own words and cut down to one
// line.
func (a Actor) Label() string {
	name := team.PlainLabel(a.Name)
	if name == "" {
		name = "unknown"
	}
	if a.Role != "" {
		return fmt.Sprintf("%s (%s)", name, team.PlainLabel(a.Role))
	}
	return name
}

// Note is one entry in a task's thread.
type Note struct {
	Author Actor     `json:"author"`
	Time   time.Time `json:"time"`
	Text   string    `json:"text"`
}

// Task is one unit of work on the board.
type Task struct {
	// ID is "T" and a number that counts up through the life of the board.
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`

	// OwnerRole is the role the task is for, and OwnerPane the pane that has it.
	// A task for a role has no pane until one claims it by starting it.
	// OwnerName is what that pane was called when it was given the task.
	OwnerRole string `json:"ownerRole,omitempty"`
	OwnerPane string `json:"ownerPane,omitempty"`
	OwnerName string `json:"ownerName,omitempty"`

	Creator Actor `json:"creator"`
	State   State `json:"state"`
	// Branch is the git branch the work is on.
	Branch string `json:"branch,omitempty"`
	// Baton is the id of a baton that gives the task its context. The baton
	// stays what it is, a handoff that is not changed; the task only points at it.
	Baton string `json:"baton,omitempty"`

	Notes   []Note    `json:"notes,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Owned reports whether the task has an owner, a role or a pane.
func (t Task) Owned() bool { return t.OwnerPane != "" || t.OwnerRole != "" }

var idRe = regexp.MustCompile(`^[Tt]?#?([0-9]{1,9})$`)

// ParseID reads a task id as it is written or typed: "T3", "t3", "#3" or "3".
func ParseID(s string) (string, error) {
	m := idRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", fmt.Errorf("%q is not a task id; ids look like T3, and `flockdeck task list` shows them", team.PlainLabel(s))
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return "", fmt.Errorf("%q is not a task id", team.PlainLabel(s))
	}
	return "T" + strconv.Itoa(n), nil
}

func idNumber(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "T"))
	return n
}

var roleRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// CleanRole reads a role id: lower case, and made of what a role id is made of.
func CleanRole(s string) (string, error) {
	r := strings.ToLower(strings.TrimSpace(s))
	if !roleRe.MatchString(r) {
		return "", fmt.Errorf("%q is not a role id", team.PlainLabel(s))
	}
	return r, nil
}

// CleanTitle makes a title one line and checks its length.
func CleanTitle(s string) (string, error) {
	if len([]rune(s)) > MaxTitle*4 {
		return "", fmt.Errorf("the title is far over the limit of %d characters", MaxTitle)
	}
	t := team.PlainText(s, MaxTitle+1)
	n := len([]rune(t))
	switch {
	case n == 0:
		return "", fmt.Errorf("the title is empty")
	case n > MaxTitle:
		return "", fmt.Errorf("the title is longer than %d characters; put the detail in the body", MaxTitle)
	}
	return t, nil
}

// CleanBody checks the length of a body.
func CleanBody(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > MaxBody {
		return "", fmt.Errorf("the body is %d bytes and the limit is %d; write the detail to a file and put its path in the body", len(s), MaxBody)
	}
	return s, nil
}

// CleanNote checks the length of a note.
func CleanNote(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("the note is empty")
	}
	if len(s) > MaxNote {
		return "", fmt.Errorf("the note is %d bytes and the limit is %d; write the detail to a file and put its path in the note", len(s), MaxNote)
	}
	return s, nil
}

// CleanBranch checks a branch name. It is the name as git spells it, so it has no
// white space or control character, and it is cut off at a length no real
// branch reaches.
func CleanBranch(s string) (string, error) {
	b := strings.TrimSpace(s)
	if b == "" {
		return "", fmt.Errorf("the branch name is empty")
	}
	if len(b) > MaxBranch {
		return "", fmt.Errorf("the branch name is %d bytes and the limit is %d", len(b), MaxBranch)
	}
	for _, r := range b {
		if r <= ' ' || r == 0x7f || r > 0x7e && r < 0xa0 || r == ' ' || r == ' ' {
			return "", fmt.Errorf("%q is not a branch name: it has white space or a control character", team.PlainLabel(s))
		}
	}
	return b, nil
}
