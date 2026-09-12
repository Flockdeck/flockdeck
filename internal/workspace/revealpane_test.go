package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

// TestRevealPaneSelectsItsTabAndFocusesIt covers showing a pane wherever it
// is. A child started in a tab of its own is not on screen, and FocusPane only
// works in the tab that is, so the tab has to be selected first.
func TestRevealPaneSelectsItsTabAndFocusesIt(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	lead := ws.CurrentTab()
	parent := lead.Focus

	child, err := ws.Spawn(parent, SpawnOptions{Task: "elsewhere", Kind: session.KindShell})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if ws.ActiveTabID() != lead.ID {
		t.Fatal("starting a child moved the window by itself, so this test shows nothing")
	}
	ws.RevealPane(child)
	if got, want := ws.ActiveTabID(), ws.TabIDOf(child); got != want {
		t.Fatalf("revealing the child left the window on tab %s; want its own tab %s", got, want)
	}
	if got := ws.CurrentTab().Focus; got != child {
		t.Errorf("revealing the child focused %s; want the child %s", got, child)
	}
}

// TestRevealPaneFocusesAPaneBesideYou covers a pane in the tab already on
// screen: the tab stays, and the focus moves off whichever pane had it.
func TestRevealPaneFocusesAPaneBesideYou(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	lead := ws.CurrentTab()
	parent := lead.Focus

	sibling, err := ws.Spawn(parent, SpawnOptions{Task: "beside", Kind: session.KindShell, Split: true})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if lead.Focus != parent {
		t.Fatal("starting a split child moved the focus by itself, so this test shows nothing")
	}
	ws.RevealPane(sibling)
	if got := ws.ActiveTabID(); got != lead.ID {
		t.Errorf("revealing a pane in this tab went to tab %s; want this one, %s", got, lead.ID)
	}
	if lead.Focus != sibling {
		t.Errorf("revealing the new pane left the focus on %s; want the new pane %s", lead.Focus, sibling)
	}
}

// TestRevealPaneIgnoresAPaneThatIsGone covers a pane that closed before it
// could be shown: the window stays where it was rather than going anywhere.
func TestRevealPaneIgnoresAPaneThatIsGone(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	lead := ws.CurrentTab()
	focus := lead.Focus

	ws.RevealPane("a pane that closed")
	if ws.ActiveTabID() != lead.ID || lead.Focus != focus {
		t.Errorf("revealing a pane that is gone moved the window to tab %s, pane %s", ws.ActiveTabID(), ws.CurrentTab().Focus)
	}
}
