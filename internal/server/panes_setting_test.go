package server

import (
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// The panes setting is a command of the desk: structured, terminal, or default
// to follow the default. Each is kept in prefs.json (the test server runs in a
// temporary state folder), a value it does not know changes nothing, and a
// window reached through the relay is refused.
func TestThePanesSettingIsSavedAndRefusedFromTheRelay(t *testing.T) {
	srv, _ := newTestServer(t)
	if err := store.SavePrefs(store.Prefs{StructuredPanes: true}); err != nil {
		t.Fatal(err)
	}
	c := &controlClient{out: make(chan []byte, 8)}
	for _, tc := range []struct {
		text, want string
	}{
		{"terminal", store.PanesTerminal},
		{"structured", store.PanesStructured},
		{"default", ""},
		{"terminal", store.PanesTerminal},
		{"nonsense", store.PanesTerminal},
	} {
		srv.handleCommand(c, command{Cmd: "panes", Text: tc.text})
		untilExport(t, "the setting "+tc.text, func() bool {
			ask(srv, func() bool { return true }) // let the queued save finish
			return store.LoadPrefs().PanesChoice() == tc.want
		})
	}
	if p := store.LoadPrefs(); p.StructuredPanes {
		t.Error("the older structuredPanes key was left beside the new one")
	}

	remote := &controlClient{remote: true, device: "d1", out: make(chan []byte, 8)}
	srv.handleCommand(remote, command{Cmd: "panes", Text: "structured"})
	untilExport(t, "the refusal", func() bool { return len(remote.out) > 0 })
	time.Sleep(50 * time.Millisecond)
	if got := store.LoadPrefs().PanesChoice(); got != store.PanesTerminal {
		t.Errorf("a window reached through the relay changed the setting to %q", got)
	}
	n := exportNotices(remote)
	if len(n) == 0 || !n[0].Error || !strings.Contains(n[0].Text, "machine Flockdeck runs on") {
		t.Errorf("notices = %+v", n)
	}
}

// restartPane with kind terminal on a pane that has no terminal way back is
// refused with a notice and leaves the pane alone.
func TestRestartAsTerminalIsRefusedForAPaneWithNoWayBack(t *testing.T) {
	srv, ws := newTestServer(t)
	id := addPane(t, srv, ws, "shell")
	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "restartPane", ID: id, Kind: "terminal"})
	untilExport(t, "the notice", func() bool { return len(c.out) > 0 })
	n := exportNotices(c)
	if len(n) != 1 || !n[0].Error || !strings.Contains(n[0].Text, "terminal version") {
		t.Errorf("notices = %+v", n)
	}
}
