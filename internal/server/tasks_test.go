package server

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// The task board is served over the same loopback API as the team commands: a
// pane acts as itself, and a pane that is not open, or a wrong token, is refused.
func TestTaskCommandsOverTheCallbackAPI(t *testing.T) {
	srv, ws := newTestServer(t)
	hookSrv := ws.HookServer()
	api, token := hookSrv.BaseURL(), hookSrv.Token()
	boss := agentPane(t, srv, ws, "boss", "manager")
	dev := agentPane(t, srv, ws, "dev", "worker")

	added, err := hooks.Task(api, token, boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "over http", Role: "worker"})
	if err != nil || added.Task == nil || added.Task.ID != "T1" || added.Task.Creator.Pane != boss {
		t.Fatalf("add = %+v, %v", added, err)
	}
	if _, err := hooks.Task(api, token, dev, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: dev}); err == nil || !strings.Contains(err.Error(), "only the manager role assigns") {
		t.Errorf("a worker assigned a task: %v", err)
	}
	res, err := hooks.Task(api, token, boss, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: dev})
	if err != nil || res.Task.OwnerPane != dev || res.Told != 1 {
		t.Fatalf("assign = %+v, %v", res, err)
	}
	if msgs, err := hooks.Inbox(api, token, dev, false); err != nil || len(msgs) != 1 || !msgs[0].System() || !strings.Contains(msgs[0].Text, "task T1 assigned to you: over http") {
		t.Errorf("the owner's inbox = %+v, %v", msgs, err)
	}
	list, err := hooks.Task(api, token, dev, hooks.TaskRequest{Op: hooks.TaskList, Mine: true})
	if err != nil || len(list.Tasks) != 1 {
		t.Errorf("list -mine = %+v, %v", list, err)
	}
	if list, _ := hooks.Task(api, token, boss, hooks.TaskRequest{Op: hooks.TaskList, Mine: true}); len(list.Tasks) != 0 {
		t.Errorf("the manager owns %d tasks", len(list.Tasks))
	}

	if _, err := hooks.Task(api, token, "forged-pane", hooks.TaskRequest{Op: hooks.TaskList}); err == nil || !strings.Contains(err.Error(), "is open here") {
		t.Errorf("a spoofed pane = %v", err)
	}
	if _, err := hooks.Task(api, "not-the-token", boss, hooks.TaskRequest{Op: hooks.TaskList}); err == nil {
		t.Error("a request with the wrong token was accepted")
	}
	if _, err := hooks.Task(api, token, boss, hooks.TaskRequest{}); err == nil {
		t.Error("a request with no operation was sent")
	}
}
