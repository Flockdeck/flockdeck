package workspace

import (
	"fmt"
	"strings"

	"github.com/jmwri/flockdeck/internal/layout"
)

// Rearranging panes moves live sessions between positions and between tabs.
// Nothing is started or stopped by any of it: a pane keeps its process, its
// conversation, its directory and its scrollback, and only where it is drawn
// changes. That is the whole point — a layout you got wrong is fixed by
// dragging, not by closing an agent and starting it again somewhere else.

// MovePane moves a pane so that it sits on one edge of another, in whichever
// tab that other pane lives in.
func (w *Workspace) MovePane(paneID, targetID string, edge layout.Edge) error {
	if paneID == "" || paneID == targetID {
		return fmt.Errorf("a pane cannot be moved onto itself")
	}
	if w.Pane(paneID) == nil {
		return fmt.Errorf("that pane is no longer open")
	}
	// The two halves of a drop fail for different reasons and the message is
	// all the user gets, so say which of them went away. Dropping onto a pane
	// another agent has just closed is the case that actually happens.
	src := w.tabOf(paneID)
	if src == nil {
		return fmt.Errorf("that pane is no longer on screen")
	}
	dest := w.tabOf(targetID)
	if dest == nil {
		return fmt.Errorf("the pane it was dropped on is no longer on screen")
	}

	if src == dest {
		if !dest.Tree.MovePane(paneID, targetID, edge) {
			return fmt.Errorf("that move is not possible")
		}
	} else {
		// Detach only once the destination is known to be good, so a failed
		// insert cannot leave a live pane belonging to no tab at all.
		w.detachPane(paneID)
		if !dest.Tree.InsertBeside(targetID, paneID, edge) {
			return fmt.Errorf("that move is not possible")
		}
	}

	w.landOn(dest, paneID)
	return nil
}

// SwapPanes exchanges two panes' positions, leaving the splits and their
// proportions exactly as they were. The panes may be in different tabs.
func (w *Workspace) SwapPanes(a, b string) error {
	if a == "" || a == b {
		return fmt.Errorf("a pane cannot be swapped with itself")
	}
	ta := w.tabOf(a)
	if ta == nil {
		return fmt.Errorf("that pane is no longer on screen")
	}
	tb := w.tabOf(b)
	if tb == nil {
		return fmt.Errorf("the pane it was swapped with is no longer on screen")
	}
	if ta == tb {
		if !ta.Tree.SwapPanes(a, b) {
			return fmt.Errorf("that swap is not possible")
		}
	} else {
		la, lb := ta.Tree.Find(a), tb.Tree.Find(b)
		if la == nil || lb == nil {
			return fmt.Errorf("that swap is not possible")
		}
		la.Pane, lb.Pane = b, a
		if ta.Focus == a {
			ta.Focus = b
		}
		if tb.Focus == b {
			tb.Focus = a
		}
	}
	w.landOn(w.tabOf(a), a)
	return nil
}

// MovePaneDir moves the focused pane past its neighbour in a direction, which
// is the keyboard equivalent of dragging it there.
//
// Neighbours are chosen by geometry rather than by tree order, so that moving
// a pane right sends it where the user can see the space is.
func (w *Workspace) MovePaneDir(dir layout.Direction) error {
	t := w.CurrentTab()
	if t == nil || t.Focus == "" {
		return fmt.Errorf("no pane is focused")
	}
	if t.Zoom {
		return fmt.Errorf("a zoomed pane fills the tab; unzoom it first")
	}
	computeTab(t)
	other := t.Tree.Neighbor(t.Focus, dir)
	// Swapping rather than re-nesting keeps a keyboard move reversible: press
	// the opposite arrow and the layout is back as it was. The geometry alone
	// does not promise that. Two panes of different sizes trade cells, and the
	// one moved can land facing several panes the other way — moved down past
	// a full-width pane from beside a wider one, back up it faces both, and the
	// wider one shares more of its edge. So the move just made is undone by
	// name, as long as nothing has rearranged the tab since.
	if m := w.lastKeyMove; m.tab == t.ID && m.pane == t.Focus && dir == opposite(m.dir) &&
		m.order == paneOrder(t) {
		other = m.other
	}
	if other == "" {
		return fmt.Errorf("there is no pane that way")
	}
	moving := t.Focus
	if err := w.SwapPanes(moving, other); err != nil {
		return err
	}
	w.lastKeyMove = keyMove{tab: t.ID, pane: moving, other: other, dir: dir, order: paneOrder(t)}
	return nil
}

