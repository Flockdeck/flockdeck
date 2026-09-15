package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The manual "Check for updates" action in Settings runs whatever the
// instance wired up as OnCheckForUpdates and tells the window that clicked it
// what came of it -- unlike the background watcher, whose rounds are silent
// unless something changes, a person who pressed a button is waiting on an
// answer.
func TestCheckForUpdateRunsOnCheckForUpdates(t *testing.T) {
	srv, _ := newTestServer(t)
	asked := make(chan struct{}, 1)
	srv.OnCheckForUpdates = func() (string, bool) {
		asked <- struct{}{}
		return "flockdeck v1.0.0 is the latest release.", false
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "checkForUpdate"})

	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("OnCheckForUpdates was never called")
	}
	select {
	case raw := <-c.out:
		var note noticeMsg
		if err := json.Unmarshal(raw, &note); err != nil || note.Type != "notice" {
			t.Fatalf("got %s, want a notice", raw)
		}
		if note.Error || !strings.Contains(note.Text, "latest release") {
			t.Errorf("notice = %+v, want the answer OnCheckForUpdates gave", note)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the window that asked was never told what was found")
	}
}

// Checking is the desk's to do, as restarting onto what it finds already is:
// a window reached through the relay is refused rather than starting a check
// on the machine it is not running on.
func TestCheckForUpdateIsRefusedThroughTheRelay(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.OnCheckForUpdates = func() (string, bool) {
		t.Error("a window through the relay ran a check on the desk's machine")
		return "", false
	}

	c := &controlClient{out: make(chan []byte, 8), remote: true}
	srv.handleCommand(c, command{Cmd: "checkForUpdate"})

	select {
	case raw := <-c.out:
		var note noticeMsg
		if err := json.Unmarshal(raw, &note); err != nil || note.Type != "notice" || !note.Error {
			t.Fatalf("got %s, want an error notice refusing it", raw)
		}
		if !strings.Contains(note.Text, "relay") {
			t.Errorf("notice = %+v, does not say why", note)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a window through the relay asking to check was never answered")
	}
}

// Nothing wired up as OnCheckForUpdates -- a test server that never set it,
// or a build that never will -- says so rather than leaving the window that
// clicked the button to wonder whether anything happened.
func TestCheckForUpdateWithNothingWiredUpSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "checkForUpdate"})

	select {
	case raw := <-c.out:
		var note noticeMsg
		if err := json.Unmarshal(raw, &note); err != nil || note.Type != "notice" || !note.Error {
			t.Fatalf("got %s, want an error notice", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing wired up went unmentioned")
	}
}
