package layout

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strconv"
	"testing"
)

func TestSplitAndRemove(t *testing.T) {
	root := NewLeaf("a")

	if !root.Split("a", "b", Horizontal) {
		t.Fatal("split a|b failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("panes = %v, want [a b]", got)
	}

	// Splitting along the same axis should append a sibling, not nest, so that
	// three panes share the row evenly.
	if !root.Split("b", "c", Horizontal) {
		t.Fatal("split b|c failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("panes = %v, want [a b c]", got)
	}
	if len(root.Children) != 3 {
		t.Fatalf("expected a flat 3-way split, got %d children", len(root.Children))
	}

	root.Compute(Rect{X: 0, Y: 0, W: 92, H: 30})
	for _, l := range root.Leaves() {
		if l.Rect().W < 29 || l.Rect().W > 31 {
			t.Fatalf("pane %s width = %d, want roughly even thirds", l.Pane, l.Rect().W)
		}
	}

	// Splitting on the other axis nests.
	if !root.Split("b", "d", Vertical) {
		t.Fatal("split b/d failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b", "d", "c"}) {
		t.Fatalf("panes = %v, want [a b d c]", got)
	}

	if !root.Remove("d") {
		t.Fatal("remove d failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("after remove, panes = %v, want [a b c]", got)
	}
	// The split that held d must have collapsed back into a plain leaf.
	if leaf := root.Find("b"); leaf == nil || !leaf.IsLeaf() {
		t.Fatal("expected b to collapse back to a leaf")
	}
}

func TestComputeFillsExactly(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("a", "c", Vertical)
	root.Compute(Rect{X: 0, Y: 0, W: 80, H: 24})

	// Columns must account for every cell including the separator.
	left := root.Children[0].Rect()
	right := root.Children[1].Rect()
	if left.W+separatorWidth+right.W != 80 {
		t.Fatalf("columns %d + %d + sep != 80", left.W, right.W)
	}
	if right.X != left.X+left.W+separatorWidth {
		t.Fatalf("right column starts at %d, want %d", right.X, left.X+left.W+separatorWidth)
	}

	// Stacked panes must tile their column with no gaps or overlap.
	top := root.Find("a").Rect()
	bottom := root.Find("c").Rect()
	if top.H+bottom.H != 24 {
		t.Fatalf("rows %d + %d != 24", top.H, bottom.H)
	}
	if bottom.Y != top.Y+top.H {
		t.Fatalf("bottom starts at %d, want %d", bottom.Y, top.Y+top.H)
	}
}

func TestNeighborUsesGeometry(t *testing.T) {
	// Layout:  a | b
	//          a | c
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Vertical)
	root.Compute(Rect{X: 0, Y: 0, W: 80, H: 24})

	if got := root.Neighbor("a", Right); got != "b" {
		t.Errorf("right of a = %q, want b (the top-right pane)", got)
	}
	if got := root.Neighbor("b", Down); got != "c" {
		t.Errorf("down from b = %q, want c", got)
	}
	if got := root.Neighbor("c", Left); got != "a" {
		t.Errorf("left of c = %q, want a", got)
	}
	if got := root.Neighbor("a", Left); got != "" {
		t.Errorf("left of a = %q, want no neighbor", got)
	}
	if got := root.Neighbor("b", Up); got != "" {
		t.Errorf("up from b = %q, want no neighbor", got)
	}
}

func TestPaneAtAndSeparators(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Compute(Rect{X: 0, Y: 0, W: 81, H: 10})

	if got := root.PaneAt(1, 1); got != "a" {
		t.Errorf("PaneAt(1,1) = %q, want a", got)
	}
	if got := root.PaneAt(70, 5); got != "b" {
		t.Errorf("PaneAt(70,5) = %q, want b", got)
	}

	seps := root.Separators()
	if len(seps) != 1 {
		t.Fatalf("expected 1 separator, got %d", len(seps))
	}
	// The separator sits in the gap that belongs to no pane.
	if got := root.PaneAt(seps[0].X, 5); got != "" {
		t.Errorf("separator column resolved to pane %q, want none", got)
	}
}

func TestResizeShiftsWeightBetweenSiblings(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Compute(Rect{X: 0, Y: 0, W: 81, H: 10})
	before := root.Find("a").Rect().W

	if !root.Resize("a", Horizontal, 0.5) {
		t.Fatal("resize failed")
	}
	root.Compute(Rect{X: 0, Y: 0, W: 81, H: 10})
	after := root.Find("a").Rect().W
	if after <= before {
		t.Fatalf("width did not grow: %d -> %d", before, after)
	}

	// A pane may not be shrunk out of existence.
	if root.Resize("a", Horizontal, -100) {
		t.Error("expected oversized shrink to be rejected")
	}
}

func TestRemoveLastPaneIsRejected(t *testing.T) {
	root := NewLeaf("a")
	if root.Remove("a") {
		t.Error("removing the only pane should be left to the caller")
	}
	if root.Remove("nope") {
		t.Error("removing an unknown pane should fail")
	}
}

// TestMovePaneReordersARow covers the simplest rearrangement: dragging a pane
// past its neighbour in a row it is already part of.
func TestMovePaneReordersARow(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Horizontal)

	if !root.MovePane("c", "a", EdgeLeft) {
		t.Fatal("moving c to the left of a failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Fatalf("panes = %v, want [c a b]", got)
	}
	if len(root.Children) != 3 {
		t.Fatalf("the row should stay flat, got %d children", len(root.Children))
	}

	if !root.MovePane("c", "b", EdgeRight) {
		t.Fatal("moving c to the right of b failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("panes = %v, want [a b c]", got)
	}
}

// TestMovePaneAcrossAxes checks that dropping onto the top or bottom of a pane
// in a row nests a column there, which is how an arbitrary layout is built up.
func TestMovePaneAcrossAxes(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Horizontal)

	if !root.MovePane("c", "a", EdgeBottom) {
		t.Fatal("moving c below a failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "c", "b"}) {
		t.Fatalf("panes = %v, want [a c b]", got)
	}
	if len(root.Children) != 2 {
		t.Fatalf("the row should now hold two columns, got %d children", len(root.Children))
	}
	col := root.Children[0]
	if col.IsLeaf() || col.Dir != Vertical || len(col.Children) != 2 {
		t.Fatalf("expected a two-pane column beside b, got %+v", col)
	}

	root.Compute(Rect{X: 0, Y: 0, W: 80, H: 40})
	if a, c := root.Find("a").Rect(), root.Find("c").Rect(); a.Y >= c.Y {
		t.Errorf("a at y=%d should sit above c at y=%d", a.Y, c.Y)
	}
}

// TestMovePaneCollapsesTheSplitItLeft makes sure a pane dragged out of a split
// does not leave an empty container behind.
func TestMovePaneCollapsesTheSplitItLeft(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Vertical) // b and c share a column beside a

	if !root.MovePane("c", "a", EdgeLeft) {
		t.Fatal("moving c out of its column failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Fatalf("panes = %v, want [c a b]", got)
	}
	if len(root.Children) != 3 {
		t.Fatalf("the column should have collapsed into the row, got %d children", len(root.Children))
	}
	for _, c := range root.Children {
		if !c.IsLeaf() {
			t.Errorf("child %s should be a leaf after the collapse", c.ID)
		}
	}
}

// TestMovePaneOntoItsOnlySibling covers the case where removing the pane first
// collapses the very split the target was sitting in, which would leave a
// pointer to the target stale.
func TestMovePaneOntoItsOnlySibling(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)

	if !root.MovePane("a", "b", EdgeRight) {
		t.Fatal("moving a past its only sibling failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Fatalf("panes = %v, want [b a]", got)
	}
	if len(root.Children) != 2 {
		t.Fatalf("expected a two-pane row, got %d children", len(root.Children))
	}
}

// TestMovePaneRejectsNonsense guards the drops that cannot mean anything.
func TestMovePaneRejectsNonsense(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)

	if root.MovePane("a", "a", EdgeRight) {
		t.Error("a pane dropped on itself should be refused")
	}
	if root.MovePane("a", "zzz", EdgeRight) {
		t.Error("a drop on a pane that is not in the tree should be refused")
	}
	if root.MovePane("zzz", "a", EdgeRight) {
		t.Error("moving a pane that is not in the tree should be refused")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("a refused move must change nothing, got %v", got)
	}
}

// TestInsertBesideAddsToAnExistingRow covers bringing a pane in from another
// tab, which is an insert rather than a move.
func TestInsertBesideAddsToAnExistingRow(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)

	if !root.InsertBeside("a", "new", EdgeTop) {
		t.Fatal("inserting above a failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"new", "a", "b"}) {
		t.Fatalf("panes = %v, want [new a b]", got)
	}
	if root.Children[0].Dir != Vertical {
		t.Error("a top edge drop should have nested a column")
	}
	if root.InsertBeside("missing", "x", EdgeLeft) {
		t.Error("inserting beside a pane that is not there should be refused")
	}
}

// TestSwapPanesKeepsTheShape checks that exchanging two panes leaves every
// split and weight alone, which is what makes it different from a move.
func TestSwapPanesKeepsTheShape(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Vertical)
	root.Find("a").Weight = 3

	if !root.SwapPanes("a", "c") {
		t.Fatal("swapping a and c failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"c", "b", "a"}) {
		t.Fatalf("panes = %v, want [c b a]", got)
	}
	if w := root.Children[0].weight(); w != 3 {
		t.Errorf("the first slot's weight = %v, want the layout untouched", w)
	}
	if root.SwapPanes("a", "a") {
		t.Error("swapping a pane with itself should be refused")
	}
	if root.SwapPanes("a", "gone") {
		t.Error("swapping with a pane that is not there should be refused")
	}
}

// TestCombineFlattensAndSharesTheSpace covers merging two trees: every pane
// comes across, a split along the same axis is flattened rather than nested,
// and each tree keeps half the room however many panes it brought.
func TestCombineFlattensAndSharesTheSpace(t *testing.T) {
	left := NewLeaf("a")
	left.Split("a", "b", Horizontal)
	left.Split("b", "c", Horizontal)
	right := NewLeaf("d")

	root := Combine(left, right, Horizontal)
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("panes = %v, want every pane of both", got)
	}
	if len(root.Children) != 4 {
		t.Fatalf("children = %d, want the row flattened rather than nested", len(root.Children))
	}
	root.Compute(Rect{W: 1000, H: 100})
	if w := root.Find("d").Rect().W; w < 450 || w > 550 {
		t.Errorf("the lone pane got %d of 1000 columns, want about half", w)
	}
	if a, b := root.Find("a").Rect().W, root.Find("b").Rect().W; a != b {
		t.Errorf("the merged row's panes = %d and %d wide, want its proportions kept", a, b)
	}
}

// TestCombineAcrossAxesNests checks that trees split the other way keep their
// shape inside the merged one instead of being interleaved.
func TestCombineAcrossAxesNests(t *testing.T) {
	left := NewLeaf("a")
	left.Split("a", "b", Vertical)
	right := NewLeaf("c")
	right.Split("c", "d", Vertical)

	root := Combine(left, right, Horizontal)
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("panes = %v, want every pane of both", got)
	}
	if len(root.Children) != 2 {
		t.Fatalf("children = %d, want the two columns side by side", len(root.Children))
	}
	for i, c := range root.Children {
		if c.Dir != Vertical || len(c.Children) != 2 {
			t.Errorf("child %d is not the column it was before the merge", i)
		}
	}
	if got := Combine(nil, right, Horizontal); got != right {
		t.Error("combining with nothing should return the tree unchanged")
	}
}