// keyMove is a keyboard move: which pane went past which, which way, and what
// the tab looked like afterwards, which is what says it is still the last
// thing that happened to it.
type keyMove struct {
	tab, pane, other string
	dir              layout.Direction
	order            string
}

// paneOrder fingerprints a tab's arrangement: its panes in tree order.
func paneOrder(t *Tab) string { return strings.Join(t.Tree.Panes(), "\x00") }

// opposite is the arrow that points back the way a move came.
func opposite(d layout.Direction) layout.Direction {
	switch d {
	case layout.Left:
		return layout.Right
	case layout.Right:
		return layout.Left
	case layout.Up:
		return layout.Down
	default:
		return layout.Up
	}
}

// MovePaneToTab moves a pane into another tab, beside that tab's focused pane.
func (w *Workspace) MovePaneToTab(paneID, tabID string) error {
	if w.Pane(paneID) == nil {
		return fmt.Errorf("that pane is no longer open")
	}
	dest := w.Tab(tabID)
	if dest == nil {
		return fmt.Errorf("that tab is no longer open")
	}
	if w.tabOf(paneID) == dest {
		return nil // already there; a drop on its own tab is a no-op, not an error
	}
	target := dest.Focus
	if dest.Tree.Find(target) == nil {
		if panes := dest.Tree.Panes(); len(panes) > 0 {
			target = panes[0]
		}
	}
	if target == "" {
		return fmt.Errorf("that tab has no panes to place it beside")
	}

	w.detachPane(paneID)
	if !dest.Tree.InsertBeside(target, paneID, layout.EdgeRight) {
		return fmt.Errorf("that move is not possible")
	}
	w.landOn(dest, paneID)
	return nil
}

// TilePanes lays the current tab's panes out as an even grid, in the order
// they are already in.
//
// It is the way back. Every other move here is relative — beside this one,
// past that one — and a tab that has been rearranged a dozen times ends up
// with panes so narrow there is no divider left to take hold of and no room to
// drop anything into. Rather than ask the user to drag their way out of that,
// this puts the tab back into rows of even columns in one go.
func (w *Workspace) TilePanes() error {
	t := w.CurrentTab()
	if t == nil {
		return fmt.Errorf("there is no tab to tile")
	}
	if t.Tree.Count() < 2 {
		return fmt.Errorf("there is nothing to tile: this tab has one pane")
	}
	t.Tree = layout.Tile(t.Tree, nominalRect)
	t.Zoom = false
	return nil
}

// MovePaneToNewTab pulls a pane out into a tab of its own, which is how a pane
// that has outgrown its split gets the room to itself.
func (w *Workspace) MovePaneToNewTab(paneID string) error {
	if w.Pane(paneID) == nil {
		return fmt.Errorf("that pane is no longer open")
	}
	src := w.tabOf(paneID)
	if src == nil {
		return fmt.Errorf("that pane is no longer on screen")
	}
	if src.Tree.Count() <= 1 {
		return fmt.Errorf("that pane already has a tab to itself")
	}
	w.detachPane(paneID)
	// The tab stays with the project the pane was pulled out of, which is the
	// tab bar the user is looking at, whatever project the agent itself
	// belongs to.
	t := w.tabHolding(paneID, src.Root)
	// Put it directly after the tab it came from rather than at the far end,
	// so a pane pulled out stays next to its old neighbours.
	w.insertTabAfter(t, src)
	w.landOn(t, paneID)
	return nil
}

// MergeTab folds one tab into another, leaving a single tab holding every pane
// of both, side by side along dir. The tab that was merged away closes, and,
// as with every other move here, nothing in it is stopped: its agents come
// across still running, with their conversations and scrollback intact.
//
// This is the counterpart to dragging a pane out into a tab of its own. Two
// agents started in separate tabs are put side by side without restarting
// either of them.
func (w *Workspace) MergeTab(id, targetID string, dir layout.Dir) error {
	return w.mergeTab(id, targetID, dir, false)
}

