package server

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestABareDriveBrowsesItsRoot covers "D:" typed into the folder box, which is
// how a drive is named everywhere else on Windows. As a path it means that
// drive's current directory -- for the drive flockdeck runs on, the directory
// it was started in -- so the browser listed that rather than the drive.
func TestABareDriveBrowsesItsRoot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive letters are Windows'")
	}
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	// The drive this test runs on, whose current directory is the test's own.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	vol := filepath.VolumeName(wd)
	if len(vol) != 2 {
		t.Skipf("the working directory %s is not on a drive letter", wd)
	}
	var br browseMsg
	sendCmd(t, conn, command{Cmd: "browse", Path: vol})
	readUntil(t, conn, "browse", &br)
	if br.Path != vol+`\` {
		t.Errorf("browsing to %s went to %q, want the drive's root %q", vol, br.Path, vol+`\`)
	}
}
