package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// paneSendReply waits for the answer to a paneSend sent with handleCommand:
// the paneSend message when it was accepted, or the notice that refused it.
func paneSendReply(t *testing.T, c *controlClient) (msg paneSendMsg, note noticeMsg) {
	t.Helper()
	select {
	case raw := <-c.out:
		var head struct{ Type string }
		_ = json.Unmarshal(raw, &head)
		switch head.Type {
		case "paneSend":
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatal(err)
			}
		case "notice":
			if err := json.Unmarshal(raw, &note); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("got %s, want a paneSend or a notice", raw)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("paneSend was not answered")
	}
	return msg, note
}

func waitForText(t *testing.T, s *session.Session, want string) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); !strings.Contains(s.RecentText(8192), want); {
		if time.Now().After(deadline) {
			t.Fatalf("%q never reached the pane; it shows:\n%s", want, s.RecentText(8192))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func sessionOf(t *testing.T, srv *Server, ws *workspace.Workspace, id string) *session.Session {
	t.Helper()
	s, _ := ask(srv, func() *session.Session {
		if p := ws.Pane(id); p != nil {
			return p.Sess
		}
		return nil
	})
	if s == nil {
		t.Fatalf("pane %s has no process", id)
	}
	return s
}

// A pane in a tab that is not on screen takes the text, which sendPrompt
// refuses, and the pane on screen does not.
func TestPaneSendReachesAPaneInATabNotOnScreen(t *testing.T) {
	srv, ws := newTestServer(t)
	first, _ := ask(srv, func() string { return ws.FocusedPane().ID })
	ask(srv, func() bool {
		ws.NewTab(session.KindShell, ws.ActiveRoot(), "second")
		return true
	})
	onScreen, _ := ask(srv, func() bool { return ws.CurrentTab().Tree.Find(first) != nil })
	if onScreen {
		t.Fatal("the first pane is still on screen; the test needs it in another tab")
	}
	bystander, _ := ask(srv, func() string { return ws.FocusedPane().ID })

	c := &controlClient{out: make(chan []byte, 8)}
	const marker = "send-to-a-pane-off-screen"
	srv.handleCommand(c, command{Cmd: "paneSend", ID: first, Text: "echo " + marker, AllowShell: true})
	msg, note := paneSendReply(t, c)
	if note.Text != "" || msg.ID != first || !msg.Shell {
		t.Fatalf("got %+v and notice %+v, want the send accepted for a shell", msg, note)
	}
	waitForText(t, sessionOf(t, srv, ws, first), marker)
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(sessionOf(t, srv, ws, bystander).RecentText(8192), marker) {
		t.Error("the text reached the pane on screen")
	}
}

// With broadcast on, the text goes to the pane named and to no other.
func TestPaneSendIgnoresBroadcast(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	st := nextState(t, conn, nil)
	named := st.Tabs[0].Root.Pane
	sendCmd(t, conn, command{Cmd: "splitPane", ID: named, Dir: "h", Kind: "shell"})
	st = nextState(t, conn, func(s stateMsg) bool { return len(s.Tabs[0].Root.Children) == 2 })
	other := st.Tabs[0].Root.Children[0].Pane
	if other == named {
		other = st.Tabs[0].Root.Children[1].Pane
	}
	// The default broadcast covers agent panes, so both are made to be.
	ask(srv, func() bool {
		ws.Pane(named).Kind = session.KindAgent
		ws.Pane(other).Kind = session.KindAgent
		ws.ToggleBroadcast()
		return true
	})
	if n, _ := ask(srv, func() int { return len(ws.BroadcastTargetsFor(named)) }); n < 2 {
		t.Fatalf("broadcast reaches %d panes, want the test to have it on for both", n)
	}

	c := &controlClient{out: make(chan []byte, 8)}
	const marker = "only-the-named-pane"
	srv.handleCommand(c, command{Cmd: "paneSend", ID: named, Text: "echo " + marker, AllowShell: true})
	if _, note := paneSendReply(t, c); note.Text != "" {
		t.Fatalf("refused: %+v", note)
	}
	waitForText(t, sessionOf(t, srv, ws, named), marker)
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(sessionOf(t, srv, ws, other).RecentText(8192), marker) {
		t.Error("with broadcast on, the text reached a pane that was not named")
	}
}

func TestPaneSendRefusals(t *testing.T) {
	srv, ws := newTestServer(t)
	id, _ := ask(srv, func() string { return ws.FocusedPane().ID })
	sess := sessionOf(t, srv, ws, id)
	c := &controlClient{out: make(chan []byte, 8)}
	refused := func(name string, cmd command, want string) {
		t.Helper()
		srv.handleCommand(c, cmd)
		msg, note := paneSendReply(t, c)
		if !note.Error || !strings.Contains(note.Text, want) {
			t.Errorf("%s: got %+v and notice %+v, want a refusal containing %q", name, msg, note, want)
		}
	}
	refused("shell without allowShell", command{Cmd: "paneSend", ID: id, Text: "echo no"}, "shell")
	refused("empty", command{Cmd: "paneSend", ID: id, Text: "  \n", AllowShell: true}, "no text")
	refused("oversized", command{Cmd: "paneSend", ID: id, Text: strings.Repeat("a", workspace.MaxPaneSendBytes+1), AllowShell: true}, "16 KB")
	refused("gone", command{Cmd: "paneSend", ID: "nope", Text: "hi", AllowShell: true}, "no longer open")

	_ = sess.Close()
	for deadline := time.Now().Add(10 * time.Second); !sess.Exited(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the pane's process did not end")
		}
	}
	refused("exited", command{Cmd: "paneSend", ID: id, Text: "hi", AllowShell: true}, "exited")
}

// Text of exactly 16 KB is taken.
func TestPaneSendTakesTheLimit(t *testing.T) {
	srv, ws := newTestServer(t)
	id, _ := ask(srv, func() string { return ws.FocusedPane().ID })
	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "paneSend", ID: id, Text: strings.Repeat("a", workspace.MaxPaneSendBytes), AllowShell: true})
	if _, note := paneSendReply(t, c); note.Text != "" {
		t.Errorf("text of exactly 16 KB was refused: %+v", note)
	}
}

