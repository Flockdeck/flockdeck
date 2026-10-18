package server

import (
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// This file is the server side of saved fan-out plans. A plan is a fan-out's
// task list kept on disk, so it can be reopened in the Fan out dialog over
// days, with which of its steps are done and every agent started for each
// remembered. The type and its commands are still called "todo" because that
// is what plans first shipped as and todos.json (see store.Todo) has to keep
// loading unchanged; nothing the person sees says "todo" any more.
//
// Starting a plan's steps is the fan-out's own job (see fanoutRequest.PlanID),
// so nothing in this file starts an agent.

// todoStepAttemptView is one attempt as the checklist shows it.
type todoStepAttemptView struct {
	PaneID    string `json:"paneId"`
	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt,omitempty"`
	// Outcome is "done", "needs" or "failed", empty while the attempt's
	// pane is still open; see fanoutOutcome.
	Outcome string `json:"outcome,omitempty"`
}

// todoStepView is one checklist item as the window shows it.
type todoStepView struct {
	ID       string                `json:"id"`
	Text     string                `json:"text"`
	Done     bool                  `json:"done"`
	Attempts []todoStepAttemptView `json:"attempts,omitempty"`
}

// todoView is one saved todo as the window shows it.
type todoView struct {
	ID        string         `json:"id"`
	Root      string         `json:"root"`
	Title     string         `json:"title"`
	Spec      string         `json:"spec"`
	Steps     []todoStepView `json:"steps"`
	CreatedAt string         `json:"createdAt"`
	UpdatedAt string         `json:"updatedAt"`
}

// todoViewOf turns a stored todo into what the window is sent.
func todoViewOf(t store.Todo) todoView {
	steps := make([]todoStepView, 0, len(t.Steps))
	for _, st := range t.Steps {
		attempts := make([]todoStepAttemptView, 0, len(st.Attempts))
		for _, a := range st.Attempts {
			av := todoStepAttemptView{PaneID: a.PaneID, StartedAt: a.StartedAt.Format(time.RFC3339), Outcome: a.Outcome}
			if !a.EndedAt.IsZero() {
				av.EndedAt = a.EndedAt.Format(time.RFC3339)
			}
			attempts = append(attempts, av)
		}
		steps = append(steps, todoStepView{ID: st.ID, Text: st.Text, Done: st.Done, Attempts: attempts})
	}
	return todoView{
		ID: t.ID, Root: t.Root, Title: t.Title, Spec: t.Spec, Steps: steps,
		CreatedAt: t.CreatedAt.Format(time.RFC3339), UpdatedAt: t.UpdatedAt.Format(time.RFC3339),
	}
}

// todosMsg answers a request for a project's saved todos.
type todosMsg struct {
	Type  string     `json:"type"`
	Root  string     `json:"root"`
	Items []todoView `json:"items"`
}

// listTodos answers a window's request for a project's saved todos. Reading
// it is a plain slice scan, so like fanoutHistory it needs no goroutine of
// its own.
func (s *Server) listTodos(c *controlClient, root string) {
	msg, ok := ask(s, func() todosMsg {
		r := root
		if r == "" {
			r = s.ws.ActiveRoot()
		}
		todos := s.ws.Todos(r)
		m := todosMsg{Type: "todos", Root: r, Items: make([]todoView, 0, len(todos))}
		for _, t := range todos {
			m.Items = append(m.Items, todoViewOf(t))
		}
		return m
	})
	if !ok {
		return
	}
	c.sendJSON(msg)
}

// saveTodo creates a new todo, or updates one already saved, from the
// checklist review dialog's editable step list -- see Workspace.SaveTodo.
func (s *Server) saveTodo(c *controlClient, cmd command) {
	root := strings.TrimSpace(cmd.Root)
	if root == "" {
		c.notify("A saved plan needs a project", true)
		return
	}
	stepIDs := cmd.StepIDs
	if len(stepIDs) != len(cmd.Tasks) {
		// A brand-new todo sends no ids at all: SaveTodo mints one for every
		// step whose id arrives empty, so padding with blanks here is
		// exactly what an all-new list means.
		stepIDs = make([]string, len(cmd.Tasks))
	}
	type result struct {
		todo store.Todo
		err  error
	}
	r, ok := ask(s, func() result {
		t, err := s.ws.SaveTodo(cmd.TodoID, root, cmd.Title, cmd.Spec, stepIDs, cmd.Tasks)
		return result{t, err}
	})
	if !ok {
		return
	}
	if r.err != nil {
		c.notify(r.err.Error(), true)
		return
	}
	c.sendJSON(map[string]any{"type": "todoSaved", "todo": todoViewOf(r.todo)})
}

// deleteTodo removes a saved todo entirely.
func (s *Server) deleteTodo(c *controlClient, id string) {
	err, ok := ask(s, func() error { return s.ws.DeleteTodo(id) })
	if !ok {
		return
	}
	if err != nil {
		c.notify(err.Error(), true)
	}
}

// setTodoStepDone ticks or unticks one step by hand, and echoes back the
// updated todo (see todoUpdated) so a checklist dialog already open can
// redraw its "X of Y done" count in place, rather than only picking up the
// change the next time the dialog is reopened.
func (s *Server) setTodoStepDone(c *controlClient, todoID, stepID string, done bool) {
	type result struct {
		todo  store.Todo
		found bool
		err   error
	}
	r, ok := ask(s, func() result {
		if err := s.ws.SetTodoStepDone(todoID, stepID, done); err != nil {
			return result{err: err}
		}
		t, found := s.ws.Todo(todoID)
		return result{todo: t, found: found}
	})
	if !ok {
		return
	}
	if r.err != nil {
		c.notify(r.err.Error(), true)
		return
	}
	if !r.found {
		return
	}
	c.sendJSON(map[string]any{"type": "todoUpdated", "todo": todoViewOf(r.todo)})
}

// todoStepBranch derives the branch a saved plan's step gets when its agent
// is started in a worktree of its own: namespaced under the plan, rather than
// derived from the step's own text alone (see workspace.BranchNameFor), so
// the steps of one plan group together in `git branch` and in the worktree
// list across however many runs it takes to start them.
func todoStepBranch(t store.Todo, step store.TodoStep) string {
	name := t.Title
	if name == "" {
		name = t.Spec
	}
	todoPart := strings.TrimPrefix(workspace.BranchNameFor(name), "agent/")
	stepPart := strings.TrimPrefix(workspace.BranchNameFor(step.Text), "agent/")
	return "agent/" + todoPart + "/" + stepPart
}

// captureTodoStepOutcome reads a pane's outcome the same way a fan-out's
// settled job is (see fanoutOutcome), ahead of the close that is about to
// remove it, and records it against whichever saved-plan step it was the
// current attempt of -- see Workspace.RecordTodoStepAttemptEnd. It is
// harmless to call for any other pane: that method does nothing unless
// paneID names a plan step's attempt, so every caller of
// captureFanoutHistory calls this alongside it, pane by pane, without first
// checking which panes belong to a plan.
//
// Must run on the workspace goroutine.
func (s *Server) captureTodoStepOutcome(id string) {
	p := s.ws.Pane(id)
	if p == nil {
		return
	}
	kind, _ := s.fanoutOutcome(p)
	s.ws.RecordTodoStepAttemptEnd(id, kind)
}
