package server

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
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

// TestStartTodoStepStartsAgentAndRecordsAttempt covers the "kick off a pane
// for this step" action: it starts a real agent for the step's own text,
// and records the pane as the step's attempt so the checklist can show it.
func TestStartTodoStepStartsAgentAndRecordsAttempt(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()

	todo, err := ws.SaveTodo("", root, "ship the thing", "spec", []string{"", ""},
		[]string{"first step", "second step"})
	if err != nil {
		t.Fatalf("save todo: %v", err)
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.startTodoStep(c, command{TodoID: todo.ID, StepID: todo.Steps[0].ID, Agent: "gocli"})
	paneID := waitAgentStarted(t, c)

	got, ok := ws.Todo(todo.ID)
	if !ok {
		t.Fatal("todo vanished")
	}
	step := got.Steps[0]
	if len(step.Attempts) != 1 || step.Attempts[0].PaneID != paneID {
		t.Fatalf("step attempts = %+v, want one attempt naming pane %q", step.Attempts, paneID)
	}
	if p := ws.Pane(paneID); p == nil || p.Task != "first step" {
		t.Fatalf("started pane's task = %+v, want %q", p, "first step")
	}
}

// TestStartTodoStepInAWorktreeNamespacesTheBranch covers point 8 of the
// feature's design: a step started in a worktree of its own gets a branch
// grouped under its todo, not one derived from the step's own text the way
// a fan-out's tasks are.
func TestStartTodoStepInAWorktreeNamespacesTheBranch(t *testing.T) {
	srv, ws, repo := newRepoServer(t)
	writeAgents(t, revealAgents)
	ws.ReloadAgents()

	todo, err := ws.SaveTodo("", repo, "ship the thing", "spec", []string{""}, []string{"add a health endpoint"})
	if err != nil {
		t.Fatalf("save todo: %v", err)
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.startTodoStep(c, command{TodoID: todo.ID, StepID: todo.Steps[0].ID, Agent: "gocli", Worktree: true})
	paneID := waitAgentStarted(t, c)

	p := ws.Pane(paneID)
	if p == nil {
		t.Fatal("the started pane does not exist")
	}
	if samePath(filepath.Clean(p.Cwd), filepath.Clean(repo)) {
		t.Errorf("pane cwd = %s, want a worktree of its own", p.Cwd)
	}
	want := todoStepBranch(todo, todo.Steps[0])
	if p.Branch != want {
		t.Errorf("branch = %q, want it namespaced under the todo: %q", p.Branch, want)
	}
}

// waitAgentStarted reads an agentStarted message off c and returns its pane
// id, the same shape runStartAgent reads for startAgent's own reply.
func waitAgentStarted(t *testing.T, c *controlClient) string {
	t.Helper()
	for i := 0; i < 2; i++ {
		select {
		case raw := <-c.out:
			var head struct {
				Type string `json:"type"`
			}
			if unmarshalHead(raw, &head) != nil {
				continue
			}
			if head.Type == "notice" {
				var msg noticeMsg
				_ = unmarshalHead(raw, &msg)
				t.Fatalf("startTodoStep said %q", msg.Text)
			}
			if head.Type == "agentStarted" {
				var msg struct {
					PaneID string `json:"paneId"`
				}
				if err := unmarshalHead(raw, &msg); err != nil {
					t.Fatalf("agentStarted: %v", err)
				}
				return msg.PaneID
			}
		case <-timeoutChan():
			t.Fatal("startTodoStep never answered")
		}
	}
	t.Fatal("startTodoStep never sent agentStarted")
	return ""
}

// TestClosingATodoStepsPaneRecordsItsOutcome covers the auto-detect half of
// the feature end to end: a step started, then whose pane is closed idle,
// comes back ticked done with its attempt's outcome recorded -- the same
// read a fan-out's own settled job takes at close time (see
// captureFanoutHistory), reused here rather than a second lifecycle hook.
func TestClosingATodoStepsPaneRecordsItsOutcome(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	todo, err := ws.SaveTodo("", root, "ship the thing", "spec", []string{""}, []string{"first step"})
	if err != nil {
		t.Fatalf("save todo: %v", err)
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.startTodoStep(c, command{TodoID: todo.ID, StepID: todo.Steps[0].ID, Agent: "gocli"})
	paneID := waitAgentStarted(t, c)
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

// TestPreviewTodoPlanAnswersWithThePaneAndRoot covers previewTodoPlan's own
// wiring, distinct from fan-out's dialog: the extraction itself (planTasks,
// ExtractTasks) is already covered by the workspace package's own tests, so
// this only pins that the answer names the pane and root it was asked
// about, the way a todo's spec-drafting flow needs to match the preview
// back to the request that opened it.
func TestPreviewTodoPlanAnswersWithThePaneAndRoot(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()
	lead := wherePlaced(t, srv, ws)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.previewTodoPlan(c, lead.pane, root)

	select {
	case raw := <-c.out:
		var msg todoPlanPreviewMsg
		if err := unmarshalHead(raw, &msg); err != nil {
			t.Fatalf("todoPlanPreview: %v", err)
		}
		if msg.PaneID != lead.pane {
			t.Errorf("paneId = %q, want %q", msg.PaneID, lead.pane)
		}
		if msg.Root != root {
			t.Errorf("root = %q, want %q", msg.Root, root)
		}
	case <-timeoutChan():
		t.Fatal("previewTodoPlan never answered")
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
