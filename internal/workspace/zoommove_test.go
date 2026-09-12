package workspace

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
)

// TestAKeyboardMoveWhileZoomedSaysHowToUnzoom covers the refusal a keyboard
// move gets on a zoomed tab. It is answered to somebody at the keyboard, and
// "unzoom it first" left them to find out how.
func TestAKeyboardMoveWhileZoomedSaysHowToUnzoom(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "zoomed")
	ws.SplitPane(layout.Horizontal, session.KindShell)
	ws.ToggleZoom()

	err := ws.MovePaneDir(layout.Left)
	if err == nil {
		t.Fatal("a keyboard move on a zoomed tab went ahead, want it refused")
	}
	if want := how("zoomPane"); !strings.Contains(err.Error(), want) {
		t.Errorf("refusal = %q, want it to say how to unzoom: %q", err, want)
	}
}