// mergeTab is MergeTab with a say in which side of the destination the
// arriving panes land on. Gathering a whole project needs that: a tab sitting
// to the left of the one being gathered into should come out on the left of
// it, so the row of panes reads in the order the tab bar did.
func (w *Workspace) mergeTab(id, targetID string, dir layout.Dir, before bool) error {
	src := w.Tab(id)
	if src == nil {
		return fmt.Errorf("that tab is no longer open")
	}
	dest := w.Tab(targetID)
	if dest == nil {
		return fmt.Errorf("the tab it was dropped on is no longer open")
	}
	if src == dest {
		return fmt.Errorf("a tab cannot be merged into itself")
	}
	if src.Root != dest.Root {
		return fmt.Errorf("a tab can only be merged into a tab of its own project")
	}

	// The pane that had the focus in the tab being dragged is the one to land
	// on, matching a pane drag: what you picked up is what you end up typing
	// into.
	focus := src.Focus
	if src.Tree.Find(focus) == nil {
		if panes := src.Tree.Panes(); len(panes) > 0 {
			focus = panes[0]
		}
	}

	if before {
		dest.Tree = layout.Combine(src.Tree, dest.Tree, dir)
	} else {
		dest.Tree = layout.Combine(dest.Tree, src.Tree, dir)
	}
	// The panes now belong to dest, so unlinking must not go looking for them
	// in src again.
	src.Tree = layout.NewSplit(dir)
	src.Focus = ""
	w.unlinkTab(src)

	if focus == "" {
		focus = dest.Focus
	}
	w.landOn(dest, focus)
	return nil
}

// MergeTabsInProject folds every other tab of a project into one, which is the
// quickest way from agents scattered across tabs to all of them on screen at
// once.
//
// The panes come out in the order the tab bar showed them, whichever tab was
// gathered into: a tab that was two to the left stays two to the left. Folding
// each tab in on the near side and working outwards is what gets that, since
// each merge only ever places a tab's panes against what has already been
// gathered.
func (w *Workspace) MergeTabsInProject(targetID string, dir layout.Dir) error {
	dest := w.Tab(targetID)
	if dest == nil {
		return fmt.Errorf("that tab is no longer open")
	}
	tabs := w.tabsOf(dest.Root)
	at := -1
	for i, t := range tabs {
		if t == dest {
			at = i
			break
		}
	}
	if at < 0 || len(tabs) == 1 {
		return fmt.Errorf("there is no other tab to merge in")
	}
	// Every merge lands on the pane it brought over, so the focus has to be
	// noted before the loop rather than read back from it afterwards: gathering
	// the tabs is a command about the tab as a whole, and it should leave the
	// user typing into the pane they were typing into.
	focus := dest.Focus
	for i := at - 1; i >= 0; i-- {
		if err := w.mergeTab(tabs[i].ID, targetID, dir, true); err != nil {
			return err
		}
	}
	for i := at + 1; i < len(tabs); i++ {
		if err := w.mergeTab(tabs[i].ID, targetID, dir, false); err != nil {
			return err
		}
	}
	if dest.Tree.Find(focus) == nil {
		focus = dest.Focus
	}
	w.landOn(dest, focus)
	return nil
}

// MoveTab reorders a tab within its project, placing it before the tab named
// by beforeID. An empty beforeID moves it to the end.
func (w *Workspace) MoveTab(id, beforeID string) error {
	moving := w.Tab(id)
	if moving == nil {
		return fmt.Errorf("that tab is no longer open")
	}
	if id == beforeID {
		return nil
	}
	before := w.Tab(beforeID)
	if beforeID != "" {
		// A destination that has been closed since the drag started is a
		// different problem from one in another project, and telling the user
		// about the wrong one sends them looking in the wrong place.
		if before == nil {
			return fmt.Errorf("the tab it was dropped before is no longer open")
		}
		if before.Root != moving.Root {
			return fmt.Errorf("a tab can only be reordered within its own project")
		}
	}

	// Tabs of every project share one slice, and only a project's own tabs are
	// shown, so a tab is reordered by placing it at its destination's index in
	// that shared slice.
	rest := make([]*Tab, 0, len(w.Tabs))
	for _, t := range w.Tabs {
		if t != moving {
			rest = append(rest, t)
		}
	}
	at := len(rest)
	if before != nil {
		for i, t := range rest {
			if t == before {
				at = i
				break
			}
		}
	} else {
		// The end of this project's tabs, not the end of every project's.
		for i, t := range rest {
			if t.Root == moving.Root {
				at = i + 1
			}
		}
	}
	w.Tabs = append(rest[:at:at], append([]*Tab{moving}, rest[at:]...)...)
	return nil
}

