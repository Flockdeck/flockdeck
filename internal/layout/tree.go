// Package layout models a tab's panes as a tree of splits and turns that tree
// into concrete screen rectangles.
//
// Every operation here leaves the tree in the same shape, and the rest of the
// package and its callers rely on that:
//
//   - a pane appears once, so closing it closes all of it and finding it finds
//     the one the user is looking at;
//   - a split holds two children or more, never one, so no divider is drawn
//     with nothing on one side of it;
//   - no split nests inside a split along its own axis, so three panes in a
//     row share the row rather than one of them sharing it with a pair;
//   - Compute tiles the box it is given exactly, leaving no cell to two panes
//     and none to nobody, as long as there is a cell in it for each pane.
//
// A tree read back from a saved layout can arrive in other shapes, and the
// operations cope with those rather than assuming them away.
package layout

import (
	"math"
	"strconv"
	"sync/atomic"
)

// nodeSeq names nodes uniquely within this process. The browser uses these ids
// to address a split container when a divider is dragged; they need to be
// stable for the life of a run, not across runs.
var nodeSeq atomic.Uint64

func nextNodeID() string { return "n" + strconv.FormatUint(nodeSeq.Add(1), 10) }

// Dir is the axis along which a split node arranges its children.
type Dir int

const (
	// Horizontal arranges children left to right, separated by a vertical rule.
	Horizontal Dir = iota
	// Vertical arranges children top to bottom.
	Vertical
)

// Rect is a screen rectangle in cells.
type Rect struct{ X, Y, W, H int }

// Contains reports whether the point lies inside the rectangle.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// centerX2 and centerY2 are twice the middle of the rectangle, which is where
// the 2 in the name is: doubling is what lets a pane of an even number of
// cells and one of an odd number be compared. Halving each of them instead put
// the middle of a 31-row pane half a row above where it is, which is enough to
// decide a tie that should have been settled by tree order — two panes of the
// same height, one above the other, beside one twice as tall are the same
// distance from its middle, and the truncation handed it to the lower of them.
//
// Nothing can use these as a coordinate; they are only ever compared with each
// other.
func (r Rect) centerX2() int { return 2*r.X + r.W }
func (r Rect) centerY2() int { return 2*r.Y + r.H }

// separatorWidth is the single column drawn between horizontally adjacent
// panes. Vertically stacked panes need no separator because each pane draws a
// header row.
const separatorWidth = 1

// Node is either a leaf holding one pane, or a split holding children.
type Node struct {
	// ID names this node so a client can refer to it, for example to set the
	// weights of a split's children after a drag.
	ID string
	// Pane is the pane id when this node is a leaf.
	Pane string
	// Dir and Children are set when this node is a split.
	Dir      Dir
	Children []*Node
	// Weight is this node's share of its parent's space, relative to its
	// siblings. Zero is treated as 1.
	Weight float64

	rect Rect
}

// NewLeaf returns a leaf node for a pane.
func NewLeaf(pane string) *Node { return &Node{ID: nextNodeID(), Pane: pane, Weight: 1} }

// NewSplit returns an empty split node along dir.
func NewSplit(dir Dir) *Node { return &Node{ID: nextNodeID(), Dir: dir, Weight: 1} }

// FindNode returns the node with the given id, or nil.
func (n *Node) FindNode(id string) *Node {
	if n == nil || id == "" {
		return nil
	}
	if n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if got := c.FindNode(id); got != nil {
			return got
		}
	}
	return nil
}

// SetChildWeights assigns weights to a split's children in order. It reports
// false if the node is not a split with a matching number of children, or if
// any weight is not a usable positive number.
//
// The weights arrive from a divider drag in the browser, so they are checked
// in full before any of them is stored: a rejected set must leave the split
// exactly as it was rather than half updated. A weight that is not finite
// would poison every later Compute of the tree, so it is rejected too.
func (n *Node) SetChildWeights(weights []float64) bool {
	if n == nil || n.IsLeaf() || len(weights) != len(n.Children) {
		return false
	}
	for _, w := range weights {
		if w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
			return false
		}
	}
	for i, w := range weights {
		n.Children[i].Weight = w
	}
	return true
}

// IsLeaf reports whether the node holds a pane rather than children.
func (n *Node) IsLeaf() bool { return len(n.Children) == 0 }