// TestSetChildWeightsRejectsWithoutMutating covers the weights that arrive from
// a divider drag: a set containing a bad value must be refused whole, not
// applied up to the offending entry.
func TestSetChildWeightsRejectsWithoutMutating(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Horizontal)

	if !root.SetChildWeights([]float64{2, 3, 4}) {
		t.Fatal("a valid set of weights should be accepted")
	}

	for _, bad := range [][]float64{
		{5, 6, 0},           // zero is not a share
		{5, 6, -1},          // nor is a negative one
		{5, 6, math.NaN()},  // nor anything that would poison Compute
		{5, 6, math.Inf(1)}, //
		{5, 6},              // too few for this split
		{5, 6, 7, 8},        // too many
	} {
		if root.SetChildWeights(bad) {
			t.Errorf("SetChildWeights(%v) should have been refused", bad)
		}
		if got := []float64{root.Children[0].Weight, root.Children[1].Weight, root.Children[2].Weight}; !reflect.DeepEqual(got, []float64{2, 3, 4}) {
			t.Fatalf("after refusing %v the weights are %v, want the split untouched", bad, got)
		}
	}

	if NewLeaf("solo").SetChildWeights([]float64{1}) {
		t.Error("a leaf has no children to weight")
	}
}

// TestNeighborBreaksTiesInTreeOrder covers the symmetric case that distance and
// alignment cannot separate: two stacked panes beside one tall one. Whichever
// pane wins, it has to be the same one every time, and the topmost is the one a
// reader of the layout would expect.
//
// The tab is an odd number of rows tall on purpose. Halving each pane's height
// to find its middle loses the half row of a pane an odd number of rows tall,
// and the two stacked panes here are exactly that: the tie is real, and only
// the rounding of the halves made one of them look nearer than the other.
func TestNeighborBreaksTiesInTreeOrder(t *testing.T) {
	// Layout:  a | c      a and b are 499 rows each, so the middle of each
	//          b | c      falls half a row from a row boundary.
	root := NewLeaf("a")
	root.Split("a", "c", Horizontal)
	root.Split("a", "b", Vertical)
	root.Compute(Rect{X: 0, Y: 0, W: 1000, H: 998})

	a, b, c := root.Find("a").Rect(), root.Find("b").Rect(), root.Find("c").Rect()
	if a.H != b.H || a.H%2 == 0 || a.Y+a.H != b.Y || a.Y != c.Y || a.H+b.H != c.H {
		t.Fatalf("this test needs two stacked panes of the same odd height filling c's edge, got %+v %+v %+v", a, b, c)
	}
	// Two panes of the same height, one above the other, are the same distance
	// from the middle of the pane they both face.
	if x, y := abs(a.centerY2()-c.centerY2()), abs(b.centerY2()-c.centerY2()); x != y {
		t.Errorf("a and b are %d and %d from the middle of c, want a genuine tie", x, y)
	}

	for i := 0; i < 50; i++ {
		if got := root.Neighbor("c", Left); got != "a" {
			t.Fatalf("left of c = %q on attempt %d, want a every time", got, i)
		}
	}
}

// TestRemoveFlattensASplitPulledUpByTheCollapse covers the layout where the
// collapse of one split hands its parent a split along the same axis. The row
// has to end up flat, the way it would have if it had been built that way.
func TestRemoveFlattensASplitPulledUpByTheCollapse(t *testing.T) {
	// a | [ b over (c | d) ]
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Vertical)
	root.Split("c", "d", Horizontal)

	if !root.Remove("b") {
		t.Fatal("remove b failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "c", "d"}) {
		t.Fatalf("panes = %v, want [a c d]", got)
	}
	if len(root.Children) != 3 {
		t.Fatalf("the row holds %d children, want the pulled-up row flattened into it", len(root.Children))
	}
	for _, c := range root.Children {
		if !c.IsLeaf() {
			t.Errorf("child %s should be a leaf once the row is flat", c.ID)
		}
	}

	// c and d shared the half of the row that b's column occupied, so they
	// should still have a quarter each against a's half.
	root.Compute(Rect{X: 0, Y: 0, W: 1000, H: 100})
	a, c, d := root.Find("a").Rect().W, root.Find("c").Rect().W, root.Find("d").Rect().W
	if a < 480 || a > 520 {
		t.Errorf("a is %d of 1000 columns, want about half", a)
	}
	if c < 230 || c > 270 || d < 230 || d > 270 {
		t.Errorf("c and d are %d and %d columns, want about a quarter each", c, d)
	}
}

// TestSplitAndInsertRefuseADuplicatePane guards the invariant the rest of the
// package relies on: one leaf per pane. A second leaf for the same id would
// render the same terminal twice and survive the pane being closed.
func TestSplitAndInsertRefuseADuplicatePane(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)

	if root.Split("a", "b", Horizontal) {
		t.Error("splitting in a pane that is already in the tree should be refused")
	}
	if root.Split("a", "", Horizontal) {
		t.Error("splitting in a pane with no id should be refused")
	}
	if root.InsertBeside("a", "b", EdgeRight) {
		t.Error("inserting a pane that is already in the tree should be refused")
	}
	if root.InsertBeside("a", "", EdgeRight) {
		t.Error("inserting a pane with no id should be refused")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("panes = %v, want the refused calls to have changed nothing", got)
	}
	// A leaf naming no pane is not in that list, so the row itself has to be
	// looked at: an empty one would sit there taking its share of the width
	// and drawing nothing.
	if len(root.Children) != 2 {
		t.Fatalf("the row holds %d slots for 2 panes", len(root.Children))
	}

	// A move is still a move: the pane is detached first, so it can go back in.
	if !root.MovePane("b", "a", EdgeLeft) {
		t.Fatal("moving b past a failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Fatalf("panes = %v, want [b a]", got)
	}
}

// TestResizeGrowsTheLastPaneToo checks that a positive delta means the same
// thing wherever the pane sits in its split. The last pane has no next sibling
// and takes its space from the previous one, but it still grows.
func TestResizeGrowsTheLastPaneToo(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Horizontal)
	view := Rect{X: 0, Y: 0, W: 300, H: 10}

	root.Compute(view)
	before := root.Find("c").Rect().W

	if !root.Resize("c", Horizontal, 0.5) {
		t.Fatal("resizing the last pane failed")
	}
	root.Compute(view)
	if after := root.Find("c").Rect().W; after <= before {
		t.Fatalf("the last pane went from %d to %d columns, want it to grow", before, after)
	}

	// And its neighbour is the one that paid for it.
	root.Compute(view)
	if root.Find("b").Rect().W >= root.Find("a").Rect().W {
		t.Error("the pane before the last one should have given up the space")
	}
}

// TestCombineIgnoresAnEmptyTree covers merging with a tab that has already been
// emptied. Its tree is a split with no children, which held no panes but would
// still have claimed half the room.
func TestCombineIgnoresAnEmptyTree(t *testing.T) {
	occupied := NewLeaf("a")
	occupied.Split("a", "b", Horizontal)

	root := Combine(occupied, NewSplit(Vertical), Horizontal)
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("panes = %v, want [a b]", got)
	}
	if len(root.Children) != 2 {
		t.Fatalf("children = %d, want nothing added for the empty tree", len(root.Children))
	}
	root.Compute(Rect{W: 101, H: 10})
	if a, b := root.Find("a").Rect().W, root.Find("b").Rect().W; a+b+separatorWidth != 101 {
		t.Errorf("panes are %d and %d wide, want the whole 101 columns between them", a, b)
	}

	if got := Combine(NewSplit(Vertical), occupied, Horizontal); got != occupied {
		t.Error("merging an empty tree into one with panes should give back the occupied tree")
	}
	if got := Combine(nil, nil, Horizontal); got != nil {
		t.Errorf("combining two nothings gave %v, want nil", got)
	}
}

// TestEmptiedSplitIsNotAPane pins the other half of the same problem: a split
// with no children names no pane, so it must not be counted as one.
func TestEmptiedSplitIsNotAPane(t *testing.T) {
	empty := NewSplit(Horizontal)
	if got := empty.Panes(); len(got) != 0 {
		t.Errorf("panes of an emptied tab = %v, want none", got)
	}
	if got := empty.Count(); got != 0 {
		t.Errorf("count = %d, want 0", got)
	}
	if got := empty.PaneAt(0, 0); got != "" {
		t.Errorf("PaneAt on an emptied tab = %q, want no pane", got)
	}
}

// TestMovePaneOntoItsOwnEdgeKeepsTheWeights covers the drag that ends where it
// started. Rebuilding the split would silently even out the columns the user
// had dragged the divider to set.
func TestMovePaneOntoItsOwnEdgeKeepsTheWeights(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Horizontal)
	if !root.SetChildWeights([]float64{3, 1, 1}) {
		t.Fatal("setting the starting weights failed")
	}

	ids := []string{root.Children[0].ID, root.Children[1].ID, root.Children[2].ID}

	// b is already immediately right of a, and immediately left of c. Each of
	// these is checked on its own: taking the pane out and putting it back
	// gives it the weight of whichever pane it was dropped against, so a pair
	// of drops that lean on opposite neighbours can undo each other's damage
	// and leave the row looking untouched.
	for _, drop := range []struct {
		onto string
		edge Edge
	}{{"a", EdgeRight}, {"c", EdgeLeft}} {
		if !root.MovePane("b", drop.onto, drop.edge) {
			t.Fatalf("dropping b where it already is, against %s, should succeed", drop.onto)
		}
		if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
			t.Fatalf("after the drop against %s, panes = %v, want [a b c]", drop.onto, got)
		}
		got := []float64{root.Children[0].Weight, root.Children[1].Weight, root.Children[2].Weight}
		if !reflect.DeepEqual(got, []float64{3, 1, 1}) {
			t.Errorf("after the drop against %s, weights = %v, want the divider left where it was", drop.onto, got)
		}
		// Nothing was rebuilt, so a divider drag already in flight still names
		// a node that is there.
		for i, c := range root.Children {
			if c.ID != ids[i] {
				t.Errorf("after the drop against %s, slot %d is node %s, was %s", drop.onto, i, c.ID, ids[i])
			}
		}
	}

	// A drop on the other edge of the same neighbour is still a real move.
	if !root.MovePane("b", "a", EdgeLeft) {
		t.Fatal("moving b to the left of a failed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Fatalf("panes = %v, want [b a c]", got)
	}
}

