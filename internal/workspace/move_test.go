package workspace

import (
	"reflect"
	"testing"

	"github.com/jmwri/agent-wrapper/internal/layout"
	"github.com/jmwri/agent-wrapper/internal/session"
)

// panesOfTab is the pane order a tab is drawn in.
func panesOfTab(t *Tab) []string {
	if t == nil {
		return nil
	}
	return t.Tree.Panes()
}

// tabTitles lists the visible tabs in order, which is what a reorder changes.
func tabTitles(w *Workspace) []string {
	var out []string
	for _, t := range w.VisibleTabs() {
		out = append(out, t.Title)
	}
	return out
}

// TestMovePaneKeepsTheSession is the point of the whole feature: rearranging a
// pane must not restart the agent in it.
func TestMovePaneKeepsTheSession(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	ws.SplitPane(layout.Horizontal, session.KindShell)

	tab := ws.CurrentTab()
	panes := panesOfTab(tab)
	if len(panes) != 2 {
		t.Fatalf("expected two panes, got %v", panes)
	}
	a, b := panes[0], panes[1]
	sess := ws.Pane(a).Sess

	if err := ws.MovePane(a, b, layout.EdgeBottom); err != nil {
		t.Fatalf("move: %v", err)
	}
	if got := ws.Pane(a).Sess; got != sess {
		t.Error("the moved pane was given a different session")
	}
	if sess.Exited() {
		t.Error("the moved pane's process was killed")
	}
	if got := panesOfTab(tab); !reflect.DeepEqual(got, []string{b, a}) {
		t.Errorf("panes = %v, want %v", got, []string{b, a})
	}
	if tab.Focus != a {
		t.Error("focus should follow the pane that was moved")
	}
}

// TestMovePaneBetweenTabs covers dragging a pane out of one tab and into
// another, including the tab it leaves behind.
func TestMovePaneBetweenTabs(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	ws.SplitPane(layout.Horizontal, session.KindShell)
	first := ws.CurrentTab()
	moving := panesOfTab(first)[1]

	ws.NewTab(session.KindShell, root, "two")
	second := ws.CurrentTab()
	stay := panesOfTab(second)[0]

	if err := ws.MovePane(moving, stay, layout.EdgeRight); err != nil {
		t.Fatalf("move: %v", err)
	}
	if got := panesOfTab(second); !reflect.DeepEqual(got, []string{stay, moving}) {
		t.Errorf("destination panes = %v, want %v", got, []string{stay, moving})
	}
	if len(panesOfTab(first)) != 1 {
		t.Errorf("source tab panes = %v, want the one that stayed", panesOfTab(first))
	}
	if ws.ActiveTabID() != second.ID {
		t.Error("the destination tab should be the one on screen")
	}
	if ws.Pane(moving) == nil || !ws.Pane(moving).Alive() {
		t.Error("the moved pane should still be running")
	}
}

// TestMovingTheLastPaneOutClosesItsTab checks that emptying a tab by dragging
// its only pane away removes the tab without taking the pane with it — the way
// closing it would.
func TestMovingTheLastPaneOutClosesItsTab(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	keep := ws.CurrentTab()
	target := panesOfTab(keep)[0]

	ws.NewTab(session.KindShell, root, "two")
	lonely := panesOfTab(ws.CurrentTab())[0]

	if err := ws.MovePane(lonely, target, layout.EdgeRight); err != nil {
		t.Fatalf("move: %v", err)
	}
	if got := len(ws.VisibleTabs()); got != 1 {
		t.Errorf("tabs = %d, want the emptied one gone", got)
	}
	p := ws.Pane(lonely)
	if p == nil || !p.Alive() {
		t.Fatal("the pane must survive the tab it emptied")
	}
	if got := panesOfTab(keep); !reflect.DeepEqual(got, []string{target, lonely}) {
		t.Errorf("panes = %v, want both in the surviving tab", got)
	}
}

