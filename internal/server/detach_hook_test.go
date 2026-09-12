package server

import (
	"testing"
	"time"
)

// TestOnDetachIsToldOncePerDetach covers the hook the application uses to let
// go of what tied it to the way it was started -- on Windows, the console a
// -no-window run was launched from, which ends it when closed even after it has
// been detached. It has to hear of every way of becoming detached, once each
// time, and not be run on the workspace goroutine, where anything it asked of
// the workspace would wait on itself.
func TestOnDetachIsToldOncePerDetach(t *testing.T) {
	srv, _ := newTestServer(t)
	calls := make(chan struct{}, 8)
	srv.OnDetach = func() {
		// Asking the workspace from here would never return if this were
		// running on its goroutine.
		ask(srv, func() bool { return true })
		calls <- struct{}{}
	}
	once := func(what string) {
		t.Helper()
		select {
		case <-calls:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: OnDetach was not called", what)
		}
		select {
		case <-calls:
			t.Fatalf("%s: OnDetach was called twice", what)
		case <-time.After(300 * time.Millisecond):
		}
	}
	never := func(what string) {
		t.Helper()
		select {
		case <-calls:
			t.Fatalf("%s: OnDetach was called", what)
		case <-time.After(300 * time.Millisecond):
		}
	}

	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "detach"})
	readUntil(t, conn, "detached", &struct{}{})
	once("the palette's Detach")

	sendCmd(t, conn, command{Cmd: "detach"})
	readUntil(t, conn, "detached", &struct{}{})
	never("Detach while already detached")

	srv.Attach()
	srv.Detach()
	once("-detach at start, after an attach")

	srv.Attach()
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	sendCmd(t, phone, command{Cmd: "detach"})
	var note noticeMsg
	readUntil(t, phone, "notice", &note)
	never("Detach through the relay, which detaches nothing")
}
