package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

// TestAPaneWhoseDirectoryIsGoneSaysSo covers the usual reason a restored pane
// will not start: the worktree it worked in was removed while Flockdeck was
// closed. What the pane showed was the operating system's own words about a
// process that could not be started, which name neither the directory as the
// problem nor anything to do about it.
func TestAPaneWhoseDirectoryIsGoneSaysSo(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	gone := filepath.Join(root, "removed-worktree")
	tab := ws.NewTab(session.KindShell, gone, "")
	p := ws.Pane(tab.Focus)
	if p == nil || p.Err == nil {
		t.Fatalf("a pane in a directory that is not there started: %+v", p)
	}
	msg := p.Err.Error()
	for _, want := range []string{gone, "no longer exists", "restart"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the pane says %q, which does not contain %q", msg, want)
		}
	}
}
