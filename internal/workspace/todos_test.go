package workspace

import "testing"

// TestSaveTodoCreatesAndUpdates covers the two shapes SaveTodo has to
// handle: an id nobody has seen yet, which mints a new todo, and an
// existing id, which replaces its steps in place while keeping a step's
// Done flag and attempt history wherever its id is reused -- the whole
// point of positional step ids, since an edited plan is not a fresh one.
func TestSaveTodoCreatesAndUpdates(t *testing.T) {
	isolateConfig(t)
	w := &Workspace{}

	created, err := w.SaveTodo("", "/code/api", "add health endpoint", "add a health endpoint",
		[]string{"", ""}, []string{"add the route", "add a test"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("SaveTodo did not mint an id for a new todo")
	}
	if len(created.Steps) != 2 || created.Steps[0].ID == "" || created.Steps[1].ID == "" {
		t.Fatalf("steps = %+v, want two steps each with a minted id", created.Steps)
	}

	// Tick the first step and give it an attempt, the way SetTodoStepDone
	// and StartTodoStepAttempt would from a running checklist.
	if err := w.SetTodoStepDone(created.ID, created.Steps[0].ID, true); err != nil {
		t.Fatalf("set done: %v", err)
	}
	if err := w.StartTodoStepAttempt(created.ID, created.Steps[0].ID, "pane-1"); err != nil {
		t.Fatalf("start attempt: %v", err)
	}

	// Editing the plan -- step 0's text changed, step 1 dropped, a new step
	// added -- must keep step 0's history since its id is unchanged, and
	// must not resurrect step 1's id for the brand-new step.
	updated, err := w.SaveTodo(created.ID, "/code/api", "add health endpoint", "add a health endpoint",
		[]string{created.Steps[0].ID, ""}, []string{"add the route and wire it up", "add docs"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.ID != created.ID {
		t.Errorf("update minted a new id %q, want the existing one %q", updated.ID, created.ID)
	}
	if len(updated.Steps) != 2 {
		t.Fatalf("steps = %+v, want two", updated.Steps)
	}
	if updated.Steps[0].ID != created.Steps[0].ID {
		t.Error("the surviving step's id changed across an edit")
	}
	if !updated.Steps[0].Done || len(updated.Steps[0].Attempts) != 1 {
		t.Errorf("step 0 = %+v, want its Done flag and attempt history kept across a text edit", updated.Steps[0])
	}
	if updated.Steps[1].ID == created.Steps[0].ID || updated.Steps[1].ID == "" {
		t.Errorf("the new step's id = %q, want a fresh one distinct from any surviving step", updated.Steps[1].ID)
	}
	if updated.Steps[1].Done || len(updated.Steps[1].Attempts) != 0 {
		t.Errorf("the brand-new step should start with no history: %+v", updated.Steps[1])
	}

	if only := w.Todos("/code/api"); len(only) != 1 {
		t.Fatalf("Todos(root) = %d entries, want 1", len(only))
	}
	if none := w.Todos("/code/other"); len(none) != 0 {
		t.Errorf("another project's Todos returned %d entries, want 0", len(none))
	}
}

// TestSaveTodoRejectsAnEmptyStepList covers the refusal that keeps a save
// from quietly turning a checklist into nothing at all: every step trimmed
// to blank leaves nothing worth saving, and the caller should be told
// rather than left with a todo of zero steps.
func TestSaveTodoRejectsAnEmptyStepList(t *testing.T) {
	isolateConfig(t)
	w := &Workspace{}
	if _, err := w.SaveTodo("", "/code/api", "t", "spec", []string{"", ""}, []string{"  ", ""}); err == nil {
		t.Fatal("SaveTodo accepted a todo with no real steps")
	}
}

// TestRecordTodoStepAttemptEndTicksDoneOnlyOnSuccess covers the auto-detect
// half of the feature: a step's Done flag follows an attempt's outcome only
// when that outcome is "done", and only for the pane that is actually the
// step's current attempt -- an unrelated pane closing must never reach in
// and tick somebody else's step.
func TestRecordTodoStepAttemptEndTicksDoneOnlyOnSuccess(t *testing.T) {
	isolateConfig(t)
	w := &Workspace{}
	todo, err := w.SaveTodo("", "/code/api", "t", "spec", []string{"", ""}, []string{"step one", "step two"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s1, s2 := todo.Steps[0].ID, todo.Steps[1].ID

	if err := w.StartTodoStepAttempt(todo.ID, s1, "pane-done"); err != nil {
		t.Fatalf("start attempt 1: %v", err)
	}
	if err := w.StartTodoStepAttempt(todo.ID, s2, "pane-failed"); err != nil {
		t.Fatalf("start attempt 2: %v", err)
	}

	// A pane belonging to nobody's step does nothing.
	w.RecordTodoStepAttemptEnd("pane-unrelated", "done")

	w.RecordTodoStepAttemptEnd("pane-done", "done")
	w.RecordTodoStepAttemptEnd("pane-failed", "failed")

	got, ok := w.Todo(todo.ID)
	if !ok {
		t.Fatal("todo vanished")
	}
	var step1, step2 *struct {
		done     bool
		outcome  string
		attempts int
	}
	for _, s := range got.Steps {
		info := &struct {
			done     bool
			outcome  string
			attempts int
		}{done: s.Done, attempts: len(s.Attempts)}
		if len(s.Attempts) > 0 {
			info.outcome = s.Attempts[len(s.Attempts)-1].Outcome
		}
		switch s.ID {
		case s1:
			step1 = info
		case s2:
			step2 = info
		}
	}
	if step1 == nil || !step1.done || step1.outcome != "done" {
		t.Errorf("step 1 = %+v, want done=true, outcome=done", step1)
	}
	if step2 == nil || step2.done || step2.outcome != "failed" {
		t.Errorf("step 2 = %+v, want done=false, outcome=failed", step2)
	}
}

// TestSetTodoStepDoneOverridesAnAutoTick covers the ordering the two ways a
// step's Done flag can change have to respect: a tick or untick made by
// hand from the checklist is always the last word, so it must still read
// back correctly after being set, regardless of what any attempt already
// recorded.
func TestSetTodoStepDoneOverridesAnAutoTick(t *testing.T) {
	isolateConfig(t)
	w := &Workspace{}
	todo, err := w.SaveTodo("", "/code/api", "t", "spec", []string{""}, []string{"step one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	step := todo.Steps[0].ID

	if err := w.StartTodoStepAttempt(todo.ID, step, "pane-1"); err != nil {
		t.Fatalf("start attempt: %v", err)
	}
	w.RecordTodoStepAttemptEnd("pane-1", "done")
	if err := w.SetTodoStepDone(todo.ID, step, false); err != nil {
		t.Fatalf("set done false: %v", err)
	}

	got, _ := w.Todo(todo.ID)
	if got.Steps[0].Done {
		t.Error("unticking a step by hand after a successful attempt did not stick")
	}
}

// TestDeleteTodoRemovesIt covers the plain removal path, and that it
// refuses cleanly for an id that is not there -- a window can hold a
// checklist open after another window has already deleted it.
func TestDeleteTodoRemovesIt(t *testing.T) {
	isolateConfig(t)
	w := &Workspace{}
	todo, err := w.SaveTodo("", "/code/api", "t", "spec", []string{""}, []string{"step one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := w.DeleteTodo(todo.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := w.Todo(todo.ID); ok {
		t.Error("todo still found after DeleteTodo")
	}
	if err := w.DeleteTodo(todo.ID); err == nil {
		t.Error("deleting an already-deleted todo should be refused, not silently accepted")
	}
}