// TestMovePaneToNewTab covers pulling a pane out of a split into a tab of its
// own, and refusing to do it to a pane that already has one.
func TestMovePaneToNewTab(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	ws.SplitPane(layout.Horizontal, session.KindShell)
	moving := panesOfTab(ws.CurrentTab())[1]

	if err := ws.MovePaneToNewTab(moving); err != nil {
		t.Fatalf("move to new tab: %v", err)
	}
	if got := len(ws.VisibleTabs()); got != 2 {
		t.Fatalf("tabs = %d, want a new one for the pane", got)
	}
	fresh := ws.CurrentTab()
	if got := panesOfTab(fresh); !reflect.DeepEqual(got, []string{moving}) {
		t.Errorf("new tab panes = %v, want just the moved one", got)
	}
	if err := ws.MovePaneToNewTab(moving); err == nil {
		t.Error("a pane already alone in a tab should be refused")
	}
}

// TestMovePaneToTabDropsItBesideTheFocus covers dropping a pane onto a tab in
// the tab bar rather than onto another pane.
func TestMovePaneToTabDropsItBesideTheFocus(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	first := ws.CurrentTab()
	sitting := panesOfTab(first)[0]

	ws.NewTab(session.KindShell, root, "two")
	ws.SplitPane(layout.Horizontal, session.KindShell)
	moving := panesOfTab(ws.CurrentTab())[1]

	if err := ws.MovePaneToTab(moving, first.ID); err != nil {
		t.Fatalf("move to tab: %v", err)
	}
	if got := panesOfTab(first); !reflect.DeepEqual(got, []string{sitting, moving}) {
		t.Errorf("panes = %v, want the arrival beside the resident", got)
	}
	// Dropping a pane on the tab it is already in is a no-op, not a failure.
	if err := ws.MovePaneToTab(moving, first.ID); err != nil {
		t.Errorf("a drop on its own tab should be accepted: %v", err)
	}
	if got := len(panesOfTab(first)); got != 2 {
		t.Errorf("panes = %d, want it unchanged", got)
	}
}

// TestSwapPanesLeavesTheShapeAlone checks that a swap exchanges two agents'
// positions without disturbing the proportions the user has dragged out.
func TestSwapPanesLeavesTheShapeAlone(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	ws.SplitPane(layout.Horizontal, session.KindShell)
	tab := ws.CurrentTab()
	a, b := panesOfTab(tab)[0], panesOfTab(tab)[1]
	tab.Tree.Children[0].Weight = 3

	if err := ws.SwapPanes(a, b); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if got := panesOfTab(tab); !reflect.DeepEqual(got, []string{b, a}) {
		t.Errorf("panes = %v, want them exchanged", got)
	}
	if w := tab.Tree.Children[0].Weight; w != 3 {
		t.Errorf("first slot weight = %v, want the layout untouched", w)
	}
}

// TestMovePaneDirUsesWhatIsOnScreen checks the keyboard move: the tree carries
// proportions rather than pixels, so the direction has to be worked out from
// the geometry those proportions imply.
func TestMovePaneDirUsesWhatIsOnScreen(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	ws.SplitPane(layout.Horizontal, session.KindShell)
	tab := ws.CurrentTab()
	left, right := panesOfTab(tab)[0], panesOfTab(tab)[1]

	ws.FocusPane(right)
	if err := ws.MovePaneDir(layout.Left); err != nil {
		t.Fatalf("move left: %v", err)
	}
	if got := panesOfTab(tab); !reflect.DeepEqual(got, []string{right, left}) {
		t.Errorf("panes = %v, want them exchanged", got)
	}
	if tab.Focus != right {
		t.Error("focus should stay on the pane that moved")
	}
	// Nothing lies further left, so the move is refused rather than wrapping.
	if err := ws.MovePaneDir(layout.Left); err == nil {
		t.Error("moving past the edge should be refused")
	}
}

// TestMoveTabReorders covers dragging a tab along the tab bar.
func TestMoveTabReorders(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	ws.NewTab(session.KindShell, root, "two")
	ws.NewTab(session.KindShell, root, "three")

	tabs := ws.VisibleTabs()
	third := tabs[2]
	if err := ws.MoveTab(third.ID, tabs[0].ID); err != nil {
		t.Fatalf("move tab: %v", err)
	}
	if got := tabTitles(ws); !reflect.DeepEqual(got, []string{"three", "one", "two"}) {
		t.Fatalf("tabs = %v, want three first", got)
	}

	// An empty destination sends it to the end.
	if err := ws.MoveTab(third.ID, ""); err != nil {
		t.Fatalf("move tab to end: %v", err)
	}
	if got := tabTitles(ws); !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
		t.Fatalf("tabs = %v, want three last", got)
	}
}

