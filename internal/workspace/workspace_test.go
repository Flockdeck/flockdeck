package workspace

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/jmwri/perch/internal/layout"
	"github.com/jmwri/perch/internal/session"
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
// project after a separator, which is what Base of "C:\\" gives.
func TestProjectNameOfARootDirectory(t *testing.T) {
	root := filepath.VolumeName(mustAbs(t)) + string(filepath.Separator)
	names := projectNames([]string{root})
	if len(names) != 1 || names[0] == "" || names[0] == string(filepath.Separator) {
		t.Errorf("name of %q = %q, want something that names a directory", root, names)
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
	edges := []layout.Edge{layout.EdgeLeft, layout.EdgeRight, layout.EdgeTop, layout.EdgeBottom}
	dirs := []layout.Dir{layout.Horizontal, layout.Vertical}
	towards := []layout.Direction{layout.Left, layout.Right, layout.Up, layout.Down}

	for seed := int64(0); seed < 200; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		w := benchWorkspace(2, 3, 3)
		roots := map[string]string{}
		for id, p := range w.panes {
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

			switch rnd.Intn(12) {
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
			}
			checkWorkspace(t, w, roots, hist)
		}
	}
}
