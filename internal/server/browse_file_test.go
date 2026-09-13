package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBrowsingToAFileSaysSo covers a path pasted into the folder browser that
// names a file rather than a folder -- one copied from an editor's title bar,
// say. The listing failed in the operating system's words for opening a file
// as a folder ("The directory name is invalid.", "not a directory"), which
// never say that the path is a file.
func TestBrowsingToAFileSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	dir := t.TempDir()
	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var br browseMsg
	sendCmd(t, conn, command{Cmd: "browse", Path: file})
	readUntil(t, conn, "browse", &br)
	if !strings.Contains(br.Error, "notes.txt is a file") {
		t.Errorf("browsing to a file was answered %q; want it to say the path is a file", br.Error)
	}
	if br.Parent != dir {
		t.Errorf("parent = %q, want the folder the file is in, %q", br.Parent, dir)
	}
}
