package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

// TestAPaneIsNotResizedBeyondWhatAScreenShows is the bound on what any client
// can make a pane's terminal, the control socket's resize command included.
// Every window showing the pane makes its own terminal the pty's size, so a
// 1000 by 1000 pane costs all of them, and a 1 by 1 one is no terminal at all.
func TestAPaneIsNotResizedBeyondWhatAScreenShows(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	defer ws.Close()
	id := ws.NewTab(session.KindShell, root, "one").Focus

	ws.ResizePaneTerminal(id, 100, 30)
	ws.ResizePaneTerminal(id, 1000, 1000)
	if cols, rows := ws.Pane(id).Sess.Size(); cols != MaxPaneCols || rows != MaxPaneRows {
		t.Fatalf("a request for 1000x1000 made the pane %dx%d, want %dx%d", cols, rows, MaxPaneCols, MaxPaneRows)
	}
	ws.ResizePaneTerminal(id, 1, 1)
	ws.ResizePaneTerminal(id, 3, 200)
	if cols, rows := ws.Pane(id).Sess.Size(); cols != MaxPaneCols || rows != MaxPaneRows {
		t.Fatalf("requests too small for a terminal moved the pane to %dx%d", cols, rows)
	}
}
