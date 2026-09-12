package workspace

import (
	"reflect"
	"testing"

	"github.com/jmwri/flockdeck/internal/layout"
)

// TestTheOppositeArrowUndoesAKeyboardMove covers the promise the help makes:
// move a pane with the arrow keys and the opposite arrow puts it back. Two
// panes of different sizes trade cells in a move, and here the one moved lands
// in the full-width cell under a wide pane and the narrow cell it left; going
// back up, the wide pane shares more of its edge, so the geometry handed the
// move to it and scrambled the tab further instead of restoring it.
func TestTheOppositeArrowUndoesAKeyboardMove(t *testing.T) {
	// P three times the width of Q beside it, R the full width underneath.
	tree := layout.NewLeaf("P")
	tree.Split("P", "R", layout.Vertical)
	tree.Split("P", "Q", layout.Horizontal)
	if !tree.Children[0].SetChildWeights([]float64{3, 1}) {
		t.Fatal("the top row would not take its weights")
	}
	w := &Workspace{panes: map[string]*Pane{}, BroadcastSet: map[string]bool{}}
	for _, id := range []string{"P", "Q", "R"} {
		w.panes[id] = &Pane{ID: id}
	}
	w.Tabs = []*Tab{{ID: "t", Root: "/r", Tree: tree, Focus: "Q"}}
	w.openRoots, w.activeRoot, w.activeTab = []string{"/r"}, "/r", "t"
	before := tree.Panes()

	if err := w.MovePaneDir(layout.Down); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := w.MovePaneDir(layout.Up); err != nil {
		t.Fatalf("up: %v", err)
	}
	if got := w.Tabs[0].Tree.Panes(); !reflect.DeepEqual(got, before) {
		t.Errorf("down then up left the tab as %v, want it back as %v", got, before)
	}
	if f := w.Tabs[0].Focus; f != "Q" {
		t.Errorf("focus = %q, want it still on the pane being moved", f)
	}
}
