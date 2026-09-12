package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// Desktop notifications can be turned off from the window, and stay off. The
// browser's own permission cannot do it: it belongs to the page's origin,
// which changes with the port on every run, so a "block" lasted one run.
func TestNotificationsCanBeTurnedOff(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "notifications", Kind: "off"})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.NotificationsOff }); !got.NotificationsOff {
		t.Fatalf("notifications were not turned off: %+v", got)
	}
	if !store.LoadPrefs().NotificationsOff {
		t.Error("turning notifications off did not reach the disk")
	}

	sendCmd(t, conn, command{Cmd: "notifications", Kind: "on"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return !p.NotificationsOff })
	if store.LoadPrefs().NotificationsOff {
		t.Error("turning notifications back on did not reach the disk")
	}
}
