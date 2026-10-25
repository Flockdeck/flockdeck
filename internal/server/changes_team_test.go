package server

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// answerOf reads the one message a command is answered with.
func answerOf(t *testing.T, c *controlClient) changesResultMsg {
	t.Helper()
	select {
	case raw := <-c.out:
		var m changesResultMsg
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("the command was not answered")
	}
	return changesResultMsg{}
}

// The hello of a window through the relay does not list the changes caps; one on
// this machine lists the ones that are built.
func TestTheRelayHelloLeavesOutTheChangesCaps(t *testing.T) {
	if !slices.Contains(apiFor(false).Caps, "changesTeam") {
		t.Error("a window on this machine is not told of changesTeam")
	}
	for _, n := range changesCaps {
		if slices.Contains(apiFor(true).Caps, n) {
			t.Errorf("a relay window is told of %s", n)
		}
	}
	if !slices.Contains(apiFor(true).Caps, "clientHello") {
		t.Error("a relay window lost the caps that are not the changes view's")
	}
}

func TestFilesPageRefusesWhatItCannotAnswer(t *testing.T) {
	srv, _ := newTestServer(t)
	for name, c := range map[string]struct {
		cmd  command
		code string
	}{
		"no id":         {command{Cmd: "filesPage", Req: "r"}, "badRequest"},
		"limit too big": {command{Cmd: "filesPage", Req: "r", ID: "p1", Limit: 501}, "badRequest"},
		"negative page": {command{Cmd: "filesPage", Req: "r", ID: "p1", Offset: -1}, "badRequest"},
		"no such pane":  {command{Cmd: "filesPage", Req: "r", ID: "nope"}, "noBranch"},
	} {
		t.Run(name, func(t *testing.T) {
			cl := &controlClient{out: make(chan []byte, 8)}
			srv.handleCommand(cl, c.cmd)
			m := answerOf(t, cl)
			if m.Type != "changesResult" || m.Op != "filesPage" || m.Req != "r" || m.OK || m.Code != c.code || m.Error == "" {
				t.Errorf("answer = %+v, want a %s refusal carrying op and req", m, c.code)
			}
		})
	}
}

func TestChangesSnapshotOfAProjectThatIsNotOpenIsRefused(t *testing.T) {
	srv, _ := newTestServer(t)
	cl := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(cl, command{Cmd: "changesSnapshot", Req: "r", Root: "Z:\\no\\such\\project"})
	if m := answerOf(t, cl); m.Code != "badRequest" || m.Req != "r" {
		t.Errorf("answer = %+v", m)
	}
}

// A watch that is replaced or unwatched stops, and a window through the relay
// cannot start one.
func TestAWatchStopsWhenItIsReplacedOrUnwatched(t *testing.T) {
	srv, _ := newTestServer(t)
	cl := &controlClient{out: make(chan []byte, 8)}
	stopped := 0
	srv.do(func() { cl.changes = &changesWatcher{root: "x", cancel: func() { stopped++ }} })
	srv.handleCommand(cl, command{Cmd: "changesUnwatch"})
	srv.do(func() {})
	if stopped != 1 || cl.changes != nil {
		t.Errorf("stopped %d, watch %v, want it stopped once and cleared", stopped, cl.changes)
	}

	relay := &controlClient{out: make(chan []byte, 8), remote: true, device: "d"}
	srv.handleCommand(relay, command{Cmd: "changesWatch", Req: "r"})
	if relay.changes != nil {
		t.Error("a relay window started a watch")
	}
	if m := answerOf(t, relay); m.Code != "deskOnly" {
		t.Errorf("answer = %+v, want deskOnly", m)
	}
}
