package server

import (
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// A window reached through the relay is refused until remote sends are decided.
func TestPaneSendIsRefusedToARemoteWindow(t *testing.T) {
	srv, ws := newTestServer(t)
	id, _ := ask(srv, func() string { return ws.FocusedPane().ID })
	sess := sessionOf(t, srv, ws, id)
	c := &controlClient{out: make(chan []byte, 8), remote: true, device: "dev-1"}
	srv.paneSend(c, command{Cmd: "paneSend", ID: id, Text: "echo remote-send", AllowShell: true})
	msg, note := paneSendReply(t, c)
	if !note.Error || note.Text != paneSendDeskOnly {
		t.Fatalf("got %+v and notice %+v, want the desk-only refusal", msg, note)
	}
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(sess.RecentText(8192), "remote-send") {
		t.Error("a remote window's text reached the pane")
	}
}

// A device that may only view sends the command over its socket and is refused.
func TestPaneSendIsRefusedToAViewerSocket(t *testing.T) {
	srv, ws := newTestServer(t)
	id, _ := ask(srv, func() string { return ws.FocusedPane().ID })
	sess := sessionOf(t, srv, ws, id)
	setAccess(t, srv, "watch-dev", "viewer", true)
	ts := remoteServer(t, srv)
	watcher, err := dialRemoteControlDevice(ts, ts.URL, "watch-dev")
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.CloseNow()
	nextState(t, watcher, nil)

	sendCmd(t, watcher, command{Cmd: "paneSend", ID: id, Text: "echo viewer-send", AllowShell: true})
	var note noticeMsg
	readUntil(t, watcher, "notice", &note)
	if note.Text != viewerRefused {
		t.Errorf("paneSend from a viewer was answered %+v", note)
	}
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(sess.RecentText(8192), "viewer-send") {
		t.Error("a viewer's text reached the pane")
	}
}

// Control characters and a pane waiting on a permission prompt are refused;
// a pane waiting on a question is sent the text and flagged.
func TestPaneSendControlCharactersAndWaitingPanes(t *testing.T) {
	srv, ws := newTestServer(t)
	id, _ := ask(srv, func() string {
		p := ws.FocusedPane()
		p.Kind = session.KindAgent
		return p.ID
	})
	sess := sessionOf(t, srv, ws, id)
	c := &controlClient{out: make(chan []byte, 8)}

	srv.handleCommand(c, command{Cmd: "paneSend", ID: id, Text: "\x1b"})
	if _, note := paneSendReply(t, c); !note.Error || !strings.Contains(note.Text, "no text") {
		t.Errorf("an escape on its own: notice %+v, want the empty refusal", note)
	}

	sess.SetStatusFull(session.StatusWaiting, "Bash", `{"command":"rm -rf x"}`)
	srv.handleCommand(c, command{Cmd: "paneSend", ID: id, Text: "1"})
	if _, note := paneSendReply(t, c); !note.Error || !strings.Contains(note.Text, "permission prompt") {
		t.Errorf("permission prompt: notice %+v, want a refusal naming it", note)
	}

	sess.SetStatusFull(session.StatusWaiting, "AskUserQuestion", `{"questions":[{"question":"which?"}]}`)
	srv.handleCommand(c, command{Cmd: "paneSend", ID: id, Text: "echo answer-to-ask"})
	if msg, note := paneSendReply(t, c); note.Text != "" || !msg.Waiting {
		t.Errorf("question: got %+v and notice %+v, want accepted with waiting", msg, note)
	}
}
