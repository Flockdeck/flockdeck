package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// structuredCancel stops the running turn of one structured pane: the agent is
// told to cancel that session, and nothing else is touched.

// spawnStructured starts another structured helper of the boss with task.
func (e *structuredEnv) spawnStructured(t *testing.T, task string) string {
	t.Helper()
	id, ok := ask(e.srv, func() string {
		id, err := e.ws.Spawn(e.boss, workspace.SpawnOptions{Task: task, Role: "worker", Agent: "opencode-acp",
			Kind: session.KindClaude, SpawnedByAgent: true})
		if err != nil {
			t.Errorf("spawn: %v", err)
		}
		return id
	})
	if !ok || id == "" {
		t.Fatal("no second structured pane")
	}
	return id
}

// waitStatus waits for the pane id to have the status.
func (e *structuredEnv) waitStatus(t *testing.T, id string, want session.Status) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		st, _ := ask(e.srv, func() session.Status { st, _ := e.ws.Pane(id).Status(); return st })
		if st == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s: status %v, want %v", id, st, want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (e *structuredEnv) cancel(t *testing.T, c *controlClient, id string) teamResultMsg {
	t.Helper()
	e.srv.handleCommand(c, command{Cmd: "structuredCancel", ID: id, Req: "c1"})
	var res teamResultMsg
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-c.out:
			if typeOf(raw) == "teamResult" {
				if err := json.Unmarshal(raw, &res); err != nil {
					t.Fatal(err)
				}
				return res
			}
		case <-deadline:
			t.Fatal("structuredCancel was not answered")
		}
	}
}

// Cancelling one pane ends its turn and leaves the other pane's turn running.
func TestStructuredCancelStopsOnlyThatPanesTurn(t *testing.T) {
	e := newStructuredEnv(t, "slow")
	other := e.spawnStructured(t, "slow")
	e.waitStatus(t, e.child, session.StatusWorking)
	e.waitStatus(t, other, session.StatusWorking)

	res := e.cancel(t, e.c, e.child)
	if !res.OK || res.Op != "structuredCancel" || res.Req != "c1" || res.Error != "" {
		t.Fatalf("answer = %+v", res)
	}
	e.waitStatus(t, e.child, session.StatusIdle)
	if st, _ := ask(e.srv, func() session.Status { st, _ := e.ws.Pane(other).Status(); return st }); st != session.StatusWorking {
		t.Errorf("the other pane's status is %v after the first was cancelled, want working", st)
	}

	// The other is still cancellable, so the first answer did not stop it by accident.
	if res := e.cancel(t, e.c, other); !res.OK {
		t.Fatalf("the second pane: %+v", res)
	}
	e.waitStatus(t, other, session.StatusIdle)
}

// A terminal pane and a pane that is not there are refused, and say why. An
// idle structured pane has a live session and a cancel of no turn is accepted;
// one whose agent is not running (structuredIdle) is refused.
func TestStructuredCancelRefusesWhatItCannotStop(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	for id, want := range map[string]string{
		e.dev:     structuredNotPane,
		"no-such": structuredNotPane,
		"":        structuredNotPane,
	} {
		if res := e.cancel(t, e.c, id); res.OK || res.Error != want {
			t.Errorf("%q: %+v", id, res)
		}
	}
	// An idle structured pane has a session but no turn.
	res := e.cancel(t, e.c, e.child)
	if !res.OK {
		t.Errorf("an idle pane with a live session: %+v", res)
	}
	e.waitStatus(t, e.child, session.StatusIdle)
}

// A window reached through the relay, a full device included, cannot stop a
// turn, and the turn goes on. A viewer is refused by the table.
func TestStructuredCancelIsRefusedToTheRelayAndTheTurnGoesOn(t *testing.T) {
	e := newStructuredEnv(t, "slow")
	e.waitStatus(t, e.child, session.StatusWorking)
	remote := &controlClient{out: make(chan []byte, 8), remote: true, device: "full-dev"}
	res := e.cancel(t, remote, e.child)
	if res.OK || res.Error != structuredDeskOnly || res.Op != "structuredCancel" {
		t.Fatalf("answer = %+v", res)
	}
	if st, _ := ask(e.srv, func() session.Status { st, _ := e.ws.Pane(e.child).Status(); return st }); st != session.StatusWorking {
		t.Errorf("the relay's refused cancel ended the turn: status %v", st)
	}
	if commandAccess["structuredCancel"] != accessFull {
		t.Error("structuredCancel is not accessFull, so a viewer could send it")
	}
	e.cancel(t, e.c, e.child) // let the fake agent go
	e.waitStatus(t, e.child, session.StatusIdle)
}
