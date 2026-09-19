package workspace

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestEveryProjectStartsAsASingletonGroup checks the additive promise the
// plan hinges on: a project nobody has grouped reads exactly as it always
// did, one entry per open root, one member in its own list.
func TestEveryProjectStartsAsASingletonGroup(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)

	projects := ws.Projects()
	if len(projects) != 2 {
		t.Fatalf("projects = %d, want 2 ungrouped roots", len(projects))
	}
	for _, p := range projects {
		if len(p.Members) != 1 {
			t.Errorf("project %q has %d members, want 1", p.Root, len(p.Members))
		}
	}
	_ = first
	_ = second
}

// TestGroupingTwoOpenProjectsMergesTheirSwitcherEntry covers the point of
// the whole feature: two projects opened separately, once grouped, appear
// as one switcher entry whose counts sum both, and whose tabs are shown
// together.
func TestGroupingTwoOpenProjectsMergesTheirSwitcherEntry(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	ws.SelectProject(second)
	ws.NewTab(session.KindShell, second, "extra")
	ws.SelectProject(first)

	primary, err := ws.NewGroupFrom([]string{first, second}, "platform")
	if err != nil {
		t.Fatalf("group: %v", err)
	}

	projects := ws.Projects()
	if len(projects) != 1 {
		t.Fatalf("projects = %d, want 1 merged entry", len(projects))
	}
	p := projects[0]
	if p.Name != "platform" {
		t.Errorf("name = %q, want platform", p.Name)
	}
	if len(p.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(p.Members))
	}
	// Every tab across both repos counts toward the one entry: first's own
	// "alpha", second's own default tab, and second's own "extra" made
	// above.
	if p.Tabs != 3 {
		t.Errorf("tabs = %d, want 3", p.Tabs)
	}
	if p.Root != primary {
		t.Errorf("root = %q, want the group's primary %q", p.Root, primary)
	}

	// The switcher shows every member's tabs together once the group is
	// active, whichever member happens to be the concrete active root.
	visible := ws.VisibleTabs()
	if len(visible) != 3 {
		t.Fatalf("visible tabs = %d, want 3", len(visible))
	}
}

// TestAddRepoToGroupJoinsWithoutANewSwitcherEntry checks "Add Repo to This
// Project…": opening a third repo into an existing group must not create a
// switcher entry of its own.
func TestAddRepoToGroupJoinsWithoutANewSwitcherEntry(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	primary, err := ws.NewGroupFrom([]string{first, second}, "platform")
	if err != nil {
		t.Fatalf("group: %v", err)
	}

	third := t.TempDir()
	if err := ws.AddRepoToGroup(primary, third); err != nil {
		t.Fatalf("add repo: %v", err)
	}

	projects := ws.Projects()
	if len(projects) != 1 {
		t.Fatalf("projects = %d, want the one merged entry, not a new one for %q", len(projects), third)
	}
	if len(projects[0].Members) != 3 {
		t.Fatalf("members = %d, want 3", len(projects[0].Members))
	}
	if !sameDir(ws.ActiveRoot(), third) {
		t.Errorf("active root = %q, want the newly added repo %q", ws.ActiveRoot(), third)
	}
}

// TestAddRepoToGroupAcceptsADirectoryWithNoGitRepository checks that a
// member need not be a git repository at all: AddRepoToGroup only checks
// that the path is a directory (see openProjectInto), and ProjectRepos --
// what the worktrees panel fans its listing out across -- lists a plain
// folder alongside a repo exactly the same way.
func TestAddRepoToGroupAcceptsADirectoryWithNoGitRepository(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	primary, err := ws.NewGroupFrom([]string{first, second}, "platform")
	if err != nil {
		t.Fatalf("group: %v", err)
	}

	// Deliberately not a git repository: no .git anywhere in it, and nothing
	// about AddRepoToGroup or the group it joins requires one.
	docs := t.TempDir()
	if err := ws.AddRepoToGroup(primary, docs); err != nil {
		t.Fatalf("add a plain directory to the group: %v", err)
	}

	repos := ws.ProjectRepos(primary)
	if len(repos) != 3 {
		t.Fatalf("ProjectRepos = %d, want 3", len(repos))
	}
	found := false
	for _, r := range repos {
		if sameDir(r.Root, docs) {
			found = true
		}
	}
	if !found {
		t.Errorf("ProjectRepos %+v does not list the plain directory %q", repos, docs)
	}
}