// TestMoveTabStaysWithinItsProject keeps one project's tab bar from being
// reordered into another's.
func TestMoveTabStaysWithinItsProject(t *testing.T) {
	isolateConfig(t)
	first := t.TempDir()
	second := t.TempDir()
	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "mine")
	mine := ws.CurrentTab()

	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second project: %v", err)
	}
	ws.NewTab(session.KindShell, second, "theirs")
	theirs := ws.CurrentTab()

	if err := ws.MoveTab(mine.ID, theirs.ID); err == nil {
		t.Error("a tab should not be reorderable into another project")
	}
}

// TestMoveRejectsPanesThatAreGone guards the drop that lands after the thing
// it was aimed at has been closed.
func TestMoveRejectsPanesThatAreGone(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	only := panesOfTab(ws.CurrentTab())[0]

	if err := ws.MovePane(only, only, layout.EdgeRight); err == nil {
		t.Error("a pane dropped on itself should be refused")
	}
	if err := ws.MovePane(only, "gone", layout.EdgeRight); err == nil {
		t.Error("a drop on a pane that is not there should be refused")
	}
	if err := ws.MovePane("gone", only, layout.EdgeRight); err == nil {
		t.Error("moving a pane that is not there should be refused")
	}
	if err := ws.MovePaneToTab(only, "gone"); err == nil {
		t.Error("a drop on a tab that is not there should be refused")
	}
	if got := len(panesOfTab(ws.CurrentTab())); got != 1 {
		t.Errorf("panes = %d, want the refused moves to have changed nothing", got)
	}
}

// TestMergeTabCombinesBothLayouts is the point of merging: two tabs become one
// holding every pane of both, with each tab's own arrangement intact and every
// agent still running.
func TestMergeTabCombinesBothLayouts(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	ws.SplitPane(layout.Vertical, session.KindShell)
	dest := ws.CurrentTab()
	top, bottom := panesOfTab(dest)[0], panesOfTab(dest)[1]

	ws.NewTab(session.KindShell, root, "two")
	ws.SplitPane(layout.Vertical, session.KindShell)
	src := ws.CurrentTab()
	third, fourth := panesOfTab(src)[0], panesOfTab(src)[1]
	ws.FocusPane(third)
	sess := ws.Pane(third).Sess

	if err := ws.MergeTab(src.ID, dest.ID, layout.Horizontal); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := tabTitles(ws); !reflect.DeepEqual(got, []string{"one"}) {
		t.Fatalf("tabs = %v, want only the one merged into", got)
	}
	want := []string{top, bottom, third, fourth}
	if got := panesOfTab(dest); !reflect.DeepEqual(got, want) {
		t.Errorf("panes = %v, want %v", got, want)
	}
	// Each side keeps its column: a row of two columns, not four panes in a row.
	if dest.Tree.Dir != layout.Horizontal || len(dest.Tree.Children) != 2 {
		t.Errorf("tree = %d children along %v, want the two columns side by side",
			len(dest.Tree.Children), dest.Tree.Dir)
	}
	if ws.ActiveTabID() != dest.ID {
		t.Error("the surviving tab should be the one on screen")
	}
	if dest.Focus != third {
		t.Errorf("focus = %q, want the merged tab's focused pane %q", dest.Focus, third)
	}
	for _, id := range want {
		if p := ws.Pane(id); p == nil || !p.Alive() {
			t.Errorf("pane %q should still be running after a merge", id)
		}
	}
	if got := ws.Pane(third).Sess; got != sess {
		t.Error("a merged pane was given a different session")
	}
}

