package workspace

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// benchWorkspace builds workspace state by hand, with no sessions behind the
// panes. It is only for measuring the walks over tabs and panes; anything that
// needs a live process builds a workspace the ordinary way.
func benchWorkspace(projects, tabsPer, panesPer int) *Workspace {
	w := &Workspace{panes: map[string]*Pane{}, BroadcastSet: map[string]bool{}}
	for i := 0; i < projects; i++ {
		root := filepath.Join(string(filepath.Separator)+"projects", fmt.Sprintf("p%d", i))
		w.openRoots = append(w.openRoots, root)
		for j := 0; j < tabsPer; j++ {
			var tree *layout.Node
			var first string
			for k := 0; k < panesPer; k++ {
				p := &Pane{ID: uuid.NewString(), Kind: session.KindClaude, Cwd: root, Root: root}
				w.panes[p.ID] = p
				if tree == nil {
					tree, first = layout.NewLeaf(p.ID), p.ID
					continue
				}
				tree.Split(first, p.ID, layout.Horizontal)
			}
			w.Tabs = append(w.Tabs, &Tab{
				ID: uuid.NewString(), Root: root, Title: fmt.Sprintf("t%d", j), Tree: tree, Focus: first,
			})
		}
	}
	w.activeRoot = w.openRoots[0]
	w.activeTab = w.Tabs[0].ID
	return w
}

// BenchmarkProjects measures the summary the project switcher is redrawn from,
// which runs on every change in every pane.
func BenchmarkProjects(b *testing.B) {
	w := benchWorkspace(6, 8, 4)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = w.Projects()
	}
}

// TestProjectCountsFollowTheAgentsProject covers the switcher badge for a pane
// borrowed by another project's tab: the project whose work has stopped is the
// one that must be shown as waiting, not the project that lent the tab.
func TestProjectCountsFollowTheAgentsProject(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	ws.SplitPaneInProject(layout.Horizontal, session.KindShell, second)

	borrowed := borrowedPane(t, ws, ws.CurrentTab())
	if borrowed == nil {
		t.Fatal("no pane of the second project on the first project's tab")
	}
	if borrowed.Sess == nil {
		t.Fatal("the borrowed pane never started")
	}
	borrowed.Sess.SetStatus(session.StatusWaiting, "")

	for _, p := range ws.Projects() {
		switch {
		case sameDir(p.Root, second):
			if p.Waiting != 1 {
				t.Errorf("the project the waiting agent belongs to reports %d waiting, want 1", p.Waiting)
			}
		case sameDir(p.Root, first):
			if p.Waiting != 0 {
				t.Errorf("the project that only lent a tab reports %d waiting, want 0", p.Waiting)
			}
		}
	}
}

// TestNewTabTakesTheProjectOfItsDirectory covers opening a checkout that is
// itself an open project in a new tab: the tab is drawn on the active
// project's tab bar, but the agent is working in the other project and has to
// belong to it — as it already would have done had it been split in rather
// than opened in a tab.
func TestNewTabTakesTheProjectOfItsDirectory(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)

	ws.NewTab(session.KindShell, second, "over there")
	tab := ws.CurrentTab()
	if tab.Root != first {
		t.Errorf("tab root = %q, want the active project %q", tab.Root, first)
	}
	p := ws.Pane(tab.Focus)
	if p == nil {
		t.Fatal("the new tab has no pane")
	}
	if !sameDir(p.Root, second) {
		t.Errorf("pane project = %q, want the project its directory is in %q", p.Root, second)
	}

	// Which is what makes closing that project stop the agent: an agent left
	// running for a project that is no longer open belongs to nothing.
	ws.CloseProject(second)
	if ws.Pane(p.ID) != nil {
		t.Error("the agent is still running after its project was closed")
	}
}

