package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// The theme is chosen in the window and kept, like the cursor's shape: "dark"
// on the wire is the default and is kept as nothing, so prefs.json written
// before there was a choice still reads as dark.
func TestTheThemeIsKept(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "theme", Text: "light"})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.Theme == "light" }); got.Theme != "light" {
		t.Fatalf("theme pushed to the windows is %q, want light", got.Theme)
	}
	if saved := store.LoadPrefs(); saved.Theme != "light" {
		t.Errorf("the theme did not reach disk: %q", saved.Theme)
	}

	// Refused: nothing is pushed for it, so the next push is the default below.
	sendCmd(t, conn, command{Cmd: "theme", Text: "sepia"})
	sendCmd(t, conn, command{Cmd: "theme", Text: "dark"})
	got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.Theme != "light" })
	if got.Theme != "" {
		t.Fatalf("dark is kept as %q, want it kept as the default", got.Theme)
	}
	if saved := store.LoadPrefs(); saved.Theme != "" {
		t.Errorf("going back to dark did not reach disk: %q", saved.Theme)
	}
}

// The accent colour is one of a fixed set of swatches; anything else is
// refused rather than let through to go illegible against one palette.
func TestTheAccentColourIsKeptAndRestricted(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "accentColor", Text: "purple"})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.AccentColor == "purple" }); got.AccentColor != "purple" {
		t.Fatalf("accent pushed to the windows is %q, want purple", got.AccentColor)
	}
	if saved := store.LoadPrefs(); saved.AccentColor != "purple" {
		t.Errorf("the accent did not reach disk: %q", saved.AccentColor)
	}

	sendCmd(t, conn, command{Cmd: "accentColor", Text: "chartreuse"})
	sendCmd(t, conn, command{Cmd: "accentColor", Text: ""})
	got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.AccentColor != "purple" })
	if got.AccentColor != "" {
		t.Fatalf("the default accent is kept as %q, want empty", got.AccentColor)
	}
}

// Fan out's own default for its "Put them in this tab" checkbox is chosen
// in Settings › Behaviour and kept, so the dialog opens on it next time.
func TestFanOutSameTabDefaultIsKept(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "fanOutSameTab", Kind: "on"})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.FanOut.SameTab }); !got.FanOut.SameTab {
		t.Fatalf("fan out's same-tab default was not turned on: %+v", got)
	}
	if !store.LoadPrefs().FanOut.SameTab {
		t.Error("fan out's same-tab default did not reach disk")
	}

	sendCmd(t, conn, command{Cmd: "fanOutSameTab", Kind: "off"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return !p.FanOut.SameTab })
	if store.LoadPrefs().FanOut.SameTab {
		t.Error("turning fan out's same-tab default off did not reach disk")
	}
}

// The default a pane with no parent starts AutoReview from is chosen in
// Settings › Behaviour and kept; see internal/workspace's own tests for what
// a pane actually starts with.
func TestAutoReviewDefaultIsKept(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "autoReviewDefault", Kind: "on"})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.AutoReviewDefault }); !got.AutoReviewDefault {
		t.Fatalf("the auto-review default was not turned on: %+v", got)
	}
	if !store.LoadPrefs().AutoReviewDefault {
		t.Error("the auto-review default did not reach disk")
	}

	sendCmd(t, conn, command{Cmd: "autoReviewDefault", Kind: "off"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return !p.AutoReviewDefault })
	if store.LoadPrefs().AutoReviewDefault {
		t.Error("turning the auto-review default off did not reach disk")
	}
}
