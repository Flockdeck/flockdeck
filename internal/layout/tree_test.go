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
func TestNeighborBreaksTiesInTreeOrder(t *testing.T) {
	// Layout:  a | c
	//          b | c
	root := NewLeaf("a")
	root.Split("a", "c", Horizontal)
	root.Split("a", "b", Vertical)
	root.Compute(Rect{X: 0, Y: 0, W: 1000, H: 1000})

	// a and b are equally far from c and equally misaligned with it.
	a, b, c := root.Find("a").Rect(), root.Find("b").Rect(), root.Find("c").Rect()
	if abs(a.centerY()-c.centerY()) != abs(b.centerY()-c.centerY()) {
		t.Fatalf("this test needs a genuine tie, got %d and %d", a.centerY(), b.centerY())
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
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("panes = %v, want the refused calls to have changed nothing", got)
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

	// b is already immediately right of a, and immediately left of c.
	if !root.MovePane("b", "a", EdgeRight) {
		t.Fatal("dropping b where it already is should succeed")
	}
	if !root.MovePane("b", "c", EdgeLeft) {
		t.Fatal("dropping b where it already is should succeed")
	}
	if got := root.Panes(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("panes = %v, want [a b c]", got)
	}
	got := []float64{root.Children[0].Weight, root.Children[1].Weight, root.Children[2].Weight}
	if !reflect.DeepEqual(got, []float64{3, 1, 1}) {
		t.Errorf("weights = %v, want the divider left where it was", got)
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
			switch rnd.Intn(4) {
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
		root.Compute(Rect{X: 0, Y: 0, W: 997, H: 401})

		for _, l := range root.Leaves() {
			from := l.Rect()
			for _, d := range dirs {
				got := root.Neighbor(l.Pane, d)
				if got == "" {
					for _, other := range root.Leaves() {
						if other != l && adjacent(from, other.Rect(), d) {
							t.Fatalf("seed %d: nothing is %d of %s, but %s is against that edge", seed, d, l.Pane, other.Pane)
						}
					}
					continue
				}
				if r := root.Find(got).Rect(); !adjacent(from, r, d) {
					t.Fatalf("seed %d: %d of %s at %+v is %s at %+v, which does not touch it", seed, d, l.Pane, from, got, r)
				}
			}
		}
	}
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
			}
		}
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