// TestComputeSpreadsTheRounding checks that panes of equal weight come out
// within a cell of each other however the space divides, rather than the last
// one swallowing everything the truncation left behind.
func TestComputeSpreadsTheRounding(t *testing.T) {
	root := NewLeaf("p0")
	for i := 1; i < 10; i++ {
		if !root.Split("p"+strconv.Itoa(i-1), "p"+strconv.Itoa(i), Horizontal) {
			t.Fatalf("building pane %d failed", i)
		}
	}
	// 10 panes and 9 separators leave 105 columns to divide ten ways.
	root.Compute(Rect{X: 0, Y: 0, W: 114, H: 30})

	lo, hi, sum := 1<<30, 0, 0
	for _, l := range root.Leaves() {
		w := l.Rect().W
		sum += w
		if w < lo {
			lo = w
		}
		if w > hi {
			hi = w
		}
	}
	if hi-lo > 1 {
		t.Errorf("widths run from %d to %d, want equal panes within a cell of each other", lo, hi)
	}
	if sum+9*separatorWidth != 114 {
		t.Errorf("panes and separators cover %d columns, want all 114", sum+9*separatorWidth)
	}

	// The same for a stack, which has no separators to account for.
	col := NewLeaf("q0")
	for i := 1; i < 7; i++ {
		col.Split("q"+strconv.Itoa(i-1), "q"+strconv.Itoa(i), Vertical)
	}
	col.Compute(Rect{X: 0, Y: 0, W: 20, H: 100})
	lo, hi, sum = 1<<30, 0, 0
	for _, l := range col.Leaves() {
		h := l.Rect().H
		sum += h
		if h < lo {
			lo = h
		}
		if h > hi {
			hi = h
		}
	}
	if hi-lo > 1 {
		t.Errorf("heights run from %d to %d, want equal panes within a cell of each other", lo, hi)
	}
	if sum != 100 {
		t.Errorf("rows cover %d of 100 lines", sum)
	}
}

// checkInvariants asserts everything that must hold of any tree the package
// hands back, whatever sequence of edits produced it. hist names that sequence
// so a failure points at the operation that broke it.
func checkInvariants(t *testing.T, root *Node, hist string) {
	t.Helper()

	seen := map[string]bool{}
	for _, p := range root.Panes() {
		if p == "" {
			t.Fatalf("%s: a leaf names no pane", hist)
		}
		if seen[p] {
			t.Fatalf("%s: pane %q appears twice", hist, p)
		}
		seen[p] = true
	}

	var walk func(n *Node)
	walk = func(n *Node) {
		if n.IsLeaf() {
			return
		}
		// A split with one child is a container that should have collapsed,
		// and a split nested in another along the same axis is the shape every
		// operation here goes out of its way not to build.
		if len(n.Children) < 2 {
			t.Fatalf("%s: split %s has %d children", hist, n.ID, len(n.Children))
		}
		for _, c := range n.Children {
			if !c.IsLeaf() && c.Dir == n.Dir {
				t.Fatalf("%s: split %s holds a split along its own axis", hist, n.ID)
			}
			walk(c)
		}
	}
	walk(root)

	// The panes must tile the tab: no overlaps, and no cell belonging to
	// neither a pane nor a separator.
	const w, h = 61, 25
	root.Compute(Rect{X: 0, Y: 0, W: w, H: h})
	if !roomFor(root) {
		// A split with more children than cells cannot tile anything: every
		// pane is owed a cell and there are not enough to go round. Nothing
		// below is a promise the package can keep there.
		return
	}
	leaves := root.Leaves()
	for i := range leaves {
		for j := i + 1; j < len(leaves); j++ {
			a, b := leaves[i].Rect(), leaves[j].Rect()
			if a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
				t.Fatalf("%s: %s at %+v overlaps %s at %+v", hist, leaves[i].Pane, a, leaves[j].Pane, b)
			}
		}
	}
	rule := map[[2]int]bool{}
	for _, s := range root.Separators() {
		if s.W != separatorWidth || s.H < 1 {
			t.Fatalf("%s: separator %+v is not a rule between two panes", hist, s)
		}
		if s.X < 0 || s.X+s.W > w || s.Y < 0 || s.Y+s.H > h {
			t.Fatalf("%s: separator %+v hangs off the tab", hist, s)
		}
		for y := s.Y; y < s.Y+s.H; y++ {
			for x := s.X; x < s.X+s.W; x++ {
				// A rule drawn over a terminal would overwrite what it says.
				if p := root.PaneAt(x, y); p != "" {
					t.Fatalf("%s: separator %+v runs through pane %s at %d,%d", hist, s, p, x, y)
				}
				rule[[2]int{x, y}] = true
			}
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if root.PaneAt(x, y) == "" && !rule[[2]int{x, y}] {
				t.Fatalf("%s: cell %d,%d belongs to neither a pane nor a separator", hist, x, y)
			}
		}
	}
}

// randomSplit returns one of the tree's split nodes, or nil if it holds only
// a single pane.
func randomSplit(root *Node, rnd *rand.Rand) *Node {
	var splits []*Node
	var walk func(*Node)
	walk = func(n *Node) {
		if n.IsLeaf() {
			return
		}
		splits = append(splits, n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	if len(splits) == 0 {
		return nil
	}
	return splits[rnd.Intn(len(splits))]
}

// roomFor reports whether every split in the tree was given at least one cell
// along its axis for each of its children.
func roomFor(n *Node) bool {
	if n.IsLeaf() {
		return true
	}
	if n.Dir == Horizontal {
		if n.Rect().W-separatorWidth*(len(n.Children)-1) < len(n.Children) {
			return false
		}
	} else if n.Rect().H < len(n.Children) {
		return false
	}
	for _, c := range n.Children {
		if !roomFor(c) {
			return false
		}
	}
	return true
}

// TestTreeInvariantsUnderRandomEditing drives the tree through sequences of
// splits, removals, drags and merges that no hand-written case would think of,
// checking after every one that the tree is still something the app can draw.
func TestTreeInvariantsUnderRandomEditing(t *testing.T) {
	for seed := int64(0); seed < 120; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		root := NewLeaf("p0")
		hist := "p0"
		next := 1

		for step := 0; step < 14; step++ {
			panes := root.Panes()
			victim := panes[rnd.Intn(len(panes))]
			switch rnd.Intn(5) {
			case 4:
				// A divider dragged hard against one end, which is the case
				// the even weights every other operation produces never
				// reaches: several panes are then owed less than a cell each.
				split := randomSplit(root, rnd)
				if split == nil {
					break
				}
				weights := make([]float64, len(split.Children))
				for i := range weights {
					weights[i] = math.Pow(10, rnd.Float64()*4-2)
				}
				hist += fmt.Sprintf("; drag %s to %v", split.ID, weights)
				if !split.SetChildWeights(weights) {
					t.Fatalf("%s: the drag was refused", hist)
				}
			case 0:
				id := "p" + strconv.Itoa(next)
				next++
				dir := Dir(rnd.Intn(2))
				hist += fmt.Sprintf("; split %s into %s along %d", victim, id, dir)
				root.Split(victim, id, dir)
			case 1:
				hist += "; remove " + victim
				root.Remove(victim)
			case 2:
				target, edge := panes[rnd.Intn(len(panes))], Edge(rnd.Intn(4))
				hist += fmt.Sprintf("; move %s onto %s edge %d", victim, target, edge)
				root.MovePane(victim, target, edge)
			case 3:
				id := "p" + strconv.Itoa(next)
				next++
				edge := Edge(rnd.Intn(4))
				hist += fmt.Sprintf("; insert %s beside %s edge %d", id, victim, edge)
				root.InsertBeside(victim, id, edge)
			}
			checkInvariants(t, root, hist)
		}

		// And merging a tab in has to leave a tree that is just as sound.
		other := NewLeaf(fmt.Sprintf("q%d", seed))
		for i := 0; i < rnd.Intn(4); i++ {
			other.Split(other.Panes()[rnd.Intn(other.Count())], fmt.Sprintf("q%d-%d", seed, i), Dir(rnd.Intn(2)))
		}
		checkInvariants(t, Combine(root, other, Dir(rnd.Intn(2))), hist+"; merged a tab in")
	}
}

// adjacent reports whether r sits directly against from on the given side,
// sharing some of the edge between them. Horizontally adjacent panes have the
// separator column between them; stacked ones touch outright.
func adjacent(from, r Rect, d Direction) bool {
	switch d {
	case Left:
		return r.X+r.W+separatorWidth == from.X && r.Y < from.Y+from.H && from.Y < r.Y+r.H
	case Right:
		return from.X+from.W+separatorWidth == r.X && r.Y < from.Y+from.H && from.Y < r.Y+r.H
	case Up:
		return r.Y+r.H == from.Y && r.X < from.X+from.W && from.X < r.X+r.W
	default:
		return from.Y+from.H == r.Y && r.X < from.X+from.W && from.X < r.X+r.W
	}
}

// TestNeighborAlwaysLandsOnAPaneThatIsThere is the property behind moving focus
// with the arrow keys: pressing right must put the cursor in a pane that is
// genuinely against the right-hand edge of this one, and must not report there
// is nothing that way while such a pane exists.
func TestNeighborAlwaysLandsOnAPaneThatIsThere(t *testing.T) {
	dirs := []Direction{Left, Right, Up, Down}

	for seed := int64(0); seed < 2500; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		root := NewLeaf("p0")
		next := 1
		for step := 0; step < 12; step++ {
			panes := root.Panes()
			victim := panes[rnd.Intn(len(panes))]
			switch rnd.Intn(4) {
			case 0:
				root.Split(victim, "p"+strconv.Itoa(next), Dir(rnd.Intn(2)))
				next++
			case 1:
				root.Remove(victim)
			case 2:
				root.MovePane(victim, panes[rnd.Intn(len(panes))], Edge(rnd.Intn(4)))
			case 3:
				// Dividers dragged hard against one end, which is what puts
				// the panes of one row out of step with the row below it.
				split := randomSplit(root, rnd)
				if split == nil {
					break
				}
				weights := make([]float64, len(split.Children))
				for i := range weights {
					weights[i] = math.Pow(10, rnd.Float64()*3-1.5)
				}
				split.SetChildWeights(weights)
			}
		}
		root.Compute(Rect{X: 0, Y: 0, W: 997, H: 401})
		if !roomFor(root) {
			continue // no layout of this tab is right; see checkInvariants
		}

		for _, l := range root.Leaves() {
			from := l.Rect()
			for _, d := range dirs {
				got := root.Neighbor(l.Pane, d)
				against := []*Node(nil)
				for _, other := range root.Leaves() {
					if other != l && adjacent(from, other.Rect(), d) {
						against = append(against, other)
					}
				}
				if got == "" {
					if len(against) > 0 {
						t.Fatalf("seed %d: nothing is %d of %s, but %s is against that edge", seed, d, l.Pane, against[0].Pane)
					}
					continue
				}
				chosen := root.Find(got)
				if len(against) == 0 {
					// Nothing is against this edge — a rule between two other
					// panes runs along it — so the nearest pane facing it is
					// the best answer there is.
					continue
				}
				if !adjacent(from, chosen.Rect(), d) {
					t.Fatalf("seed %d: %d of %s at %+v is %s at %+v, which does not touch it", seed, d, l.Pane, from, got, chosen.Rect())
				}
				// Of the panes against that edge, the answer must be one of
				// those sharing the most of it: that is the pane the user is
				// looking at when they press the key.
				mine := edgeShared(from, chosen.Rect(), d)
				for _, other := range against {
					if theirs := edgeShared(from, other.Rect(), d); theirs > mine {
						t.Fatalf("seed %d: %d of %s chose %s facing %d of its edge, but %s faces %d",
							seed, d, l.Pane, got, mine, other.Pane, theirs)
					}
				}
			}
		}
	}
}

