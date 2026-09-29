package workspace

import (
	"testing"
)

// TestClosingARepoLastTabInAGroupFocusesAnotherOfItsTabs checks that a
// multi-repo project whose tab bar still has tabs in it keeps one of them
// active when the last tab of one member repo is closed. The bar shows every
// member's tabs together, so looking for the next tab among the closed tab's
// own repo alone found none and left no tab active at all: the window went on
// drawing a tab, while every command aimed at the tab on screen — split,
// close, zoom — quietly did nothing.
func TestClosingARepoLastTabInAGroupFocusesAnotherOfItsTabs(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	if _, err := ws.NewGroupFrom([]string{first, second}, "platform"); err != nil {
		t.Fatalf("group: %v", err)
	}
	ws.SelectRepo(first)
	var alpha *Tab
	for _, tab := range ws.VisibleTabs() {
		if tab.Root == first {
			alpha = tab
		}
	}
	if alpha == nil {
		t.Fatal("no tab of the first repo")
	}
	ws.SelectTab(alpha.ID)

	ws.CloseTab(alpha.ID)

	visible := ws.VisibleTabs()
	if len(visible) == 0 {
		t.Fatal("the project has no tabs left; the test needs one")
	}
	cur := ws.CurrentTab()
	if cur == nil {
		t.Fatalf("no tab is active, with %d tabs still in the project's bar", len(visible))
	}
	if cur.ID != visible[0].ID {
		t.Errorf("active tab = %q, want the tab beside the one closed, %q", cur.Title, visible[0].Title)
	}
}

// TestCyclingIntoAnotherRepoTabComesBackThere checks that a tab reached with
// the next-tab key in a multi-repo project is where switching away and back
// lands. The key moved the active tab onto another member repo's tab without
// making that repo the one worked in, so leaving the project recorded the tab
// against the wrong repo and coming back put the user on a different tab.
func TestCyclingIntoAnotherRepoTabComesBackThere(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	third := t.TempDir()
	if _, err := ws.NewGroupFrom([]string{first, second}, "platform"); err != nil {
		t.Fatalf("group: %v", err)
	}
	if err := ws.OpenProject(third); err != nil {
		t.Fatalf("open third: %v", err)
	}
	ws.SelectRepo(first)

	var onSecond *Tab
	for i := 0; i < len(ws.VisibleTabs()); i++ {
		ws.NextTab()
		if cur := ws.CurrentTab(); cur != nil && cur.Root == second {
			onSecond = cur
			break
		}
	}
	if onSecond == nil {
		t.Fatal("the next-tab key never reached the second repo's tab")
	}

	ws.SelectProject(third)
	ws.SelectProject(first)

	if cur := ws.CurrentTab(); cur == nil || cur.ID != onSecond.ID {
		got := "none"
		if cur != nil {
			got = cur.Title
		}
		t.Errorf("came back on tab %q, want %q, the one the project was left on", got, onSecond.Title)
	}
}
