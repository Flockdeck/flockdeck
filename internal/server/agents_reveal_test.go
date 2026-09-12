package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestRevealFindsThePaneWhereItIsNow covers revealing a pane from a list drawn
// earlier -- the overview, or a notification clicked minutes after it came.
// The list names the tab the pane was in then, and a pane moved since was not
// revealed at all: the window went to its old tab and focused nothing.
func TestRevealFindsThePaneWhereItIsNow(t *testing.T) {
	srv, ws := newTestServer(t)
	type place struct{ tab, pane string }
	where := func() place {
		p, _ := ask(srv, func() place {
			tab := ws.CurrentTab()
			return place{tab.ID, tab.Focus}
		})
		return p
	}
	first := where()
	other, _ := ask(srv, func() string {
		return ws.NewTabWith(workspace.Choice{Kind: session.KindShell}, "", "other").ID
	})

	// The list puts the pane in the other tab, as it would once the pane had
	// moved out of there.
	c := &controlClient{out: make(chan []byte, 8)}
	srv.revealPane(c, ws.ActiveRoot(), other, first.pane)

	if got := where(); got != first {
		t.Fatalf("revealing the pane left the window on tab %s focused on %s; want its own tab %s focused on it",
			got.tab, got.pane, first.tab)
	}
}
