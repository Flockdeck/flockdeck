package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// Update checks can be turned off from the window, which before took the
// FLOCKDECK_UPDATE environment variable, and the choice is kept.
func TestUpdateChecksCanBeTurnedOff(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "updates", Kind: "off"})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.UpdatesOff }); !got.UpdatesOff {
		t.Fatalf("update checks were not turned off: %+v", got)
	}
	if !store.LoadPrefs().UpdatesOff {
		t.Error("turning update checks off did not reach the disk")
	}

	sendCmd(t, conn, command{Cmd: "updates", Kind: "on"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return !p.UpdatesOff })
	if store.LoadPrefs().UpdatesOff {
		t.Error("turning update checks back on did not reach the disk")
	}
}
