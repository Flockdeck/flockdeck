package workspace

import (
	"slices"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
)

// TestALateHookFromBeforeARestartIsDropped covers a pane restarted while a
// hook of the process before it was still on its way. The pane keeps its id,
// and on Windows the hook -- a windowed program -- can outlive the process tree
// that ran it, so the old process's question landed on the new one and turned
// it amber with nothing on screen asking anything.
func TestALateHookFromBeforeARestartIsDropped(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}
	before := p.launch
	if before == "" {
		t.Fatal("the pane's start was given no name")
	}

	ws.RestartPane()
	if p.Sess == nil {
		t.Fatalf("the restarted pane did not start: %v", p.Err)
	}
	if p.launch == before {
		t.Fatal("the restart was given the same name as the start before it")
	}
	if env := ws.paneEnv(p, "", ""); !slices.Contains(env, hooks.LaunchEnv+"="+p.launch) {
		t.Errorf("the pane's environment does not name its start, so its hooks cannot: %q", env)
	}

	ws.handleHook(hooks.Event{SessionID: p.ID, Event: "Notification", Launch: before})
	if st, _ := p.Sess.Status(); st == session.StatusWaiting {
		t.Error("a hook from the process before the restart turned the new one amber")
	}
	ws.handleHook(hooks.Event{SessionID: p.ID, Event: "Notification", Launch: p.launch})
	if st, _ := p.Sess.Status(); st != session.StatusWaiting {
		t.Errorf("status = %v after the restarted process's own question, want waiting", st)
	}
}
