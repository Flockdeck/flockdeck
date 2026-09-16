package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// Whether the rail is a panel of icons and names, or icons alone, is chosen
// in the window and kept with the other preferences, like the cursor's shape:
// a window opened on the next run should not have to widen it again.
func TestTheRailsExpandedStateIsKept(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "railExpanded", Kind: "on"})
	if got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.RailExpanded }); !got.RailExpanded {
		t.Fatalf("the rail was not recorded as expanded: %+v", got)
	}
	if !store.LoadPrefs().RailExpanded {
		t.Error("the rail's expanded state did not reach the disk")
	}

	sendCmd(t, conn, command{Cmd: "railExpanded", Kind: "off"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return !p.RailExpanded })
	if store.LoadPrefs().RailExpanded {
		t.Error("folding the rail again did not reach the disk")
	}
}

// How wide the rail is drawn while expanded is chosen by dragging its edge or
// the keyboard, and kept the same way, within what a window will still show
// something in.
func TestTheRailsWidthIsRemembered(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	// Too narrow to show a name beside an icon, and wider than the window will
	// sensibly go: both ignored.
	sendCmd(t, conn, command{Cmd: "railWidth", Size: 40})
	sendCmd(t, conn, command{Cmd: "railWidth", Size: 5000})
	sendCmd(t, conn, command{Cmd: "railWidth", Size: 260})

	got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.RailWidth != 0 })
	if got.RailWidth != 260 {
		t.Fatalf("the rail width pushed to the windows is %d, want 260", got.RailWidth)
	}
	if saved := store.LoadPrefs(); saved.RailWidth != 260 {
		t.Errorf("the rail width did not reach the disk: %+v", saved)
	}
}
