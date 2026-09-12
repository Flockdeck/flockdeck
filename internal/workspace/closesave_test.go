package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestClosingAProjectSaysWhenItsLayoutCouldNotBeSaved checks a project closed
// with a layout that will not save is still closed, and that the failure is
// reported rather than dropped.
func TestClosingAProjectSaysWhenItsLayoutCouldNotBeSaved(t *testing.T) {
	isolateConfig(t)
	first, second := t.TempDir(), t.TempDir()
	ws := newTestWorkspace(t, first)
	// No agent this machine can start, so each project opens on a shell.
	ws.UseAgent("no-such-agent")
	ws.NewTab(session.KindShell, first, "alpha")
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second: %v", err)
	}

	// Save it once to learn the name its layout goes under, then stand a
	// directory there: a rename cannot replace one on any platform.
	if err := ws.SaveProject(second); err != nil {
		t.Fatalf("save: %v", err)
	}
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	layouts, err := filepath.Glob(filepath.Join(dir, "layout-*.json"))
	if err != nil || len(layouts) != 1 {
		t.Fatalf("layouts saved = %v, %v; want the second project's alone", layouts, err)
	}
	if err := os.Remove(layouts[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(layouts[0], 0o700); err != nil {
		t.Fatal(err)
	}

	err = ws.CloseProject(second)
	if err == nil {
		t.Fatal("closing a project whose layout could not be saved reported nothing")
	}
	if !strings.Contains(err.Error(), filepath.Base(second)) {
		t.Errorf("the report does not name the project: %v", err)
	}
	if got := ws.Projects(); len(got) != 1 || got[0].Root != first {
		t.Errorf("projects after closing = %+v, want the first alone: closing is what was asked for", got)
	}
}