// edgeShared returns how much of the edge between from and r the two share.
func edgeShared(from, r Rect, d Direction) int {
	if d == Left || d == Right {
		return overlap(r.Y, r.H, from.Y, from.H)
	}
	return overlap(r.X, r.W, from.X, from.W)
}

// TestNeighborNeedsGeometry covers being asked to move focus before anything
// has been measured. Every rectangle is empty then, so every pane looks
// adjacent to every other, and an answer would land the focus at random.
func TestNeighborNeedsGeometry(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Vertical)

	for _, d := range []Direction{Left, Right, Up, Down} {
		if got := root.Neighbor("a", d); got != "" {
			t.Errorf("before Compute, %d of a = %q, want no answer", d, got)
		}
	}

	root.Compute(Rect{X: 0, Y: 0, W: 80, H: 24})
	if got := root.Neighbor("a", Right); got != "b" {
		t.Errorf("once measured, right of a = %q, want b", got)
	}
}

// TestResizeIsIndependentOfTheWeightScale covers the same keypress landing on
// two rows that look identical but whose weights are a thousand times apart,
// which is what merging tabs leaves behind. It has to move both dividers by
// the same visible amount.
func TestResizeIsIndependentOfTheWeightScale(t *testing.T) {
	build := func(scale float64) *Node {
		root := NewLeaf("a")
		root.Split("a", "b", Horizontal)
		root.Split("b", "c", Horizontal)
		if !root.SetChildWeights([]float64{scale, scale, scale}) {
			t.Fatalf("scaling the row by %v failed", scale)
		}
		return root
	}
	view := Rect{X: 0, Y: 0, W: 302, H: 10}

	plain, tiny := build(1), build(0.001)
	if !plain.Resize("a", Horizontal, 0.4) || !tiny.Resize("a", Horizontal, 0.4) {
		t.Fatal("resize failed")
	}
	plain.Compute(view)
	tiny.Compute(view)

	for _, pane := range []string{"a", "b", "c"} {
		want, got := plain.Find(pane).Rect().W, tiny.Find(pane).Rect().W
		if want != got {
			t.Errorf("pane %s is %d columns in the scaled-down row and %d in the plain one", pane, got, want)
		}
	}
	// And it actually moved: a took space from b, c is untouched.
	if a, c := plain.Find("a").Rect().W, plain.Find("c").Rect().W; a <= c {
		t.Errorf("a is %d columns and c is %d, want a to have grown", a, c)
	}
}

// TestComputeIsProportionalWithinACell states what the weights actually
// promise: every pane gets its share of the space to within one cell, whatever
// the weights are and however the space divides. Rounding is allowed to lose a
// cell here and there, but never to accumulate.
func TestComputeIsProportionalWithinACell(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		n := 2 + rnd.Intn(6)
		dir := Dir(rnd.Intn(2))

		root := NewLeaf("p0")
		for i := 1; i < n; i++ {
			root.Split("p"+strconv.Itoa(i-1), "p"+strconv.Itoa(i), dir)
		}
		weights := make([]float64, n)
		total := 0.0
		for i := range weights {
			weights[i] = 0.2 + rnd.Float64()*5
			total += weights[i]
		}
		if !root.SetChildWeights(weights) {
			t.Fatalf("seed %d: weights refused", seed)
		}

		size := 200 + rnd.Intn(800)
		// A horizontal split spends a column on each separator; a stack does not.
		avail := float64(size)
		if dir == Horizontal {
			root.Compute(Rect{W: size, H: 40})
			avail -= float64(n - 1)
		} else {
			root.Compute(Rect{W: 40, H: size})
		}

		covered := 0
		for i, c := range root.Children {
			got := c.Rect().W
			if dir == Vertical {
				got = c.Rect().H
			}
			covered += got
			want := avail * weights[i] / total
			if math.Abs(want-float64(got)) > 1 {
				t.Fatalf("seed %d: pane %d got %d cells, want %.2f", seed, i, got, want)
			}
		}
		if float64(covered) != avail {
			t.Fatalf("seed %d: panes cover %d cells of %v", seed, covered, avail)
		}
	}
}

// TestDividerDragLandsWhereItWasDropped follows the path a drag actually takes:
// the browser names the split by node id, sends the shares it wants, and
// expects the next layout to match. FindNode has to reach nested splits, and
// the weights have to survive into Compute.
func TestDividerDragLandsWhereItWasDropped(t *testing.T) {
	// a | [ b over c ]
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Vertical)
	column := root.Children[1]

	if got := root.FindNode(root.ID); got != root {
		t.Error("FindNode should reach the root itself")
	}
	if got := root.FindNode(column.ID); got != column {
		t.Fatal("FindNode should reach a nested split")
	}
	if got := root.FindNode("nosuchnode"); got != nil {
		t.Errorf("FindNode of an unknown id = %v, want nil", got)
	}
	if got := root.FindNode(""); got != nil {
		t.Errorf("FindNode of no id = %v, want nil", got)
	}

	// Drag the outer divider to a quarter, and the inner one to a fifth.
	if !root.FindNode(root.ID).SetChildWeights([]float64{1, 3}) {
		t.Fatal("setting the row's weights failed")
	}
	if !root.FindNode(column.ID).SetChildWeights([]float64{1, 4}) {
		t.Fatal("setting the column's weights failed")
	}

	root.Compute(Rect{X: 0, Y: 0, W: 401, H: 100})
	// 400 columns to share once the separator is paid for.
	if w := root.Find("a").Rect().W; w != 100 {
		t.Errorf("a is %d columns wide, want a quarter of 400", w)
	}
	if h := root.Find("b").Rect().H; h != 20 {
		t.Errorf("b is %d lines tall, want a fifth of 100", h)
	}
	if h := root.Find("c").Rect().H; h != 80 {
		t.Errorf("c is %d lines tall, want the other four fifths", h)
	}
	// Both panes of the column keep the width the outer drag gave it.
	if b, c := root.Find("b").Rect().W, root.Find("c").Rect().W; b != 300 || c != 300 {
		t.Errorf("the column's panes are %d and %d wide, want 300 each", b, c)
	}
}

// remaining returns ps with drop taken out, keeping the order of the rest.
func remaining(ps []string, drop string) []string {
	out := []string{}
	for _, p := range ps {
		if p != drop {
			out = append(out, p)
		}
	}
	return out
}

// TestClosingOrDraggingOnePaneLeavesTheRestInOrder pins a promise the UI makes
// everywhere it walks the tree: the pane list, the agent list and the history
// view are all in Panes order, so closing one pane or dragging one somewhere
// else must not shuffle the panes nobody touched.
func TestClosingOrDraggingOnePaneLeavesTheRestInOrder(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		root := NewLeaf("p0")
		next := 1

		for step := 0; step < 18; step++ {
			panes := root.Panes()
			victim := panes[rnd.Intn(len(panes))]
			before := append([]string(nil), panes...)

			switch rnd.Intn(3) {
			case 0:
				root.Split(victim, "p"+strconv.Itoa(next), Dir(rnd.Intn(2)))
				next++
			case 1:
				if !root.Remove(victim) {
					continue
				}
				if got, want := root.Panes(), remaining(before, victim); !reflect.DeepEqual(got, want) {
					t.Fatalf("seed %d: closing %s left %v, want %v", seed, victim, got, want)
				}
			case 2:
				target, edge := panes[rnd.Intn(len(panes))], Edge(rnd.Intn(4))
				if !root.MovePane(victim, target, edge) {
					continue
				}
				got := remaining(root.Panes(), victim)
				want := remaining(before, victim)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("seed %d: dragging %s onto %s left the others as %v, want %v", seed, victim, target, got, want)
				}
				// And it has to be on the side of the target it was dropped
				// on, whatever the split it came out of did on the way.
				root.Compute(Rect{X: 0, Y: 0, W: 401, H: 197})
				if !roomFor(root) {
					continue
				}
				if m, o := root.Find(victim).Rect(), root.Find(target).Rect(); !onEdge(m, o, edge) {
					t.Fatalf("seed %d: dragged %s onto edge %d of %s, and it landed at %+v against %+v",
						seed, victim, edge, target, m, o)
				}
			}
		}
	}
}

// onEdge reports whether dropped ended up on the given side of onto.
func onEdge(dropped, onto Rect, edge Edge) bool {
	switch edge {
	case EdgeLeft:
		return dropped.X+dropped.W <= onto.X
	case EdgeRight:
		return onto.X+onto.W <= dropped.X
	case EdgeTop:
		return dropped.Y+dropped.H <= onto.Y
	default:
		return onto.Y+onto.H <= dropped.Y
	}
}

