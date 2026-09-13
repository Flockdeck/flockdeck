package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestADamagedLayoutThatWillNotMoveIsStillKept checks a damaged layout is not
// written over when moving it aside fails. It was left where it was, and the
// save on the way out replaced the one copy of the tabs it still held.
func TestADamagedLayoutThatWillNotMoveIsStillKept(t *testing.T) {
	isolateConfig(t)
	p, err := path("/repo/a")
	if err != nil {
		t.Fatal(err)
	}
	damaged := []byte(`{"version": 2, "tabs": [{"title": "alpha"`)
	if err := os.WriteFile(p, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	// Something standing at the name it would move to, which no rename can
	// replace on any platform: a directory with a file in it.
	if err := os.MkdirAll(filepath.Join(p+damagedSuffix, "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}

	if st, err := Load("/repo/a"); err != nil || st != nil {
		t.Fatalf("load of a damaged layout = %+v, %v; want nothing to restore and no error", st, err)
	}
	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "fresh"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	kept, err := os.ReadFile(p + unreadSuffix)
	if err != nil {
		t.Fatalf("the damaged layout that would not move was not kept: %v", err)
	}
	if string(kept) != string(damaged) {
		t.Errorf("kept %q, want the damaged layout as it was", kept)
	}
}