// A pane in the middle of a turn is sent the text and told so, for the window
// to show it as queued behind the turn.
func TestPaneSendSaysWhenThePaneIsMidTurn(t *testing.T) {
	srv, ws := newTestServer(t)
	id, _ := ask(srv, func() string {
		p := ws.FocusedPane()
		p.Kind = session.KindAgent
		return p.ID
	})
	sess := sessionOf(t, srv, ws, id)
	c := &controlClient{out: make(chan []byte, 8)}

	sess.SetStatus(session.StatusIdle, "")
	srv.handleCommand(c, command{Cmd: "paneSend", ID: id, Text: "echo idle-turn"})
	if msg, note := paneSendReply(t, c); note.Text != "" || msg.Queued || msg.Shell {
		t.Fatalf("idle agent: got %+v and notice %+v, want accepted and not queued", msg, note)
	}
	waitForText(t, sess, "idle-turn")

	sess.SetStatus(session.StatusWorking, "")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		srv.handleCommand(c, command{Cmd: "paneSend", ID: id, Text: "echo busy-turn"})
		msg, note := paneSendReply(t, c)
		if note.Text == "" {
			if !msg.Queued {
				t.Fatalf("working agent: got %+v, want queued", msg)
			}
			break
		}
		// The write of the first send may still be in flight.
		if !strings.Contains(note.Text, "still taking in") || time.Now().After(deadline) {
			t.Fatalf("working agent: refused with %+v", note)
		}
	}
	waitForText(t, sess, "busy-turn")
}

func TestPaneSendIsNotForViewers(t *testing.T) {
	if commandAccess["paneSend"] != accessFull {
		t.Error("paneSend types into a pane, so a viewer must not be allowed it")
	}
}
