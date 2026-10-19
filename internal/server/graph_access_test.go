package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Who may use graphSnapshot, graphWatch, graphUnwatch and paneSend: the window on
// the machine Flockdeck runs on, and nobody reached through the relay. A device
// that may only view is refused by commandAccess, and a full device is refused
// by the command itself (teamDeskOnly for the graph, paneSendDeskOnly for
// paneSend). The phone scope is not decided here; the refusals are the places to
// lift when it is.

var graphAndSendCommands = []string{"graphSnapshot", "graphWatch", "graphUnwatch", "paneSend"}

// readQuick is readUntil with a short wait, so a refusal that never comes fails
// the test in a few seconds.
func readQuick(t *testing.T, conn *websocket.Conn, typ string, out any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for a %q message: %v", typ, err)
		}
		if typeOf(data) == typ {
			if err := json.Unmarshal(data, out); err != nil {
				t.Fatalf("decode %s: %v", typ, err)
			}
			return
		}
	}
}

func TestGraphAndPaneSendAreFullAccessOnly(t *testing.T) {
	for _, cmd := range graphAndSendCommands {
		if commandAccess[cmd] != accessFull {
			t.Errorf("%s is not full-access only, so a viewer could use it", cmd)
		}
	}
}

// accessCommand is a command of the four, aimed at the pane id given.
func accessCommand(name, root, pane string) command {
	return command{Cmd: name, Root: root, ID: pane, Text: "echo access-test", AllowShell: true, Req: "r"}
}

// The window on this machine may use each of the four.
func TestALocalWindowMayUseTheGraphCommandsAndPaneSend(t *testing.T) {
	srv, ws := newTestServer(t)
	root := ws.ActiveRoot()
	pane, _ := ask(srv, func() string { return ws.FocusedPane().ID })
	c := &controlClient{out: make(chan []byte, 16)}

	next := func(want string) []byte {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			select {
			case raw := <-c.out:
				if typeOf(raw) == want {
					return raw
				}
			case <-deadline:
				t.Fatalf("no %s message", want)
			}
		}
	}

	srv.handleCommand(c, accessCommand("graphSnapshot", root, pane))
	var g graphMsg
	if err := json.Unmarshal(next("graph"), &g); err != nil || g.Root == "" {
		t.Errorf("graphSnapshot answered %+v (%v)", g, err)
	}

	srv.handleCommand(c, accessCommand("graphWatch", root, pane))
	next("graph")
	if watching, _ := ask(srv, func() bool { return c.graphRoot != "" }); !watching {
		t.Error("graphWatch did not subscribe the window")
	}

	srv.handleCommand(c, accessCommand("graphUnwatch", root, pane))
	if watching, _ := ask(srv, func() bool { return c.graphRoot != "" }); watching {
		t.Error("graphUnwatch did not unsubscribe the window")
	}

	srv.handleCommand(c, accessCommand("paneSend", root, pane))
	var sent paneSendMsg
	if err := json.Unmarshal(next("paneSend"), &sent); err != nil || sent.ID != pane {
		t.Errorf("paneSend answered %+v (%v)", sent, err)
	}
	// The text is typed into the pane, which is what the relay test's check that
	// none was typed relies on being visible.
	waitForText(t, sessionOf(t, srv, ws, pane), "access-test")
}

// A window reached through the relay is refused each of the four, and nothing is
// done: it is not subscribed and no text reaches a pane.
func TestARelayWindowIsRefusedTheGraphCommandsAndPaneSend(t *testing.T) {
	srv, ws := newTestServer(t)
	root := ws.ActiveRoot()
	pane, _ := ask(srv, func() string { return ws.FocusedPane().ID })
	sess := sessionOf(t, srv, ws, pane)
	c := &controlClient{out: make(chan []byte, 16), remote: true, device: "dev-1"}

	for _, name := range graphAndSendCommands {
		srv.handleCommand(c, accessCommand(name, root, pane))
		var refusals []string
		for len(refusals) == 0 {
			select {
			case raw := <-c.out:
				switch typeOf(raw) {
				case "teamResult":
					var res teamResultMsg
					_ = json.Unmarshal(raw, &res)
					if res.OK || res.Op != name {
						t.Errorf("%s: %+v", name, res)
					}
					refusals = append(refusals, res.Error)
				case "notice":
					var n noticeMsg
					_ = json.Unmarshal(raw, &n)
					if n.Error {
						refusals = append(refusals, n.Text)
					}
				case "graph", "paneSend":
					t.Fatalf("%s was answered: %s", name, raw)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s was not answered", name)
			}
		}
		want := teamDeskOnly
		if name == "paneSend" {
			want = paneSendDeskOnly
		}
		if refusals[0] != want {
			t.Errorf("%s refused with %q, want %q", name, refusals[0], want)
		}
		for len(c.out) > 0 { // the notice that goes with a refusal
			<-c.out
		}
	}
	if watching, _ := ask(srv, func() bool { return c.graphRoot != "" }); watching {
		t.Error("a relay window was subscribed to the graph")
	}
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(sess.RecentText(8192), "access-test") {
		t.Error("a relay window's text reached the pane")
	}
}

// A viewer device, on its own socket, is refused each of the four by
// commandAccess before the command runs.
func TestAViewerDeviceIsRefusedTheGraphCommandsAndPaneSend(t *testing.T) {
	srv, ws := newTestServer(t)
	root := ws.ActiveRoot()
	pane, _ := ask(srv, func() string { return ws.FocusedPane().ID })
	setAccess(t, srv, "watch-dev", "viewer", true)
	ts := remoteServer(t, srv)
	watcher, err := dialRemoteControlDevice(ts, ts.URL, "watch-dev")
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.CloseNow()
	nextState(t, watcher, nil)

	for _, name := range graphAndSendCommands {
		sendCmd(t, watcher, accessCommand(name, root, pane))
		var note noticeMsg
		readQuick(t, watcher, "notice", &note)
		if note.Text != viewerRefused {
			t.Errorf("%s from a viewer was answered %+v", name, note)
		}
	}
}

// A full device, on its own socket through the relay, is refused each of the
// four by the command.
func TestAFullRemoteDeviceIsRefusedTheGraphCommandsAndPaneSend(t *testing.T) {
	srv, ws := newTestServer(t)
	root := ws.ActiveRoot()
	pane, _ := ask(srv, func() string { return ws.FocusedPane().ID })
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()

	for _, name := range graphAndSendCommands {
		sendCmd(t, phone, accessCommand(name, root, pane))
		if name == "paneSend" {
			var note noticeMsg
			readQuick(t, phone, "notice", &note)
			if !note.Error || note.Text != paneSendDeskOnly {
				t.Errorf("paneSend from a full remote device = %+v", note)
			}
			continue
		}
		var res teamResultMsg
		readQuick(t, phone, "teamResult", &res)
		if res.OK || res.Error != teamDeskOnly || res.Op != name {
			t.Errorf("%s from a full remote device = %+v", name, res)
		}
		// A refusal of a graph command is also told as a notice.
		var note noticeMsg
		readQuick(t, phone, "notice", &note)
		if !note.Error || note.Text != teamDeskOnly {
			t.Errorf("%s: the notice with the refusal = %+v", name, note)
		}
	}
}
