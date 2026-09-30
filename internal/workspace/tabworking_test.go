package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
)

// TestATabIsWorkingWhileAnAgentInItIsBusyOrHasBackgroundWork covers the tab
// strip saying nothing about work: a pane whose turn had ended with a
// background command still running rightly reads idle, and the tab showed
// only a waiting agent, so a tab full of running work looked as quiet as an
// empty one.
func TestATabIsWorkingWhileAnAgentInItIsBusyOrHasBackgroundWork(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	tab := ws.NewTab(session.KindShell, root, "one")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}
	send := func(name string) {
		ws.handleHook(hooks.Event{SessionID: p.ID, Event: name, Launch: p.launch})
	}

	send("UserPromptSubmit")
	send("Stop")
	if st, _ := p.Sess.Status(); st != session.StatusIdle {
		t.Fatalf("status = %v after Stop, want idle", st)
	}
	if ws.TabWorking(tab) {
		t.Error("an idle tab with no background work is marked working")
	}

	send("UserPromptSubmit")
	if !ws.TabWorking(tab) {
		t.Error("a tab with a working agent is not marked working")
	}
	send("Stop")

	p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	if st, _ := p.Sess.Status(); st != session.StatusIdle {
		t.Fatalf("background work changed the pane's status to %v, want idle", st)
	}
	if !ws.TabWorking(tab) {
		t.Error("an idle agent with background work running left its tab unmarked")
	}

	p.Sess.SetBackground(nil)
	if ws.TabWorking(tab) {
		t.Error("the tab is still marked working once the background work ended")
	}
}
