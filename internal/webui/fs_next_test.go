//go:build next

package webui

import (
	"bytes"
	"io/fs"
	"slices"
	"testing"
)

// The tagged build serves the new shell's index.html at the root and falls back
// to the old tree for every file the new one lacks.
func TestNextFSIsTheNewShellOverTheOldTree(t *testing.T) {
	if Variant != "next" {
		t.Fatalf("Variant = %q", Variant)
	}
	idx, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(idx, []byte("flockdeck-next-placeholder")) {
		t.Error("index.html is not the new shell's")
	}
	old, err := fs.ReadFile(oldTree(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(idx, old) {
		t.Error("index.html is the old one")
	}

	// Every old file the new tree lacks is served, and unchanged.
	have := files(t, FS())
	for _, name := range files(t, oldTree()) {
		if name == "index.html" {
			continue
		}
		if !slices.Contains(have, name) {
			t.Errorf("%s is missing from the tagged build", name)
			continue
		}
		a, _ := fs.ReadFile(oldTree(), name)
		b, err := fs.ReadFile(FS(), name)
		if err != nil || !bytes.Equal(a, b) {
			t.Errorf("%s differs from the old tree (%v)", name, err)
		}
	}
	for _, shared := range []string{"e2e.js", "icon-256.png"} {
		if !slices.Contains(have, shared) {
			t.Errorf("%s is not shared", shared)
		}
	}
}

// A directory lists each name once, whichever tree it is in.
func TestNextFSListsADirectoryOnce(t *testing.T) {
	entries, err := fs.ReadDir(FS(), ".")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, e := range entries {
		seen[e.Name()]++
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("%s listed %d times", name, n)
		}
	}
	if seen["index.html"] != 1 || seen["e2e.js"] != 1 {
		t.Errorf("root lists %v", seen)
	}
	if _, err := fs.ReadFile(FS(), "no-such-file"); err == nil {
		t.Error("a file in neither tree was found")
	}
}
