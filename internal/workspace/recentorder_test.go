package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestRestoreSessionLeavesTheActiveProjectFirstInTheRecentList checks the
// projects brought back around the one a run starts on do not push it down
// the recent list. Each was recorded as used as it reopened, after the active
// one, so the picker led with whichever came last in the saved session.
func TestRestoreSessionLeavesTheActiveProjectFirstInTheRecentList(t *testing.T) {
	isolateConfig(t)
	first, second, third := t.TempDir(), t.TempDir(), t.TempDir()

	ws := newTestWorkspace(t, first)
	// No agent this machine can start, so each project opens on a shell.
	ws.UseAgent("no-such-agent")
	ws.NewTab(session.KindShell, first, "alpha")
	for _, root := range []string{second, third} {
		if err := ws.OpenProject(root); err != nil {
			t.Fatalf("open %s: %v", root, err)
		}
	}
	ws.SelectProject(first)
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	again := newTestWorkspace(t, first)
	again.UseAgent("no-such-agent")
	if _, err := again.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n := again.RestoreSession(); n != 2 {
		t.Fatalf("reopened %d projects, want 2", n)
	}
	recents, err := store.Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(recents) != 3 || recents[0].Root != first {
		t.Errorf("recents = %+v, want %s, the project the run started on, first", recents, first)
	}
}
