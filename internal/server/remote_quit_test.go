package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestRemoteWindowCannotQuit covers Quit chosen on a phone. It stops every
// agent on the desk, and nothing on the phone can start them again -- which is
// why the help says a remote window cannot quit the instance. /quit kept that
// promise by wanting the token; the palette's Quit, on the control socket, did
// not.
func TestRemoteWindowCannotQuit(t *testing.T) {
	srv, _ := newTestServer(t)
	quit := make(chan struct{}, 2)
	srv.OnQuit = func() { quit <- struct{}{} }
	ts := remoteServer(t, srv)

	remoteConn, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer remoteConn.CloseNow()
	sendCmd(t, remoteConn, command{Cmd: "quit"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, data, err := remoteConn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for the answer to quit: %v", err)
		}
		var msg noticeMsg
		if json.Unmarshal(data, &msg) == nil && msg.Type == "notice" {
			if !msg.Error {
				t.Errorf("quit through the relay was answered %q, want it refused", msg.Text)
			}
			break
		}
	}
	select {
	case <-quit:
		t.Fatal("a window through the relay quit the instance on the desk")
	case <-time.After(300 * time.Millisecond):
	}

	sendCmd(t, dialControl(t, srv), command{Cmd: "quit"})
	select {
	case <-quit:
	case <-time.After(5 * time.Second):
		t.Error("the window on the desk could not quit")
	}
}