// TestClosingPanesOneAtATimeEndsWithTheLastOneFillingTheTab walks a layout all
// the way down, which is what closing panes until one is left does.
func TestClosingPanesOneAtATimeEndsWithTheLastOneFillingTheTab(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Vertical)
	root.Split("c", "d", Horizontal)

	for _, closing := range []string{"b", "d", "c"} {
		if !root.Remove(closing) {
			t.Fatalf("closing %s failed", closing)
		}
		if root.Find(closing) != nil {
			t.Fatalf("%s is still in the tree", closing)
		}
	}

	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("panes = %v, want just [a]", got)
	}
	if !root.IsLeaf() {
		t.Error("the last pane should be the whole tree, not a split around it")
	}
	if root.Remove("a") {
		t.Error("closing the last pane is the caller's decision, not the tree's")
	}

	root.Compute(Rect{X: 0, Y: 0, W: 80, H: 24})
	if got := root.Find("a").Rect(); got != (Rect{X: 0, Y: 0, W: 80, H: 24}) {
		t.Errorf("the surviving pane got %+v, want the whole tab", got)
	}
	if seps := root.Separators(); len(seps) != 0 {
		t.Errorf("a single pane needs no separators, got %d", len(seps))
	}
}

// TestEveryDropEdgePutsThePaneWhereItWasDropped checks the four drop zones by
// where the pane actually ends up on screen, not just where it lands in the
// tree: a left drop has to produce a pane to the left, whether it nests a new
// split or joins a row that is already there.
func TestEveryDropEdgePutsThePaneWhereItWasDropped(t *testing.T) {
	view := Rect{X: 0, Y: 0, W: 400, H: 200}

	for _, tc := range []struct {
		name string
		edge Edge
		// want reports whether the dropped pane sits correctly against the
		// pane it was dropped on.
		want func(dropped, onto Rect) bool
	}{
		{"left", EdgeLeft, func(d, o Rect) bool { return d.X+d.W <= o.X }},
		{"right", EdgeRight, func(d, o Rect) bool { return o.X+o.W <= d.X }},
		{"top", EdgeTop, func(d, o Rect) bool { return d.Y+d.H <= o.Y }},
		{"bottom", EdgeBottom, func(d, o Rect) bool { return o.Y+o.H <= d.Y }},
	} {
		// Dropped on a lone pane, which has to nest a split around it.
		lone := NewLeaf("target")
		if !lone.InsertBeside("target", "dropped", tc.edge) {
			t.Fatalf("%s: dropping on a lone pane failed", tc.name)
		}
		lone.Compute(view)
		if !tc.want(lone.Find("dropped").Rect(), lone.Find("target").Rect()) {
			t.Errorf("%s: dropped at %+v is not on that side of %+v",
				tc.name, lone.Find("dropped").Rect(), lone.Find("target").Rect())
		}

		// Dropped on a pane in the middle of a row and of a column, where the
		// same-axis case joins the existing split instead of nesting.
		for _, dir := range []Dir{Horizontal, Vertical} {
			row := NewLeaf("first")
			row.Split("first", "target", dir)
			row.Split("target", "last", dir)

			if !row.InsertBeside("target", "dropped", tc.edge) {
				t.Fatalf("%s: dropping into a split along %d failed", tc.name, dir)
			}
			row.Compute(view)
			if !tc.want(row.Find("dropped").Rect(), row.Find("target").Rect()) {
				t.Errorf("%s in a split along %d: dropped at %+v is not on that side of %+v",
					tc.name, dir, row.Find("dropped").Rect(), row.Find("target").Rect())
			}
			// The panes it was not dropped between must be untouched.
			if got := remaining(row.Panes(), "dropped"); !reflect.DeepEqual(got, []string{"first", "target", "last"}) {
				t.Errorf("%s in a split along %d: the rest of the panes are %v", tc.name, dir, got)
			}
		}
	}
}

// TestCombineKeepsEachSidesProportions covers the weights a merge produces
// rather than just its shape: each tab gets half the room, and inside its half
// its panes stay in the proportions the user had dragged them to.
func TestCombineKeepsEachSidesProportions(t *testing.T) {
	// A row dragged to 3:1, merged with a tab holding a single pane.
	row := NewLeaf("wide")
	row.Split("wide", "narrow", Horizontal)
	if !row.SetChildWeights([]float64{3, 1}) {
		t.Fatal("setting the row's weights failed")
	}
	root := Combine(row, NewLeaf("lone"), Horizontal)

	root.Compute(Rect{W: 1002, H: 100}) // 1000 columns once the rules are paid for
	wide, narrow, lone := root.Find("wide").Rect().W, root.Find("narrow").Rect().W, root.Find("lone").Rect().W
	if lone < 495 || lone > 505 {
		t.Errorf("the lone pane got %d of 1000 columns, want about half", lone)
	}
	if wide < 370 || wide > 380 || narrow < 120 || narrow > 130 {
		t.Errorf("the merged row is %d and %d wide, want its 3:1 kept inside its half", wide, narrow)
	}

	// Two columns merged side by side each keep their own stacking.
	left := NewLeaf("a")
	left.Split("a", "b", Vertical)
	left.SetChildWeights([]float64{1, 3})
	right := NewLeaf("c")
	right.Split("c", "d", Vertical)

	pair := Combine(left, right, Horizontal)
	pair.Compute(Rect{W: 201, H: 200})
	for _, pane := range []string{"a", "b", "c", "d"} {
		if w := pair.Find(pane).Rect().W; w != 100 {
			t.Errorf("pane %s is %d columns wide, want the two columns to halve the row", pane, w)
		}
	}
	if a, b := pair.Find("a").Rect().H, pair.Find("b").Rect().H; a != 50 || b != 150 {
		t.Errorf("the dragged column came back as %d and %d lines, want 50 and 150", a, b)
	}
	if c, d := pair.Find("c").Rect().H, pair.Find("d").Rect().H; c != 100 || d != 100 {
		t.Errorf("the even column came back as %d and %d lines, want an even split", c, d)
	}
}

// TestCombineHalvesTheRoomEachTimeATabIsFoldedIn states what folding tabs in
// one at a time actually produces. Each merge is an even split between the two
// trees, which is right for a single drag, but repeated it compounds: the last
// tab in takes half the tab and the first ones are squeezed to nothing. A
// caller gathering a whole project this way needs to share the room out across
// all the tabs at once instead.
func TestCombineHalvesTheRoomEachTimeATabIsFoldedIn(t *testing.T) {
	root := NewLeaf("t0")
	for i := 1; i < 5; i++ {
		root = Combine(root, NewLeaf("t"+strconv.Itoa(i)), Horizontal)
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"t0", "t1", "t2", "t3", "t4"}) {
		t.Fatalf("panes = %v, want every tab in the order it was folded in", got)
	}
	if len(root.Children) != 5 {
		t.Fatalf("children = %d, want one flat row", len(root.Children))
	}

	root.Compute(Rect{W: 1004, H: 100}) // 1000 columns once the four rules are paid for
	widths := make([]int, 0, 5)
	for _, l := range root.Leaves() {
		widths = append(widths, l.Rect().W)
	}
	// 1/16, 1/16, 1/8, 1/4, 1/2 of the room.
	if !reflect.DeepEqual(widths, []int{62, 63, 125, 250, 500}) {
		t.Errorf("widths = %v, want the halving this merge compounds to", widths)
	}
}

// facing returns the direction that undoes d.
func facing(d Direction) Direction {
	switch d {
	case Left:
		return Right
	case Right:
		return Left
	case Up:
		return Down
	default:
		return Up
	}
}

// TestKeyboardMoveReversesBetweenPanesOfEqualSpan pins the promise behind
// moving a pane with the arrow keys: swap it past its neighbour, press the
// opposite arrow, and the layout is back as it was.
//
// That holds whenever the two panes span the same width across a vertical move
// or the same height across a horizontal one, which is every evenly split
// layout. It cannot hold in general: a pane moved into a slot twice as wide
// has several panes under it on the way back, and geometry alone has no memory
// of which one it came from. So the guarantee is asserted where it applies and
// the rest is left alone rather than pretended about.
func TestKeyboardMoveReversesBetweenPanesOfEqualSpan(t *testing.T) {
	view := Rect{X: 0, Y: 0, W: 997, H: 401}
	checked := 0

	for seed := int64(0); seed < 200; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		root := NewLeaf("p0")
		next := 1
		for step := 0; step < 12; step++ {
			panes := root.Panes()
			victim := panes[rnd.Intn(len(panes))]
			switch rnd.Intn(3) {
			case 0:
				root.Split(victim, "p"+strconv.Itoa(next), Dir(rnd.Intn(2)))
				next++
			case 1:
				root.Remove(victim)
			case 2:
				root.MovePane(victim, panes[rnd.Intn(len(panes))], Edge(rnd.Intn(4)))
			}
		}
		root.Compute(view)

		for _, l := range root.Leaves() {
			for _, d := range []Direction{Left, Right, Up, Down} {
				me := l.Pane
				other := root.Neighbor(me, d)
				if other == "" {
					continue
				}
				mine, theirs := l.Rect(), root.Find(other).Rect()
				equalSpan := mine.Y == theirs.Y && mine.H == theirs.H
				if d == Up || d == Down {
					equalSpan = mine.X == theirs.X && mine.W == theirs.W
				}
				if !equalSpan {
					continue
				}
				checked++

				root.SwapPanes(me, other)
				root.Compute(view)
				if back := root.Neighbor(me, facing(d)); back != other {
					t.Fatalf("seed %d: moved %s past %s going %d; the opposite arrow found %q instead",
						seed, me, other, d, back)
				}
				root.SwapPanes(me, other) // put the layout back for the next case
				root.Compute(view)
			}
		}
	}

	if checked < 100 {
		t.Fatalf("only %d moves were of equal span, too few to mean anything", checked)
	}
}

