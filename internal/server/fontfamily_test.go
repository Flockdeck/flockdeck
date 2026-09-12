package server

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// The terminals' typeface is chosen in the window and kept; it was fixed in
// the source.
func TestTheTerminalFontIsRemembered(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	// Longer than any font list, as a paste gone wrong would be: refused.
	sendCmd(t, conn, command{Cmd: "fontFamily", Text: strings.Repeat("x", 500)})
	sendCmd(t, conn, command{Cmd: "fontFamily", Text: "  Fira Code  "})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.FontFamily != "" }); got.FontFamily != "Fira Code" {
		t.Fatalf("the font pushed to the windows is %q, want %q", got.FontFamily, "Fira Code")
	}
	if saved := store.LoadPrefs(); saved.FontFamily != "Fira Code" {
		t.Errorf("the font did not reach the disk: %+v", saved)
	}

	// Empty goes back to the default.
	sendCmd(t, conn, command{Cmd: "fontFamily", Text: ""})
	nextPrefs(t, conn, func(p store.Prefs) bool { return p.FontFamily == "" })
}
