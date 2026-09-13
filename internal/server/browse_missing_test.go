package server

import (
	"path/filepath"
	"testing"
)

// TestBrowsingToAMissingFolderSaysSo covers a path typed into the folder box
// with a slip in it. The listing failed in the operating system's words, which
// begin with the call that failed and on Windows speak of a file ("open
// C:\...\cdoe: The system cannot find the file specified."), where opening a
// project says "... does not exist" of the same path.
func TestBrowsingToAMissingFolderSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	parent := t.TempDir()
	missing := filepath.Join(parent, "cdoe")
	var br browseMsg
	sendCmd(t, conn, command{Cmd: "browse", Path: missing})
	readUntil(t, conn, "browse", &br)
	if want := missing + " does not exist"; br.Error != want {
		t.Errorf("browsing to a folder that is not there was answered %q, want %q", br.Error, want)
	}
	if br.Parent != parent {
		t.Errorf("parent = %q, want %q, the way back out", br.Parent, parent)
	}
}
