package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// The version picker asks whatever the instance wired up as OnListVersions
// and hands the window that opened it the releases it found, the same way
// OnCheckForUpdates's answer reaches the "Check for updates" button.
func TestListVersionsRunsOnListVersions(t *testing.T) {
	srv, _ := newTestServer(t)
	asked := make(chan struct{}, 1)
	srv.OnListVersions = func(ctx context.Context) ([]VersionView, error) {
		asked <- struct{}{}
		return []VersionView{
			{Version: "v1.5.0", Relation: "newer"},
			{Version: "v1.4.0", Relation: "current"},
		}, nil
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "listVersions"})

	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("OnListVersions was never called")
	}
	select {
	case raw := <-c.out:
		var msg versionsMsg
		if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != "versions" {
			t.Fatalf("got %s, want a versions message", raw)
		}
		if msg.Error != "" || len(msg.Items) != 2 || msg.Items[0].Version != "v1.5.0" {
			t.Errorf("versions = %+v, want the releases OnListVersions gave", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the window that asked was never sent the list")
	}
}

// A failure to list -- the site and GitHub both out of reach, say -- is told
// to the window as an error on the message itself, not as a bare notice: the
// picker's dialog is what has to redraw either way.
func TestListVersionsReportsAFailure(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.OnListVersions = func(ctx context.Context) ([]VersionView, error) {
		return nil, errors.New("could not reach dl.flockdeck.ai or GitHub")
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "listVersions"})

	select {
	case raw := <-c.out:
		var msg versionsMsg
		if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != "versions" {
			t.Fatalf("got %s, want a versions message", raw)
		}
		if msg.Error == "" || len(msg.Items) != 0 {
			t.Errorf("versions = %+v, want the error reported and no items", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the failure was never reported")
	}
}

// Nothing wired up as OnListVersions -- a build that never will, or a test
// server that never set it -- says so rather than leaving the picker to spin
// forever.
func TestListVersionsWithNothingWiredUpSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "listVersions"})

	select {
	case raw := <-c.out:
		var msg versionsMsg
		if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != "versions" || msg.Error == "" {
			t.Fatalf("got %s, want a versions message with an error", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing wired up went unmentioned")
	}
}

// Listing versions is the desk's to do, as checking for an ordinary update
// already is: a window reached through the relay is refused rather than
// listing releases the machine it is not running on would install.
func TestListVersionsIsRefusedThroughTheRelay(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.OnListVersions = func(ctx context.Context) ([]VersionView, error) {
		t.Error("a window through the relay listed versions on the desk's machine")
		return nil, nil
	}

	c := &controlClient{out: make(chan []byte, 8), remote: true}
	srv.handleCommand(c, command{Cmd: "listVersions"})

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
		t.Fatal("a window through the relay asking to list versions was never answered")
	}
}

// Choosing a version to install runs whatever the instance wired up as
// OnInstallVersion, naming the version the picker's row was for, and tells
// the window that asked what came of it -- the same way OnCheckForUpdates's
// answer does for the ordinary chip.
func TestInstallVersionRunsOnInstallVersion(t *testing.T) {
	srv, _ := newTestServer(t)
	got := make(chan string, 1)
	srv.OnInstallVersion = func(ctx context.Context, v string) (string, bool) {
		got <- v
		return "Rolling back to v1.3.0. Restart flockdeck when you are ready to install it.", false
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "installVersion", Text: "v1.3.0"})

	select {
	case v := <-got:
		if v != "v1.3.0" {
			t.Errorf("OnInstallVersion was called with %q, want v1.3.0", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnInstallVersion was never called")
	}
	select {
	case raw := <-c.out:
		var note noticeMsg
		if err := json.Unmarshal(raw, &note); err != nil || note.Type != "notice" {
			t.Fatalf("got %s, want a notice", raw)
		}
		if note.Error || !strings.Contains(note.Text, "v1.3.0") {
			t.Errorf("notice = %+v, want the answer OnInstallVersion gave", note)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the window that asked was never told what came of it")
	}
}

// An empty version names nothing to install, and is not sent to
// OnInstallVersion at all -- a stray or malformed command, not a choice
// anybody made in the picker.
func TestInstallVersionIgnoresAnEmptyVersion(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.OnInstallVersion = func(ctx context.Context, v string) (string, bool) {
		t.Errorf("OnInstallVersion was called with %q", v)
		return "", false
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "installVersion", Text: "  "})

	select {
	case raw := <-c.out:
		t.Fatalf("got %s, want nothing sent for an empty version", raw)
	case <-time.After(200 * time.Millisecond):
	}
}

// Installing a specific version is the desk's to do, as an ordinary update
// already is: a window reached through the relay is refused rather than
// installing something on a machine it is not running on.
func TestInstallVersionIsRefusedThroughTheRelay(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.OnInstallVersion = func(ctx context.Context, v string) (string, bool) {
		t.Error("a window through the relay installed a version on the desk's machine")
		return "", false
	}

	c := &controlClient{out: make(chan []byte, 8), remote: true}
	srv.handleCommand(c, command{Cmd: "installVersion", Text: "v1.3.0"})

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
		t.Fatal("a window through the relay asking to install a version was never answered")
	}
}

// Nothing wired up as OnInstallVersion says so rather than leaving the
// window to wonder whether the install started.
func TestInstallVersionWithNothingWiredUpSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "installVersion", Text: "v1.3.0"})

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
