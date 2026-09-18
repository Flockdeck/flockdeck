package workspace

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jmwri/flockdeck/internal/store"
)

// This file is the todo checklist feature's workspace-side cache, the same
// shape groups.go is for project groupings: store.Todo is read once at
// startup, kept in memory as w.todos, and every mutation writes the whole
// list straight back out via persistTodos -- there being far fewer todos in
// a lifetime than there are keystrokes in a terminal, unlike the layouts
// that are saved only on a timer or on the way out.
//
// Unlike a fan-out's own history (see fanouthistory.go), which is kept only
// for the run and starts empty on every restart, a todo is meant to be come
// back to over days, so it is the one of the two that is actually
// persisted.

// loadSavedTodos reads todos.json once, at startup. A damaged or absent
// file is not fatal: the checklists simply come up empty, which is what a
// fresh install has anyway.
func loadSavedTodos() []store.Todo {
	todos, _ := store.LoadTodos()
	return todos
}

// Todos returns every todo saved against root, most recently updated
// first.
func (w *Workspace) Todos(root string) []store.Todo {
	var out []store.Todo
	for _, t := range w.todos {
		if sameDir(t.Root, root) {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// Todo returns one todo by id, and whether it was found.
func (w *Workspace) Todo(id string) (store.Todo, bool) {
	for _, t := range w.todos {
		if t.ID == id {
			return t, true
		}
	}
	return store.Todo{}, false
}

// SaveTodo creates a new todo, or updates one that already exists (id
// non-empty and found), replacing its title, spec and step list wholesale.
//
// stepIDs and stepTexts are positional, the same shape a fan-out's own
// per-row overrides are: a step whose id matches one this todo already had
// keeps that step's Done flag and attempt history; a step arriving with no
// id, or with one this todo has never had, starts fresh with an id minted
// here. A step of the todo not named in the arrays is dropped -- what lets
// the checklist's editable list delete a step simply by leaving it out when
// it saves, and reorder them by sending them in the order the user left
// them in.
func (w *Workspace) SaveTodo(id, root, title, spec string, stepIDs, stepTexts []string) (store.Todo, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return store.Todo{}, errors.New("a todo needs a project")
	}
	if len(stepIDs) != len(stepTexts) {
		return store.Todo{}, fmt.Errorf("internal error: %d step ids for %d steps", len(stepIDs), len(stepTexts))
	}

	now := time.Now()
	idx := -1
	if id != "" {
		for i := range w.todos {
			if w.todos[i].ID == id {
				idx = i
				break
			}
		}
	}

	prior := map[string]store.TodoStep{}
	if idx >= 0 {
		for _, s := range w.todos[idx].Steps {
			prior[s.ID] = s
		}
	}

	steps := make([]store.TodoStep, 0, len(stepTexts))
	for i, text := range stepTexts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		step := store.TodoStep{ID: stepIDs[i], Text: text}
		if p, ok := prior[step.ID]; ok && step.ID != "" {
			step.Done, step.Attempts = p.Done, p.Attempts
		}
		if step.ID == "" {
			step.ID = uuid.NewString()
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return store.Todo{}, errors.New("a todo needs at least one step")
	}

	t := store.Todo{
		ID:        id,
		Root:      root,
		Title:     strings.TrimSpace(title),
		Spec:      spec,
		Steps:     steps,
		UpdatedAt: now,
	}
	if idx >= 0 {
		t.CreatedAt = w.todos[idx].CreatedAt
	} else {
		t.CreatedAt = now
	}
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.Title == "" {
		t.Title = summarisePrompt(t.Spec)
	}
	if t.Title == "" {
		t.Title = "Untitled todo"
	}

	if idx >= 0 {
		w.todos[idx] = t
	} else {
		w.todos = append(w.todos, t)
	}
	if err := w.persistTodos(); err != nil {
		return store.Todo{}, err
	}
	return t, nil
}

// SetTodoStepDone ticks or unticks a step by hand, overriding whatever an
// attempt's outcome last set it to (see RecordTodoStepAttemptEnd) in either
// direction.
func (w *Workspace) SetTodoStepDone(todoID, stepID string, done bool) error {
	step, err := w.todoStep(todoID, stepID)
	if err != nil {
		return err
	}
	step.Done = done
	return w.touchTodo(todoID)
}

// DeleteTodo removes a todo entirely.
func (w *Workspace) DeleteTodo(id string) error {
	idx, err := w.todoIndex(id)
	if err != nil {
		return err
	}
	w.todos = append(w.todos[:idx], w.todos[idx+1:]...)
	return w.persistTodos()
}

// StartTodoStepAttempt records that a pane was just started for a step: the
// opening half of an attempt, closed out by RecordTodoStepAttemptEnd once
// the pane's tab or pane itself is closed.
func (w *Workspace) StartTodoStepAttempt(todoID, stepID, paneID string) error {
	step, err := w.todoStep(todoID, stepID)
	if err != nil {
		return err
	}
	step.Attempts = append(step.Attempts, store.TodoStepAttempt{PaneID: paneID, StartedAt: time.Now()})
	return w.touchTodo(todoID)
}

// RecordTodoStepAttemptEnd fills in an attempt's outcome once its pane
// closes, and ticks the step's Done flag when the outcome reads as
// finished ("done" -- see fanoutOutcome for what the three outcomes mean).
// It is a no-op for a pane that is not the current attempt of any step,
// which is every pane that is not a todo's own: called for every closing
// pane regardless (see captureTodoStepOutcome), it costs nothing to call
// for one that never started from a todo at all.
//
// A step already ticked, or unticked, by hand is left exactly as the user
// left it: only a step with no Done of its own yet -- because this is its
// first attempt to finish -- picks up the outcome. That is deliberately
// asymmetric with SetTodoStepDone, whose whole point is to be the last
// word; recording an attempt's end must not undo it.
func (w *Workspace) RecordTodoStepAttemptEnd(paneID, outcome string) {
	for ti := range w.todos {
		for si := range w.todos[ti].Steps {
			step := &w.todos[ti].Steps[si]
			if len(step.Attempts) == 0 {
				continue
			}
			last := &step.Attempts[len(step.Attempts)-1]
			if last.PaneID != paneID || !last.EndedAt.IsZero() {
				continue
			}
			last.EndedAt, last.Outcome = time.Now(), outcome
			if outcome == "done" && !step.Done {
				step.Done = true
			}
			w.todos[ti].UpdatedAt = time.Now()
			_ = w.persistTodos()
			return
		}
	}
}

// touchTodo stamps a todo's UpdatedAt and saves the list, the common tail
// of a mutation that changes one already-found todo in place.
func (w *Workspace) touchTodo(todoID string) error {
	idx, err := w.todoIndex(todoID)
	if err != nil {
		return err
	}
	w.todos[idx].UpdatedAt = time.Now()
	return w.persistTodos()
}

// todoIndex finds a todo by id, or reports that it no longer exists -- a
// window can hold a checklist open after another window has deleted it.
func (w *Workspace) todoIndex(id string) (int, error) {
	for i := range w.todos {
		if w.todos[i].ID == id {
			return i, nil
		}
	}
	return -1, fmt.Errorf("this todo no longer exists")
}

// todoStep finds a step by todo and step id, for a caller about to change a
// field on it in place.
func (w *Workspace) todoStep(todoID, stepID string) (*store.TodoStep, error) {
	idx, err := w.todoIndex(todoID)
	if err != nil {
		return nil, err
	}
	for i := range w.todos[idx].Steps {
		if w.todos[idx].Steps[i].ID == stepID {
			return &w.todos[idx].Steps[i], nil
		}
	}
	return nil, fmt.Errorf("this step no longer exists")
}

// persistTodos writes every todo to todos.json.
func (w *Workspace) persistTodos() error {
	return store.SaveTodos(w.todos)
}
