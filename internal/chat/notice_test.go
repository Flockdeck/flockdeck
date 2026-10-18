package chat

import (
	"strings"
	"sync"
	"testing"
)

// A message for a chat pane is told in the reply to its prompt hook. Flockdeck
// counts the message as told once it has answered, so the reply has to reach
// the model: dropped, the message would never be announced again.
func TestAMessageNoticeInTheReplyToAPromptReachesTheModelForThatTurnOnly(t *testing.T) {
	pane := newPaneServer(t, "you are pane one")
	var mu sync.Mutex
	var told []string
	pane.srv.SetNoticeHandler(func(string) string {
		mu.Lock()
		defer mu.Unlock()
		if len(told) > 0 {
			return ""
		}
		told = append(told, "x")
		return "[flockdeck] 1 new message from work (reviewer) (#4). Run: flockdeck inbox."
	})
	wire := &scriptedWire{turns: []turnFunc{says("first"), says("second")}}
	run(t, Options{
		Agent: "anthropic", Model: "claude-opus-5",
		API: pane.srv.BaseURL(), Token: pane.srv.Token(),
	}, "hello\nagain\n/exit\n", wire)

	reqs := wire.requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want 2", len(reqs))
	}
	if !strings.Contains(reqs[0].System, "1 new message from work (reviewer) (#4)") {
		t.Errorf("the first turn did not carry the notice:\n%s", reqs[0].System)
	}
	if strings.Contains(reqs[1].System, "new message") {
		t.Errorf("the notice was repeated on the next turn:\n%s", reqs[1].System)
	}
	if !strings.Contains(reqs[1].System, "you are pane one") {
		t.Errorf("the briefing was lost:\n%s", reqs[1].System)
	}
}
