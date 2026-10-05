package server

import (
	"strings"
	"testing"
	"time"
)

// A window reached through the relay is never sent the approval notice, and is
// not counted as a window that could be asked: with only a phone connected there
// is nobody to ask.
func TestARelayWindowIsNeverAskedToApproveABaton(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	err = <-spawnElsewhere(hook, pane)
	if err == nil || !strings.Contains(err.Error(), "no Flockdeck window") {
		t.Errorf("err = %v, want a refusal that says no window is open on this machine", err)
	}
}

// With a local window and a phone both open, only the local window is sent the
// notice, and a hand-sent approveBaton from the phone, carrying the local
// window's real token, is refused as coming from the wrong kind of window, not
// merely the wrong window.
func TestARelayWindowCannotApproveABatonEvenWithTheRealToken(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	window := dialWindow(t, srv)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	shortApprovalWait(t, 20*time.Second)

	result := spawnElsewhere(hook, pane)
	n := nextApproval(t, window)

	// The phone sends approveBaton with the token the window was given.
	sendCmd(t, phone, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	var refusal approvalNotice
	for !refusal.Error {
		readUntil(t, phone, "notice", &refusal)
		if refusal.Action != nil {
			t.Fatalf("the phone was sent an approval notice: %+v", refusal)
		}
	}
	if !strings.Contains(refusal.Text, "from the Flockdeck window on this machine") {
		t.Errorf("the phone's approval was refused for the wrong reason: %q", refusal.Text)
	}
	waitingStill(t, result, "after the phone tried to approve", 300*time.Millisecond)

	// The real window still can, with the same token, so the phone's try did not
	// use it up.
	sendCmd(t, window, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	select {
	case <-result:
	case <-time.After(30 * time.Second):
		t.Fatal("the window's own approval did not go through")
	}
	// The phone was never sent the notice at any point.
	if !noApproval(phone, 300*time.Millisecond) {
		t.Error("the phone was sent an approval notice")
	}
}
