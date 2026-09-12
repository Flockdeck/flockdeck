package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// How many lines a terminal keeps is chosen in the window and kept with the
// other preferences; before, it was a number in the source.
func TestTheScrollbackIsRemembered(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	// Too few to be useful, and more than a dozen panes should hold: ignored.
	sendCmd(t, conn, command{Cmd: "scrollback", Size: 10})
	sendCmd(t, conn, command{Cmd: "scrollback", Size: 5_000_000})
	sendCmd(t, conn, command{Cmd: "scrollback", Size: 50000})

	got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.Scrollback != 0 })
	if got.Scrollback != 50000 {
		t.Fatalf("the scrollback pushed to the windows is %d, want 50000", got.Scrollback)
	}
	if saved := store.LoadPrefs(); saved.Scrollback != 50000 {
		t.Errorf("the scrollback did not reach the disk: %+v", saved)
	}
}
