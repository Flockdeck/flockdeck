package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// Whether the terminal cursors blink is chosen in the window and kept; it was
// fixed in the source.
func TestTheCursorCanBeMadeSteady(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "cursorBlink", Kind: "off"})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.CursorSteady }); !got.CursorSteady {
		t.Fatalf("the cursor was not made steady: %+v", got)
	}
	if !store.LoadPrefs().CursorSteady {
		t.Error("a steady cursor did not reach the disk")
	}

	sendCmd(t, conn, command{Cmd: "cursorBlink", Kind: "on"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return !p.CursorSteady })
	if store.LoadPrefs().CursorSteady {
		t.Error("making the cursor blink again did not reach the disk")
	}
}
