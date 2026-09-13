package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestARestoredPaneStartsAtTheSizeItWasDrawnAt covers the first moments of a
// restore. Every pane is started before any window has opened to measure it,
// and an agent resuming a conversation prints it straight away: at the default
// eighty by twenty-four it came out wrapped at eighty columns in a pane twice
// that wide, and stayed that way in the scrollback once the window resized it.
func TestARestoredPaneStartsAtTheSizeItWasDrawnAt(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()

	ws := newTestWorkspace(t, root)
	id := ws.NewTab(session.KindShell, root, "one").Focus
	ws.ResizePaneTerminal(id, 132, 41)
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	restored := newTestWorkspace(t, root)
	if ok, err := restored.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	p := restored.Pane(id)
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not come back running: %+v", p)
	}
	if cols, rows := p.Sess.Size(); cols != 132 || rows != 41 {
		t.Errorf("restored pane started at %dx%d, want the 132x41 it was drawn at", cols, rows)
	}
}

// TestARestoredSizeNoTerminalHasIsIgnored checks a size from a file edited by
// hand does not reach the terminal.
func TestARestoredSizeNoTerminalHasIsIgnored(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	saved := &store.State{Tabs: []store.Tab{{
		Title: "one",
		Root:  &store.Node{Pane: &store.Pane{ID: "huge", Kind: "shell", Cwd: root, Cols: 1 << 20, Rows: 50}},
	}}}
	if err := store.Save(root, saved); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws := newTestWorkspace(t, root)
	if ok, err := ws.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	p := ws.Pane("huge")
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not come back running: %+v", p)
	}
	if cols, rows := p.Sess.Size(); cols != 80 || rows != 24 {
		t.Errorf("a pane saved at an impossible size started at %dx%d, want the default 80x24", cols, rows)
	}
}
