package hooks

import (
	"fmt"
	"net/http"

	"github.com/jmwri/flockdeck/internal/tasks"
)

// This file is the callback API of the task board (`flockdeck task`). It is
// served the way the team commands are, and by the same rules: the request names
// the caller's own pane and carries the run's token, the application refuses a
// pane it does not have open, and the pane acts as itself. The sender is
// whatever pane id the caller sent: it is not authenticated beyond that.

// Task operations a TaskRequest can name.
const (
	TaskAdd    = "add"
	TaskList   = "list"
	TaskShow   = "show"
	TaskAssign = "assign"
	TaskStatus = "status"
	TaskNote   = "note"
	TaskBranch = "branch"
	TaskBaton  = "baton"
	TaskPrune  = "prune"
)

// TaskRequest is one operation on the caller's project's board. Which of the
// fields it reads depends on Op.
type TaskRequest struct {
	Auth
	Op string `json:"op"`
	// ID names the task for every operation but add and list.
	ID string `json:"id,omitempty"`

	// Add: Title, Body, Role (the role the task is for) and Branch.
	Title  string `json:"title,omitempty"`
	Body   string `json:"body,omitempty"`
	Role   string `json:"role,omitempty"`
	Branch string `json:"branch,omitempty"`

	// List: State, Role, Mine and All say which tasks.
	State string `json:"state,omitempty"`
	Mine  bool   `json:"mine,omitempty"`
	All   bool   `json:"all,omitempty"`

	// Prune: Days is the age, in days, of the finished tasks to remove. Zero is
	// the default of 30.
	Days int `json:"days,omitempty"`

	// Assign: Target is a role, a name or a pane id.
	Target string `json:"target,omitempty"`
	// Status: State is the state to move to, Note says why. Note: Text.
	Note string `json:"note,omitempty"`
	Text string `json:"text,omitempty"`
	// Baton is the id of the baton to point the task at.
	Baton string `json:"baton,omitempty"`
}

// TaskResult is the answer: the task an operation changed or showed, or the tasks
// a list matched.
type TaskResult struct {
	Task  *tasks.Task  `json:"task,omitempty"`
	Tasks []tasks.Task `json:"tasks,omitempty"`
	// Hidden is how many finished tasks a list left out.
	Hidden int `json:"hidden,omitempty"`
	// Pruned is how many finished tasks a prune removed.
	Pruned int `json:"pruned,omitempty"`
	// Told says how many panes were sent a message because of the change.
	Told int `json:"told,omitempty"`
	// Warning is something the caller should know about a change that was made,
	// such as an assign that took a task from the pane working on it.
	Warning string `json:"warning,omitempty"`
}

// SetTaskHandler installs the function that answers `flockdeck task`.
func (s *Server) SetTaskHandler(fn func(TaskRequest) (TaskResult, error)) {
	s.mu.Lock()
	s.onTeam.task = fn
	s.mu.Unlock()
}

func (s *Server) handleTask() http.HandlerFunc {
	return serveJSON(s, "the task board", func() func(TaskRequest) (TaskResult, error) { return s.onTeam.task })
}

// Task is the client half of `flockdeck task`.
func Task(api, token, pane string, req TaskRequest) (TaskResult, error) {
	if req.Op == "" {
		return TaskResult{}, fmt.Errorf("no task operation named")
	}
	req.Auth = Auth{Pane: pane, Token: token}
	return post[TaskResult](api, "/task", req)
}