// TestProjectNamesGrowUntilTheyDiffer covers the switcher's labels for trees
// laid out the same way — a mirror, a backup, the same worktree layout under
// two parents. Borrowing a single parent element leaves those as identical as
// they started, and an identical label is no label at all.
func TestProjectNamesGrowUntilTheyDiffer(t *testing.T) {
	sep := string(filepath.Separator)
	join := func(parts ...string) string { return filepath.Join(append([]string{sep}, parts...)...) }

	cases := []struct {
		name  string
		roots []string
		want  []string
	}{{
		name:  "names that stand on their own are left alone",
		roots: []string{join("work", "api"), join("work", "web")},
		want:  []string{"api", "web"},
	}, {
		name:  "one parent is enough to tell two checkouts apart",
		roots: []string{join("a", "service"), join("b", "service")},
		want:  []string{filepath.Join("a", "service"), filepath.Join("b", "service")},
	}, {
		name:  "mirrored trees agree until the element that differs",
		roots: []string{join("disk1", "src", "service"), join("disk2", "src", "service")},
		want: []string{
			filepath.Join("disk1", "src", "service"),
			filepath.Join("disk2", "src", "service"),
		},
	}, {
		name:  "a grown name that collides with a shorter one grows again",
		roots: []string{join("a", "service"), join("b", "a", "service")},
		want:  []string{filepath.Join("a", "service"), filepath.Join("b", "a", "service")},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := projectNames(tc.roots)
			if len(got) != len(tc.want) {
				t.Fatalf("names = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("name of %s = %q, want %q", tc.roots[i], got[i], tc.want[i])
				}
			}
		})
	}
}

// TestProjectNameOfARootDirectory keeps the top of a path from naming a
// project after a separator, which is what Base of "C:\\" gives. With no
// element to borrow, a root is named by the whole of itself: "C:\\" on
// Windows and "/" elsewhere. On Windows a lone separator can only have come
// from Base; on Linux the root is one, so the name is checked against the
// root rather than against the separator.
func TestProjectNameOfARootDirectory(t *testing.T) {
	root := filepath.VolumeName(mustAbs(t)) + string(filepath.Separator)
	names := projectNames([]string{root})
	if len(names) != 1 || names[0] != filepath.Clean(root) {
		t.Errorf("name of %q = %q, want the root itself", root, names)
	}
}

