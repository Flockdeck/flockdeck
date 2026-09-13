package workspace

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// TestARestoredPaneWhoseAgentIsGoneSaysWhatToDo covers a layout naming an
// agent this machine has no entry for: one since removed from agents.json, or
// a layout brought over from a machine that had it. The pane said the agent
// was not configured and stopped there, leaving the user to find out where
// agents are configured and that the pane could be restarted once it was.
func TestARestoredPaneWhoseAgentIsGoneSaysWhatToDo(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	saved := &store.State{Tabs: []store.Tab{{
		Title: "one",
		Root:  &store.Node{Pane: &store.Pane{ID: "gone", Kind: "agent", Agent: "no-such-agent", Cwd: root}},
	}}}
	if err := store.Save(root, saved); err != nil {
		t.Fatalf("save: %v", err)
	}

	ws := newTestWorkspace(t, root)
	if ok, err := ws.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	p := ws.Pane("gone")
	if p == nil || p.Err == nil {
		t.Fatalf("a pane naming an agent that is not configured started: %+v", p)
	}
	for _, want := range []string{`"no-such-agent"`, "agents.json", "restart the pane"} {
		if !strings.Contains(p.Err.Error(), want) {
			t.Errorf("pane error = %q, want it to say %q", p.Err, want)
		}
	}
}
