package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// A project whose folder was not there at start -- a USB stick not plugged
// in, a network drive not yet connected -- was dropped from the list of open
// projects at the next save, for good: the folder coming back did not bring
// the project back. It is kept in the list, with a count of the starts it has
// been away, and is opened by the first start that finds it there again.
func TestAProjectWhoseFolderIsAwayIsKeptForItsReturn(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	away := filepath.Join(t.TempDir(), "usb")
	if err := store.SaveSession(&store.Session{Open: []string{root, away}, Active: root}); err != nil {
		t.Fatal(err)
	}

	ws := newTestWorkspace(t, root)
	if n := ws.RestoreSession(); n != 0 {
		t.Fatalf("reopened %d projects, want none: the other folder is not there", n)
	}
	if err := ws.SaveSession(); err != nil {
		t.Fatal(err)
	}
	saved, err := store.LoadSession()
	if err != nil || saved == nil {
		t.Fatalf("LoadSession = %v, %v", saved, err)
	}
	if !slices.Contains(saved.Open, away) || saved.Away[away] != 1 {
		t.Fatalf("saved session = %+v, want %s kept, away for 1 start", saved, away)
	}
	ws.Close()

	// The folder is back, and the next start opens the project.
	if err := os.MkdirAll(away, 0o755); err != nil {
		t.Fatal(err)
	}
	again := newTestWorkspace(t, root)
	if n := again.RestoreSession(); n != 1 {
		t.Fatalf("reopened %d projects, want the one whose folder is back", n)
	}
	if s := again.Session(); len(s.Away) != 0 {
		t.Errorf("session = %+v, want nothing counted away once the folder is back", s)
	}
}

// A folder that comes back during the run can be opened then, and closing it
// is the user saying it can go: it is not kept for its return any more.
func TestAProjectBackAndClosedIsLetGo(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	away := filepath.Join(t.TempDir(), "usb")
	if err := store.SaveSession(&store.Session{Open: []string{root, away}, Active: root}); err != nil {
		t.Fatal(err)
	}
	ws := newTestWorkspace(t, root)
	ws.RestoreSession()

	if err := os.MkdirAll(away, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ws.OpenProject(away); err != nil {
		t.Fatalf("open the folder that is back: %v", err)
	}
	if s := ws.Session(); len(s.Away) != 0 || !slices.Contains(s.Open, away) {
		t.Errorf("session = %+v, want %s among the open, and nothing away", s, away)
	}
	if err := ws.CloseProject(away); err != nil {
		t.Fatalf("close: %v", err)
	}
	if s := ws.Session(); slices.Contains(s.Open, away) {
		t.Errorf("session = %+v, want %s gone once the user closed it", s, away)
	}
}

// A folder that stays away is not kept for ever: once it has been missing for
// maxAwayStarts starts in a row, the project is let go of.
func TestAProjectAwayTooLongIsLetGo(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	away := filepath.Join(t.TempDir(), "gone")
	if err := store.SaveSession(&store.Session{Open: []string{root, away}, Active: root, Away: map[string]int{away: maxAwayStarts}}); err != nil {
		t.Fatal(err)
	}
	ws := newTestWorkspace(t, root)
	ws.RestoreSession()
	if s := ws.Session(); slices.Contains(s.Open, away) {
		t.Errorf("session = %+v, want %s let go after %d starts away", s, away, maxAwayStarts)
	}
}
