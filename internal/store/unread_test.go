package store

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestALayoutThatCouldNotBeReadIsNotWrittenOver checks that a layout the run
// failed to read survives the save on the way out.
//
// A project whose layout cannot be read opens empty, or on one fresh tab, and
// that is what gets saved when the window closes. Written straight over the
// file, it took with it every tab the user had saved, for nothing worse than a
// read that failed once.
func TestALayoutThatCouldNotBeReadIsNotWrittenOver(t *testing.T) {
	isolateConfig(t)
	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "alpha"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	p, err := path("/repo/a")
	if err != nil {
		t.Fatalf("path: %v", err)
	}

	// The read fails the way one held past the retry budget does, or one on a
	// drive that has gone away for a moment: not because the file is missing.
	gone := errors.New("the device is not ready")
	readFile = func(name string) ([]byte, error) {
		if name == p {
			return nil, gone
		}
		return os.ReadFile(name)
	}
	t.Cleanup(func() { readFile = os.ReadFile })
	if _, err := Load("/repo/a"); !errors.Is(err, gone) {
		t.Fatalf("load gave %v, want the read's own failure", err)
	}
	readFile = os.ReadFile

	// The run went on with a fresh tab, and saves it on the way out.
	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "fresh"}}}); err != nil {
		t.Fatalf("save after a failed read: %v", err)
	}
	kept, err := os.ReadFile(p + unreadSuffix)
	if err != nil {
		t.Fatalf("the layout that could not be read was not kept: %v", err)
	}
	if !strings.Contains(string(kept), `"alpha"`) {
		t.Errorf("the kept layout holds %s, want the tabs saved before", kept)
	}
	if got, err := Load("/repo/a"); err != nil || got == nil || got.Tabs[0].Title != "fresh" {
		t.Fatalf("load after saving = %+v, %v; want the tabs just saved", got, err)
	}

	// Once it is kept, a save is an ordinary save again: moving each new
	// layout aside would put the one that could not be read out of reach.
	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "later"}}}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if again, err := os.ReadFile(p + unreadSuffix); err != nil || !strings.Contains(string(again), `"alpha"`) {
		t.Errorf("a later save replaced the kept layout: %s, %v", again, err)
	}
}
