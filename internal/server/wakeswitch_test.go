package server

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// The three switches are set on the machine, kept, and each leaves the others
// alone. A bad command changes nothing.
func TestWakeSwitchesAreSetAtTheDeskAndKept(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	if hello := nextHello(t, conn); !hello.Prefs.WakeEnabled("claude", "p1") {
		t.Fatal("wake starts off")
	}

	sendCmd(t, conn, command{Cmd: "wakeSwitch", Kind: "pane", ID: "p1", Text: "off"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return !p.WakeEnabled("claude", "p1") })
	sendCmd(t, conn, command{Cmd: "wakeSwitch", Kind: "agent", ID: "aider", Text: "off"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return !p.WakeEnabled("aider", "p2") })
	sendCmd(t, conn, command{Cmd: "wakeSwitch", Kind: "global", Text: "off"})
	got := nextPrefs(t, conn, func(p store.Prefs) bool { return !p.WakeEnabled("claude", "p2") })
	if !got.Wake.Off || len(got.Wake.Agents) != 1 || len(got.Wake.Panes) != 1 {
		t.Fatalf("wake = %+v", got.Wake)
	}
	if disk := store.LoadPrefs().Wake; !disk.Off || len(disk.Agents) != 1 || len(disk.Panes) != 1 {
		t.Fatalf("on disk: %+v", disk)
	}

	// Global back on leaves the pane and the type off.
	sendCmd(t, conn, command{Cmd: "wakeSwitch", Kind: "global", Text: "on"})
	got = nextPrefs(t, conn, func(p store.Prefs) bool { return !p.Wake.Off })
	if got.WakeEnabled("claude", "p1") || got.WakeEnabled("aider", "p2") || !got.WakeEnabled("claude", "p2") {
		t.Fatalf("precedence after global on: %+v", got.Wake)
	}

	// None of these changes anything; a following good one is the next push.
	for _, bad := range []command{
		{Cmd: "wakeSwitch", Kind: "team", ID: "x", Text: "off"},
		{Cmd: "wakeSwitch", Kind: "pane", Text: "off"},
		{Cmd: "wakeSwitch", Kind: "pane", ID: "p7"},
		{Cmd: "wakeSwitch", Kind: "pane", ID: "p7", Text: "maybe"},
	} {
		sendCmd(t, conn, bad)
	}
	sendCmd(t, conn, command{Cmd: "wakeSwitch", Kind: "pane", ID: "p1", Text: "on"})
	got = nextPrefs(t, conn, func(p store.Prefs) bool { return len(p.Wake.Panes) == 0 })
	if len(got.Wake.Agents) != 1 {
		t.Fatalf("wake = %+v", got.Wake)
	}
}

// A window reached through the relay is refused, off as well as on, and is told.
func TestWakeSwitchIsRefusedToRelayWindows(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()

	for _, text := range []string{"off", "on"} {
		sendCmd(t, phone, command{Cmd: "wakeSwitch", Kind: "global", Text: text})
		var note noticeMsg
		readUntil(t, phone, "notice", &note)
		if !note.Error || !strings.Contains(note.Text, "machine") {
			t.Errorf("%s through the relay was told %+v", text, note)
		}
	}
	if w := store.LoadPrefs().Wake; w.Off || len(w.Agents) != 0 || len(w.Panes) != 0 {
		t.Fatalf("a relay window changed wake: %+v", w)
	}
}

// A viewer device is refused by commandAccess before the command runs.
func TestWakeSwitchIsRefusedToViewers(t *testing.T) {
	srv, _ := newTestServer(t)
	setAccess(t, srv, "watch-dev", "viewer", true)
	ts := remoteServer(t, srv)
	viewer, err := dialRemoteControlDevice(ts, ts.URL, "watch-dev")
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.CloseNow()
	nextState(t, viewer, nil)

	sendCmd(t, viewer, command{Cmd: "wakeSwitch", Kind: "global", Text: "off"})
	var note noticeMsg
	readQuick(t, viewer, "notice", &note)
	if note.Text != viewerRefused {
		t.Errorf("wakeSwitch from a viewer was answered %+v", note)
	}
	if store.LoadPrefs().Wake.Off {
		t.Fatal("a viewer turned wake off")
	}
}