// detachPane removes a pane from its tab without touching its process, closing
// the tab when that leaves it empty.
func (w *Workspace) detachPane(paneID string) {
	t := w.tabOf(paneID)
	if t == nil {
		return
	}
	if t.Tree.Count() <= 1 {
		w.unlinkTab(t)
		return
	}
	// When the pane being taken away is the focused one, choose where the focus
	// lands before mutating the tree.
	next := ""
	if t.Focus == paneID {
		next = paneBesideTheGap(t, paneID)
	}

	t.Tree.Remove(paneID)
	t.Zoom = false
	if t.Focus == paneID {
		t.Focus = next
		if t.Focus == "" {
			if panes := t.Tree.Panes(); len(panes) > 0 {
				t.Focus = panes[0]
			}
		}
	}
}

// unlinkTab drops a tab from the workspace, leaving every session in it alone.
// It is what CloseTab does minus the killing, for a tab emptied by a move.
func (w *Workspace) unlinkTab(t *Tab) {
	siblings := w.tabsOf(t.Root)
	pos := 0
	for i, s := range siblings {
		if s == t {
			pos = i
		}
	}

	kept := make([]*Tab, 0, len(w.Tabs))
	for _, other := range w.Tabs {
		if other != t {
			kept = append(kept, other)
		}
	}
	w.Tabs = kept

	if w.activeTab != t.ID {
		return
	}
	w.activeTab = ""
	remaining := w.tabsOf(t.Root)
	if len(remaining) > 0 {
		if pos >= len(remaining) {
			pos = len(remaining) - 1
		}
		w.activeTab = remaining[pos].ID
	}
}

// insertTabAfter places a new tab directly behind an existing one.
func (w *Workspace) insertTabAfter(t, after *Tab) {
	at := len(w.Tabs)
	for i, existing := range w.Tabs {
		if existing == after {
			at = i + 1
			break
		}
	}
	w.Tabs = append(w.Tabs[:at:at], append([]*Tab{t}, w.Tabs[at:]...)...)
}

// landOn brings a moved pane into view: its tab is selected and the pane takes
// the focus, so typing goes where the user just dropped it.
//
// The project switch is a safeguard rather than a normal path. The window is
// only ever shown the tabs of the project on screen, so a drop cannot land in
// another one; a caller reaching these methods directly still gets a coherent
// workspace rather than a pane hidden behind a project that is not selected.
func (w *Workspace) landOn(t *Tab, paneID string) {
	if t == nil {
		return
	}
	t.Focus = paneID
	t.Zoom = false
	if t.Root != w.activeRoot && w.isOpen(t.Root) {
		w.rememberTab()
		w.activeRoot = t.Root
	}
	w.activeTab = t.ID
}

// nominalRect is the notional screen the layout tree is measured against when
// a question needs geometry — which pane is to the left of which — but the real
// pixel sizes live in the browser. The tree's weights carry the proportions, so
// any large rectangle puts the panes in the right places relative to each
// other; this one is big enough that no pane rounds away to nothing.
var nominalRect = layout.Rect{W: 1000, H: 1000}

// paneBesideTheGap chooses the pane to focus once paneID is taken out of a
// tab: the one next to the space it leaves rather than whichever happens to
// come first in tree order, so the focus ends up where the user was looking.
// Neighbour lookup is geometric, so the tree needs its rectangles first.
func paneBesideTheGap(t *Tab, paneID string) string {
	computeTab(t)
	for _, dir := range []layout.Direction{layout.Right, layout.Left, layout.Down, layout.Up} {
		if next := t.Tree.Neighbor(paneID, dir); next != "" {
			return next
		}
	}
	return ""
}

// computeTab assigns rectangles to a tab's panes so the geometric queries —
// Neighbor, PaneAt — have something to work from.
func computeTab(t *Tab) {
	if t != nil && t.Tree != nil {
		t.Tree.Compute(nominalRect)
	}
}
