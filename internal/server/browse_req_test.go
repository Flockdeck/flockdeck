package server

import "testing"

// A browse answer repeats the req of the command, so a window that asked twice can
// tell the late answer to the first from the answer to the last.
func TestBrowseAnswerRepeatsTheReq(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	dir := t.TempDir()
	var br browseMsg
	sendCmd(t, conn, command{Cmd: "browse", Path: dir, Req: "b7"})
	readUntil(t, conn, "browse", &br)
	if br.Req != "b7" {
		t.Errorf("req = %q, want b7", br.Req)
	}
	if br.Path != dir {
		t.Errorf("path = %q, want %q", br.Path, dir)
	}

	// A path that cannot be listed is answered with the req too.
	br = browseMsg{}
	sendCmd(t, conn, command{Cmd: "browse", Path: dir + "/nope", Req: "b8"})
	readUntil(t, conn, "browse", &br)
	if br.Req != "b8" || br.Error == "" {
		t.Errorf("req = %q, error = %q, want b8 and an error", br.Req, br.Error)
	}

	// No req in, none out.
	br = browseMsg{}
	sendCmd(t, conn, command{Cmd: "browse", Path: dir})
	readUntil(t, conn, "browse", &br)
	if br.Req != "" {
		t.Errorf("req = %q, want none", br.Req)
	}
}
