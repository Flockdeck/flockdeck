package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestADefaultNotSavedWhileClosingSaysSo covers a default agent set as
// Flockdeck closes, when the workspace gives no answer. The empty answer was
// read as one, and the window was told `there is no agent called ""` -- an
// agent nobody asked for, and a reason that was not the reason.
func TestADefaultNotSavedWhileClosingSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.Close()

	c := &controlClient{out: make(chan []byte, 8)}
	srv.applyAgentDefault(c, command{Cmd: "setAgentDefault", Agent: "anthropic"})
	select {
	case raw := <-c.out:
		var n noticeMsg
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(n.Text, "no agent called") || !strings.Contains(n.Text, "not saved") || !n.Error {
			t.Errorf("the window was told %q (error %v), want that the default was not saved", n.Text, n.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the window was told nothing")
	}
}