// TestProjectNamesTellDrivesApart covers the mirror on another drive, which
// agrees with the original on every element it has. Both were called
// code\api, since the names stopped growing at the top of each path without
// the drive that is the whole of the difference.
func TestProjectNamesTellDrivesApart(t *testing.T) {
	if filepath.VolumeName(`C:\`) == "" {
		t.Skip("paths here have no drive")
	}
	roots := []string{`C:\code\api`, `D:\code\api`}
	names := projectNames(roots)
	if names[0] == names[1] {
		t.Errorf("both projects are called %q", names[0])
	}
	for i, want := range roots {
		if names[i] != want {
			t.Errorf("name of %s = %q, want %q", roots[i], names[i], want)
		}
	}
}

func mustAbs(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return abs
}

// checkWorkspace asserts what every reader of the workspace assumes: a tab
// holds at least one pane, its focus is one of them, no pane is drawn twice,
// and the focused tab exists. hist names the moves that got here, since a
// randomised failure is unreadable without it.
func checkWorkspace(t *testing.T, w *Workspace, roots map[string]string, hist []string) {
	t.Helper()
	fail := func(format string, args ...any) {
		t.Helper()
		t.Log("after these moves:")
		for _, h := range hist {
			t.Log("  " + h)
		}
		t.Fatalf(format, args...)
	}
	at := map[string]string{}
	for _, tab := range w.Tabs {
		panes := tab.Tree.Panes()
		if len(panes) == 0 {
			fail("tab %s is empty", tab.Title)
		}
		if tab.Tree.Find(tab.Focus) == nil {
			fail("tab %s is focused on %q, which is not in it", tab.Title, tab.Focus)
		}
		for _, id := range panes {
			if other, ok := at[id]; ok {
				fail("pane %s is drawn in both %s and %s", id, other, tab.Title)
			}
			at[id] = tab.Title
			if w.panes[id] == nil {
				fail("tab %s draws pane %s, which the workspace does not know", tab.Title, id)
			}
		}
	}
	for id, p := range w.panes {
		if _, ok := at[id]; !ok {
			fail("pane %s is running but is on no tab", id)
		}
		// Rearranging is about where a pane is drawn. Which project an agent
		// is working in is not something dragging it somewhere may decide.
		if want, ok := roots[id]; ok && p.Root != want {
			fail("pane %s now belongs to project %s, but was started in %s", id, p.Root, want)
		}
	}
	if w.activeTab != "" {
		focused := w.Tab(w.activeTab)
		if focused == nil {
			fail("the focused tab %q is not open", w.activeTab)
		} else if focused.Root != w.activeRoot {
			fail("the focused tab belongs to %s, but %s is the project on screen",
				focused.Root, w.activeRoot)
		}
	} else if len(w.tabsOf(w.activeRoot)) > 0 {
		fail("no tab is focused, but the project on screen has %d", len(w.tabsOf(w.activeRoot)))
	}
}

// TestMovesKeepTheWorkspaceCoherent rearranges a workspace at random and
// checks after every step that it is still one the interface could draw.
func TestMovesKeepTheWorkspaceCoherent(t *testing.T) {
	isolateConfig(t)
	edges := []layout.Edge{layout.EdgeLeft, layout.EdgeRight, layout.EdgeTop, layout.EdgeBottom}
	dirs := []layout.Dir{layout.Horizontal, layout.Vertical}
	towards := []layout.Direction{layout.Left, layout.Right, layout.Up, layout.Down}

	for seed := int64(0); seed < 200; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		w := benchWorkspace(2, 3, 3)
		w.settingsDir = t.TempDir()
		roots := map[string]string{}
		for id, p := range w.panes {
			// Some of them belong to the other project, so the moves are
			// shuffling tabs that hold each other's agents rather than tabs
			// that each hold their own.
			if rnd.Intn(4) == 0 {
				p.Root = w.openRoots[rnd.Intn(len(w.openRoots))]
			}
			roots[id] = p.Root
		}
		var hist []string

		for step := 0; step < 120 && len(w.Tabs) > 0; step++ {
			all := []string{}
			for _, tab := range w.Tabs {
				all = append(all, tab.Tree.Panes()...)
			}
			pane := func() string { return all[rnd.Intn(len(all))] }
			tab := func() *Tab { return w.Tabs[rnd.Intn(len(w.Tabs))] }
			note := func(format string, args ...any) { hist = append(hist, fmt.Sprintf(format, args...)) }

			switch rnd.Intn(13) {
			case 0:
				a, b, e := pane(), pane(), edges[rnd.Intn(len(edges))]
				note("MovePane(%s, %s, %v)", a, b, e)
				_ = w.MovePane(a, b, e)
			case 1:
				a, b := pane(), pane()
				note("SwapPanes(%s, %s)", a, b)
				_ = w.SwapPanes(a, b)
			case 2:
				a, dest := pane(), tab()
				note("MovePaneToTab(%s, %s)", a, dest.Title)
				_ = w.MovePaneToTab(a, dest.ID)
			case 3:
				a := pane()
				note("MovePaneToNewTab(%s)", a)
				_ = w.MovePaneToNewTab(a)
			case 4:
				src, dest, d := tab(), tab(), dirs[rnd.Intn(len(dirs))]
				note("MergeTab(%s, %s, %v)", src.Title, dest.Title, d)
				_ = w.MergeTab(src.ID, dest.ID, d)
			case 5:
				dest, d := tab(), dirs[rnd.Intn(len(dirs))]
				note("MergeTabsInProject(%s, %v)", dest.Title, d)
				_ = w.MergeTabsInProject(dest.ID, d)
			case 6:
				moving, before := tab(), tab()
				note("MoveTab(%s, %s)", moving.Title, before.Title)
				_ = w.MoveTab(moving.ID, before.ID)
			case 7:
				dest := tab()
				note("SelectTab(%s)", dest.Title)
				w.SelectTab(dest.ID)
			case 8:
				d := towards[rnd.Intn(len(towards))]
				note("MovePaneDir(%v)", d)
				_ = w.MovePaneDir(d)
			case 9:
				note("ClosePane() in %s", w.activeTab)
				w.ClosePane()
			case 10:
				dead := tab()
				note("CloseTab(%s)", dead.Title)
				w.CloseTab(dead.ID)
			case 11:
				note("ToggleZoom() in %s", w.activeTab)
				w.ToggleZoom()
			case 12:
				if len(w.openRoots) < 2 {
					break
				}
				closing := w.openRoots[rnd.Intn(len(w.openRoots))]
				note("CloseProject(%s)", closing)
				w.CloseProject(closing)
				for id := range roots {
					if _, running := w.panes[id]; !running {
						delete(roots, id)
					}
				}
			}
			checkWorkspace(t, w, roots, hist)
		}
	}
}

// TestTheDefaultBroadcastSetFollowsTheTabOnScreen covers leaving broadcast on
// and moving to another tab. The default means "every agent in front of me",
// so it has to mean the tab in front of you now; pinned to the tab it was
// switched on in, typing reached panes that are not on screen and nothing
// that is.
func TestTheDefaultBroadcastSetFollowsTheTabOnScreen(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	first := ws.NewTab(session.KindClaude, root, "first")
	ws.ToggleBroadcast()
	if !ws.InBroadcast(first.Focus) {
		t.Fatal("turning broadcast on should cover the agents in the tab")
	}

	second := ws.NewTab(session.KindClaude, root, "second")
	if !ws.InBroadcast(second.Focus) {
		t.Error("the tab now on screen is not being broadcast to")
	}
	if ws.InBroadcast(first.Focus) {
		t.Error("a pane on another tab is still a broadcast target")
	}

	ws.SelectTab(first.ID)
	if !ws.InBroadcast(first.Focus) {
		t.Error("going back should broadcast to the tab that is back on screen")
	}
}

// TestRemovingOnePaneFromTheDefaultKeepsTheRest covers turning the default
// into a selection: it starts as what the default covered, so deselecting one
// agent leaves the others selected rather than clearing everything.
func TestRemovingOnePaneFromTheDefaultKeepsTheRest(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	tab := ws.NewTab(session.KindClaude, root, "pair")
	ws.SplitPane(layout.Horizontal, session.KindClaude)

	ws.ToggleBroadcast()
	dropped := tab.Focus
	ws.ToggleBroadcastMember()
	if ws.InBroadcast(dropped) {
		t.Error("the pane that was just deselected is still a target")
	}
	others := 0
	for _, id := range tab.Tree.Panes() {
		if id != dropped && ws.InBroadcast(id) {
			others++
		}
	}
	if others != 1 {
		t.Errorf("%d other panes are still selected, want the one that was not deselected", others)
	}
}

// TestClosingAProjectSpareTheOtherProjectsAgents covers closing the project
// whose tab was only lending the space: the agent shown there belongs to a
// project that is still open, and closing a window onto an agent is not the
// same as ending it.
func TestClosingAProjectSparesTheOtherProjectsAgents(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	ws.SplitPaneInProject(layout.Horizontal, session.KindShell, second)

	borrowed := borrowedPane(t, ws, ws.CurrentTab())
	if borrowed == nil {
		t.Fatal("no pane of the second project on the first project's tab")
	}
	host := ""
	for _, id := range ws.CurrentTab().Tree.Panes() {
		if id != borrowed.ID {
			host = id
		}
	}
	if host == "" {
		t.Fatal("the tab has no pane of its own project")
	}

	ws.CloseProject(first)

	if ws.Pane(borrowed.ID) == nil {
		t.Fatal("the agent was stopped along with a project it does not belong to")
	}
	if !borrowed.Alive() {
		t.Error("the rescued agent's process was killed")
	}
	tab := ws.tabOf(borrowed.ID)
	if tab == nil {
		t.Fatal("the rescued agent is on no tab")
	}
	if !sameDir(tab.Root, second) {
		t.Errorf("the rescued agent's tab belongs to %q, want its own project %q", tab.Root, second)
	}
	// The closed project's own agent is gone, and so is every tab of it.
	if ws.Pane(host) != nil {
		t.Error("the closed project's own agent is still running")
	}
	for _, other := range ws.Tabs {
		if sameDir(other.Root, first) {
			t.Errorf("tab %q of the closed project is still open", other.Title)
		}
	}
}

// TestComingBackToAProjectReturnsToItsLastTab covers switching away to look at
// another project and back: with ten tabs open, being dropped on the first one
// means finding your place again every time.
func TestComingBackToAProjectReturnsToItsLastTab(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	working := ws.NewTab(session.KindShell, first, "the one you were on")
	if ws.ActiveTabID() != working.ID {
		t.Fatalf("a new tab should be focused; active = %q", ws.ActiveTabID())
	}

	ws.SelectProject(second)
	ws.SelectProject(first)
	if ws.ActiveTabID() != working.ID {
		t.Errorf("came back to tab %q, want the one the project was left on", ws.CurrentTab().Title)
	}

	// A remembered tab that has since been closed falls back to what is there.
	ws.SelectProject(second)
	ws.CloseTab(working.ID)
	ws.SelectProject(first)
	tab := ws.CurrentTab()
	if tab == nil || tab.Root != first {
		t.Fatalf("no tab of the project is focused: %#v", tab)
	}
}

// TestClosingAProjectLeavesTheFocusBesideTheGap covers a tab that loses a pane
// from under it: the agent you were typing into disappears, and the focus
// should land next to where it was rather than at the front of the tab.
func TestClosingAProjectLeavesTheFocusBesideTheGap(t *testing.T) {
	isolateConfig(t)
	ws, _, second := twoProjects(t)

	// Three panes in a row on the first project's tab, the middle one an agent
	// of the second project.
	left := ws.CurrentTab().Focus
	ws.SplitPane(layout.Horizontal, session.KindShell)
	right := ws.CurrentTab().Focus
	ws.FocusPane(left)
	ws.SplitPaneInProject(layout.Horizontal, session.KindShell, second)
	borrowed := ws.CurrentTab().Focus

	tab := ws.CurrentTab()
	panes := tab.Tree.Panes()
	if len(panes) != 3 || panes[0] != left || panes[1] != borrowed || panes[2] != right {
		t.Fatalf("panes = %v, want [%v %v %v]", panes, left, borrowed, right)
	}
	tab.Zoom = true

	ws.CloseProject(second)

	if tab.Focus != right {
		t.Errorf("focus landed on %q, want %q, the pane beside the one that went away",
			tab.Focus, right)
	}
	if tab.Zoom {
		t.Error("the tab is still zoomed, on a pane the user did not choose")
	}
}

// TestAPaneKnowsItsProjectWhateverTheSpellingSaved covers a pane restored from
// a layout written under another spelling of the same directory, which is what
// a project opened as "c:epo" once and "C:\Repo" the next time leaves
// behind. Everything that groups tabs and panes by project compares those
// strings, so a pane naming its project in the wrong case belongs to nothing.
func TestAPaneKnowsItsProjectWhateverTheSpellingSaved(t *testing.T) {
	isolateConfig(t)
	// A layout saved under another spelling is only the same project where
	// the filesystem ignores case, which is asked for here on every platform.
	was := foldsCase
	foldsCase = true
	t.Cleanup(func() { foldsCase = was })
	ws, first, second := twoProjects(t)
	ws.SplitPaneInProject(layout.Horizontal, session.KindShell, second)

	borrowed := borrowedPane(t, ws, ws.CurrentTab())
	if borrowed == nil {
		t.Fatal("no pane of the second project on the first project's tab")
	}
	// As a layout saved under a differently cased path would restore it.
	borrowed.Root = strings.ToUpper(second)

	if got := ws.RootOf(borrowed.ID); got != second {
		t.Errorf("project of the pane = %q, want it under the spelling it is open with, %q", got, second)
	}
	c, ok := ws.PaneContext(borrowed.ID)
	if !ok || c.ProjectRoot != second {
		t.Errorf("context project root = %q, want %q", c.ProjectRoot, second)
	}

	// And it is still that project's agent when the tab lending it space goes.
	ws.CloseProject(first)
	tab := ws.tabOf(borrowed.ID)
	if tab == nil {
		t.Fatal("the agent was stopped or left on no tab")
	}
	if tab.Root != second {
		t.Errorf("its tab belongs to %q, want %q, the project as it is open", tab.Root, second)
	}
	shown := false
	for _, visible := range ws.VisibleTabs() {
		if visible == tab {
			shown = true
		}
	}
	if !shown {
		t.Error("the tab holding it is not among the ones the window would draw")
	}
}

// TestClosingProjectsAtRandomKeepsEveryAgentAccountedFor drives the other half
// of the model: projects closing under a workspace whose tabs hold each
// other's agents. Every agent has to end up either stopped with its own
// project or running on a tab of a project that is still open — never running
// where nothing draws it, and never stopped because somebody else's project
// went away.
func TestClosingProjectsAtRandomKeepsEveryAgentAccountedFor(t *testing.T) {
	isolateConfig(t)
	for seed := int64(0); seed < 40; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		w := benchWorkspace(4, 2, 3)
		w.settingsDir = t.TempDir()
		roots := map[string]string{}
		for id, p := range w.panes {
			roots[id] = p.Root
		}
		// Lend some agents to other projects' tabs, which is what makes
		// closing one of them interesting.
		for _, tab := range w.Tabs {
			for _, id := range tab.Tree.Panes() {
				if rnd.Intn(3) == 0 {
					lender := w.openRoots[rnd.Intn(len(w.openRoots))]
					w.panes[id].Root = lender
					roots[id] = lender
				}
			}
		}
		var hist []string

		for len(w.openRoots) > 1 {
			closing := w.openRoots[rnd.Intn(len(w.openRoots))]
			hist = append(hist, "CloseProject("+closing+")")
			w.CloseProject(closing)

			if w.isOpen(closing) {
				t.Fatalf("%s is still open after being closed", closing)
			}
			for id, home := range roots {
				_, running := w.panes[id]
				switch {
				case sameDir(home, closing) && running:
					t.Fatalf("an agent of the closed project %s is still running", closing)
				case !sameDir(home, closing) && !running:
					t.Fatalf("an agent of %s was stopped when %s closed", home, closing)
				}
			}
			for _, tab := range w.Tabs {
				if !w.isOpen(tab.Root) {
					t.Fatalf("tab %q belongs to %s, which is not open", tab.Title, tab.Root)
				}
			}
			for id := range roots {
				if _, running := w.panes[id]; !running {
					delete(roots, id)
				}
			}
			checkWorkspace(t, w, roots, hist)
		}
	}
}

// TestClosingAProjectDropsATabLeftHoldingNothing names the case the random
// closes found: a tab whose panes are all borrowed. A layout tree refuses to
// give up its last pane, so removing them one at a time left the tab drawing
// an agent that had already been stopped — a pane with nothing behind it that
// only closing the tab would get rid of.
func TestClosingAProjectDropsATabLeftHoldingNothing(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	ws.SplitPaneInProject(layout.Horizontal, session.KindShell, second)

	tab := ws.CurrentTab()
	borrowed := borrowedPane(t, ws, tab)
	if borrowed == nil {
		t.Fatal("no pane of the second project on the first project's tab")
	}
	// Close the tab's own agent, leaving it showing nothing but the borrowed
	// one. This is a tab of the first project holding only the second's work.
	for _, id := range tab.Tree.Panes() {
		if id != borrowed.ID {
			ws.FocusPane(id)
			ws.ClosePane()
		}
	}
	if panes := tab.Tree.Panes(); len(panes) != 1 || panes[0] != borrowed.ID {
		t.Fatalf("tab holds %v, want just the borrowed pane", panes)
	}

	ws.CloseProject(second)

	if ws.Pane(borrowed.ID) != nil {
		t.Error("the closed project's agent is still running")
	}
	for _, open := range ws.Tabs {
		for _, id := range open.Tree.Panes() {
			if id == borrowed.ID {
				t.Errorf("tab %q still draws the stopped agent", open.Title)
			}
			if ws.Pane(id) == nil {
				t.Errorf("tab %q draws pane %s, which no longer exists", open.Title, id)
			}
		}
	}
	if sameDir(first, second) {
		t.Fatal("the projects should be different directories")
	}
}

// TestClaudeArgvIsUnchanged is the promise that somebody who only ever runs
// Claude notices nothing: the argv a Claude pane is started with now comes out
// of its Spec rather than out of session.ClaudeArgs, and the two must agree
// exactly. A stray or missing argument here is a pane that dies at launch, or
// a conversation that starts fresh where it should have resumed.
func TestClaudeArgvIsUnchanged(t *testing.T) {
	var w Workspace
	spec, ok := w.specFor("", "")
	if !ok {
		t.Fatal("the default agent has no spec")
	}

	const (
		id       = "11111111-2222-4333-8444-555555555555"
		settings = "/state/sessions/11111111.settings.json"
	)
	tests := []struct {
		name   string
		resume bool
		prompt string
	}{
		{name: "a fresh pane with no task"},
		{name: "a fresh pane with a task", prompt: "fix the parser"},
		{name: "a task that begins with a dash", prompt: "-p is not what I meant"},
		{name: "a pane resuming its conversation", resume: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var extra []string
			if tc.prompt != "" {
				extra = []string{tc.prompt}
			}
			want := session.ClaudeArgs(id, settings, tc.resume, extra)
			got := agent.BuildArgv(spec, tc.resume, agent.Tokens{
				Session:  id,
				Settings: settings,
				Prompt:   tc.prompt,
			})
			if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
				t.Errorf("argv = %q, want %q", got, want)
			}
		})
	}
}

// TestClaudeArgvNamesTheModelOnlyWhenOneIsChosen covers the one argument the
// Spec adds. An empty model is not a default to be filled in: it means
// whatever the CLI is already set to, and passing --model with nothing after
// it would swallow the next argument.
func TestClaudeArgvNamesTheModelOnlyWhenOneIsChosen(t *testing.T) {
	var w Workspace
	spec, _ := w.specFor("", "claude")

	tests := []struct {
		name  string
		model string
		want  bool
	}{
		{name: "no model chosen"},
		{name: "a model chosen", model: "opus", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			argv := agent.BuildArgv(spec, false, agent.Tokens{Session: "s", Model: tc.model})
			var named bool
			for i, a := range argv {
				if a == "--model" {
					named = true
					if i+1 >= len(argv) || argv[i+1] != tc.model {
						t.Fatalf("--model is not followed by %q in %q", tc.model, argv)
					}
				}
			}
			if named != tc.want {
				t.Errorf("--model present = %v, want %v (argv %q)", named, tc.want, argv)
			}
		})
	}
}

// TestUnknownAgentFailsThePaneRatherThanTheWindow checks a pane whose agent is
// not in the catalog — one named in a layout written on a machine that had it,
// or in a hand-edited agents.json — reports itself in place. Everything else
// in the window has to carry on around it.
func TestUnknownAgentFailsThePaneRatherThanTheWindow(t *testing.T) {
	var w Workspace
	if _, ok := w.specFor("", "no-such-agent"); ok {
		t.Fatal("an agent that does not exist resolved to a spec")
	}
}

// TestNewPaneRecordsTheChoice covers what a new pane remembers of the agent
// picker. A shell runs no agent, so one handed to it is dropped rather than
// written to the layout and read back as a pane that is somehow both.
func TestNewPaneRecordsTheChoice(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()

	tests := []struct {
		name        string
		choice      Choice
		wantAgent   string
		wantModel   string
		wantIsAgent bool
	}{
		{
			name:   "a shell keeps neither",
			choice: Choice{Kind: session.KindShell, Agent: "claude", Model: "opus"},
		},
		{
			name:        "an agent keeps both",
			choice:      Choice{Kind: session.KindClaude, Agent: "codex", Model: "gpt-5"},
			wantAgent:   "codex",
			wantModel:   "gpt-5",
			wantIsAgent: true,
		},
		{
			// Recorded as what it resolved to, so that the header can say what
			// it runs and a default changed later cannot move its conversation.
			name:        "an agent chosen by neither name records the defaults it runs",
			choice:      Choice{Kind: session.KindClaude},
			wantAgent:   agent.DefaultAgentID,
			wantIsAgent: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := newTestWorkspace(t, root)
			p := ws.newPane(tc.choice, root, "", "")
			if p.IsAgent() != tc.wantIsAgent {
				t.Errorf("IsAgent = %v, want %v", p.IsAgent(), tc.wantIsAgent)
			}
			if p.Agent != tc.wantAgent {
				t.Errorf("agent = %q, want %q", p.Agent, tc.wantAgent)
			}
			if p.Model != tc.wantModel {
				t.Errorf("model = %q, want %q", p.Model, tc.wantModel)
			}
		})
	}
}

// TestNoTranscriptMeansNoResume covers the gate in front of resuming. An agent
// that records nothing has nothing to reattach to, and asking it to resume is
// how a pane dies on restart: `claude --resume` with no transcript prints "No
// conversation found" and exits, and a restored layout would lose every pane
// at once.
func TestNoTranscriptMeansNoResume(t *testing.T) {
	var w Workspace
	claude, _ := w.specFor("", "claude")

	tests := []struct {
		name string
		spec agent.Spec
		want bool
	}{
		{
			name: "an agent that records nothing",
			spec: agent.Spec{ID: "codex", Caps: agent.Caps{Resume: true}},
		},
		{
			// There is no conversation under this id, because the id is not
			// one anything has ever been started with.
			name: "an agent that records, with nothing recorded",
			spec: claude,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.transcriptExists(tc.spec, uuid.NewString()); got != tc.want {
				t.Errorf("transcriptExists = %v, want %v", got, tc.want)
			}
		})
	}
}

// -agent names the agent for a run, and a pane that records none of its own
// has to take it. Without this the flag is parsed, checked against the catalog
// and then quietly ignored, which is worse than refusing it.
func TestUseAgentOutranksTheCatalogDefault(t *testing.T) {
	isolateConfig(t)
	w := newTestWorkspace(t, t.TempDir())

	_, def := w.Agents()
	if def != "claude" {
		t.Fatalf("default agent = %q, want the catalog's own to begin with", def)
	}

	w.UseAgent("openai")
	if _, def := w.Agents(); def != "openai" {
		t.Errorf("default agent = %q, want the one -agent named", def)
	}
	spec, ok := w.specFor("", "")
	if !ok || spec.ID != "openai" {
		t.Errorf("a pane with no agent of its own resolved to %q (found %v), want openai", spec.ID, ok)
	}

	// A pane that names its own agent still wins: a restored layout knows what
	// it was, and a run-wide default must not rewrite it.
	if spec, ok := w.specFor("", "claude"); !ok || spec.ID != "claude" {
		t.Errorf("a pane naming claude resolved to %q (found %v)", spec.ID, ok)
	}
}

// An agent with no lifecycle hooks is briefed through its opening prompt or
// not at all, so the briefing has to reach the argv the pane is started with.
func TestAnAgentWithoutHooksIsBriefedInItsPrompt(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	w := newTestWorkspace(t, root)
	w.NewTab(session.KindShell, root, "first")

	p := &Pane{ID: uuid.NewString(), Cwd: root, Name: "api", Root: root, Task: "fix the parser"}
	w.mu.Lock()
	w.panes[p.ID] = p
	w.mu.Unlock()

	hooked := w.OpeningPrompt(p.ID, "fix the parser", agent.ContextHook)
	if hooked != "fix the parser" {
		t.Errorf("an agent with hooks got %q, want the task untouched", hooked)
	}

	prompted := w.OpeningPrompt(p.ID, "fix the parser", agent.ContextPrompt)
	if !strings.Contains(prompted, "fix the parser") {
		t.Errorf("the task did not survive the briefing: %q", prompted)
	}
	if !strings.Contains(prompted, "flockdeck-context") {
		t.Errorf("an agent with no hooks was given no briefing: %q", prompted)
	}
	if len(prompted) <= len("fix the parser") {
		t.Errorf("the briefing added nothing: %q", prompted)
	}
}

// Where an agent keeps what it said is its own arrangement. Asking Claude
// Code's store about an API pane answers no every time, so such a pane would be
// started fresh on top of its own conversation at every restart.
func TestResumeAsksTheAgentsOwnReader(t *testing.T) {
	isolateConfig(t)
	w := newTestWorkspace(t, t.TempDir())

	api := agent.Spec{
		ID:     "openai",
		Runner: agent.RunnerAPI,
		Caps:   agent.Caps{Transcript: true, Resume: true},
	}
	id := uuid.NewString()
	if w.transcriptExists(api, id) {
		t.Error("an API pane with nothing recorded was offered a resume")
	}

	// The reader for an API agent is the chat client's own, so its answer has
	// to change when that file appears — not when Claude's store does. The
	// path is built here rather than asked for, because the reader reports
	// nothing until the file is there.
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	path := filepath.Join(dir, "chats", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make the transcript directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"user","text":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write the transcript: %v", err)
	}
	if !w.transcriptExists(api, id) {
		t.Error("an API pane with a conversation recorded was not offered a resume")
	}
}
