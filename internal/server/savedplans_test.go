package server

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// runSaveTodo calls saveTodo directly and reads back its answer: a
// todoSaved message, or a notice on refusal.
func runSaveTodo(t *testing.T, srv *Server, cmd command) (todoView, string) {
	t.Helper()
	cmd.Cmd = "todoSave"
	c := &controlClient{out: make(chan []byte, 8)}
	srv.saveTodo(c, cmd)
	select {
	case raw := <-c.out:
		var head struct {
			Type string `json:"type"`
		}
		if err := unmarshalHead(raw, &head); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		switch head.Type {
		case "todoSaved":
			var msg struct {
				Todo todoView `json:"todo"`
			}
			if err := unmarshalHead(raw, &msg); err != nil {
				t.Fatalf("todoSaved: %v", err)
			}
			return msg.Todo, ""
		case "notice":
			var msg noticeMsg
			if err := unmarshalHead(raw, &msg); err != nil {
				t.Fatalf("notice: %v", err)
			}
			return todoView{}, msg.Text
		default:
			t.Fatalf("unexpected message type %q", head.Type)
		}
	default:
		t.Fatal("saveTodo did not answer")
	}
	return todoView{}, ""
}

// TestSaveTodoAndListTodos covers the plain round trip a saved checklist
// takes through the server layer: creating one, listing it back for its
// project, and that another project's list stays empty.
func TestSaveTodoAndListTodos(t *testing.T) {
	srv, ws := newTestServer(t)
	root := ws.ActiveRoot()

	saved, notice := runSaveTodo(t, srv, command{Root: root, Title: "ship the thing",
		Tasks: []string{"write the code", "write the tests"}})
	if notice != "" {
		t.Fatalf("saveTodo said %q, want it to succeed", notice)
	}
	if saved.ID == "" || len(saved.Steps) != 2 {
		t.Fatalf("saved = %+v, want an id and two steps", saved)
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.listTodos(c, root)
	var msg todosMsg
	select {
	case raw := <-c.out:
		if err := unmarshalHead(raw, &msg); err != nil {
			t.Fatalf("todos: %v", err)
		}
	default:
		t.Fatal("listTodos did not answer")
	}
	if len(msg.Items) != 1 || msg.Items[0].ID != saved.ID {
		t.Fatalf("todos for %s = %+v, want just the one just saved", root, msg.Items)
	}

	c2 := &controlClient{out: make(chan []byte, 8)}
	srv.listTodos(c2, t.TempDir())
	var other todosMsg
	select {
	case raw := <-c2.out:
		if err := unmarshalHead(raw, &other); err != nil {
			t.Fatalf("todos: %v", err)
		}
	default:
		t.Fatal("listTodos did not answer")
	}
	if len(other.Items) != 0 {
		t.Errorf("another project's todos = %+v, want none", other.Items)
	}
}

// TestSaveTodoRefusesAnEmptyRoot covers the guard that keeps a todo from
// being saved with nothing to belong to -- there is no project a later
// "start this step" could resolve against.
func TestSaveTodoRefusesAnEmptyRoot(t *testing.T) {
	srv, _ := newTestServer(t)
	_, notice := runSaveTodo(t, srv, command{Tasks: []string{"a step"}})
	if notice == "" {
		t.Fatal("saveTodo accepted a todo with no project")
	}
}

// stepPane returns the pane id recorded as step i's latest attempt.
func stepPane(t *testing.T, ws *workspace.Workspace, planID string, i int) string {
	t.Helper()
	got, ok := ws.Todo(planID)
	if !ok {
		t.Fatal("saved plan vanished")
	}
	at := got.Steps[i].Attempts
	if len(at) == 0 {
		t.Fatalf("step %d has no attempt", i)
	}
	return at[len(at)-1].PaneID
}

// TestFanOutFromASavedPlanRecordsAnAttemptPerStep covers starting a saved
// plan's steps from the Fan out dialog: each started row records its pane as
// an attempt against the step it came from, and a row typed in since (no step
// id) records nothing.
func TestFanOutFromASavedPlanRecordsAnAttemptPerStep(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()

	plan, err := ws.SaveTodo("", root, "ship the thing", "spec", []string{"", ""},
		[]string{"first step", "second step"})
	if err != nil {
		t.Fatalf("save plan: %v", err)
	}

	runFanout(t, srv, fanoutRequest{
		Tasks:       []string{"second step", "typed in later"},
		PlanID:      plan.ID,
		PlanStepIDs: []string{plan.Steps[1].ID, ""},
		Agent:       "gocli",
	})

	got, _ := ws.Todo(plan.ID)
	if n := len(got.Steps[0].Attempts); n != 0 {
		t.Errorf("a step left out of the run has %d attempts, want none", n)
	}
	paneID := stepPane(t, ws, plan.ID, 1)
	if len(got.Steps[1].Attempts) != 1 {
		t.Fatalf("step attempts = %+v, want one", got.Steps[1].Attempts)
	}
	if p := ws.Pane(paneID); p == nil || p.Task != "second step" {
		t.Fatalf("the attempt's pane = %+v, want one running %q", p, "second step")
	}
	if len(got.Steps) != 2 {
		t.Errorf("the plan has %d steps, want the 2 it was saved with", len(got.Steps))
	}
}

// TestFanOutFromADeletedPlanStillStartsTheAgents covers a plan deleted from
// another window while the dialog was open: the run goes ahead as an
// ordinary fan-out.
func TestFanOutFromADeletedPlanStillStartsTheAgents(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()
	plan, err := ws.SaveTodo("", root, "gone soon", "", []string{""}, []string{"only step"})
	if err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if err := ws.DeleteTodo(plan.ID); err != nil {
		t.Fatalf("delete plan: %v", err)
	}
	runFanout(t, srv, fanoutRequest{
		Tasks: []string{"only step"}, PlanID: plan.ID, PlanStepIDs: []string{plan.Steps[0].ID}, Agent: "gocli",
	})
}

// TestFanOutFromASavedPlanInWorktreesNamespacesTheBranches covers the branch
// names: grouped under the plan, as they were when a todo started its steps,
// rather than derived from each step's text alone.
func TestFanOutFromASavedPlanInWorktreesNamespacesTheBranches(t *testing.T) {
	srv, ws, repo := newRepoServer(t)
	writeAgents(t, revealAgents)
	ws.ReloadAgents()

	plan, err := ws.SaveTodo("", repo, "ship the thing", "spec", []string{""}, []string{"add a health endpoint"})
	if err != nil {
		t.Fatalf("save plan: %v", err)
	}
	runFanout(t, srv, fanoutRequest{
		Tasks: []string{"add a health endpoint"}, PlanID: plan.ID, PlanStepIDs: []string{plan.Steps[0].ID},
		Agent: "gocli", Worktrees: true,
	})

	p := ws.Pane(stepPane(t, ws, plan.ID, 0))
	if p == nil {
		t.Fatal("the started pane does not exist")
	}
	if samePath(filepath.Clean(p.Cwd), filepath.Clean(repo)) {
		t.Errorf("pane cwd = %s, want a worktree of its own", p.Cwd)
	}
	if want := todoStepBranch(plan, plan.Steps[0]); p.Branch != want {
		t.Errorf("branch = %q, want it namespaced under the plan: %q", p.Branch, want)
	}
}

// TestClosingATodoStepsPaneRecordsItsOutcome covers the auto-detect half of
// the feature end to end: a step started, then whose pane is closed idle,
// comes back ticked done with its attempt's outcome recorded -- the same
// read a fan-out's own settled job takes at close time (see
// captureFanoutHistory), reused here rather than a second lifecycle hook.
func TestClosingATodoStepsPaneRecordsItsOutcome(t *testing.T) {
	srv, ws := fanoutServer(t)
	// fanoutServer's agent prints an error and exits on its own clock, which
	// can land after settlePane below: fresh output moves an idle pane back
	// to working, and the exit marks it failed, so the close would read
	// "failed" rather than the idle the test set. An agent that never
	// produces output or exits leaves nothing to race; see hangingAgents.
	writeAgents(t, hangingAgents(t))
	ws.ReloadAgents()
	root := ws.ActiveRoot()
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	todo, err := ws.SaveTodo("", root, "ship the thing", "spec", []string{""}, []string{"first step"})
	if err != nil {
		t.Fatalf("save todo: %v", err)
	}

	runFanout(t, srv, fanoutRequest{
		Tasks: []string{"first step"}, PlanID: todo.ID, PlanStepIDs: []string{todo.Steps[0].ID}, Agent: "gocli",
	})
	paneID := stepPane(t, ws, todo.ID, 0)
	settlePane(t, srv, ws, paneID, session.StatusIdle)

	sendCmd(t, conn, command{Cmd: "closePane", ID: paneID})
	nextState(t, conn, func(s stateMsg) bool { return ws.Pane(paneID) == nil })

	got, ok := ws.Todo(todo.ID)
	if !ok {
		t.Fatal("todo vanished")
	}
	step := got.Steps[0]
	if len(step.Attempts) != 1 {
		t.Fatalf("step attempts = %+v, want exactly one", step.Attempts)
	}
	if step.Attempts[0].Outcome != "done" {
		t.Errorf("attempt outcome = %q, want %q", step.Attempts[0].Outcome, "done")
	}
	if step.Attempts[0].EndedAt.IsZero() {
		t.Error("attempt EndedAt was never set")
	}
	if !step.Done {
		t.Error("the step should have been ticked done once its only attempt closed idle")
	}
}

// TestClosingAPaneNotOwnedByATodoLeavesTodosAlone covers the harmless-no-op
// half of captureTodoStepOutcome: an ordinary pane closing must not reach
// into any todo's steps, since it is called for every pane that closes,
// todo or not.
func TestClosingAPaneNotOwnedByATodoLeavesTodosAlone(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	todo, err := ws.SaveTodo("", root, "ship the thing", "spec", []string{""}, []string{"first step"})
	if err != nil {
		t.Fatalf("save todo: %v", err)
	}

	// An ordinary agent, started the ordinary way, has no attempt on any
	// todo step.
	reply := runStartAgent(t, srv, command{Root: root, Agent: "gocli", Task: "unrelated work"})
	settlePane(t, srv, ws, reply.paneID, session.StatusIdle)

	sendCmd(t, conn, command{Cmd: "closePane", ID: reply.paneID})
	nextState(t, conn, func(s stateMsg) bool { return ws.Pane(reply.paneID) == nil })

	got, ok := ws.Todo(todo.ID)
	if !ok {
		t.Fatal("todo vanished")
	}
	if len(got.Steps[0].Attempts) != 0 {
		t.Errorf("an unrelated pane's close recorded an attempt on the todo: %+v", got.Steps[0].Attempts)
	}
}

// TestDeleteTodoAndToggleStepDone cover the two remaining plain mutations:
// ticking a step by hand, and removing a todo outright.
func TestDeleteTodoAndToggleStepDone(t *testing.T) {
	srv, ws := newTestServer(t)
	root := ws.ActiveRoot()

	todo, err := ws.SaveTodo("", root, "t", "spec", []string{""}, []string{"a step"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.setTodoStepDone(c, todo.ID, todo.Steps[0].ID, true)
	select {
	case raw := <-c.out:
		var head struct {
			Type string   `json:"type"`
			Todo todoView `json:"todo"`
		}
		if err := unmarshalHead(raw, &head); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if head.Type != "todoUpdated" {
			t.Fatalf("setTodoStepDone sent type %q, want the todoUpdated echo a checklist redraws from", head.Type)
		}
		if head.Todo.ID != todo.ID || !head.Todo.Steps[0].Done {
			t.Fatalf("todoUpdated = %+v, want %s ticked", head.Todo, todo.ID)
		}
	default:
		t.Fatal("setTodoStepDone did not answer")
	}
	got, _ := ws.Todo(todo.ID)
	if !got.Steps[0].Done {
		t.Error("setTodoStepDone did not tick the step")
	}

	srv.deleteTodo(c, todo.ID)
	select {
	case raw := <-c.out:
		var msg noticeMsg
		_ = unmarshalHead(raw, &msg)
		t.Fatalf("deleteTodo said %q, want no notice on success", msg.Text)
	default:
	}
	if _, ok := ws.Todo(todo.ID); ok {
		t.Error("todo still found after deleteTodo")
	}
}

func timeoutChan() <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		// A generous ceiling: every case above is answered synchronously or
		// by a real (fast) process started under revealAgents.
		<-time.After(15 * time.Second)
		close(ch)
	}()
	return ch
}

func unmarshalHead(raw []byte, v any) error { return json.Unmarshal(raw, v) }
