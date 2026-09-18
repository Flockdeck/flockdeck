package server

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// This file is the desktop side of the todo checklist feature: a standalone
// flow from a free-text spec, through a plan an agent drafts and the user
// edits, to a saved checklist whose steps are started one at a time, on
// demand -- as opposed to fan-out (fanout.go), which starts every task of a
// plan at once and forgets the run the moment its tab closes. See
// internal/help/pages/todo.md for how the two are told apart in the
// interface.
//
// Deliberately unlike fan-out, nothing here ever adds a "save as todo"
// affordance to the fan-out dialog; the two are reached from separate rail
// buttons, and share only the pieces of plumbing noted below.

// todoPlanPreviewMsg answers a request to read a planning pane's plan, for
// the user to edit into a todo's steps before it is saved. It is
// fanoutPreviewMsg pared down to just what that needs: unlike a fan-out, a
// todo never starts anything at the moment its plan is read, so it carries
// none of a fan-out dialog's agent, model or worktree choices.
type todoPlanPreviewMsg struct {
	Type   string `json:"type"`
	PaneID string `json:"paneId"`
	Root   string `json:"root"`
	// Tasks are the steps read out of the pane, and FromReply reports
	// whether they came from what the agent said rather than from scraping
	// its screen -- see planTasks.
	Tasks     []string `json:"tasks"`
	FromReply bool     `json:"fromReply"`
}

// previewTodoPlan reads a pane's plan the same way a fan-out's own preview
// does (see previewFanout and planTasks), for a user about to save it as a
// todo rather than start it.
func (s *Server) previewTodoPlan(c *controlClient, paneID, root string) {
	type info struct {
		id   string
		root string
		src  workspace.PlanSource
	}
	in, ok := ask(s, func() info {
		id := paneID
		if id == "" {
			if t := s.ws.CurrentTab(); t != nil {
				id = t.Focus
			}
		}
		r := root
		if r == "" {
			r = s.ws.ActiveRoot()
		}
		return info{id: id, root: r, src: s.ws.PlanSourceFor(id)}
	})
	if !ok {
		return
	}
	go func() {
		defer s.surviveFor(c, "reading the pane's plan")
		tasks, fromReply := planTasks(in.src)
		c.sendJSON(todoPlanPreviewMsg{Type: "todoPlanPreview", PaneID: in.id, Root: in.root, Tasks: tasks, FromReply: fromReply})
	}()
}

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
		c.notify("a todo needs a project", true)
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

// todoStepBranch derives the branch a step's agent gets when it is started
// in a worktree of its own: namespaced under the todo, rather than derived
// from the step's own text the way a fan-out's tasks are (see
// workspace.BranchNameFor) -- so every step of one todo groups together in
// `git branch` and in the worktree list, instead of scattering across a
// dozen unrelated agent/ branches that happen to have come from the same
// checklist.
func todoStepBranch(t store.Todo, step store.TodoStep) string {
	name := t.Title
	if name == "" {
		name = t.Spec
	}
	todoPart := strings.TrimPrefix(workspace.BranchNameFor(name), "agent/")
	stepPart := strings.TrimPrefix(workspace.BranchNameFor(step.Text), "agent/")
	return "agent/" + todoPart + "/" + stepPart
}

// prepareNamedWorktree gives job a worktree at a branch already chosen (see
// todoStepBranch), disambiguated against what the repository already has
// the same way a fan-out's own branches are; see nameBranches. It is
// prepareWorktrees narrowed to a single job whose branch must not be
// derived from its task text.
func prepareNamedWorktree(c *controlClient, repo string, job *fanoutJob) bool {
	defer lockRepo(repo)()
	taken, err := localBranches(repo)
	if err != nil {
		c.notify(fmt.Sprintf("could not read the branches of %s, so no worktree was created: %v", filepath.Base(repo), err), true)
		return false
	}
	job.branch = uniqueBranch(job.branch, taken, map[string]bool{})
	placeWorktrees(repo, []*fanoutJob{job})
	from := job.cwd
	makeWorktrees([]*fanoutJob{job}, func(branch, path string) error {
		return gitx.AddNewBranch(repo, path, branch)
	})
	if job.created {
		job.cwd, job.origin = sameFolderIn(repo, from, job.path)
	}
	return true
}

