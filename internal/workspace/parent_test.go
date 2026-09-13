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

// TestHelperIgnoresIdleReminderWhileParentIsOpen is the change this file is
// about. It fails without it: on the code before this, every Notification --
// idle_prompt included -- turns a pane amber regardless of who spawned it, so
// a helper whose lead agent is still right there watching it notified the
// user anyway, all afternoon.
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

// TestPaneWithNoParentIdleReminderTurnsWaiting pins today's behaviour for a
// pane nobody spawned as a helper: its own idle reminders go on turning it
// amber, exactly as before this change.
func TestPaneWithNoParentIdleReminderTurnsWaiting(t *testing.T) {
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
	if st, _ := p.Sess.Status(); st != session.StatusWaiting {
		t.Errorf("status after an idle reminder on an unspawned pane = %v, want waiting, as today", st)
	}
}

// TestHelperIdleReminderTurnsWaitingAfterParentCloses covers the parent's
// pane being closed: from then on the helper is the user's own, so its next
// idle reminder turns it amber like any other pane's.
func TestHelperIdleReminderTurnsWaitingAfterParentCloses(t *testing.T) {
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
	if st, _ := p.Sess.Status(); st != session.StatusWaiting {
		t.Errorf("status after an idle reminder once the parent is closed = %v, want waiting", st)
	}
}
