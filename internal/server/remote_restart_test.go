package server

import (
	"strings"
	"testing"
	"time"
)

// TestRemoteWindowCannotRestart covers Restart now pressed on a phone, which
// the update badge offered it whenever a release had been staged. A restart
// stops every agent at the desk as quitting does, and a relaunch that fails
// leaves nothing the phone can start again -- so it is the desk's to choose,
// as Quit is. The window is told in its hello that it came through the relay,
// which is how it knows not to offer it.
func TestRemoteWindowCannotRestart(t *testing.T) {
	srv, _ := newTestServer(t)
	restarts := make(chan struct{}, 2)
	srv.OnRestart = func() { restarts <- struct{}{} }
	ts := remoteServer(t, srv)

	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()
	var hello helloMsg
	readUntil(t, phone, "hello", &hello)
	if !hello.Remote {
		t.Error("a window through the relay was not told so in its hello")
	}
	desk := dialControl(t, srv)
	var deskHello helloMsg
	readUntil(t, desk, "hello", &deskHello)
	if deskHello.Remote {
		t.Error("a window on this machine was told it came through the relay")
	}

	// The phone asks first and the desk after it. Only the desk's restart
	// may come: a restart saves the layout before it asks, which takes as
	// long as it takes, so the phone's is looked for once the desk's is in.
	sendCmd(t, phone, command{Cmd: "restart"})
	sendCmd(t, desk, command{Cmd: "restart"})
	select {
	case <-restarts:
	case <-time.After(10 * time.Second):
		t.Fatal("the window on the desk could not restart")
	}
	select {
	case <-restarts:
		t.Fatal("a window through the relay restarted the instance on the desk")
	case <-time.After(time.Second):
	}
	var note noticeMsg
	readUntil(t, phone, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "restart") {
		t.Errorf("restart through the relay was answered %+v, want it refused", note)
	}
}