// maxWeight is the largest share a node is given when the box is divided.
//
// A weight only means anything next to its siblings, and a pane a trillionth
// the size of the one beside it is a pane nobody can see on any screen. Past
// that the numbers stop being comparable at all: weights near what a float can
// hold overflow when they are added up or scaled to the width of the box, and
// the arithmetic then hands the whole row to whichever pane the resulting
// nonsense happens to favour — two panes weighted alike came out one column
// and ninety-eight. Weights that large do not come from a drag, but they can
// come out of a layout file, and the tree has to survive one.
const maxWeight = 1e12

// weight returns the node's effective weight, which is always a positive
// number no larger than maxWeight, whatever is stored in the node.
//
// Everything that divides a box relies on that: the weights are summed and
// divided by, so one that is not a number at all takes the whole tab with it.
// SetChildWeights refuses those, but Weight is an exported field and a layout
// file is JSON on disk, so this is where the guarantee has to be made. A pane
// weighted with a nothing is given an equal share, which is what a pane with
// no weight recorded at all gets.
func (n *Node) weight() float64 {
	if !(n.Weight > 0) { // false for zero, for a negative, and for a NaN
		return 1
	}
	if n.Weight > maxWeight {
		return maxWeight
	}
	return n.Weight
}

// Rect returns the rectangle assigned by the last Compute call.
func (n *Node) Rect() Rect { return n.rect }

