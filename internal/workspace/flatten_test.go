package workspace

import (
	"reflect"
	"testing"

	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestRestoreFlattensARowNestedInARow covers a layout an older build saved
// with a split inside a split along the same axis. It has to come back as the
// one row it looked like: left nested, a pane split beside one of the inner
// panes shared that pane's half of the row instead of the row.
func TestRestoreFlattensARowNestedInARow(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()

	saved := &store.State{Tabs: []store.Tab{{
		Title: "one",
		Root: &store.Node{Dir: "h", Children: []*store.Node{
			{Weight: 1, Pane: &store.Pane{ID: "left", Kind: "shell", Cwd: root}},
			{Weight: 2, Dir: "h", Children: []*store.Node{
				{Weight: 1, Pane: &store.Pane{ID: "middle", Kind: "shell", Cwd: root}},
				{Weight: 1, Pane: &store.Pane{ID: "right", Kind: "shell", Cwd: root}},
			}},
		}},
	}}}
	if err := store.Save(root, saved); err != nil {
		t.Fatalf("save: %v", err)
	}

	ws := newTestWorkspace(t, root)
	if ok, err := ws.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	tree := ws.VisibleTabs()[0].Tree
	if len(tree.Children) != 3 {
		t.Fatalf("the row came back with %d children, want the three panes side by side", len(tree.Children))
	}
	var weights []float64
	for _, c := range tree.Children {
		if !c.IsLeaf() {
			t.Fatalf("the row still holds a split: %+v", c)
		}
		weights = append(weights, c.Weight)
	}
	if want := []float64{1, 1, 1}; !reflect.DeepEqual(weights, want) {
		t.Errorf("weights = %v, want %v: each pane keeps the width it had", weights, want)
	}
	if tree.Dir != layout.Horizontal {
		t.Errorf("the row came back along the other axis")
	}
}
