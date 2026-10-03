package workspace

import (
	"strings"
	"testing"
	"time"

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

// TestAStopListThatCannotBeReadLeavesTheCountAlone runs real hook payloads
// through the hook server: a Stop whose background_tasks cannot be read whole
// leaves what is counted as it was, rather than replacing it with nothing or
// with part of it, while an empty list says nothing is running.
func TestAStopListThatCannotBeReadLeavesTheCountAlone(t *testing.T) {
	isolateConfig(t)
	t.Setenv(hooks.LaunchEnv, "")
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := agentPaneIn(t, ws, root, "a")
	p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	p.Sess.NoteBackground("SubagentStart", hooks.BackgroundStart, "agent:a1")
	srv := ws.HookServer()
	stop := func(list string) {
		t.Helper()
		if _, err := hooks.Emit(strings.NewReader(`{"hook_event_name":"Stop","background_tasks":`+list+`}`), srv.Endpoint(), srv.Token(), p.ID, "Stop"); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}
	for _, list := range []string{`["s1","s2"]`, `[{"id":"b1","type":"shell"},7]`, `[{"id":1}]`, `null`} {
		stop(list)
		if n := p.Sess.BackgroundTasks(); n != 2 {
			t.Fatalf("a Stop listing %s left %d counted, want the 2 there were", list, n)
		}
	}
	stop(`[]`)
	deadline := time.Now().Add(5 * time.Second)
	for p.Sess.BackgroundTasks() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("an empty list left %d counted", p.Sess.BackgroundTasks())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
