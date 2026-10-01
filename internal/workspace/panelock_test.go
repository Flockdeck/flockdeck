package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
)

// lockedPane opens a pane in a tab of its own and locks it.
func lockedPane(t *testing.T, ws *Workspace, root, title string) *Pane {
	t.Helper()
	tab := ws.NewTab(session.KindShell, root, title)
	p := ws.Pane(tab.Tree.Panes()[0])
	if !ws.SetPaneLocked(p.ID, true) {
		t.Fatal("SetPaneLocked did not find the pane")
	}
	return p
}

func TestLockedPaneCannotBeClosed(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := lockedPane(t, ws, root, "locked")

	if ws.ClosePaneByID(p.ID) {
		t.Error("ClosePaneByID closed a locked pane")
	}
	ws.ClosePane() // the focused pane is the locked one
	if ws.Pane(p.ID) == nil {
		t.Fatal("the locked pane was closed")
	}

	if !ws.SetPaneLocked(p.ID, false) {
		t.Fatal("unlock did not find the pane")
	}
	if !ws.ClosePaneByID(p.ID) || ws.Pane(p.ID) != nil {
		t.Error("an unlocked pane should close again")
	}
	if ws.SetPaneLocked(p.ID, true) {
		t.Error("locking a closed pane should report it gone")
	}
}

func TestLockedPaneKeepsItsTabOpen(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "mixed")
	tab := ws.CurrentTab()
	ws.SplitPane(layout.Horizontal, session.KindShell)
	ids := tab.Tree.Panes()
	ws.SetPaneLocked(ids[1], true)

	if got := ws.TabLockedPanes(tab.ID); len(got) != 1 || got[0] != ids[1] {
		t.Fatalf("TabLockedPanes = %v, want just %s", got, ids[1])
	}
	if ws.CloseTab(tab.ID) {
		t.Error("CloseTab closed a tab holding a locked pane")
	}
	for _, id := range ids {
		if ws.Pane(id) == nil {
			t.Errorf("pane %s went with a tab that should have stayed", id)
		}
	}
	// The unlocked neighbour can still be closed on its own.
	if !ws.ClosePaneByID(ids[0]) {
		t.Error("the unlocked pane beside it should close")
	}
	ws.SetPaneLocked(ids[1], false)
	if !ws.CloseTab(tab.ID) {
		t.Error("CloseTab should work once nothing is locked")
	}
}

func TestCloseFinishedPanesSkipsLockedAndCountsThem(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	keep := agentPaneIn(t, ws, root, "keep")
	keep.Sess.SetStatus(session.StatusIdle, "")
	ws.SetPaneLocked(keep.ID, true)
	gone := agentPaneIn(t, ws, root, "gone")
	gone.Sess.SetStatus(session.StatusIdle, "")

	panes, tabs, locked := ws.CloseFinishedPanes()
	if panes != 1 || tabs != 1 || locked != 1 {
		t.Fatalf("CloseFinishedPanes() = (%d, %d, %d), want (1, 1, 1)", panes, tabs, locked)
	}
	if ws.Pane(keep.ID) == nil || ws.Pane(gone.ID) != nil {
		t.Error("the locked pane should remain and the other should be closed")
	}
}

func TestLockSurvivesRestartAndMove(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := lockedPane(t, ws, root, "one")

	// Restarting relaunches the process and nothing else.
	if !ws.RestartPaneByID(p.ID) {
		t.Fatal("restart did not find the pane")
	}
	if !ws.PaneLocked(p.ID) {
		t.Error("restart dropped the lock")
	}

	// Dragging it to another tab carries the pane, lock included.
	other := ws.NewTab(session.KindShell, root, "two")
	if err := ws.MovePaneToTab(p.ID, other.ID); err != nil {
		t.Fatalf("move: %v", err)
	}
	if !ws.PaneLocked(p.ID) || ws.tabOf(p.ID).ID != other.ID {
		t.Error("the pane should have moved to the other tab still locked")
	}
}

func TestLockSurvivesSaveAndRestore(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := lockedPane(t, ws, root, "kept")
	plain := ws.Pane(ws.NewTab(session.KindShell, root, "plain").Tree.Panes()[0])
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	restored := newTestWorkspace(t, root)
	if ok, err := restored.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	if !restored.PaneLocked(p.ID) {
		t.Error("the lock was lost across a restore")
	}
	if restored.PaneLocked(plain.ID) {
		t.Error("an unlocked pane came back locked")
	}
}