// startTodoStep starts a fresh agent for one step of a todo -- the "kick
// off a pane for this step" action of the checklist -- the same way
// startAgent starts one for a task typed by hand, sharing its rate limit,
// except that succeeding also records the attempt against the step (see
// Workspace.StartTodoStepAttempt) and, when a worktree is asked for, the
// branch is namespaced under the todo rather than under the step's own
// text; see todoStepBranch.
func (s *Server) startTodoStep(c *controlClient, cmd command) {
	if !c.startAgentLimit.allow(startAgentRateLimit, startAgentRateWindow) {
		c.notify("too many agents started too quickly -- wait a moment and try again", true)
		return
	}

	type facts struct {
		todo store.Todo
		step store.TodoStep
		root string
		err  error
	}
	f, ok := ask(s, func() facts {
		t, found := s.ws.Todo(cmd.TodoID)
		if !found {
			return facts{err: fmt.Errorf("this todo no longer exists")}
		}
		var step store.TodoStep
		stepFound := false
		for _, st := range t.Steps {
			if st.ID == cmd.StepID {
				step, stepFound = st, true
				break
			}
		}
		if !stepFound {
			return facts{err: fmt.Errorf("this step no longer exists")}
		}
		root, _, err := s.ws.ValidateStartAgent(t.Root, cmd.Agent, cmd.Model)
		if err != nil {
			return facts{err: err}
		}
		return facts{todo: t, step: step, root: root}
	})
	if !ok {
		return
	}
	if f.err != nil {
		c.notify(f.err.Error(), true)
		return
	}
	todo, step, root, task := f.todo, f.step, f.root, f.step.Text

	go func() {
		defer s.surviveFor(c, "starting an agent")
		job := &fanoutJob{task: task, cwd: root, branch: todoStepBranch(todo, step)}
		var repo string
		if cmd.Worktree {
			repo = gitRoot(root)
			switch {
			case !gitx.Available():
				c.notify("git is not installed, so no worktree can be created", true)
				return
			case repo == "":
				c.notify(fmt.Sprintf("%s is not in a git repository, so no worktree can be created", filepath.Base(root)), true)
				return
			}
			if !prepareNamedWorktree(c, repo, job) {
				return
			}
			if job.err != nil {
				c.notify(job.err.Error(), true)
				return
			}
		}

		type spawned struct {
			id  string
			err error
		}
		r, ok := ask(s, func() spawned {
			id, err := s.ws.StartAgent(root, cmd.Agent, cmd.Model, job.cwd, task)
			return spawned{id: id, err: err}
		})
		if !ok {
			return
		}
		if r.err != nil {
			msg := r.err.Error()
			if err := discardWorktree(repo, job, s.paneWorkingIn); err != nil {
				msg += fmt.Sprintf(" (%v)", err)
			}
			c.notify(msg, true)
			return
		}
		if _, ok := ask(s, func() bool {
			_ = s.ws.StartTodoStepAttempt(todo.ID, step.ID, r.id)
			return true
		}); !ok {
			return
		}
		c.sendJSON(map[string]any{"type": "agentStarted", "paneId": r.id, "id": cmd.ID})
		s.Wake()
	}()
}

// captureTodoStepOutcome reads a pane's outcome the same way a fan-out's
// settled job is (see fanoutOutcome), ahead of the close that is about to
// remove it, and records it against whichever todo step it was the current
// attempt of -- see Workspace.RecordTodoStepAttemptEnd. It is harmless to
// call for any other pane: that method does nothing unless paneID names a
// todo's own attempt, so every caller of captureFanoutHistory calls this
// alongside it, pane by pane, without first checking which panes belong to
// a todo.
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
