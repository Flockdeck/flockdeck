package layout

import (
	"math"
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
