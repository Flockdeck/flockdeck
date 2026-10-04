package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// TestRecentChangesMadeAtTheSameTimeAreNotLost covers the workspace recording
// a switch between projects on its own goroutine while a window renames and
// archives them. Every change reads the whole list and writes it back, so one
// that read before another wrote put back the list without the other's change:
// a rename that was acknowledged and then was not there.
func TestRecentChangesMadeAtTheSameTimeAreNotLost(t *testing.T) {
	isolateConfig(t)
	base := t.TempDir()
	var roots []string
	for i := range 6 {
		roots = append(roots, filepath.Join(base, fmt.Sprintf("repo%d", i)))
	}
	if err := TouchRecents(roots...); err != nil {
		t.Fatalf("touch: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 60 {
			if err := TouchRecent(roots[i%len(roots)]); err != nil {
				t.Errorf("touch: %v", err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i, root := range roots {
			if err := SetProjectName(root, fmt.Sprintf("name%d", i)); err != nil {
				t.Errorf("set name: %v", err)
			}
			if err := SetProjectArchived(root, true); err != nil {
				t.Errorf("archive: %v", err)
			}
		}
	}()
	wg.Wait()

	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(list) != len(roots) {
		t.Fatalf("recents = %+v, want %d projects", list, len(roots))
	}
	for i, root := range roots {
		p, ok := findProject(list, root)
		if !ok || p.Name != fmt.Sprintf("name%d", i) || !p.Archived {
			t.Errorf("%s = %+v, want name%d and archived", root, p, i)
		}
	}
}
