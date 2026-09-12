package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// The terminal font size is kept here with the other preferences, for the same
// reason they are: the window's origin changes with the port on every run, so a
// size it kept itself was back to the default each time the application started.
func TestTheFontSizeIsRemembered(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	// Out of range, as a stale or hostile window might send it: ignored.
	sendCmd(t, conn, command{Cmd: "fontSize", Size: 400})
	sendCmd(t, conn, command{Cmd: "fontSize", Size: 16})

	got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.FontSize != 0 })
	if got.FontSize != 16 {
		t.Fatalf("the font size pushed to the windows is %d, want 16", got.FontSize)
	}
	if saved := store.LoadPrefs(); saved.FontSize != 16 {
		t.Errorf("the font size did not reach the disk: %+v", saved)
	}
	if hello := nextHello(t, dialControl(t, srv)); hello.Prefs.FontSize != 16 {
		t.Errorf("a window opened later was given font size %d, want 16", hello.Prefs.FontSize)
	}
}