// TestMergeTabSharesTheSpaceEqually checks that a merge gives each tab half
// the room rather than letting the busier one shrink the other away.
func TestMergeTabSharesTheSpaceEqually(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "wide")
	ws.SplitPane(layout.Horizontal, session.KindShell)
	ws.SplitPane(layout.Horizontal, session.KindShell)
	wide := ws.CurrentTab()

	ws.NewTab(session.KindShell, root, "lonely")
	lonely := ws.CurrentTab()
	only := panesOfTab(lonely)[0]

	if err := ws.MergeTab(lonely.ID, wide.ID, layout.Horizontal); err != nil {
		t.Fatalf("merge: %v", err)
	}
	// Three columns merged with one is four columns, not a row holding a row.
	if got := len(wide.Tree.Children); got != 4 {
		t.Fatalf("children = %d, want the rows flattened into one", got)
	}
	wide.Tree.Compute(layout.Rect{W: 1000, H: 100})
	got := wide.Tree.Find(only).Rect().W
	if got < 450 || got > 550 {
		t.Errorf("the merged pane got %d of 1000 columns, want about half", got)
	}
}

// TestMergeTabRejectsWhatItCannotDo guards the drops that must not happen.
func TestMergeTabRejectsWhatItCannotDo(t *testing.T) {
	isolateConfig(t)
	first := t.TempDir()
	second := t.TempDir()
	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "mine")
	mine := ws.CurrentTab()

	if err := ws.MergeTab(mine.ID, mine.ID, layout.Horizontal); err == nil {
		t.Error("a tab merged into itself should be refused")
	}
	if err := ws.MergeTab(mine.ID, "gone", layout.Horizontal); err == nil {
		t.Error("a merge into a tab that is not there should be refused")
	}
	if err := ws.MergeTabsInProject(mine.ID, layout.Horizontal); err == nil {
		t.Error("merging a lone tab into itself should be refused")
	}

	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second project: %v", err)
	}
	ws.NewTab(session.KindShell, second, "theirs")
	theirs := ws.CurrentTab()
	if err := ws.MergeTab(mine.ID, theirs.ID, layout.Horizontal); err == nil {
		t.Error("a tab should not be mergeable into another project's tab")
	}
	if got := len(panesOfTab(theirs)); got != 1 {
		t.Errorf("panes = %d, want the refused merge to have changed nothing", got)
	}
}

// TestMergeTabsInProjectGathersEverything covers the "put all of them on one
// screen" command, and checks it leaves the other project alone.
func TestMergeTabsInProjectGathersEverything(t *testing.T) {
	isolateConfig(t)
	first := t.TempDir()
	second := t.TempDir()
	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "one")
	keep := ws.CurrentTab()
	ws.NewTab(session.KindShell, first, "two")
	ws.NewTab(session.KindShell, first, "three")
	var want []string
	for _, t := range ws.VisibleTabs() {
		want = append(want, panesOfTab(t)...)
	}

	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second project: %v", err)
	}
	ws.NewTab(session.KindShell, second, "theirs")
	elsewhere := tabTitles(ws)
	ws.SelectProject(first)

	if err := ws.MergeTabsInProject(keep.ID, layout.Horizontal); err != nil {
		t.Fatalf("merge all: %v", err)
	}
	if got := tabTitles(ws); !reflect.DeepEqual(got, []string{"one"}) {
		t.Fatalf("tabs = %v, want just the one they were gathered into", got)
	}
	if got := panesOfTab(keep); !reflect.DeepEqual(got, want) {
		t.Errorf("panes = %v, want every pane in tab order %v", got, want)
	}
	ws.SelectProject(second)
	if got := tabTitles(ws); !reflect.DeepEqual(got, elsewhere) {
		t.Errorf("other project tabs = %v, want it untouched %v", got, elsewhere)
	}
}

// TestMergeTabsInProjectKeepsTheFocus checks that gathering every tab leaves
// the user typing where they were. Each individual merge lands on the pane it
// brought over, so without care the focus ends up on whichever tab happened to
// be merged last.
func TestMergeTabsInProjectKeepsTheFocus(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	ws.SplitPane(layout.Horizontal, session.KindShell)
	keep := ws.CurrentTab()
	mine := panesOfTab(keep)[1]
	ws.FocusPane(mine)

	ws.NewTab(session.KindShell, root, "two")
	ws.NewTab(session.KindShell, root, "three")
	ws.SelectTab(keep.ID)

	if err := ws.MergeTabsInProject(keep.ID, layout.Horizontal); err != nil {
		t.Fatalf("merge all: %v", err)
	}
	if keep.Focus != mine {
		t.Errorf("focus = %q, want the pane it started on %q", keep.Focus, mine)
	}
}
