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

// TestAProjectCountsIdleAgentsWithBackgroundWorkApart covers the rail's mark:
// Projects counted only agents whose status is working, so a project whose
// only agent sat idle over a running background command read as quiet. The
// count is kept apart from Working, which the tallies use unchanged.
func TestAProjectCountsIdleAgentsWithBackgroundWorkApart(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}
	send := func(name string) {
		ws.handleHook(hooks.Event{SessionID: p.ID, Event: name, Launch: p.launch})
	}
	counts := func() (working, background int) {
		for _, pr := range ws.Projects() {
			if pr.Active {
				return pr.Working, pr.Background
			}
		}
		t.Fatal("no active project")
		return 0, 0
	}

	send("UserPromptSubmit")
	send("Stop")
	if w, b := counts(); w != 0 || b != 0 {
		t.Errorf("idle, nothing running: working=%d background=%d, want 0 0", w, b)
	}
	p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	if w, b := counts(); w != 0 || b != 1 {
		t.Errorf("idle over background work: working=%d background=%d, want 0 1", w, b)
	}
	if st, _ := p.Sess.Status(); st != session.StatusIdle {
		t.Errorf("status = %v, want idle: the status inference must not change", st)
	}
	send("UserPromptSubmit")
	if w, b := counts(); w != 1 || b != 0 {
		t.Errorf("working with background work: working=%d background=%d, want 1 0 (counted once)", w, b)
	}
}
