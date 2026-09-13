package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestAPromptWithNowhereToGoIsSaidSo covers the prompt bar in a tab whose
// focused pane has stopped, with nothing else in the broadcast. Only a pane
// with a running process takes the text, so it went nowhere -- and the bar
// closed as though it had been sent.
func TestAPromptWithNowhereToGoIsSaidSo(t *testing.T) {
	srv, ws := newTestServer(t)
	p, _ := ask(srv, func() *workspace.Pane { return ws.FocusedPane() })
	if p == nil || p.Sess == nil {
		t.Fatal("the test server was expected to start a shell pane")
	}
	_ = p.Sess.Close()
	for deadline := time.Now().Add(10 * time.Second); ; {
		alive, _ := ask(srv, func() bool {
			fp := ws.FocusedPane()
			return fp != nil && fp.Alive()
		})
		if !alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane's process did not end")
		}
		time.Sleep(50 * time.Millisecond)
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "sendPrompt", Text: "hello"})
	ask(srv, func() bool { return true }) // the command has run once this has
	select {
	case raw := <-c.out:
		var note noticeMsg
		if err := json.Unmarshal(raw, &note); err != nil || note.Type != "notice" || !note.Error {
			t.Fatalf("got %s; want an error notice saying the text went nowhere", raw)
		}
	default:
		t.Fatal("a prompt that reached no pane went unmentioned")
	}
}

// TestAPromptGoesToThePaneItWasWrittenFor covers the prompt bar while focus
// moves under it: the desk clicking another pane, a fan-out revealing its
// first agent, a pane picked from the agents list. The prompt went to
// whichever pane had focus when it was sent, rather than the one the bar was
// opened on, and a pane no longer on screen had its prompt sent elsewhere.
func TestAPromptGoesToThePaneItWasWrittenFor(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	st := nextState(t, conn, nil)
	written := st.Tabs[0].Root.Pane
	sendCmd(t, conn, command{Cmd: "splitPane", ID: written, Dir: "h", Kind: "shell"})
	st = nextState(t, conn, func(s stateMsg) bool { return len(s.Tabs[0].Root.Children) == 2 })
	other := st.Tabs[0].Root.Children[0].Pane
	if other == written {
		other = st.Tabs[0].Root.Children[1].Pane
	}
	// Focus moves on while the bar is open.
	sendCmd(t, conn, command{Cmd: "focusPane", ID: other})
	nextState(t, conn, func(s stateMsg) bool { return s.Tabs[0].Focus == other })

	sessOf := func(id string) *session.Session {
		s, _ := ask(srv, func() *session.Session {
			if p := ws.Pane(id); p != nil {
				return p.Sess
			}
			return nil
		})
		if s == nil {
			t.Fatalf("pane %s has no shell", id)
		}
		return s
	}
	target, bystander := sessOf(written), sessOf(other)

	const marker = "prompt-for-the-pane-it-was-written-for"
	sendCmd(t, conn, command{Cmd: "sendPrompt", ID: written, Text: "echo " + marker})
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(target.RecentText(8192), marker) {
		if time.Now().After(deadline) {
			t.Fatalf("the prompt never reached the pane it was written for; it shows:\n%s", target.RecentText(8192))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.Contains(bystander.RecentText(8192), marker) {
		t.Error("the prompt reached the pane focus had moved to")
	}
	if focus, _ := ask(srv, func() string { return ws.CurrentTab().Focus }); focus != other {
		t.Errorf("sending the prompt moved focus to %s, want it left on %s", focus, other)
	}

	// A pane no longer on screen is not guessed at.
	sendCmd(t, conn, command{Cmd: "sendPrompt", ID: "gone", Text: "echo " + marker + "-again"})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "not sent") {
		t.Errorf("a prompt for a pane no longer on screen was told %+v, want it not sent", note)
	}
	time.Sleep(500 * time.Millisecond)
	if strings.Contains(bystander.RecentText(8192), marker+"-again") {
		t.Error("a prompt for a pane no longer on screen reached the focused one")
	}
}
