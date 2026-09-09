package workspace

import (
	"testing"

	"github.com/google/uuid"

	"github.com/jmwri/perch/internal/layout"
	"github.com/jmwri/perch/internal/session"
	"github.com/jmwri/perch/internal/store"
)

// twoProjects opens two projects and leaves the first one active with a tab of
// its own, which is the starting point for working on both at once.
func twoProjects(t *testing.T) (ws *Workspace, first, second string) {
	t.Helper()
	first, second = t.TempDir(), t.TempDir()
	ws = newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "alpha")
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second project: %v", err)
	}
	ws.SelectProject(first)
	return ws, first, second
}

// borrowedPane returns the pane in tab that does not belong to the tab's own
// project — the whole point of the feature, and what every test below checks.
func borrowedPane(t *testing.T, ws *Workspace, tab *Tab) *Pane {
	t.Helper()
	for _, id := range tab.Tree.Panes() {
		if p := ws.Pane(id); p != nil && !sameDir(p.Root, tab.Root) {
			return p
		}
	}
	return nil
}

// TestSplitIntoAnotherProjectSharesOneTab covers working on two projects at
// the same time rather than one after the other: a tab of one project holding
// an agent of another, with each pane still knowing which project it is in.
func TestSplitIntoAnotherProjectSharesOneTab(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)

	tab := ws.CurrentTab()
	if tab == nil {
		t.Fatal("no tab to split")
	}
	before := tab.Tree.Count()
	ws.SplitPaneInProject(layout.Horizontal, session.KindShell, second)

	if got := tab.Tree.Count(); got != before+1 {
		t.Fatalf("tab holds %d panes, want %d", got, before+1)
	}
	// The tab stays where it was: it is shown in the first project's tab bar
	// and saved with the first project's layout.
	if tab.Root != first {
		t.Errorf("tab moved to %q, want it to stay in %q", tab.Root, first)
	}

	p := borrowedPane(t, ws, tab)
	if p == nil {
		t.Fatal("the split did not leave a pane belonging to the other project")
	}
	if p.Root != second {
		t.Errorf("pane project = %q, want %q", p.Root, second)
	}
	if p.Cwd != second {
		t.Errorf("pane cwd = %q, want %q", p.Cwd, second)
	}
	// This is what the agent is told and what the overview names it by, and it
	// used to be answered by the tab.
	if got := ws.RootOf(p.ID); got != second {
		t.Errorf("RootOf = %q, want the pane's own project %q", got, second)
	}
}

// TestClosingAProjectStopsItsAgentsOnAnotherProjectsTab checks that closing a
// project still means what it says once its agents can be shown anywhere.
// Walking only its own tabs would leave one running with no project.
func TestClosingAProjectStopsItsAgentsOnAnotherProjectsTab(t *testing.T) {
	isolateConfig(t)
	ws, _, second := twoProjects(t)

	tab := ws.CurrentTab()
	ws.SplitPaneInProject(layout.Horizontal, session.KindShell, second)
	p := borrowedPane(t, ws, tab)
	if p == nil {
		t.Fatal("the split did not leave a pane belonging to the other project")
	}
	borrowed := p.ID
	kept := tab.Tree.Count()

	ws.CloseProject(second)

	if ws.Pane(borrowed) != nil {
		t.Error("closing a project left one of its agents running on another project's tab")
	}
	tab = ws.CurrentTab()
	if tab == nil {
		t.Fatal("closing the other project took this project's tab with it")
	}
	if got := tab.Tree.Count(); got != kept-1 {
		t.Errorf("tab holds %d panes, want %d", got, kept-1)
	}
	if tab.Tree.Find(tab.Focus) == nil {
		t.Error("focus was left on a pane that is gone")
	}
}

// TestABorrowedPaneComesBackWithItsProject covers the restart. The pane is
// saved in the first project's layout but belongs to the second, so restoring
// has to open the second project as well — otherwise the pane comes back
// belonging to a project that is not open.
func TestABorrowedPaneComesBackWithItsProject(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	ws.SplitPaneInProject(layout.Horizontal, session.KindShell, second)
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	again := newTestWorkspace(t, first)
	if _, err := again.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}

	var tab *Tab
	for _, candidate := range again.VisibleTabs() {
		if borrowedPane(t, again, candidate) != nil {
			tab = candidate
		}
	}
	if tab == nil {
		t.Fatal("the borrowed pane did not come back")
	}
	if p := borrowedPane(t, again, tab); p.Root != second {
		t.Errorf("restored pane project = %q, want %q", p.Root, second)
	}

	opened := false
	for _, p := range again.Projects() {
		if sameDir(p.Root, second) {
			opened = true
		}
	}
	if !opened {
		t.Error("the borrowed pane's project was not opened, so nothing owns the agent")
	}
}

// TestALayoutWithoutPaneProjectsUsesTheTabs covers every layout written before
// panes recorded a project of their own. Those files mean "the tab's project",
// and reading them as "no project" would leave restored panes owned by nothing.
func TestALayoutWithoutPaneProjectsUsesTheTabs(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()

	id := uuid.NewString()
	err := store.Save(root, &store.State{
		Tabs: []store.Tab{{
			Title: "legacy",
			Focus: id,
			Root:  &store.Node{Pane: &store.Pane{ID: id, Kind: "shell", Cwd: root, Name: "legacy"}},
		}},
	})
	if err != nil {
		t.Fatalf("save legacy layout: %v", err)
	}

	ws := newTestWorkspace(t, root)
	if _, err := ws.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	p := ws.Pane(id)
	if p == nil {
		t.Fatal("the legacy layout did not restore its pane")
	}
	if p.Root != root {
		t.Errorf("pane project = %q, want the tab's project %q", p.Root, root)
	}
}
