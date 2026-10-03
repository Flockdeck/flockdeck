package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// TestWhatAHookSaysOfBackgroundWorkReachesTheSession covers handleHook
// passing on BackgroundInfo and BackgroundTaskInfo, so the header can list
// the work rather than only count it.
func TestWhatAHookSaysOfBackgroundWorkReachesTheSession(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := agentPaneIn(t, ws, root, "a")

	ws.handleHook(hooks.Event{SessionID: p.ID, Event: "PostToolUse", Tool: "Bash", Launch: p.launch,
		Background: hooks.BackgroundStart, BackgroundID: "shell:b1",
		BackgroundInfo: &hooks.BackgroundInfo{Type: "shell", Command: "sleep 9", Description: "Sleep"}})
	work := p.Sess.BackgroundWork()
	if len(work) != 1 || work[0].Command != "sleep 9" || work[0].Description != "Sleep" {
		t.Fatalf("work after a start = %+v", work)
	}

	ids := []string{"shell:b1", "agent:a7"}
	ws.handleHook(hooks.Event{SessionID: p.ID, Event: "Stop", Launch: p.launch, BackgroundTasks: &ids,
		BackgroundTaskInfo: map[string]hooks.BackgroundInfo{"agent:a7": {Type: "subagent", AgentType: "general-purpose"}}})
	got := map[string]string{}
	for _, w := range p.Sess.BackgroundWork() {
		got[w.ID] = w.Command + "|" + w.AgentType
	}
	if got["shell:b1"] != "sleep 9|" || got["agent:a7"] != "|general-purpose" || len(got) != 2 {
		t.Fatalf("work after a Stop's list = %v", got)
	}

	ws.handleHook(hooks.Event{SessionID: p.ID, Event: "SessionStart", Source: "clear", Launch: p.launch})
	if work := p.Sess.BackgroundWork(); len(work) != 0 {
		t.Fatalf("a cleared conversation kept %+v", work)
	}
}
