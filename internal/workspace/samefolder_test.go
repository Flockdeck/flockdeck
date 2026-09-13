package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// linkFolder makes name lead to target: a symlink, or on Windows, where one
// needs a privilege the test run may not have, a directory junction.
func linkFolder(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err == nil {
		return
	} else if runtime.GOOS != "windows" {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", name, target).CombinedOutput(); err != nil {
		t.Skipf("cannot link a folder here: %v: %s", err, out)
	}
}

// A folder reached by another path -- a junction or symlink, an 8.3 short
// name, macOS's /tmp for /private/tmp -- opened a second project onto a folder
// that was already open, since the paths were compared as strings alone. It
// is the project already open, under the spelling it was opened with, which
// is also what names its layout file.
func TestTheSameFolderByAnotherPathIsTheSameProject(t *testing.T) {
	isolateConfig(t)
	real := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	linkFolder(t, real, alias)

	ws := newTestWorkspace(t, real)
	if err := ws.OpenProject(alias); err != nil {
		t.Fatalf("open by the other path: %v", err)
	}
	if n := len(ws.Projects()); n != 1 {
		t.Errorf("projects = %d, want the one folder open once", n)
	}
	if got := ws.ActiveRoot(); got != real {
		t.Errorf("active project = %q, want it under the spelling it was opened with, %q", got, real)
	}
}

// The saved list of open projects can name the folder the window started on
// by another path, and restoring it opened the folder a second time.
func TestRestoreSessionKnowsAnOpenFolderByAnotherPath(t *testing.T) {
	isolateConfig(t)
	real := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	linkFolder(t, real, alias)
	if err := store.SaveSession(&store.Session{Open: []string{real, alias}, Active: real}); err != nil {
		t.Fatal(err)
	}

	ws := newTestWorkspace(t, real)
	if n := ws.RestoreSession(); n != 0 {
		t.Errorf("reopened %d projects, want none: the other path is the folder already open", n)
	}
	if n := len(ws.Projects()); n != 1 {
		t.Errorf("projects = %d, want the one folder open once", n)
	}
}
