//go:build windows

package main

import (
	"syscall"
	"testing"
	"time"
)

// A logoff or shutdown reaches the release, which has no console once it has
// started, only through a top-level window of its own. The end of the session
// asks for the orderly stop, and the answer, after which Windows may end the
// process at any moment, waits until the save has been made.
func TestEndOfSessionStopsInOrder(t *testing.T) {
	stopped := make(chan struct{})
	saved := make(chan struct{})
	hwnd := watchEndSession(func() { close(stopped) }, saved)
	if hwnd == 0 {
		t.Fatal("no window to hear the end of the session with")
	}
	send := syscall.NewLazyDLL("user32.dll").NewProc("SendMessageW")

	if r, _, _ := send.Call(uintptr(hwnd), wmQueryEndSession, 0, 0); r == 0 {
		t.Error("the window objected to the session ending")
	}
	answered := make(chan struct{})
	go func() {
		_, _, _ = send.Call(uintptr(hwnd), wmEndSession, 1, 0)
		close(answered)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the end of the session did not ask for the orderly stop")
	}
	select {
	case <-answered:
		t.Fatal("the end of the session was answered before the save was made")
	case <-time.After(200 * time.Millisecond):
	}
	close(saved)
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Fatal("the end of the session was still not answered once the save was made")
	}
}
