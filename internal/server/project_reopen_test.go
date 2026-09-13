package server

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestOpeningAnOpenProjectSaysItSwitched covers the recent-projects list and
// the folder browser, which both open a project by its path whether it is open
// already or not. Picking one that was open only switched to it, and was
// answered "opened app" -- as though a second copy of it had been started.
func TestOpeningAnOpenProjectSaysItSwitched(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	first := srv.activeRoot()
	srv.openProjectOnOwner(t, t.TempDir())

	sendCmd(t, conn, command{Cmd: "openProject", Path: first})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if note.Error || strings.HasPrefix(note.Text, "opened") || !strings.Contains(note.Text, filepath.Base(first)) {
		t.Errorf("opening a project that was already open was answered %+v; want it to say it switched to %s", note, filepath.Base(first))
	}
	if got := srv.activeRoot(); got != first {
		t.Errorf("the active project is %s, want %s", got, first)
	}
	if n := len(srv.projectRoots(t)); n != 2 {
		t.Errorf("%d projects are open, want the same 2", n)
	}
}
