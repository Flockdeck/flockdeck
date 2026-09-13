package layout

import "testing"

// TestFlattenKeepsEveryPaneWhereItWas checks that splicing nested splits into
// their parents changes the tree's shape and nothing on screen.
func TestFlattenKeepsEveryPaneWhereItWas(t *testing.T) {
	split := func(dir Dir, weight float64, children ...*Node) *Node {
		n := NewSplit(dir)
		n.Weight = weight
		n.Children = children
		return n
	}
	leaf := func(pane string, weight float64) *Node {
		l := NewLeaf(pane)
		l.Weight = weight
		return l
	}
	// A row holding a row, which holds a row of its own and a column, beside a
	// column holding a column: every shape a saved layout can nest.
	root := split(Horizontal, 1,
		leaf("a", 1),
		split(Horizontal, 3,
			leaf("b", 2),
			split(Horizontal, 1, leaf("c", 1), leaf("d", 3)),
			split(Vertical, 1, leaf("e", 1), leaf("f", 1)),
		),
		split(Vertical, 2,
			split(Vertical, 1, leaf("g", 1), leaf("h", 1)),
			leaf("i", 2),
		),
	)
	box := Rect{W: 240, H: 60}
	root.Compute(box)
	before := map[string]Rect{}
	for _, l := range root.Leaves() {
		before[l.Pane] = l.Rect()
	}

	root.Flatten()
	checkInvariants(t, root, "flatten")
	root.Compute(box)
	// Within a cell or two rather than exactly: a nested row pays for the rules
	// between its panes out of its own share and a flat one out of the whole
	// row, so the two round differently at the edges.
	near := func(a, b int) bool { return abs(a-b) <= 2 }
	for _, l := range root.Leaves() {
		got, want := l.Rect(), before[l.Pane]
		if !near(got.X, want.X) || !near(got.Y, want.Y) || !near(got.W, want.W) || !near(got.H, want.H) {
			t.Errorf("pane %s moved from %+v to %+v", l.Pane, want, got)
		}
	}
	if n := len(root.Children); n != 6 {
		t.Errorf("the row holds %d children, want a, b, c, d, the column of e and f, and the column of g, h and i", n)
	}
}
