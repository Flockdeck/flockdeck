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

// The cursor's shape is chosen in the settings and kept. A block is the
// default and is kept as nothing, so prefs.json written before there was a
// choice still reads as a block; a shape the terminals cannot draw is refused.
func TestTheCursorShapeIsKept(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "cursorStyle", Text: "bar"})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.CursorStyle != "" }); got.CursorStyle != "bar" {
		t.Fatalf("the cursor shape pushed to the windows is %q, want bar", got.CursorStyle)
	}
	if saved := store.LoadPrefs(); saved.CursorStyle != "bar" {
		t.Errorf("the cursor shape did not reach the disk: %q", saved.CursorStyle)
	}

	// Refused: nothing is pushed for it, so the next push is the block below.
	sendCmd(t, conn, command{Cmd: "cursorStyle", Text: "triangle"})
	sendCmd(t, conn, command{Cmd: "cursorStyle", Text: "block"})
	got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.CursorStyle != "bar" })
	if got.CursorStyle != "" {
		t.Fatalf("a block is kept as %q, want it kept as the default", got.CursorStyle)
	}
	if saved := store.LoadPrefs(); saved.CursorStyle != "" {
		t.Errorf("going back to a block did not reach the disk: %q", saved.CursorStyle)
	}
}