// Leaves returns every leaf holding a pane, in tree order: each split's
// children in the order they are laid out, and each of those in full before
// the next one begins. That is not the order the tab reads in — a column
// beside a tall pane comes out whole, before the pane to the right of its
// top half.
//
// A split with no children left looks structurally like a leaf but names no
// pane, which is what a tab emptied by a merge is holding. It is skipped, so
// it is not counted as a pane, offered as a drop target, or drawn as an empty
// frame by a client walking the tree.
func (n *Node) Leaves() []*Node {
	out := make([]*Node, 0, n.Count())
	n.eachLeaf(func(l *Node) bool {
		out = append(out, l)
		return true
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

// eachLeaf calls fn for each leaf holding a pane, in the order Leaves returns
// them, and stops as soon as fn returns false. It reports whether it reached
// the end of the tree.
//
// Every question about the panes of a tab goes through this rather than
// through Leaves. Building the slice first cost an allocation per level of the
// tree, on lookups a client makes several of per keystroke and per redraw, and
// it walked the whole tree even when the answer was the first pane in it.
func (n *Node) eachLeaf(fn func(*Node) bool) bool {
	if n == nil {
		return true
	}
	if n.IsLeaf() {
		if n.Pane == "" {
			return true
		}
		return fn(n)
	}
	for _, c := range n.Children {
		if !c.eachLeaf(fn) {
			return false
		}
	}
	return true
}

// Panes returns the ids of every pane in the tree, in tree order.
func (n *Node) Panes() []string {
	out := make([]string, 0, n.Count())
	n.eachLeaf(func(l *Node) bool {
		out = append(out, l.Pane)
		return true
	})
	return out
}

// Find returns the leaf holding the given pane, or nil.
func (n *Node) Find(pane string) *Node {
	var found *Node
	n.eachLeaf(func(l *Node) bool {
		if l.Pane != pane {
			return true
		}
		found = l
		return false
	})
	return found
}

// parentOf returns the parent of target and target's index within it.
func (n *Node) parentOf(target *Node) (*Node, int) {
	if n == nil || n.IsLeaf() {
		return nil, -1
	}
	for i, c := range n.Children {
		if c == target {
			return n, i
		}
		if p, idx := c.parentOf(target); p != nil {
			return p, idx
		}
	}
	return nil, -1
}

// Compute assigns rectangles to every node in the tree.
func (n *Node) Compute(r Rect) {
	if n == nil {
		return
	}
	n.rect = r
	if n.IsLeaf() {
		return
	}

	total, tightest := 0.0, math.Inf(1)
	for _, c := range n.Children {
		w := c.weight()
		total += w
		if w < tightest {
			tightest = w
		}
	}

	// Everything below works along the split's own axis, so the box is named
	// once here and the other axis is handed straight down to every child. A
	// row spends a column on each rule between its panes; a stack needs none,
	// because every pane draws a header row of its own.
	box, boxSize, sep := r.Y, r.H, 0
	if n.Dir == Horizontal {
		box, boxSize, sep = r.X, r.W, separatorWidth
	}
	avail := boxSize - sep*(len(n.Children)-1)
	if avail < len(n.Children) {
		avail = len(n.Children) // degenerate, but never negative
	}

	over, spare := n.overrun(avail, total, tightest)
	at := box
	used, acc := 0, 0.0
	held, paid := 0, 0
	for i, c := range n.Children {
		acc += c.weight()
		size := portion(avail, used, acc, total, i == len(n.Children)-1)
		used += size

		// Hand back what the row runs past the end of the box. Each pane gives
		// up a share of it in proportion to what it holds above the one cell it
		// must keep, measured to a running boundary like the sizes themselves,
		// so panes of the same size give up the same amount and the last cell
		// of the debt still lands on somebody. Taking it from whoever came
		// first instead left one pane a single cell while the pane weighted the
		// same as it, further along the row, kept nine.
		if over > 0 {
			held += size - 1
			give := over*held/spare - paid
			size -= give
			paid += give
		}

		at, size = fit(at, size, box, boxSize)
		if n.Dir == Horizontal {
			c.Compute(Rect{X: at, Y: r.Y, W: size, H: r.H})
		} else {
			c.Compute(Rect{X: r.X, Y: at, W: r.W, H: size})
		}
		at += size + sep
	}
}

// portion returns how many of avail cells the child whose weight brings the
// running total to acc gets, given that used have been handed out already.
//
// Each child is measured to a running boundary rather than on its own, so the
// cells lost to truncation are spread one at a time across the row instead of
// piling up in the last child: ten equal panes in 105 columns come out ten and
// eleven wide, not nine of ten and one of fifteen. The last child takes what
// is left over, and no child is given less than the one cell a pane needs.
func portion(avail, used int, acc, total float64, last bool) int {
	size := int(float64(avail)*acc/total) - used
	if last {
		size = avail - used
	}
	if size < 1 {
		size = 1
	}
	return size
}

// overrun returns how many cells a split's children run past a box of avail
// cells along its axis, and how many they hold between them above the one cell
// each pane must keep, which is what there is to take the overrun from.
// tightest is the smallest weight among the children.
//
// A pane owed less than a whole cell is given one anyway, since a pane nobody
// can see is worse than one a cell too wide. Several such panes in a row add
// up, though, and what they add up to used to come off the end of the box: the
// panes at that end were laid out beyond their own parent, over whatever the
// split next to it had drawn, and a click or an arrow key landed in the wrong
// one of them. Compute takes this back off the panes with cells to spare, so
// the row fits whatever the weights are.
//
// Working it out means walking the children twice, so the walk is skipped
// unless a pane really is owed less than a cell — which is what the smallest
// weight answers, since the boundaries are floored and floor(a+s) - floor(a)
// is at least 1 for any share s of a cell or more.
func (n *Node) overrun(avail int, total, tightest float64) (over, spare int) {
	if float64(avail)*tightest/total >= 1 {
		return 0, 0
	}
	used, acc := 0, 0.0
	for i, c := range n.Children {
		acc += c.weight()
		used += portion(avail, used, acc, total, i == len(n.Children)-1)
	}
	// avail is never below the number of children, so there is always at least
	// as much room above the floor as there is overrun to take back.
	return used - avail, used - len(n.Children)
}

// fit pulls a child of size cells, laid out at at, back inside a box of
// boxSize cells starting at box.
//
// Splitting a pane and turning the axis each time halves the box at every
// level, so about twenty levels down there is no longer a cell for each pane.
// There is no right answer there — a pane needs a cell and there are not
// enough — but a pane must still be somewhere in the tab it belongs to. Left
// to run off the end it would sit on top of whatever the split next door had
// drawn, and "what is left of this" and "what did I just click on" would then
// be answered from a part of the screen the user is not looking at. Panes that
// will not fit pile up in the last cells of their own box instead.
func fit(at, size, box, boxSize int) (int, int) {
	if size > boxSize {
		size = boxSize
	}
	if end := box + boxSize; at+size > end {
		at = end - size
	}
	if at < box {
		at = box
	}
	return at, size
}

// Separators returns the vertical rules between horizontally adjacent panes,
// each as a 1-cell-wide rectangle.
func (n *Node) Separators() []Rect {
	if n == nil || n.IsLeaf() {
		return nil
	}
	var out []Rect
	if n.Dir == Horizontal {
		for i := 0; i < len(n.Children)-1; i++ {
			c := n.Children[i]
			out = append(out, Rect{X: c.rect.X + c.rect.W, Y: c.rect.Y, W: separatorWidth, H: c.rect.H})
		}
	}
	for _, c := range n.Children {
		out = append(out, c.Separators()...)
	}
	return out
}

// Split replaces the leaf holding pane with a split containing that pane and
// newPane, arranged along dir. It returns false if the pane is not in the
// tree, or if newPane is empty or already somewhere in it.
//
// Splitting an existing split along the same axis appends a sibling instead of
// nesting, which keeps three-way splits evenly sized rather than lopsided.
func (root *Node) Split(pane, newPane string, dir Dir) bool {
	// A pane may appear once. A second leaf for the same id would show the
	// same terminal in two places, and Find and Remove would only ever reach
	// the first of them, so closing the pane would leave the other behind.
	if newPane == "" || root.Find(newPane) != nil {
		return false
	}
	leaf := root.Find(pane)
	if leaf == nil {
		return false
	}
	if parent, idx := root.parentOf(leaf); parent != nil && parent.Dir == dir {
		child := NewLeaf(newPane)
		child.Weight = leaf.weight()
		parent.Children = append(parent.Children, nil)
		copy(parent.Children[idx+2:], parent.Children[idx+1:])
		parent.Children[idx+1] = child
		return true
	}

	// Convert the leaf in place into a split so the caller's root stays valid.
	// The moved child takes a fresh id; the node being converted keeps its own,
	// which is now the id of the split.
	moved := &Node{ID: nextNodeID(), Pane: leaf.Pane, Weight: 1}
	leaf.Pane = ""
	leaf.Dir = dir
	leaf.Children = []*Node{moved, NewLeaf(newPane)}
	return true
}

// Edge names where a pane lands relative to the one it was dropped on.
type Edge int

// Drop edges.
const (
	// EdgeLeft puts the moved pane to the left of the target, and so on round.
	EdgeLeft Edge = iota
	EdgeRight
	EdgeTop
	EdgeBottom
)

// axis reports the split direction an edge implies and whether the moved pane
// goes before the target along it.
func (e Edge) axis() (Dir, bool) {
	switch e {
	case EdgeLeft:
		return Horizontal, true
	case EdgeRight:
		return Horizontal, false
	case EdgeTop:
		return Vertical, true
	default:
		return Vertical, false
	}
}

// InsertBeside places a pane on one edge of the leaf holding target. It
// returns false when the target is not in the tree.
//
// Like Split, it appends to an existing split along the same axis rather than
// nesting a new one, so dropping a third pane to the right of two side by side
// gives three columns rather than a column containing a column.
func (root *Node) InsertBeside(target, pane string, edge Edge) bool {
	// As in Split, the same pane must not land in the tree twice. Every caller
	// that moves a pane detaches it from wherever it was first, so a pane that
	// is still here is a mistake rather than a move.
	if pane == "" || root.Find(pane) != nil {
		return false
	}
	leaf := root.Find(target)
	if leaf == nil {
		return false
	}
	dir, before := edge.axis()

	if parent, idx := root.parentOf(leaf); parent != nil && parent.Dir == dir {
		child := NewLeaf(pane)
		child.Weight = leaf.weight()
		at := idx
		if !before {
			at = idx + 1
		}
		parent.Children = append(parent.Children, nil)
		copy(parent.Children[at+1:], parent.Children[at:])
		parent.Children[at] = child
		return true
	}

	// Convert the leaf in place, so a caller holding the root keeps a valid
	// pointer. The displaced pane takes a fresh node id; the node being
	// converted keeps its own, which is now the id of the split.
	moved := &Node{ID: nextNodeID(), Pane: leaf.Pane, Weight: 1}
	inserted := NewLeaf(pane)
	leaf.Pane = ""
	leaf.Dir = dir
	if before {
		leaf.Children = []*Node{inserted, moved}
	} else {
		leaf.Children = []*Node{moved, inserted}
	}
	return true
}

// MovePane moves an existing pane so that it sits on one edge of another,
// within the same tree. It returns false when either pane is missing, or when
// they are the same pane.
//
// The removal happens first and the target is found again afterwards, because
// collapsing a split left with one child can move that child's contents into
// its parent and leave any pointer to it stale.
func (root *Node) MovePane(pane, target string, edge Edge) bool {
	if pane == "" || pane == target {
		return false
	}
	moving, onto := root.Find(pane), root.Find(target)
	if moving == nil || onto == nil {
		return false
	}
	if reorder(root, moving, onto, edge) {
		return true
	}
	if !root.Remove(pane) {
		return false
	}
	return root.InsertBeside(target, pane, edge)
}

// reorder moves a pane to one side of a sibling in the split they share along
// the edge's axis, and reports false when they share no such split.
//
// The pane's own node is moved, weight and all. Taking it out and putting it
// back would rebuild it with a fresh id and the weight of the pane it was
// dropped against: a pane the user had widened to half a row came out a third
// of it at the other end, and a drop on the edge it was already on evened out
// the whole row.
func reorder(root, moving, onto *Node, edge Edge) bool {
	dir, before := edge.axis()
	parent, from := root.parentOf(moving)
	if op, _ := root.parentOf(onto); parent == nil || op != parent || parent.Dir != dir {
		return false
	}
	// Both slices are cut to their length so each append makes a new array
	// rather than writing over the children still being read.
	rest := append(parent.Children[:from:from], parent.Children[from+1:]...)
	at := 0
	for i, c := range rest {
		if c == onto {
			at = i
		}
	}
	if !before {
		at++
	}
	parent.Children = append(rest[:at:at], append([]*Node{moving}, rest[at:]...)...)
	return true
}

// SwapPanes exchanges the positions of two panes, keeping every split and
// weight as it was. It returns false when either pane is missing.
func (root *Node) SwapPanes(a, b string) bool {
	la, lb := root.Find(a), root.Find(b)
	if la == nil || lb == nil || la == lb {
		return false
	}
	la.Pane, lb.Pane = lb.Pane, la.Pane
	return true
}

// Combine returns a tree holding every pane of both trees, arranged along dir
// with src's panes after dst's. Neither tree is copied: the nodes come across
// as they are, so a pane keeps its node id and the splits inside each tree
// keep their shape and proportions.
//
// The space is shared out a column at a time rather than a tree at a time, so
// merging a lone pane with a row of three gives four even columns. A tree that
// is already a split along dir is flattened into the result instead of nested
// inside it, for the same reason Split and InsertBeside append to an existing
// split: a row of two merged with a row of two is four columns, not a row of
// rows.
//
// Half the room each is the obvious rule and the wrong one, because it
// compounds. Folding tabs in one at a time gave the last one half the width,
// the one before a quarter and the one before that an eighth, so gathering a
// project's tabs left the first of them a column wide — and dragging that back
// into shape means finding a divider that is no longer there to grab. Per
// column, a tab folded in behind four others is one of five even columns
// however it got there, and a divider the user has moved keeps its proportion
// against its own neighbours.
func Combine(dst, src *Node, dir Dir) *Node {
	// A tree with no panes in it, which is what a tab emptied by an earlier
	// merge is left holding, contributes nothing. Merging it anyway made a
	// childless split a child of the result, so it took a share of the room
	// and left that share blank.
	if dst.Count() == 0 {
		return src
	}
	if src.Count() == 0 {
		return dst
	}
	// The merged tree gets its own slice of children. Appending straight onto
	// the one shareOf hands back would leave it sharing an array with the tree
	// it came from, so a later split in either could write over the other's
	// children.
	from, joining := shareOf(dst, dir), shareOf(src, dir)
	root := NewSplit(dir)
	root.Children = make([]*Node, 0, len(from)+len(joining))
	root.Children = append(root.Children, from...)
	root.Children = append(root.Children, joining...)
	return root
}

// shareOf returns the children a tree contributes to a split along dir,
// weighted so that they average one share each: a tree bringing three columns
// is worth three of them and a lone pane one, while the proportions those
// children had against each other are left as they were.
func shareOf(n *Node, dir Dir) []*Node {
	if n.IsLeaf() || n.Dir != dir {
		n.Weight = 1
		return []*Node{n}
	}
	total := 0.0
	for _, c := range n.Children {
		total += c.weight()
	}
	scale := float64(len(n.Children)) / total
	for _, c := range n.Children {
		c.Weight = c.weight() * scale
	}
	return n.Children
}

// Grid arranges panes into rows of even columns, in the order given.
//
// A fan-out puts a dozen agents in one tab, and neither a row of twelve nor a
// stack of twelve is a tab anyone can read: at that width a terminal wraps
// every line it prints, and at that height it holds four lines at a time. Rows
// of columns keeps both dimensions usable, and it is the shape someone
// arranging a dozen panes by hand would end up dragging out.
//
// The tree is built afresh each time, so a divider the user had moved in the
// old one is not carried over. That is the trade a caller makes by asking for
// a grid: it is for a tab being filled, where the alternative is each new pane
// halving the last one.
func Grid(panes []string) *Node {
	panes = distinct(panes)
	switch len(panes) {
	case 0:
		// An empty split is what a tab emptied by a move holds, and every
		// reader of the tree already copes with one.
		return NewSplit(Horizontal)
	case 1:
		return NewLeaf(panes[0])
	}
	rows := gridRows(len(panes))
	if rows == 1 {
		return gridRow(panes)
	}
	root := NewSplit(Vertical)
	root.Children = make([]*Node, 0, rows)
	for at := 0; at < len(panes); {
		// The panes left over are shared among the rows left over, so a count
		// that does not divide evenly puts the extra panes in the top rows
		// rather than all of them in the last one.
		n := (len(panes) - at + rows - len(root.Children) - 1) / (rows - len(root.Children))
		row := gridRow(panes[at : at+n])
		// A row is as tall as it is full. Seven panes are a row of four over a
		// row of three, and given equal heights the three would be half again
		// the size of the four; weighted by what they hold, every pane in the
		// grid is the same size as every other.
		row.Weight = float64(n)
		root.Children = append(root.Children, row)
		at += n
	}
	return root
}

// Tile rebuilds a tree as an even grid of the same panes, measured against box.
//
// The panes fill the grid in tree order, as Grid fills it — unless every pane
// already sits in a cell of its own in that grid, when each keeps its cell. A
// tab already laid out in rows of columns then has its proportions evened and
// nothing moved. Filling it in tree order instead swapped two panes of a grid
// built a column at a time, since a column comes out of the tree whole, before
// the pane beside its top; tiling is the way back to a tidy tab, and one that
// was tidy already should not come back rearranged.
func Tile(root *Node, box Rect) *Node {
	grid := Grid(root.Panes())
	root.Compute(box)
	grid.Compute(box)
	cells := grid.Leaves()
	placed := make([]string, len(cells))
	for _, l := range root.Leaves() {
		x, y := l.rect.X+l.rect.W/2, l.rect.Y+l.rect.H/2
		at := -1
		for i, c := range cells {
			if c.rect.Contains(x, y) {
				at = i
				break
			}
		}
		if at < 0 || placed[at] != "" {
			return grid
		}
		placed[at] = l.Pane
	}
	for i, c := range cells {
		c.Pane = placed[i]
	}
	return grid
}

// gridRows chooses how many rows a grid of n panes is drawn in.
//
// The square root, rounded down, so the grid is as square as it can be while
// erring towards rows that are wide rather than tall: three panes are a row of
// three, and twelve are three rows of four. A terminal needs its width more
// than its height, since what it loses when it runs out of height scrolls back
// and what it loses when it runs out of width is reflowed away.
func gridRows(n int) int {
	if rows := int(math.Sqrt(float64(n))); rows > 1 {
		return rows
	}
	return 1
}

// gridRow returns one row of a grid: a leaf when it holds a single pane, so
// that no split is left with one child.
func gridRow(panes []string) *Node {
	if len(panes) == 1 {
		return NewLeaf(panes[0])
	}
	row := NewSplit(Horizontal)
	row.Children = make([]*Node, 0, len(panes))
	for _, pane := range panes {
		row.Children = append(row.Children, NewLeaf(pane))
	}
	return row
}

// distinct drops the empty and repeated ids from a list of panes, so that a
// tree built from it keeps the rule that a pane appears in it once.
func distinct(panes []string) []string {
	out := make([]string, 0, len(panes))
	seen := make(map[string]bool, len(panes))
	for _, pane := range panes {
		if pane == "" || seen[pane] {
			continue
		}
		seen[pane] = true
		out = append(out, pane)
	}
	return out
}

// Remove deletes the pane's leaf, collapsing any split left with one child.
// It returns false if the pane is not in the tree.
func (root *Node) Remove(pane string) bool {
	leaf := root.Find(pane)
	if leaf == nil {
		return false
	}
	parent, idx := root.parentOf(leaf)
	if parent == nil {
		return false // removing the last pane is the caller's decision
	}

	parent.Children = append(parent.Children[:idx], parent.Children[idx+1:]...)
	if len(parent.Children) != 1 {
		return true
	}
	only := parent.Children[0]
	parent.Pane = only.Pane
	parent.Dir = only.Dir
	parent.Children = only.Children

	// Collapsing a split can pull a split up into a parent along the same
	// axis, which is exactly the nesting Split, InsertBeside and Combine all
	// take care to avoid: a row of three would read as a row of two, giving
	// the lone pane half the width and the other two a quarter each, and its
	// divider would move both of them together. Splice the collapsed split
	// into its parent, sharing out the slot it was occupying so the panes
	// keep the proportions they had.
	grand, at := root.parentOf(parent)
	if grand == nil || parent.IsLeaf() || grand.Dir != parent.Dir {
		return true
	}
	total := 0.0
	for _, c := range parent.Children {
		total += c.weight()
	}
	for _, c := range parent.Children {
		c.Weight = c.weight() * parent.weight() / total
	}
	spliced := make([]*Node, 0, len(grand.Children)+len(parent.Children)-1)
	spliced = append(spliced, grand.Children[:at]...)
	spliced = append(spliced, parent.Children...)
	spliced = append(spliced, grand.Children[at+1:]...)
	grand.Children = spliced
	return true
}

// Flatten splices every split nested in a split along its own axis into that
// split, sharing out the room the nested one held among its children so that
// every pane keeps the size it had.
//
// No operation here builds that shape, but a tree read back from a saved
// layout can hold it: builds before Remove spliced a collapsed split into its
// parent left one behind every time, and a layout carries it from run to run
// for as long as nothing rewrites that part of it. A row nested in a row looks
// like one row and does not behave like one — a pane split beside one of its
// panes shares that pane's group rather than the row, coming out a fraction of
// the width its neighbours have, and the divider between the groups moves
// several panes at once.
func (n *Node) Flatten() {
	if n == nil || n.IsLeaf() {
		return
	}
	out := make([]*Node, 0, len(n.Children))
	for _, c := range n.Children {
		c.Flatten()
		if c.IsLeaf() || c.Dir != n.Dir {
			out = append(out, c)
			continue
		}
		total := 0.0
		for _, g := range c.Children {
			total += g.weight()
		}
		for _, g := range c.Children {
			g.Weight = g.weight() * c.weight() / total
			out = append(out, g)
		}
	}
	n.Children = out
}

// Count returns the number of panes in the tree.
func (n *Node) Count() int {
	count := 0
	n.eachLeaf(func(*Node) bool {
		count++
		return true
	})
	return count
}

// Resize shifts space between the pane and one of its siblings along the given
// axis. A positive delta always grows the named pane, whichever sibling pays
// for it: normally the next one along, or the previous one when the pane is
// last in its split.
//
// delta is measured against an even share of the split, so 0.5 moves the
// divider by half of what one pane would get if they all shared equally, and a
// pane may not be squeezed below a tenth of that share.
func (root *Node) Resize(pane string, dir Dir, delta float64) bool {
	leaf := root.Find(pane)
	if leaf == nil {
		return false
	}
	// Walk up until we find an ancestor split along the requested axis that has
	// a sibling to trade space with.
	node := leaf
	for {
		parent, idx := root.parentOf(node)
		if parent == nil {
			return false
		}
		if parent.Dir == dir && len(parent.Children) > 1 {
			other := idx + 1
			if other >= len(parent.Children) {
				other = idx - 1
			}
			// Weights are only meaningful against their siblings, and their
			// absolute scale drifts: merging tabs scales a whole row down, so
			// one row can sum to 2 and another to 0.03 and look identical on
			// screen. Measuring delta against an even share of this split is
			// what makes one keypress move the divider by the same visible
			// amount wherever it is pressed.
			total := 0.0
			for _, c := range parent.Children {
				total += c.weight()
			}
			share := total / float64(len(parent.Children))
			step := delta * share

			a, b := parent.Children[idx], parent.Children[other]
			na, nb := a.weight()+step, b.weight()-step
			if na < share/10 || nb < share/10 {
				return false
			}
			a.Weight, b.Weight = na, nb
			return true
		}
		node = parent
	}
}

// Neighbor returns the pane adjacent to the given one in a direction, chosen
// by geometry so that focus movement matches what is on screen. Compute must
// have been called first.
func (root *Node) Neighbor(pane string, dir Direction) string {
	cur := root.Find(pane)
	if cur == nil {
		return ""
	}
	from := cur.rect
	if from.W <= 0 || from.H <= 0 {
		// Nothing has been measured yet, so every rectangle is empty and every
		// pane looks equally adjacent. Answering from that would send the
		// focus somewhere the user is not looking; say there is nothing that
		// way instead, which callers already report.
		return ""
	}

	type cand struct {
		pane string
		// primary is distance along the movement axis, shared is how much of
		// the two panes' edges face each other across it, and secondary is the
		// misalignment of their centres.
		primary, shared, secondary int
	}
	// A pane this one does not face is never the answer while a pane it does
	// face is on offer, however close the two happen to be. Rows split
	// differently sit their dividers at different columns, so a pane in the
	// row below can begin exactly where this row's divider is — nearer, by the
	// arithmetic, than the pane genuinely beside this one.
	//
	// Among the panes it does face, the nearest wins, and then the one most of
	// the edge is against: a full-height pane beside a tall one and a sliver
	// under it must not hand the focus to the sliver because the sliver's
	// centre happens to sit nearer.
	//
	// Centres still separate panes sharing the edge equally, and after that
	// everything can tie — two stacked panes beside one tall one already do.
	// The walk keeps the first of the tied panes, which is the topmost and
	// leftmost, so the answer never depends on the order the tree came out in.
	better := func(a, b cand) bool {
		if (a.shared > 0) != (b.shared > 0) {
			return a.shared > 0
		}
		if a.primary != b.primary {
			return a.primary < b.primary
		}
		if a.shared != b.shared {
			return a.shared > b.shared
		}
		return a.secondary < b.secondary
	}

	var best cand
	found := false
	root.eachLeaf(func(l *Node) bool {
		if l == cur {
			return true
		}
		r := l.rect
		if r.W <= 0 || r.H <= 0 {
			return true
		}
		var primary, shared, secondary int
		switch dir {
		case Left:
			if r.X+r.W > from.X {
				return true
			}
			primary = from.X - (r.X + r.W)
			shared = overlap(r.Y, r.H, from.Y, from.H)
			secondary = abs(r.centerY2() - from.centerY2())
		case Right:
			if r.X < from.X+from.W {
				return true
			}
			primary = r.X - (from.X + from.W)
			shared = overlap(r.Y, r.H, from.Y, from.H)
			secondary = abs(r.centerY2() - from.centerY2())
		case Up:
			if r.Y+r.H > from.Y {
				return true
			}
			primary = from.Y - (r.Y + r.H)
			shared = overlap(r.X, r.W, from.X, from.W)
			secondary = abs(r.centerX2() - from.centerX2())
		case Down:
			if r.Y < from.Y+from.H {
				return true
			}
			primary = r.Y - (from.Y + from.H)
			shared = overlap(r.X, r.W, from.X, from.W)
			secondary = abs(r.centerX2() - from.centerX2())
		}
		if c := (cand{l.Pane, primary, shared, secondary}); !found || better(c, best) {
			best, found = c, true
		}
		return true
	})
	return best.pane
}

// PaneAt returns the pane whose rectangle contains the point, or "".
func (root *Node) PaneAt(x, y int) string {
	pane := ""
	root.eachLeaf(func(l *Node) bool {
		if !l.rect.Contains(x, y) {
			return true
		}
		pane = l.Pane
		return false
	})
	return pane
}

// Direction is a focus movement direction.
type Direction int

// Focus movement directions.
const (
	Left Direction = iota
	Right
	Up
	Down
)

// overlap returns how much of two spans, each given as a start and a length,
// lie against each other. It is negative when they do not meet at all, which
// orders panes that miss the edge entirely by how far they miss it by.
func overlap(aStart, aLen, bStart, bLen int) int {
	end := aStart + aLen
	if o := bStart + bLen; o < end {
		end = o
	}
	start := aStart
	if bStart > start {
		start = bStart
	}
	return end - start
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
