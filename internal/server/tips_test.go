package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// A dismissed hint could be brought back only by editing prefs.json by hand.
func TestDismissedTipsCanBeBroughtBack(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "dismissTip", ID: "palette"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return p.Dismissed("palette") })

	sendCmd(t, conn, command{Cmd: "resetTips"})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return !p.Dismissed("palette") }); got.Dismissed("palette") {
		t.Fatalf("the hint is still dismissed: %+v", got)
	}
	if store.LoadPrefs().Dismissed("palette") {
		t.Error("bringing the hints back did not reach the disk")
	}
}
