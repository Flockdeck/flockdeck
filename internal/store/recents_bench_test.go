package store

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestTouchingTheFirstProjectLeavesTheListAlone checks a switch back to the
// project used last does not rewrite the recent list, while one to any other
// project, or to the first under a spelling the list does not hold, still
// does.
func TestTouchingTheFirstProjectLeavesTheListAlone(t *testing.T) {
	isolateConfig(t)
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	for _, root := range []string{b, a} {
		if err := TouchRecent(root); err != nil {
			t.Fatalf("touch %s: %v", root, err)
		}
	}
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, recentsFile)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	if err := TouchRecent(a); err != nil {
		t.Fatalf("touch first: %v", err)
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
		t.Errorf("touching the project already first rewrote the list:\n%s\nbecame\n%s", before, after)
	}

	if err := TouchRecent(a + string(filepath.Separator)); err != nil {
		t.Fatalf("touch first, spelled untidily: %v", err)
	}
	if list, _ := Recents(); len(list) != 2 || list[0].Root != a {
		t.Errorf("an untidy spelling of the first project gave %+v, want it first as %s", list, a)
	}

	if err := TouchRecent(b); err != nil {
		t.Fatalf("touch second: %v", err)
	}
	if list, _ := Recents(); len(list) != 2 || list[0].Root != b || list[1].Root != a {
		t.Errorf("touching the second project gave %+v, want it moved to the front", list)
	}
}

// BenchmarkTouchRecentAlreadyFirst is what switching to a project costs the
// recent list when that project is the one used last, which is where a switch
// back and forth between two projects keeps finding it.
func BenchmarkTouchRecentAlreadyFirst(b *testing.B) {
	dir := b.TempDir()
	b.Setenv("APPDATA", dir)
	b.Setenv("XDG_CONFIG_HOME", dir)
	b.Setenv("HOME", dir)
	for i := maxRecents; i > 0; i-- {
		if err := TouchRecent(filepath.Join(dir, fmt.Sprintf("project-%02d", i))); err != nil {
			b.Fatal(err)
		}
	}
	first := filepath.Join(dir, "project-01")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := TouchRecent(first); err != nil {
			b.Fatal(err)
		}
	}
}