// TestRemoveRepoFromGroupSplitsItOutWithoutClosingIt checks ungrouping: the
// repo stays open, with its tabs intact, but reads as its own project again.
func TestRemoveRepoFromGroupSplitsItOutWithoutClosingIt(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	if _, err := ws.NewGroupFrom([]string{first, second}, "platform"); err != nil {
		t.Fatalf("group: %v", err)
	}

	if err := ws.RemoveRepoFromGroup(second); err != nil {
		t.Fatalf("remove: %v", err)
	}

	projects := ws.Projects()
	if len(projects) != 2 {
		t.Fatalf("projects = %d, want the group and the split-out repo as two entries", len(projects))
	}
	if ws.Pane(ws.tabsOf(second)[0].Focus) == nil {
		t.Error("splitting a repo out of its group must not touch its panes")
	}
}

// TestRemoveRepoFromGroupRefusesTheLastMember mirrors CloseProject's own
// refusal to close the last open project: a group cannot be emptied by
// removing its own last member.
func TestRemoveRepoFromGroupRefusesTheLastMember(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	if err := ws.RemoveRepoFromGroup(root); err == nil {
		t.Error("removing a group's only member should be refused")
	}
	if len(ws.Projects()) != 1 {
		t.Fatalf("projects = %d, want the one project still there", len(ws.Projects()))
	}
}

// TestClosingAGroupedProjectClosesEveryMember checks CloseProject named by
// any one member closes the whole project, and that a pane one member
// borrowed from the other -- now also closing -- is destroyed rather than
// rescued into a tab that is about to disappear too.
func TestClosingAGroupedProjectClosesEveryMember(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	ws.SplitPaneInProject(layout.Horizontal, session.KindShell, second)
	if _, err := ws.NewGroupFrom([]string{first, second}, "platform"); err != nil {
		t.Fatalf("group: %v", err)
	}
	third := t.TempDir()
	if err := ws.OpenProject(third); err != nil {
		t.Fatalf("open third: %v", err)
	}

	if err := ws.CloseProject(first); err != nil {
		t.Fatalf("close: %v", err)
	}

	for _, t2 := range ws.Tabs {
		if sameDir(t2.Root, first) || sameDir(t2.Root, second) {
			t.Errorf("a tab of the closed project is still around: %+v", t2)
		}
	}
	if len(ws.Projects()) != 1 {
		t.Fatalf("projects = %d, want only the third one left", len(ws.Projects()))
	}
}

// TestCloseProjectRefusesTheLastOpenProject checks the group-level version
// of the rule that used to be root-level: the last open project, however
// many repos it spans, cannot be closed -- naming one of its two repos
// must not close even that one, since the whole project would then have
// nothing left open.
func TestCloseProjectRefusesTheLastOpenProject(t *testing.T) {
	isolateConfig(t)
	first, second := t.TempDir(), t.TempDir()
	ws := newTestWorkspace(t, first)
	// newTestWorkspace opens first with no tabs of its own yet -- unlike
	// OpenProject, New does not create one -- so first needs one made by
	// hand before its tabs surviving the refused close means anything.
	ws.NewTab(session.KindShell, first, "alpha")
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := ws.NewGroupFrom([]string{first, second}, "platform"); err != nil {
		t.Fatalf("group: %v", err)
	}

	if err := ws.CloseProject(first); err != nil {
		t.Fatalf("close: %v", err)
	}
	if len(ws.Projects()) != 1 || len(ws.Projects()[0].Members) != 2 {
		t.Fatalf("projects = %+v, want both repos still there, refused as the last open project", ws.Projects())
	}
	if len(ws.tabsOf(first)) == 0 || len(ws.tabsOf(second)) == 0 {
		t.Error("closing the last open project should have been refused, but a repo's tabs are gone")
	}
}