// TestNeighborPrefersTheWiderSharedEdge covers moving focus out of a pane that
// faces two panes of very different sizes. The one a person is looking at is
// the one most of the edge is against; picking by how well the centres line up
// instead hands the focus to a sliver in the corner.
func TestNeighborPrefersTheWiderSharedEdge(t *testing.T) {
	// Layout:  a | c      c is nine tenths of the right-hand column,
	//          b | c      so b, the lower-left pane, faces mostly c
	//          b | d      and only clips the corner of d.
	root := NewLeaf("a")
	root.Split("a", "c", Horizontal)
	root.Split("a", "b", Vertical)
	root.Split("c", "d", Vertical)

	right, _ := root.parentOf(root.Find("d"))
	if !right.SetChildWeights([]float64{9, 1}) {
		t.Fatal("could not make the right-hand column lopsided")
	}
	root.Compute(Rect{X: 0, Y: 0, W: 101, H: 100})

	b, c, d := root.Find("b").Rect(), root.Find("c").Rect(), root.Find("d").Rect()
	if d.H >= b.H || c.Y+c.H != d.Y {
		t.Fatalf("this test needs a sliver under a tall pane, got c=%+v d=%+v b=%+v", c, d, b)
	}
	if abs(d.centerY2()-b.centerY2()) >= abs(c.centerY2()-b.centerY2()) {
		t.Fatal("this test needs the sliver to be the better centre match")
	}

	if got := root.Neighbor("b", Right); got != "c" {
		t.Errorf("right of b = %q, want c: b shares %d rows with c and %d with d",
			got, c.Y+c.H-b.Y, b.Y+b.H-d.Y)
	}
	if got := root.Neighbor("a", Right); got != "c" {
		t.Errorf("right of a = %q, want c", got)
	}
	if got := root.Neighbor("d", Left); got != "b" {
		t.Errorf("left of d = %q, want b", got)
	}
	if got := root.Neighbor("c", Left); got != "a" {
		t.Errorf("left of c = %q, want a", got)
	}
}

// benchTree builds a tree of n panes by splitting the panes in turn, along
// alternating axes, which is roughly the shape a tab grows into.
func benchTree(n int) *Node {
	root := NewLeaf("p0")
	for i := 1; i < n; i++ {
		panes := root.Panes()
		root.Split(panes[i%len(panes)], "p"+strconv.Itoa(i), Dir(i%2))
	}
	root.Compute(Rect{X: 0, Y: 0, W: 997, H: 401})
	return root
}

func BenchmarkCompute(b *testing.B) {
	root := benchTree(16)
	view := Rect{X: 0, Y: 0, W: 997, H: 401}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		root.Compute(view)
	}
}

func BenchmarkNeighbor(b *testing.B) {
	root := benchTree(16)
	panes := root.Panes()
	dirs := []Direction{Left, Right, Up, Down}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		root.Neighbor(panes[i%len(panes)], dirs[i%len(dirs)])
	}
}

func BenchmarkFind(b *testing.B) {
	root := benchTree(16)
	panes := root.Panes()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		root.Find(panes[i%len(panes)])
	}
}

func BenchmarkPaneAt(b *testing.B) {
	root := benchTree(16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		root.PaneAt(i%997, i%401)
	}
}

func BenchmarkCount(b *testing.B) {
	root := benchTree(16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		root.Count()
	}
}

// TestLookupsDoNotAllocate pins the walks a client leans on hardest. Finding a
// pane, counting the panes, resolving a click and moving the focus all happen
// several times per keystroke and per redraw, on every open tab; none of them
// needs to build a list of the tree to answer.
func TestLookupsDoNotAllocate(t *testing.T) {
	root := benchTree(16)
	panes := root.Panes()
	last := panes[len(panes)-1]

	for name, fn := range map[string]func(){
		"Find":     func() { root.Find(last) },
		"Count":    func() { root.Count() },
		"PaneAt":   func() { root.PaneAt(500, 200) },
		"Neighbor": func() { root.Neighbor(last, Left) },
	} {
		if got := testing.AllocsPerRun(50, fn); got != 0 {
			t.Errorf("%s allocates %.0f times per call, want none", name, got)
		}
	}
}

// TestComputeStaysInsideTheBoxWhenAWeightIsTiny covers a divider dragged hard
// against one end. The pane on the losing side is then owed a fraction of a
// cell, and every pane after it is owed the same; rounding each of them up to
// the one cell a pane must have pushed the row past the edge of the tab it was
// being measured into, so the panes on the end sat outside their own parent —
// on top of whatever the neighbouring split had put there.
func TestComputeStaysInsideTheBoxWhenAWeightIsTiny(t *testing.T) {
	view := Rect{X: 0, Y: 0, W: 61, H: 25}

	row := NewLeaf("p0")
	for i := 1; i < 5; i++ {
		row.Split("p"+strconv.Itoa(i-1), "p"+strconv.Itoa(i), Horizontal)
	}
	if !row.SetChildWeights([]float64{1000, 1, 1, 1, 1}) {
		t.Fatal("could not skew the row")
	}
	row.Compute(view)

	sum := 0
	for _, l := range row.Leaves() {
		r := l.Rect()
		if r.W < 1 || r.H < 1 {
			t.Errorf("pane %s got %+v, every pane needs a cell", l.Pane, r)
		}
		if r.X < view.X || r.X+r.W > view.X+view.W {
			t.Errorf("pane %s at %+v hangs off a tab %d columns wide", l.Pane, r, view.W)
		}
		sum += r.W
	}
	if want := view.W - 4*separatorWidth; sum != want {
		t.Errorf("panes cover %d columns, want %d", sum, want)
	}

	// The same for a stack, which is where it bites hardest: a column nested
	// in a row has only its share of the height to divide, so it takes far
	// less of a drag to run out of rows than to run out of columns.
	col := NewLeaf("q0")
	for i := 1; i < 4; i++ {
		col.Split("q"+strconv.Itoa(i-1), "q"+strconv.Itoa(i), Vertical)
	}
	if !col.SetChildWeights([]float64{97, 1, 1, 1}) {
		t.Fatal("could not skew the column")
	}
	col.Compute(Rect{X: 0, Y: 0, W: 20, H: 10})

	sum = 0
	for _, l := range col.Leaves() {
		r := l.Rect()
		if r.H < 1 {
			t.Errorf("pane %s got %+v, every pane needs a row", l.Pane, r)
		}
		if r.Y < 0 || r.Y+r.H > 10 {
			t.Errorf("pane %s at %+v hangs off a column 10 rows deep", l.Pane, r)
		}
		sum += r.H
	}
	if sum != 10 {
		t.Errorf("panes cover %d rows, want 10", sum)
	}
}

// TestNeighborIgnoresAPaneItDoesNotFace covers two rows split differently, so
// that a divider in one row lines up with a pane in the other. Moving right
// out of the top-left pane must land in the pane beside it and not in the row
// below, however neatly that pane happens to start where this row's divider
// is.
func TestNeighborIgnoresAPaneItDoesNotFace(t *testing.T) {
	// Layout:  a  | b        two columns over three, in eight cells:
	//          d | e | f     a covers 0-2 and e starts at 3, where the
	//                        divider between a and b sits.
	root := NewLeaf("a")
	root.Split("a", "d", Vertical)
	root.Split("a", "b", Horizontal)
	root.Split("d", "e", Horizontal)
	root.Split("e", "f", Horizontal)
	root.Compute(Rect{X: 0, Y: 0, W: 8, H: 4})

	a, b, e := root.Find("a").Rect(), root.Find("b").Rect(), root.Find("e").Rect()
	if e.X != a.X+a.W || b.X != e.X+separatorWidth {
		t.Fatalf("this test needs e to start at a's divider, got a=%+v b=%+v e=%+v", a, b, e)
	}

	if got := root.Neighbor("a", Right); got != "b" {
		t.Errorf("right of a = %q, want b: %q is in the row below and shares no edge with a", got, got)
	}
	if got := root.Neighbor("b", Left); got != "a" {
		t.Errorf("left of b = %q, want a", got)
	}
	if got := root.Neighbor("d", Up); got != "a" {
		t.Errorf("up from d = %q, want a", got)
	}
}

// TestClosingAPaneLeavesTheRestOfTheTabWhereItWas is the promise behind
// closing one of several agents: the panes you were reading do not move under
// you. Only the panes that shared the closed pane's slot grow into it.
//
// It is also what says the weights are shared out correctly when the split the
// pane came out of collapses into its parent. Getting that scaling wrong does
// not break any structural invariant — the tab still tiles — it just silently
// resizes panes at the other end of the screen.
func TestClosingAPaneLeavesTheRestOfTheTabWhereItWas(t *testing.T) {
	view := Rect{X: 0, Y: 0, W: 997, H: 401}
	checked, worst := 0, 0

	for seed := int64(0); seed < 400; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		root := NewLeaf("p0")
		next := 1
		for step := 0; step < 10; step++ {
			panes := root.Panes()
			victim := panes[rnd.Intn(len(panes))]
			switch rnd.Intn(4) {
			case 0, 1:
				root.Split(victim, "p"+strconv.Itoa(next), Dir(rnd.Intn(2)))
				next++
			case 2:
				root.MovePane(victim, panes[rnd.Intn(len(panes))], Edge(rnd.Intn(4)))
			case 3:
				if s := randomSplit(root, rnd); s != nil {
					weights := make([]float64, len(s.Children))
					for i := range weights {
						weights[i] = math.Pow(10, rnd.Float64()*3-1.5)
					}
					s.SetChildWeights(weights)
				}
			}
		}
		panes := root.Panes()
		if len(panes) < 3 {
			continue
		}
		root.Compute(view)
		if !roomFor(root) {
			continue
		}

		victim := panes[rnd.Intn(len(panes))]
		parent, _ := root.parentOf(root.Find(victim))
		if parent == nil {
			continue
		}
		slot := parent.Rect()
		// A split of two dissolves when one of them goes, and its panes move
		// up into its parent. The row they land in then divides its own space
		// rather than the slot's, and its dividers fall where that arithmetic
		// puts them, so the panes beyond can shift by a cell or two. Anything
		// more than rounding is the weights being shared out wrongly.
		dissolves := len(parent.Children) == 2

		before := map[string]Rect{}
		for _, l := range root.Leaves() {
			before[l.Pane] = l.Rect()
		}
		if !root.Remove(victim) {
			t.Fatalf("seed %d: closing %s failed", seed, victim)
		}
		root.Compute(view)
		if !roomFor(root) {
			continue
		}

		for _, l := range root.Leaves() {
			was, now := before[l.Pane], l.Rect()
			inSlot := was.X >= slot.X && was.Y >= slot.Y &&
				was.X+was.W <= slot.X+slot.W && was.Y+was.H <= slot.Y+slot.H
			if inSlot {
				continue // free to grow into the room the closed pane left
			}
			checked++
			off := abs(now.X - was.X)
			for _, v := range []int{abs(now.Y - was.Y), abs(now.W - was.W), abs(now.H - was.H)} {
				if v > off {
					off = v
				}
			}
			if off > worst {
				worst = off
			}
			limit := 0
			if dissolves {
				limit = 2
			}
			if off > limit {
				t.Fatalf("seed %d: closing %s moved %s from %+v to %+v, %d cells away and outside the slot %+v",
					seed, victim, l.Pane, was, now, off, slot)
			}
		}
	}

	if checked < 300 {
		t.Fatalf("only %d panes lay outside a closed slot, too few to mean anything", checked)
	}
	t.Logf("%d panes outside a closed slot, worst shift %d cells", checked, worst)
}

