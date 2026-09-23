package server

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// Whether the last lines of a pane's output may be sent to TypeSafe is chosen
// in Settings › Behaviour, is off until then, and is kept.
func TestJevStatusIsOffByDefaultAndKept(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	if hello := nextHello(t, conn); hello.Prefs.JevStatus {
		t.Fatal("sending terminal output to TypeSafe starts on")
	}

	sendCmd(t, conn, command{Cmd: "jevStatus", Kind: "on"})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.JevStatus }); !got.JevStatus {
		t.Fatalf("the setting was not turned on: %+v", got)
	}
	if !store.LoadPrefs().JevStatus {
		t.Error("the setting did not reach disk")
	}

	sendCmd(t, conn, command{Cmd: "jevStatus", Kind: "off"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return !p.JevStatus })
	if store.LoadPrefs().JevStatus {
		t.Error("turning the setting off did not reach disk")
	}

	// Anything but an explicit "on" is off.
	sendCmd(t, conn, command{Cmd: "jevStatus", Kind: "on"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return p.JevStatus })
	sendCmd(t, conn, command{Cmd: "jevStatus"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return !p.JevStatus })
}

// A window reached through the relay cannot turn it on -- the relay is meant to
// be blind to terminal content, and this would have the machine send it
// elsewhere -- but can always turn it off.
func TestJevStatusIsTurnedOnOnlyAtTheDesk(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()

	sendCmd(t, phone, command{Cmd: "jevStatus", Kind: "on"})
	var note noticeMsg
	readUntil(t, phone, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "machine") {
		t.Errorf("turning it on through the relay was told %+v, want to do it on the machine itself", note)
	}
	if store.LoadPrefs().JevStatus {
		t.Fatal("a window through the relay turned on sending terminal output to TypeSafe")
	}

	desk := dialControl(t, srv)
	nextHello(t, desk)
	sendCmd(t, desk, command{Cmd: "jevStatus", Kind: "on"})
	nextPrefs(t, desk, func(p store.Prefs) bool { return p.JevStatus })
	sendCmd(t, phone, command{Cmd: "jevStatus", Kind: "off"})
	nextPrefs(t, desk, func(p store.Prefs) bool { return !p.JevStatus })
}
