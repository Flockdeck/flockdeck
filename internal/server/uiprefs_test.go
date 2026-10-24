package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

func TestUIPrefsIsMergedKeptAndBroadcast(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "uiPrefs", UI: map[string]json.RawMessage{"density": json.RawMessage(`"tight"`), "fontSize": json.RawMessage(`15`)}})
	got := nextPrefs(t, conn, func(p store.Prefs) bool { return string(p.UI["density"]) != "" })
	if string(got.UI["density"]) != `"tight"` || string(got.UI["fontSize"]) != "15" {
		t.Fatalf("broadcast ui = %v", got.UI)
	}
	// An older interface reads the old field, which is filled from the blob.
	if got.FontSize != 15 {
		t.Errorf("the old fontSize field = %d, want 15", got.FontSize)
	}
	if saved := store.LoadPrefs(); string(saved.UI["density"]) != `"tight"` {
		t.Errorf("not on disk: %v", saved.UI)
	}

	// Another key does not undo the first, and null removes one.
	sendCmd(t, conn, command{Cmd: "uiPrefs", UI: map[string]json.RawMessage{"other": json.RawMessage(`[1]`), "density": json.RawMessage(`null`)}})
	got = nextPrefs(t, conn, func(p store.Prefs) bool { return string(p.UI["other"]) != "" })
	if _, has := got.UI["density"]; has || string(got.UI["fontSize"]) != "15" {
		t.Errorf("after the second patch: %v", got.UI)
	}
}

func TestUIPrefsOutsideTheLimitsIsRefusedAndSaysWhy(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)
	sendCmd(t, conn, command{Cmd: "uiPrefs", UI: map[string]json.RawMessage{"bad key": json.RawMessage(`1`)}})
	n := nextOfType(t, conn, "notice")
	if text, _ := n["text"].(string); !strings.Contains(text, "was not kept") || n["error"] != true {
		t.Errorf("notice = %v", n)
	}
	if saved := store.LoadPrefs(); len(saved.UI) != 0 {
		t.Errorf("a refused patch was kept: %v", saved.UI)
	}
	// An empty patch is no change and says nothing.
	sendCmd(t, conn, command{Cmd: "uiPrefs"})
}

func TestTheOldCommandsWriteIntoTheBlob(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)
	sendCmd(t, conn, command{Cmd: "fontSize", Size: 16})
	sendCmd(t, conn, command{Cmd: "scrollback", Size: 8000})
	sendCmd(t, conn, command{Cmd: "fontFamily", Text: "Fira Code"})
	sendCmd(t, conn, command{Cmd: "helpSeen"})
	got := nextPrefs(t, conn, func(p store.Prefs) bool { return len(p.UI) == 4 })
	for k, want := range map[string]string{"fontSize": "16", "scrollback": "8000", "fontFamily": `"Fira Code"`, "helpSeen": "true"} {
		if string(got.UI[k]) != want {
			t.Errorf("ui[%s] = %s, want %s", k, got.UI[k], want)
		}
	}
	// The family back to the default is kept as nothing.
	sendCmd(t, conn, command{Cmd: "fontFamily", Text: "  "})
	got = nextPrefs(t, conn, func(p store.Prefs) bool { return len(p.UI) == 3 })
	if _, has := got.UI["fontFamily"]; has {
		t.Errorf("the default family was kept: %v", got.UI)
	}
	// A size outside what a window accepts is still refused.
	sendCmd(t, conn, command{Cmd: "fontSize", Size: 99})
	sendCmd(t, conn, command{Cmd: "helpSeen"})
	sendCmd(t, conn, command{Cmd: "fontSize", Size: 17})
	got = nextPrefs(t, conn, func(p store.Prefs) bool { return string(p.UI["fontSize"]) == "17" })
	if string(got.UI["fontSize"]) != "17" {
		t.Errorf("ui = %v", got.UI)
	}
}

func TestAViewerMayNotSendUIPrefs(t *testing.T) {
	if commandAccess["uiPrefs"] != accessFull {
		t.Errorf("uiPrefs access = %v, want full", commandAccess["uiPrefs"])
	}
}

func TestHelloSaysThereAreUIPrefs(t *testing.T) {
	srv, _ := newTestServer(t)
	hello := nextHello(t, dialControl(t, srv))
	found := false
	for _, c := range hello.API.Caps {
		found = found || c == "uiPrefs"
	}
	if !found || hello.API.Minor != APIMinor || APIMinor < 1 {
		t.Errorf("api = %+v", hello.API)
	}
}