// TestComputeFillsAnyBoxWithAnyWeights states Compute's contract directly:
// whatever the weights and whatever the box, the children of a split tile it
// exactly, in order, and every one of them gets a cell. The random editing
// test reaches this through whole trees at one size; this reaches every size
// and every ratio, including the boxes too small to hold the panes at all.
func TestComputeFillsAnyBoxWithAnyWeights(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))

	for i := 0; i < 20000; i++ {
		n := 2 + rnd.Intn(8)
		dir := Dir(rnd.Intn(2))
		root := NewLeaf("p0")
		for j := 1; j < n; j++ {
			root.Split("p"+strconv.Itoa(j-1), "p"+strconv.Itoa(j), dir)
		}
		weights := make([]float64, n)
		for j := range weights {
			weights[j] = math.Pow(10, rnd.Float64()*8-4)
		}
		if !root.SetChildWeights(weights) {
			t.Fatalf("case %d: weights %v refused", i, weights)
		}

		box := Rect{X: 3, Y: 5, W: 1 + rnd.Intn(60), H: 1 + rnd.Intn(60)}
		root.Compute(box)

		// A box with fewer cells than panes cannot be tiled; every pane still
		// gets its cell, and that is the whole promise there.
		room := box.H
		if dir == Horizontal {
			room = box.W - separatorWidth*(n-1)
		}

		pos, start, end := box.Y, box.Y, box.Y+box.H
		if dir == Horizontal {
			pos, start, end = box.X, box.X, box.X+box.W
		}
		last := start
		for j, c := range root.Children {
			r := c.Rect()
			at, size := r.Y, r.H
			if dir == Horizontal {
				at, size = r.X, r.W
			}
			if size < 1 {
				t.Fatalf("case %d: pane %d of %d in %+v got %d cells", i, j, n, box, size)
			}
			if at < start || at+size > end {
				t.Fatalf("case %d: pane %d of %d at %d+%d is outside the box %+v", i, j, n, at, size, box)
			}
			if at < last {
				t.Fatalf("case %d: pane %d of %d starts at %d, behind the pane before it at %d", i, j, n, at, last)
			}
			last = at
			if room >= n {
				// With a cell for each of them they tile the box exactly.
				if at != pos {
					t.Fatalf("case %d: pane %d of %d in %+v starts at %d, want %d", i, j, n, box, at, pos)
				}
				pos += size
				if dir == Horizontal && j < n-1 {
					pos += separatorWidth
				}
			}
			// The cross axis is handed straight down.
			if dir == Horizontal && (r.Y != box.Y || r.H != box.H) {
				t.Fatalf("case %d: pane %d got %+v across a box of %+v", i, j, r, box)
			}
			if dir == Vertical && (r.X != box.X || r.W != box.W) {
				t.Fatalf("case %d: pane %d got %+v across a box of %+v", i, j, r, box)
			}
		}
		if room >= n && pos != end {
			t.Fatalf("case %d: %d panes weighted %v end at %d in %+v, want %d", i, n, weights, pos, box, end)
		}
	}
}

// TestDeeplyNestedSplitsStayInsideTheTab covers splitting the pane you just
// made, over and over, turning the axis each time. Every split nests inside
// the last, so the box each one divides is half the size of the one before it,
// and around twenty levels down there is no longer a cell for each pane.
//
// The layout there cannot be right — a pane needs a cell and there are not
// enough — but it must still be about the tab on screen. Panes laid out past
// the end of their parent land on top of whatever the split next door drew,
// and the answers to "what is left of this" and "what did I just click on"
// then come from a part of the screen the user is not looking at.
func TestDeeplyNestedSplitsStayInsideTheTab(t *testing.T) {
	for _, depth := range []int{4, 12, 20, 30} {
		root := NewLeaf("p0")
		for i := 1; i <= depth; i++ {
			if !root.Split("p"+strconv.Itoa(i-1), "p"+strconv.Itoa(i), Dir(i%2)) {
				t.Fatalf("depth %d: split %d failed", depth, i)
			}
		}
		view := Rect{X: 0, Y: 0, W: 1000, H: 1000}
		root.Compute(view)

		for _, l := range root.Leaves() {
			r := l.Rect()
			if r.W < 1 || r.H < 1 {
				t.Errorf("depth %d: %s got %+v, every pane needs a cell", depth, l.Pane, r)
			}
			if r.X < view.X || r.Y < view.Y || r.X+r.W > view.X+view.W || r.Y+r.H > view.Y+view.H {
				t.Errorf("depth %d: %s at %+v is outside a tab of %+v", depth, l.Pane, r, view)
			}
		}
		// Nothing at this depth has room to spare, so the panes are on top of
		// one another; what matters is that they are still in reading order,
		// which is what makes the answers point the right way.
		leaves := root.Leaves()
		for i := 1; i < len(leaves); i++ {
			a, b := leaves[i-1].Rect(), leaves[i].Rect()
			if b.X < a.X || (b.X == a.X && b.Y < a.Y) {
				t.Errorf("depth %d: %s at %+v comes after %s at %+v", depth, leaves[i].Pane, b, leaves[i-1].Pane, a)
			}
		}
	}
}

// TestWeightsTooBigToCompareStillDivideTheBox covers weights that arrive from
// a layout file rather than from a divider: nothing stops one being written as
// a number near the largest a float can hold. Adding those up, or scaling them
// to the width of a tab, runs off the end of what a float can represent, and
// the arithmetic that follows is not merely imprecise — it is not about the
// weights at all.
func TestWeightsTooBigToCompareStillDivideTheBox(t *testing.T) {
	cases := []struct {
		weights []float64
		want    []int // roughly, in columns of a hundred
	}{
		{[]float64{1e308, 1}, []int{98, 1}},
		{[]float64{1, 1e308}, []int{1, 98}},
		{[]float64{math.MaxFloat64, math.MaxFloat64}, []int{49, 50}},
		{[]float64{math.MaxFloat64, math.MaxFloat64, math.MaxFloat64}, []int{32, 33, 33}},
	}

	for _, c := range cases {
		root := NewLeaf("p0")
		for i := 1; i < len(c.weights); i++ {
			root.Split("p"+strconv.Itoa(i-1), "p"+strconv.Itoa(i), Horizontal)
		}
		if !root.SetChildWeights(c.weights) {
			t.Fatalf("%v refused", c.weights)
		}
		root.Compute(Rect{X: 0, Y: 0, W: 100, H: 10})

		for i, l := range root.Leaves() {
			if got := l.Rect().W; got != c.want[i] {
				t.Errorf("weights %v: pane %d got %d columns of 100, want %d", c.weights, i, got, c.want[i])
			}
		}
	}
}

// TestPanesWeightedAlikeGiveUpTheSameRoom covers who pays when a box cannot
// hold what the weights ask for. The panes owed less than a cell are given one
// anyway, and that has to come from the panes with room to spare; taking it
// from whoever comes first leaves the earliest of them a single cell while the
// pane weighted the same as it, further down, keeps almost everything.
func TestPanesWeightedAlikeGiveUpTheSameRoom(t *testing.T) {
	const n = 22
	root := NewLeaf("p0")
	for i := 1; i < n; i++ {
		root.Split("p"+strconv.Itoa(i-1), "p"+strconv.Itoa(i), Vertical)
	}
	weights := make([]float64, n)
	weights[0], weights[1] = 10, 10
	for i := 2; i < n; i++ {
		weights[i] = 0.001 // twenty panes owed a fortieth of a row each
	}
	if !root.SetChildWeights(weights) {
		t.Fatal("weights refused")
	}
	root.Compute(Rect{X: 0, Y: 0, W: 20, H: 30})

	leaves := root.Leaves()
	first, second := leaves[0].Rect().H, leaves[1].Rect().H
	if first != second {
		t.Errorf("the two panes weighted 10 got %d and %d rows of 30", first, second)
	}
	if first < 2 {
		t.Errorf("a pane weighted ten thousand times its neighbours got %d rows", first)
	}
	rows := 0
	for _, l := range leaves {
		if got := l.Rect().H; got < 1 {
			t.Errorf("%s got %d rows", l.Pane, got)
		}
		rows += l.Rect().H
	}
	if rows != 30 {
		t.Errorf("the panes cover %d rows of 30", rows)
	}
}

// reachedFrom walks out of every cell of a pane's edge in one direction and
// returns, for each pane the walk lands on first, how many of those cells
// reached it and how far the nearest of them travelled.
//
// It is the question the arrow keys are really asking — what is out that way,
// and how much of it — worked out from the cells on screen rather than from
// the tree, so it knows nothing of weights, splits or the order the panes were
// created in.
func reachedFrom(root *Node, view Rect, pane string, d Direction) (votes, nearest map[string]int) {
	from := root.Find(pane).Rect()
	votes, nearest = map[string]int{}, map[string]int{}

	cells := from.H
	if d == Up || d == Down {
		cells = from.W
	}
	for i := 0; i < cells; i++ {
		x, y := from.X+from.W, from.Y+i
		dx, dy := 1, 0
		switch d {
		case Left:
			x, dx = from.X-1, -1
		case Up:
			x, y, dx, dy = from.X+i, from.Y-1, 0, -1
		case Down:
			x, y, dx, dy = from.X+i, from.Y+from.H, 0, 1
		}
		for dist := 0; x >= view.X && y >= view.Y && x < view.X+view.W && y < view.Y+view.H; dist++ {
			if p := root.PaneAt(x, y); p != "" && p != pane {
				votes[p]++
				if was, seen := nearest[p]; !seen || dist < was {
					nearest[p] = dist
				}
				break
			}
			x, y = x+dx, y+dy
		}
	}
	return votes, nearest
}

