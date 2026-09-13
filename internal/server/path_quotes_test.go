package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAQuotedPathIsTakenAsThePath covers a path copied with Windows Explorer's
// "Copy as path", which wraps it in double quotes -- the usual way to copy a
// folder's path there. Pasted into the project picker's folder box it was
// looked for as a folder whose name starts with a quote, and "Open this
// folder" said a project has to be named by its full path, which it was.
func TestAQuotedPathIsTakenAsThePath(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, quoted := range []string{`"` + dir + `"`, `'` + dir + `'`} {
		var br browseMsg
		sendCmd(t, conn, command{Cmd: "browse", Path: quoted})
		readUntil(t, conn, "browse", &br)
		if br.Error != "" || br.Path != dir {
			t.Errorf("browsing to %s went to %q (%s), want %q", quoted, br.Path, br.Error, dir)
		}
	}

	sendCmd(t, conn, command{Cmd: "openProject", Path: `"` + dir + `"`})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if note.Error {
		t.Fatalf("opening a quoted path was refused: %s", note.Text)
	}
	if got := srv.activeRoot(); got != dir {
		t.Errorf("the active project is %q, want %q", got, dir)
	}
}
