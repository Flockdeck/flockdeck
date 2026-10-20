package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

// A terminal pane answers every question as its session does, and a pane given
// another runtime answers with that and not with its session.
func TestTerminalRuntimeReadsThePaneAndSetRuntimeReplacesIt(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	tab := ws.NewTab(session.KindShell, root, "one")
	p := ws.Pane(tab.Tree.Panes()[0])
	rt := ws.Runtime(p)

	if rt.Kind() != PaneKindTerminal || rt.Caps() != (PaneCaps{}) {
		t.Errorf("kind %q, caps %+v", rt.Kind(), rt.Caps())
	}
	p.Sess.SetStatusFull(session.StatusWaiting, "Bash", `{"command":"ls"}`)
	st, detail := rt.Status()
	if want, wd := p.Status(); st != want || detail != wd {
		t.Errorf("status %v %q, the pane says %v %q", st, detail, want, wd)
	}
	if on := rt.Waiting(); on.Detail != "Bash" || on.ToolInput != `{"command":"ls"}` {
		t.Errorf("waiting on %+v", on)
	}
	if rt.Activity() != PaneActivity(p) || !rt.StatusSince().Equal(p.Sess.StatusSince()) {
		t.Errorf("activity %q since %v", rt.Activity(), rt.StatusSince())
	}
	if rt.HasRun() {
		t.Error("a shell has no spend to expect")
	}
	if got := rt.PermissionOptions("Bash"); got == nil || len(got) != 0 {
		t.Errorf("options for a pane that is not Claude = %#v", got)
	}
	p.Agent = "claude"
	if got := rt.PermissionOptions("Bash"); len(got) != 2 || got[0].Kind != OptionAllowOnce {
		t.Errorf("options for a Claude pane = %+v", got)
	}

	other := stubRuntime{terminalRuntime{w: ws, p: p}}
	if !ws.SetRuntime(p.ID, other) || ws.Runtime(p).Kind() != PaneKindStructured {
		t.Error("the pane did not take the runtime it was given")
	}
	if ws.SetRuntime("nobody", other) {
		t.Error("a pane that is not there took a runtime")
	}
	if ws.Runtime(nil).Activity() != ActivityNone {
		t.Error("no pane is nothing")
	}
}

type stubRuntime struct{ terminalRuntime }

func (stubRuntime) Kind() PaneKind { return PaneKindStructured }
