package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestAPaneFollowsItsAgentIntoANewConversation covers /clear in a Claude pane.
// Claude Code carries on under a new session id and reports it with every hook
// event, but everything that finds the pane's conversation again asked by the
// pane's own id, which from then on names the conversation before the clear:
// restarting the pane, or restoring the layout, brought that one back.
func TestAPaneFollowsItsAgentIntoANewConversation(t *testing.T) {
	isolateConfig(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// `go` given the session it is started under says it has no such command,
	// naming it, which is how the test sees what a restart handed the agent.
	body := `{"version": 1, "agents": [{"id": "gocli", "name": "Go", "exe": "go",
		"args": [{"value": "{{session}}"}], "caps": {"resume": true}}]}`
	if err := os.WriteFile(filepath.Join(dir, agent.ConfigName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "gocli"}, root, "")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}
	waitExited(t, p.Sess)

	// What /clear looks like from here: the agent carries on under a new id.
	const cleared = "55555555-5555-5555-5555-555555555555"
	ws.handleHook(hooks.Event{SessionID: p.ID, Event: "SessionStart", Source: "clear", Conversation: cleared})

	ws.RestartPane()
	if p.Sess == nil {
		t.Fatalf("the restarted pane did not start: %v", p.Err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(p.Sess.RecentText(4096), cleared) {
		if time.Now().After(deadline) {
			t.Fatalf("the restart was not handed the conversation after the clear; it printed:\n%s", p.Sess.RecentText(4096))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := ws.PlanSourceFor(p.ID).SessionID; got != cleared {
		t.Errorf("a fan-out would read the plan from %q, want the conversation after the clear", got)
	}

	// And it outlives the window.
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()
	again := newTestWorkspace(t, root)
	if ok, err := again.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	if got := again.ConversationOf(p.ID); got != cleared {
		t.Errorf("the restored pane is in conversation %q, want the one after the clear", got)
	}
}
