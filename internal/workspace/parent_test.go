package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
)

// TestAgentSpawnRecordsParentButAUserFanoutDoesNot covers who a helper's idle
// nudges belong to. A `flockdeck spawn` run by an agent's own pane is that
// agent's helper, and records it as its Parent; a fan-out the user runs
// themselves from the window hands work to the user directly, and its
// children carry no parent at all.
func TestAgentSpawnRecordsParentButAUserFanoutDoesNot(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus

	agentHelper, err := ws.Spawn(parent, SpawnOptions{Task: "helper task", Kind: session.KindShell, SpawnedByAgent: true})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if p := ws.Pane(agentHelper); p.Parent != parent {
		t.Errorf("a helper spawned by its agent has Parent = %q, want %q", p.Parent, parent)
	}

	userChild, err := ws.Spawn(parent, SpawnOptions{Task: "fan-out task", Kind: session.KindShell})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if p := ws.Pane(userChild); p.Parent != "" {
		t.Errorf("a user's own fan-out recorded a parent: %q", p.Parent)
	}
}

// TestParentSurvivesSaveAndRestore covers a helper's Parent coming back after
// the layout is saved and restored, since pane ids -- and so the parent id a
// helper names -- are the session ids a restore reattaches to.
func TestParentSurvivesSaveAndRestore(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus

	helper, err := ws.Spawn(parent, SpawnOptions{Task: "helper task", Kind: session.KindShell, SpawnedByAgent: true})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	restored := newTestWorkspace(t, root)
	if ok, err := restored.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	p := restored.Pane(helper)
	if p == nil {
		t.Fatal("the helper pane did not come back")
	}
	if p.Parent != parent {
		t.Errorf("restored helper.Parent = %q, want %q", p.Parent, parent)
	}
}

// TestHelperIgnoresIdleReminderWhileParentIsOpen covers a helper's idle nudge
// with its lead agent's pane still open. It is the general rule -- an idle
// nudge never turns any pane amber, see TestIdleReminderNeverTurnsAPaneWaiting
// -- applying here too, where a special case used to be the only thing
// keeping the helper quiet.
func TestHelperIgnoresIdleReminderWhileParentIsOpen(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus

	helper, err := ws.Spawn(parent, SpawnOptions{Task: "helper task", Kind: session.KindShell, Split: true, SpawnedByAgent: true})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	p := ws.Pane(helper)
	if p == nil || p.Sess == nil {
		t.Fatalf("the helper did not start: %v", p.Err)
	}

	// A finished turn, as it would be before an idle nudge follows it.
	ws.handleHook(hooks.Event{SessionID: helper, Event: "Stop"})
	if st, _ := p.Sess.Status(); st != session.StatusIdle {
		t.Fatalf("status after Stop = %v, want idle", st)
	}

	ws.handleHook(hooks.Event{SessionID: helper, Event: "Notification", NotificationType: "idle_prompt"})
	if st, _ := p.Sess.Status(); st != session.StatusIdle {
		t.Errorf("status after an idle reminder with the parent still open = %v, want still idle", st)
	}

	// A real ask is never swallowed: only a person can answer it.
	ws.handleHook(hooks.Event{SessionID: helper, Event: "Notification", NotificationType: "permission_prompt"})
	if st, _ := p.Sess.Status(); st != session.StatusWaiting {
		t.Errorf("status after a permission prompt = %v, want waiting even with the parent open", st)
	}
}

// TestIdleReminderNeverTurnsAPaneWaiting covers the general rule an idle
// nudge follows now: it is Claude Code saying a pane has simply gone quiet,
// not a real ask, so it leaves the pane exactly as the Stop before it left
// it -- whether or not the pane is a helper, and whether or not a helper's
// parent is still open to notice for it.
func TestIdleReminderNeverTurnsAPaneWaiting(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "solo")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}
	ws.handleHook(hooks.Event{SessionID: p.ID, Event: "Stop"})

	ws.handleHook(hooks.Event{SessionID: p.ID, Event: "Notification", NotificationType: "idle_prompt"})
	if st, _ := p.Sess.Status(); st != session.StatusIdle {
		t.Errorf("status after an idle reminder on an unspawned pane = %v, want still idle", st)
	}
}

// TestHelperIdleReminderStaysIdleAfterParentCloses covers the parent's pane
// being closed: the helper's idle nudges go on leaving it idle exactly as
// they did while the parent was open, since the rule no longer depends on
// who, if anyone, is watching. Only a real ask -- a permission prompt, say --
// turns it amber, and does so whether or not the parent is still open.
func TestHelperIdleReminderStaysIdleAfterParentCloses(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus

	helper, err := ws.Spawn(parent, SpawnOptions{Task: "helper task", Kind: session.KindShell, Split: true, SpawnedByAgent: true})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	p := ws.Pane(helper)
	if p == nil || p.Sess == nil {
		t.Fatalf("the helper did not start: %v", p.Err)
	}
	ws.handleHook(hooks.Event{SessionID: helper, Event: "Stop"})

	if !ws.ClosePaneByID(parent) {
		t.Fatal("could not close the parent pane")
	}
	if ws.Pane(parent) != nil {
		t.Fatal("the parent pane is still open after closing it")
	}

	ws.handleHook(hooks.Event{SessionID: helper, Event: "Notification", NotificationType: "idle_prompt"})
	if st, _ := p.Sess.Status(); st != session.StatusIdle {
		t.Errorf("status after an idle reminder once the parent is closed = %v, want still idle", st)
	}

	// A real ask still reaches the user, parent open or not.
	ws.handleHook(hooks.Event{SessionID: helper, Event: "Notification", NotificationType: "permission_prompt"})
	if st, _ := p.Sess.Status(); st != session.StatusWaiting {
		t.Errorf("status after a permission prompt once the parent is closed = %v, want waiting", st)
	}
}
