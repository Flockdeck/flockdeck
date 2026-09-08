// Package layout models a tab's panes as a tree of splits and turns that tree
// into concrete screen rectangles.
package layout

import (
	"math"
	"sort"
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

func (r Rect) centerX() int { return r.X + r.W/2 }
func (r Rect) centerY() int { return r.Y + r.H/2 }

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

// weight returns the node's effective weight.
func (n *Node) weight() float64 {
	if n.Weight <= 0 {
		return 1
	}
	return n.Weight
}

// Rect returns the rectangle assigned by the last Compute call.
func (n *Node) Rect() Rect { return n.rect }

// Leaves returns every leaf holding a pane, in left-to-right, top-to-bottom
// tree order.
//
// A split with no children left looks structurally like a leaf but names no
// pane, which is what a tab emptied by a merge is holding. It is skipped, so
// it is not counted as a pane, offered as a drop target, or drawn as an empty
// frame by a client walking the tree.
func (n *Node) Leaves() []*Node {
	if n == nil {
		return nil
	}
	if n.IsLeaf() {
		if n.Pane == "" {
			return nil
		}
		return []*Node{n}
	}
	var out []*Node
	for _, c := range n.Children {
		out = append(out, c.Leaves()...)
	}
	return out
}

// Panes returns the ids of every pane in the tree, in tree order.
func (n *Node) Panes() []string {
	leaves := n.Leaves()
	out := make([]string, 0, len(leaves))
	for _, l := range leaves {
		out = append(out, l.Pane)
	}
	return out
}

// Find returns the leaf holding the given pane, or nil.
func (n *Node) Find(pane string) *Node {
	for _, l := range n.Leaves() {
		if l.Pane == pane {
			return l
		}
	}
	return nil
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

	total := 0.0
	for _, c := range n.Children {
		total += c.weight()
	}
	if total <= 0 {
		total = 1
	}

	if n.Dir == Horizontal {
		seps := separatorWidth * (len(n.Children) - 1)
		avail := r.W - seps
		if avail < len(n.Children) {
			avail = len(n.Children) // degenerate, but never negative
		}
		x := r.X
		used := 0
		for i, c := range n.Children {
			w := int(float64(avail) * c.weight() / total)
			if i == len(n.Children)-1 {
				w = avail - used // absorb rounding into the last child
			}
			if w < 1 {
				w = 1
			}
			c.Compute(Rect{X: x, Y: r.Y, W: w, H: r.H})
			used += w
			x += w + separatorWidth
		}
		return
	}

	y := r.Y
	used := 0
	for i, c := range n.Children {
		h := int(float64(r.H) * c.weight() / total)
		if i == len(n.Children)-1 {
			h = r.H - used
		}
		if h < 1 {
			h = 1
		}
		c.Compute(Rect{X: r.X, Y: y, W: r.W, H: h})
		used += h
		y += h
	}
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
	if root.Find(pane) == nil || root.Find(target) == nil {
		return false
	}
	if !root.Remove(pane) {
		return false
	}
	return root.InsertBeside(target, pane, edge)
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
// The two trees share the space equally. Each side's weights are scaled to add
// up to one share between them, so merging a lone pane with a row of three
// gives that pane half the width rather than a quarter of it. A tree that is
// already a split along dir is flattened into the result instead of nested
// inside it, for the same reason Split and InsertBeside append to an existing
// split: a row of two merged with a row of two is four columns, not a row of
// rows.
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
	root := NewSplit(dir)
	root.Children = append(shareOf(dst, dir), shareOf(src, dir)...)
	return root
}

// shareOf returns the children a tree contributes to a split along dir,
// weighted so that they add up to 1 between them.
func shareOf(n *Node, dir Dir) []*Node {
	if n.IsLeaf() || n.Dir != dir {
		n.Weight = 1
		return []*Node{n}
	}
	total := 0.0
	for _, c := range n.Children {
		total += c.weight()
	}
	if total <= 0 {
		total = 1
	}
	for _, c := range n.Children {
		c.Weight = c.weight() / total
	}
	return n.Children
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
	if total <= 0 {
		total = 1
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

// Count returns the number of panes in the tree.
func (n *Node) Count() int { return len(n.Leaves()) }

// Resize shifts space between the pane and one of its siblings along the given
// axis, by delta cells' worth of weight. A positive delta always grows the
// named pane, whichever sibling pays for it: normally the next one along, or
// the previous one when the pane is last in its split.
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
			a, b := parent.Children[idx], parent.Children[other]
			na, nb := a.weight()+delta, b.weight()-delta
			if na < 0.1 || nb < 0.1 {
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

	type cand struct {
		pane string
		// primary is distance along the movement axis, secondary is
		// misalignment across it, order is the leaf's position in the tree.
		primary, secondary, order int
	}
	var cands []cand

	for i, l := range root.Leaves() {
		if l == cur {
			continue
		}
		r := l.rect
		var primary, secondary int
		switch dir {
		case Left:
			if r.X+r.W > from.X {
				continue
			}
			primary = from.X - (r.X + r.W)
			secondary = abs(r.centerY() - from.centerY())
		case Right:
			if r.X < from.X+from.W {
				continue
			}
			primary = r.X - (from.X + from.W)
			secondary = abs(r.centerY() - from.centerY())
		case Up:
			if r.Y+r.H > from.Y {
				continue
			}
			primary = from.Y - (r.Y + r.H)
			secondary = abs(r.centerX() - from.centerX())
		case Down:
			if r.Y < from.Y+from.H {
				continue
			}
			primary = r.Y - (from.Y + from.H)
			secondary = abs(r.centerX() - from.centerX())
		}
		cands = append(cands, cand{l.Pane, primary, secondary, i})
	}
	if len(cands) == 0 {
		return ""
	}
	// Distance and alignment tie whenever the layout is symmetric about the
	// pane being left, which two stacked panes beside one tall one already
	// are. Fall back to tree order so the winner is the topmost, leftmost of
	// the tied panes rather than whichever the sort happened to leave first.
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].primary != cands[j].primary {
			return cands[i].primary < cands[j].primary
		}
		if cands[i].secondary != cands[j].secondary {
			return cands[i].secondary < cands[j].secondary
		}
		return cands[i].order < cands[j].order
	})
	return cands[0].pane
}

// PaneAt returns the pane whose rectangle contains the point, or "".
func (root *Node) PaneAt(x, y int) string {
	for _, l := range root.Leaves() {
		if l.rect.Contains(x, y) {
			return l.Pane
		}
	}
	return ""
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

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
