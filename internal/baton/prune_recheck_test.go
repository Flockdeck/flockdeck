package baton

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A baton used after the candidate list was made is not removed. The list is built from a
// snapshot, and the in-use check is asked again just before each removal: here the answer
// to that second question is "no", but the baton's file has been touched meanwhile, which
// only the fresh look at its file time can see.
func TestAPruneDoesNotRemoveABatonTouchedAfterTheListWasMade(t *testing.T) {
	quietCleanups(t)
	withClock(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	now := time.Now()
	old := now.Add(-100 * 24 * time.Hour)
	b := sample()
	b.ID = NewID(old)
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(s.Path(b.ID), old, old); err != nil {
		t.Fatal(err)
	}
	calls := 0
	removed, err := s.Prune(RetainFor, now, func(id string) bool {
		calls++
		if calls == 2 {
			if err := os.Chtimes(s.Path(id), now, now); err != nil {
				t.Fatal(err)
			}
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatalf("the in-use check was asked %d times, want it asked again before the removal", calls)
	}
	if len(removed) != 0 {
		t.Errorf("removed %v, a baton touched after the list was made", removed)
	}
	if _, err := os.Stat(s.Path(b.ID)); err != nil {
		t.Errorf("the baton is gone: %v", err)
	}
}
