package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
)

// TestAHardDenialThatEndsTheTurnMarksTheTabForAttention covers the gap a
// permission or safety classifier's own refusal left: a pane that hit one and
// then simply explained it and stopped used to end up StatusIdle, the same as
// a pane that finished cleanly, and its tab was not marked for attention at
// all -- somebody had to find it by hand. Flockdeck's own hook path
// (workspace.handleHook, session.ResolveBlocked) now reports it as
// StatusBlocked instead, and that is enough on its own to mark the tab, the
// way a StatusWaiting pane already does.
func TestAHardDenialThatEndsTheTurnMarksTheTabForAttention(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	tab := ws.NewTab(session.KindShell, root, "one")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}

	ws.handleHook(hooks.Event{SessionID: p.ID, Event: "PreToolUse", Tool: "Bash", Launch: p.launch})
	ws.handleHook(hooks.Event{SessionID: p.ID, Event: "PermissionDenied", Tool: "Bash", Launch: p.launch})
	if st, _ := p.Sess.Status(); st != session.StatusWorking {
		t.Fatalf("status = %v right after the denial, want working -- the turn goes on", st)
	}
	if ws.TabNeedsAttention(tab) {
		t.Error("a denial mid-turn already marked the tab, before the turn even ended")
	}

	ws.handleHook(hooks.Event{SessionID: p.ID, Event: "Stop", Launch: p.launch})
	st, detail := p.Sess.Status()
	if st != session.StatusBlocked || detail != "Bash" {
		t.Fatalf("status = %v %q after the turn ended on the denial, want blocked naming Bash", st, detail)
	}
	if !st.NeedsAttention() {
		t.Error("StatusBlocked does not report NeedsAttention")
	}
	if !ws.TabNeedsAttention(tab) {
		t.Error("a pane stuck on a hard denial did not mark its tab for attention")
	}
	if waiting, _ := ws.AttentionCount(); waiting != 1 {
		t.Errorf("AttentionCount waiting = %d, want 1", waiting)
	}
}
