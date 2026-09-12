package server

import (
	"encoding/json"
	"testing"
	"time"

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