// TestNeighborIsWhatThePaneLooksOnto checks the whole of Neighbor against what
// is actually on screen: for every pane of a few hundred random tabs, in every
// direction, the pane it names must be the pane that most of that edge looks
// onto.
//
// The two are worked out from opposite ends — one from the tree and its
// weights, the other by walking cell by cell out of the pane and asking what
// is there — so agreeing on every pane of every tab is a real statement that
// focus movement goes where the layout says it should. Where the same amount
// of the edge looks onto two panes the tab itself cannot answer, and the walk
// is told the same tie-breaks Neighbor uses: the nearer pane, then the one
// better lined up with this one, then the first in the tree.
func TestNeighborIsWhatThePaneLooksOnto(t *testing.T) {
	view := Rect{X: 0, Y: 0, W: 200, H: 120}

	for seed := int64(0); seed < 600; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		root := NewLeaf("p0")
		next := 1
		for step := 0; step < 12; step++ {
			panes := root.Panes()
			victim := panes[rnd.Intn(len(panes))]
			switch rnd.Intn(4) {
			case 0, 1:
				root.Split(victim, "p"+strconv.Itoa(next), Dir(rnd.Intn(2)))
				next++
			case 2:
				root.MovePane(victim, panes[rnd.Intn(len(panes))], Edge(rnd.Intn(4)))
			case 3:
				if s := randomSplit(root, rnd); s != nil {
					weights := make([]float64, len(s.Children))
					for i := range weights {
						weights[i] = math.Pow(10, rnd.Float64()*2-1)
					}
					s.SetChildWeights(weights)
				}
			}
		}
		root.Compute(view)
		if !roomFor(root) {
			continue // the panes are on top of each other; nothing is right
		}

		for _, l := range root.Leaves() {
			from := l.Rect()
			for _, d := range []Direction{Left, Right, Up, Down} {
				votes, nearest := reachedFrom(root, view, l.Pane, d)
				aligned := func(r Rect) int {
					if d == Left || d == Right {
						return abs(r.centerY2() - from.centerY2())
					}
					return abs(r.centerX2() - from.centerX2())
				}
				want, most, closest, lined := "", 0, 1<<30, 1<<30
				for _, other := range root.Leaves() {
					v := votes[other.Pane]
					if v == 0 {
						continue
					}
					dist, off := nearest[other.Pane], aligned(other.Rect())
					if v > most ||
						(v == most && dist < closest) ||
						(v == most && dist == closest && off < lined) {
						want, most, closest, lined = other.Pane, v, dist, off
					}
				}
				if got := root.Neighbor(l.Pane, d); got != want {
					t.Fatalf("seed %d: %d of %s at %+v is %q, but %q is what most of that edge looks onto",
						seed, d, l.Pane, from, got, want)
				}
			}
		}
	}
}

// TestAWeightThatIsNotANumberIsAnEqualShare covers a weight that reaches the
// tree without going through SetChildWeights, which refuses the ones that
// cannot be divided by. Weight is an exported field and a layout file is JSON
// on disk, so a tab can arrive holding a number that is not one.
//
// Everything that lays out a box adds the weights up and divides by the total.
// One NaN among them makes the total a NaN, every share a NaN, and the cell
// count that comes out of it is not large but hugely negative: the panes were
// each given the single cell the floor allows and the middle one was left
// covering the whole tab, on top of both of the others.
func TestAWeightThatIsNotANumberIsAnEqualShare(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Horizontal)
	root.Children[1].Weight = math.NaN()
	root.Compute(Rect{X: 0, Y: 0, W: 100, H: 10})

	widths := []int{}
	for _, l := range root.Leaves() {
		widths = append(widths, l.Rect().W)
	}
	if !reflect.DeepEqual(widths, []int{32, 33, 33}) {
		t.Errorf("widths = %v, want the panes sharing 98 columns evenly", widths)
	}
	if !roomFor(root) {
		t.Fatal("three panes have room in a hundred columns")
	}
	for i, l := range root.Leaves() {
		r := l.Rect()
		if r.X < 0 || r.X+r.W > 100 {
			t.Errorf("pane %d at %+v is outside the tab", i, r)
		}
	}
}

// TestResizeTakesTheRoomFromTheNextPaneAlong pins which pane pays. Growing a
// pane in the middle of a row has to move the divider on its right, leaving
// everything to its left where it was; taking the room from the pane before it
// instead would grow the right pane just as much while sliding the whole row
// under the user's hands.
func TestResizeTakesTheRoomFromTheNextPaneAlong(t *testing.T) {
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Horizontal)
	view := Rect{X: 0, Y: 0, W: 300, H: 10}
	root.Compute(view)
	was := [3]Rect{root.Find("a").Rect(), root.Find("b").Rect(), root.Find("c").Rect()}

	if !root.Resize("b", Horizontal, 0.5) {
		t.Fatal("growing the middle pane failed")
	}
	root.Compute(view)
	a, b, c := root.Find("a").Rect(), root.Find("b").Rect(), root.Find("c").Rect()

	if a != was[0] {
		t.Errorf("the pane before it went from %+v to %+v, want it left alone", was[0], a)
	}
	if b.W <= was[1].W {
		t.Errorf("the pane being grown went from %d to %d columns", was[1].W, b.W)
	}
	if c.W >= was[2].W {
		t.Errorf("the pane after it went from %d to %d columns, want it to have paid", was[2].W, c.W)
	}
}

// TestResizeReachesTheSplitThatCanGive covers a pane with no sibling on the
// axis being resized: the pane below it cannot give it width, so the width has
// to come from the column the two of them are in.
func TestResizeReachesTheSplitThatCanGive(t *testing.T) {
	// Layout:  a | b
	//          a | c
	root := NewLeaf("a")
	root.Split("a", "b", Horizontal)
	root.Split("b", "c", Vertical)
	view := Rect{X: 0, Y: 0, W: 300, H: 20}
	root.Compute(view)
	wasA, wasB, wasC := root.Find("a").Rect().W, root.Find("b").Rect().W, root.Find("c").Rect().W

	if !root.Resize("b", Horizontal, 0.5) {
		t.Fatal("growing a pane nested in a column failed")
	}
	root.Compute(view)
	a, b, c := root.Find("a").Rect().W, root.Find("b").Rect().W, root.Find("c").Rect().W

	if b <= wasB {
		t.Errorf("b went from %d to %d columns, want it wider", wasB, b)
	}
	if a >= wasA {
		t.Errorf("a went from %d to %d columns, want it to have paid", wasA, a)
	}
	// c shares b's column, so it moved with it rather than being squeezed.
	if c != b || c-wasC != b-wasB {
		t.Errorf("c went from %d to %d columns while b went from %d to %d; they share a column",
			wasC, c, wasB, b)
	}

	// b and c are stacked, so height is theirs to trade between them.
	if !root.Resize("b", Vertical, 0.5) {
		t.Error("b should be able to take height from c")
	}
	// a is the full height of the tab and nothing is stacked with it, so there
	// is no height anywhere for it to take.
	if root.Resize("a", Vertical, 0.5) {
		t.Error("a has nothing stacked with it, so there is no height to take")
	}
}

// TestATabWithNoRoomAtAllGivesEveryPaneNothing covers the tab measured into a
// box with no cells in it, which is what a window collapsed to nothing hands
// down. A pane is owed a cell everywhere else, but there is no cell here to
// give it, and a pane an inch outside a tab of no width would be worse than a
// pane of no width: everything that answers a question about the screen —
// which pane is left of this, what is under the pointer — would be answering
// about somewhere the tab is not.
func TestATabWithNoRoomAtAllGivesEveryPaneNothing(t *testing.T) {
	for _, view := range []Rect{{X: 4, Y: 7, W: 0, H: 20}, {X: 4, Y: 7, W: 20, H: 0}, {X: 0, Y: 0, W: 0, H: 0}} {
		root := NewLeaf("p0")
		for i := 1; i < 5; i++ {
			root.Split("p"+strconv.Itoa(i-1), "p"+strconv.Itoa(i), Dir(i%2))
		}
		root.Compute(view)

		for _, l := range root.Leaves() {
			r := l.Rect()
			if r.W < 0 || r.H < 0 {
				t.Errorf("view %+v: %s got %+v, which is less than nothing", view, l.Pane, r)
			}
			if r.X < view.X || r.Y < view.Y || r.X+r.W > view.X+view.W || r.Y+r.H > view.Y+view.H {
				t.Errorf("view %+v: %s at %+v is outside it", view, l.Pane, r)
			}
		}
		// And no arrow key sends the focus wandering off into it.
		for _, d := range []Direction{Left, Right, Up, Down} {
			if got := root.Neighbor("p2", d); got != "" {
				t.Errorf("view %+v: %d of p2 = %q, want no answer at all", view, d, got)
			}
		}
		if got := root.PaneAt(view.X, view.Y); got != "" {
			t.Errorf("view %+v: the first cell resolves to %q, and there is no first cell", view, got)
		}
	}
}

// TestCombineIgnoresTheWeightATreeArrivesWith covers merging a tab whose tree
// carries a weight of its own. A tab's tree is usually weighted one, but not
// always: restoring a layout collapses a split that has lost all but one child
// into that child, which takes over the split's share, and that share is
// whatever the file recorded — a thirtieth, after a few merges.
//
// Merging is between tabs, and a tab is a tab whatever the number on its root
// says. The weight a tree arrives with means something only inside the tab it
// came from, so it is dropped rather than let through into the split the merge
// builds, where it would decide how the two tabs share the screen.
func TestCombineIgnoresTheWeightATreeArrivesWith(t *testing.T) {
	view := Rect{X: 0, Y: 0, W: 1000, H: 100}

	// A lone pane whose tab was left holding a thirtieth of a share.
	lone := NewLeaf("lone")
	lone.Weight = 0.03
	root := Combine(NewLeaf("other"), lone, Horizontal)
	root.Compute(view)
	if w := root.Find("lone").Rect().W; w < 450 || w > 550 {
		t.Errorf("the merged pane got %d of 1000 columns, want about half", w)
	}

	// And the same for a tab holding a column, which comes across whole.
	column := NewLeaf("top")
	column.Split("top", "bottom", Vertical)
	column.Weight = 0.03
	root = Combine(NewLeaf("beside"), column, Horizontal)
	root.Compute(view)
	if w := root.Find("top").Rect().W; w < 450 || w > 550 {
		t.Errorf("the merged column got %d of 1000 columns, want about half", w)
	}
}

// TestCombineGivesTheMergedTreeItsOwnChildren covers what a merge does and does
// not take from the tabs it merges. The nodes come across as they are, which is
// what keeps a pane's id and a split's proportions; the slice holding them must
// not, because a slice with room to spare is written into rather than copied,
// and the tab it came from would then be writing into the merged one.
//
// Nothing holds a tab after merging it away today. This is the kind of sharing
// that is invisible until something does.
func TestCombineGivesTheMergedTreeItsOwnChildren(t *testing.T) {
	left := NewLeaf("l0")
	left.Split("l0", "l1", Horizontal)
	left.Split("l1", "l2", Horizontal) // three in a row, with a slot to spare
	right := NewLeaf("r0")             // one pane, which is what fits in that spare slot

	merged := Combine(left, right, Horizontal)
	was := merged.Panes()
	if len(was) != 4 {
		t.Fatalf("the merged tab holds %v, want all four panes", was)
	}

	// A pane added to the tab that was merged away has to stay there.
	if !left.Split("l2", "x", Horizontal) {
		t.Fatal("splitting the merged-away tab failed")
	}
	if got := merged.Panes(); !reflect.DeepEqual(got, was) {
		t.Errorf("the merged tab now holds %v, want %v", got, was)
	}
}
