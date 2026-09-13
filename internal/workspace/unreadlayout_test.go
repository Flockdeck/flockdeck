package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestALayoutThatCouldNotBeReadAtStartIsKeptFromTheTimedSave covers a project
// reopened with the others whose layout could not be read at start, for a
// reason that has passed by the time the layouts are next saved.
//
// It comes up on one fresh tab, and the window is on another project, so the
// save reads the layout again for the tab it was last left on. That read
// working cleared the record of the one that had not, and the save wrote the
// fresh tab over every tab the user had saved, keeping nothing.
func TestALayoutThatCouldNotBeReadAtStartIsKeptFromTheTimedSave(t *testing.T) {
	isolateConfig(t)
	// The fresh tab may be an agent's, where one is installed; it is kept away
	// from the developer's own conversations.
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	first, second := t.TempDir(), t.TempDir()

	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "alpha")
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second: %v", err)
	}
	ws.NewTab(session.KindShell, second, "beta")
	ws.NewTab(session.KindShell, second, "gamma")
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	file, saved := layoutHolding(t, `"gamma"`)
	// A folder where the layout goes is the portable way to make its read fail
	// with something other than "does not exist": a file held past the retry
	// budget, a drive slow to wake.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0o700); err != nil {
		t.Fatal(err)
	}

	again := newTestWorkspace(t, first)
	if ok, err := again.Restore(); err != nil || !ok {
		t.Fatalf("restore of the first project = %v, %v; want its tab back", ok, err)
	}
	if n := again.RestoreSession(); n != 1 {
		t.Fatalf("reopened %d projects, want the second", n)
	}

	// The trouble passes before the timed save.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := again.SaveLayouts(); err != nil {
		t.Fatalf("save layouts: %v", err)
	}

	kept, err := os.ReadFile(file + ".unread")
	if err != nil {
		t.Fatalf("the layout that could not be read at start was written over without being kept: %v", err)
	}
	if !bytes.Equal(kept, saved) {
		t.Errorf("kept %s, want the layout saved before", kept)
	}
}

// TestALayoutThatCouldNotBeReadAtStartIsReported checks a restore says which
// saved layouts and which list of open projects it could not read.
//
// It dropped the errors, so the project came up on one fresh tab with nothing
// said, and the file was moved aside by the next save with nothing said either.
func TestALayoutThatCouldNotBeReadAtStartIsReported(t *testing.T) {
	isolateConfig(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	first, second := t.TempDir(), t.TempDir()

	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "alpha")
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second: %v", err)
	}
	ws.NewTab(session.KindShell, second, "beta")
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	// Both layouts are made unreadable, each with a folder where it goes.
	for _, want := range []string{`"alpha"`, `"beta"`} {
		file, _ := layoutHolding(t, want)
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(file, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	again := newTestWorkspace(t, first)
	ok, err := again.Restore()
	if ok {
		t.Errorf("restore of an unreadable layout says it restored something")
	}
	if err == nil || !strings.Contains(err.Error(), first) {
		t.Errorf("restore = %v, want the first project's layout named", err)
	}
	if n := again.RestoreSession(); n != 1 {
		t.Fatalf("reopened %d projects, want the second", n)
	}
	if err := again.RestoreErrors(); err == nil || !strings.Contains(err.Error(), second) {
		t.Errorf("after reopening the others = %v, want the second project's layout named", err)
	}
	if err := again.RestoreErrors(); err != nil {
		t.Errorf("asked again = %v, want what was said already forgotten", err)
	}

	// The list of open projects, unreadable in the same way.
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(dir, "session.json")
	if err := os.Remove(sessionFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sessionFile, 0o700); err != nil {
		t.Fatal(err)
	}
	third := newTestWorkspace(t, t.TempDir())
	third.RestoreSession()
	if err := third.RestoreErrors(); err == nil || !strings.Contains(err.Error(), "open projects") {
		t.Errorf("an unreadable list of open projects = %v, want it said", err)
	}
}

// layoutHolding finds the saved layout whose contents hold want, and returns
// its path and contents. Layout files are named by a hash of their project,
// which is the store's business, so they are told apart by what is in them.
func layoutHolding(t *testing.T, want string) (string, []byte) {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "layout-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err == nil && bytes.Contains(data, []byte(want)) {
			return f, data
		}
	}
	t.Fatalf("no saved layout holds %s among %v", want, files)
	return "", nil
}
