package session

import (
	"testing"
	"time"
)

// A window showing a pane has to be told when its size changes, so that it can
// keep its own terminal the same size as the pty the program draws for.
func TestSizeWatchSaysWhenThePaneIsResized(t *testing.T) {
	s := fakeSession(newFakePTY())
	cols, rows, changed := s.SizeWatch()
	if cols != 80 || rows != 24 {
		t.Fatalf("size is %dx%d, want 80x24", cols, rows)
	}
	select {
	case <-changed:
		t.Fatal("the size was reported changed before anything changed it")
	default:
	}

	s.Resize(100, 30)
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("a resize was not reported")
	}
	cols, rows, changed = s.SizeWatch()
	if cols != 100 || rows != 30 {
		t.Fatalf("size is %dx%d after the resize, want 100x30", cols, rows)
	}

	// A resize to the size it already is changes nothing, and the program is
	// not redrawn for it, so nobody is told.
	s.Resize(100, 30)
	select {
	case <-changed:
		t.Fatal("a resize to the same size was reported as a change")
	default:
	}
}
