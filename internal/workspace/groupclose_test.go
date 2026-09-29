package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/layout"
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

// groupedTabs groups two open projects and returns the tab of each, as the
// one tab bar of the grouped project shows them.
func groupedTabs(t *testing.T) (ws *Workspace, a, b *Tab) {
	t.Helper()
	ws, first, second := twoProjects(t)
	if _, err := ws.NewGroupFrom([]string{first, second}, "platform"); err != nil {
		t.Fatalf("group: %v", err)
	}
	ws.SelectRepo(first)
	for _, tab := range ws.VisibleTabs() {
		switch tab.Root {
		case first:
			a = tab
		case second:
			b = tab
		}
	}
	if a == nil || b == nil {
		t.Fatalf("want a tab of each repo in the grouped bar, got %d tabs", len(ws.VisibleTabs()))
	}
	return ws, a, b
}

// TestTabsReorderAcrossReposOfOneProject checks that a multi-repo project's
// tabs, drawn in one bar, can be dragged into any order in it. Reordering
// compared the tabs' repos rather than their project, so dropping a tab
// beside a tab of another member was refused as a move to another project.
func TestTabsReorderAcrossReposOfOneProject(t *testing.T) {
	isolateConfig(t)
	ws, a, b := groupedTabs(t)

	if err := ws.MoveTab(b.ID, a.ID); err != nil {
		t.Fatalf("move tab before a tab of the other repo: %v", err)
	}
	if got := ws.VisibleTabs(); got[0] != b || got[1] != a {
		t.Errorf("order = %q, %q; want %q first", got[0].Title, got[1].Title, b.Title)
	}
	if err := ws.MoveTab(b.ID, ""); err != nil {
		t.Fatalf("move tab to the end: %v", err)
	}
	if got := ws.VisibleTabs(); got[len(got)-1] != b {
		t.Errorf("moved to the end, but the last tab in the bar is %q", got[len(got)-1].Title)
	}
}

// TestTabsMergeAcrossReposOfOneProject checks that two tabs of one
// multi-repo project can be merged, the way any two tabs in one bar can. The
// merged tab shows the other repo's agent the way a tab showing a pane
// split into another project does, and the agent keeps its own repo.
func TestTabsMergeAcrossReposOfOneProject(t *testing.T) {
	isolateConfig(t)
	ws, a, b := groupedTabs(t)
	moved := a.Tree.Panes()[0]

	if err := ws.MergeTab(a.ID, b.ID, layout.Horizontal); err != nil {
		t.Fatalf("merge a tab into a tab of the other repo: %v", err)
	}
	if ws.Tab(a.ID) != nil {
		t.Error("the merged tab is still open")
	}
	if b.Tree.Find(moved) == nil {
		t.Fatal("the merged tab's pane is not in the tab it was merged into")
	}
	if got, want := ws.RootOf(moved), a.Root; got != want {
		t.Errorf("merged pane's project = %q, want its own repo %q", got, want)
	}
}
