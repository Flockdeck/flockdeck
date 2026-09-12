//go:build windows

package server

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestBrowseDimsWhatWindowsHides covers the project picker on Windows, where a
// home folder is lined with hidden system junctions that cannot be opened.
// They are marked hidden, as a dot-folder is, so the picker dims them and
// lists them after the folders a person would choose.
func TestBrowseDimsWhatWindowsHides(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	parent := t.TempDir()
	for _, name := range []string{"Cookies", "work"} {
		if err := os.Mkdir(filepath.Join(parent, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p, err := syscall.UTF16PtrFromString(filepath.Join(parent, "Cookies"))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.SetFileAttributes(p, syscall.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}

	var br browseMsg
	sendCmd(t, conn, command{Cmd: "browse", Path: parent})
	readUntil(t, conn, "browse", &br)
	if len(br.Entries) != 2 || br.Entries[0].Name != "work" || br.Entries[0].Hidden ||
		br.Entries[1].Name != "Cookies" || !br.Entries[1].Hidden {
		t.Fatalf("listed %+v, want work first and Cookies after it, marked hidden", br.Entries)
	}
}