// TestGroupingPersistsAcrossARestart checks the plan's persistence design:
// groups.json plus the ordinary session.json reconstruct a grouping as its
// members are reopened, without either file changing shape.
func TestGroupingPersistsAcrossARestart(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	if _, err := ws.NewGroupFrom([]string{first, second}, "platform"); err != nil {
		t.Fatalf("group: %v", err)
	}
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	again := newTestWorkspace(t, first)
	if n := again.RestoreSession(); n == 0 {
		t.Fatal("the second project was not reopened")
	}

	projects := again.Projects()
	if len(projects) != 1 {
		t.Fatalf("projects after restart = %d, want the grouping to survive as 1", len(projects))
	}
	if projects[0].Name != "platform" {
		t.Errorf("name = %q, want platform", projects[0].Name)
	}
}

// TestPaneContextSiblingsSpanTheGroup checks the closing of the plan's own
// loop: an agent in one member of a multi-repo project is told about an
// agent in another member, named by its own repo, rather than left invisible
// -- and that OtherProjects names the group once, not once per repo.
func TestPaneContextSiblingsSpanTheGroup(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	ws.SelectProject(first)
	own := ws.CurrentTab().Focus

	ws.SplitPaneInProject(layout.Horizontal, session.KindShell, second)
	if _, err := ws.NewGroupFrom([]string{first, second}, "platform"); err != nil {
		t.Fatalf("group: %v", err)
	}

	third := t.TempDir()
	if err := ws.OpenProject(third); err != nil {
		t.Fatalf("open third: %v", err)
	}
	ws.SelectProject(first)

	c, ok := ws.PaneContext(own)
	if !ok {
		t.Fatal("no context")
	}
	var crossRepo *Sibling
	for i := range c.Siblings {
		if c.Siblings[i].Repo != "" {
			crossRepo = &c.Siblings[i]
		}
	}
	if crossRepo == nil {
		t.Fatalf("no sibling named which repo it is in, among %+v", c.Siblings)
	}
	if len(c.OtherProjects) != 1 {
		t.Fatalf("other projects = %v, want the third project named once", c.OtherProjects)
	}

	text := c.Render()
	if !strings.Contains(text, "spans more than one repo") {
		t.Errorf("rendered context does not say the project spans more than one repo:\n%s", text)
	}
}

// TestRenameGroupClearsBackToTheDerivedName checks the same shape as
// clearing a tab's name: an empty name goes back to the derived one.
func TestRenameGroupClearsBackToTheDerivedName(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	if err := ws.RenameGroup(root, "  my project  "); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := ws.Projects()[0].Name; got != "my project" {
		t.Errorf("name = %q, want the trimmed name", got)
	}

	if err := ws.RenameGroup(root, ""); err != nil {
		t.Fatalf("clear name: %v", err)
	}
	if got, want := ws.Projects()[0].Name, pathTail(root, 1); got != want {
		t.Errorf("name after clearing = %q, want the derived %q", got, want)
	}
}

// TestGroupsPersistImmediately checks that a grouping change is written to
// disk as it happens, the same immediacy the store package's own
// project-display settings have, rather than waiting for the ordinary
// session save on the way out.
func TestGroupsPersistImmediately(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	if _, err := ws.NewGroupFrom([]string{first, second}, "platform"); err != nil {
		t.Fatalf("group: %v", err)
	}

	saved, err := store.LoadGroups()
	if err != nil {
		t.Fatalf("load groups: %v", err)
	}
	if len(saved) != 1 || saved[0].Name != "platform" {
		t.Fatalf("saved groups = %+v, want one named platform", saved)
	}
}
