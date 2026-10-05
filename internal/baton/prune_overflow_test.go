package baton

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func overflowFor(t *testing.T, root string, s *Store, id string) string {
	t.Helper()
	p := filepath.Join(root, "repo-"+id, ".git", "flockdeck", "baton-"+id+".md")
	if err := WriteOverflow(p, "text"); err != nil {
		t.Fatal(err)
	}
	if err := s.NoteOverflow(id, p); err != nil {
		t.Fatal(err)
	}
	return p
}

// A removal that does not answer must not hold up the save that pruned: Prune returns
// at once. Two prunes in turn each want a different overflow file removed while the
// first removal is stuck, and only one removal is ever started: the cleanup is
// single-flight, so a hung share cannot collect goroutines.
func TestAnOverflowRemovalThatNeverAnswersDoesNotHoldUpAPrune(t *testing.T) {
	withClock(t)
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	one, two := oldBaton(t, s, 2), oldBaton(t, s, 3)
	overflowFor(t, root, s, one)
	overflowFor(t, root, s, two)
	release := make(chan struct{})
	var calls atomic.Int32
	var seen sync.Map
	old := overflowRemove
	overflowRemove = func(path, id string, within time.Duration) bool {
		calls.Add(1)
		seen.Store(path, true)
		<-release
		return false
	}
	t.Cleanup(func() { overflowRemove = old })

	start := time.Now()
	if removed, err := s.Prune(RetainFor, time.Now(), nil); err != nil || len(removed) != 1 {
		t.Fatalf("first prune: removed %v, err %v", removed, err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Prune took %s with a removal that does not answer", d)
	}
	// The second baton goes in the next run, and its overflow file wants removing too.
	if removed, err := s.Prune(RetainFor, time.Now(), nil); err != nil || len(removed) != 1 {
		t.Fatalf("second prune: removed %v, err %v", removed, err)
	}
	time.Sleep(150 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Errorf("%d removals were started while the first was stuck, want 1", n)
	}
	close(release)
	WaitOverflowCleanup()
}

// A path that cannot be removed is kept with a count, and forgotten with a line in
// the log after maxOverflowFailures.
func TestAnOverflowFileThatCannotBeRemovedIsGivenUpOn(t *testing.T) {
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	id := oldBaton(t, s, 2)
	p := overflowFor(t, root, s, id)
	old := overflowRemove
	overflowRemove = func(path, id string, within time.Duration) bool { return false }
	t.Cleanup(func() { overflowRemove = old })
	var logged []string
	SetLogf(func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })
	t.Cleanup(func() { SetLogf(func(string, ...any) {}) })
	for i := 0; i < maxOverflowFailures; i++ {
		if m, _ := s.readOverflows(); len(m[id]) != 1 {
			t.Fatalf("after %d failures the path was already forgotten: %v", i, m)
		}
		s.cleanOverflowSoon(map[string]bool{id: true}, nil, time.Now(), time.Now(), nil)
		WaitOverflowCleanup()
	}
	if m, _ := s.readOverflows(); len(m) != 0 {
		t.Errorf("the path was kept after %d failures: %v", maxOverflowFailures, m)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], p) {
		t.Errorf("logged = %q, want one line naming %s", logged, p)
	}
}

// A path whose removal is stuck is not asked about again, so the goroutines do not
// pile up on a hung share.
func TestAStuckRemovalIsNotStartedAgain(t *testing.T) {
	inflightMu.Lock()
	inflight["/x/flockdeck/baton-20260101-000000-abcdef.md"] = true
	inflightMu.Unlock()
	t.Cleanup(func() {
		inflightMu.Lock()
		delete(inflight, "/x/flockdeck/baton-20260101-000000-abcdef.md")
		inflightMu.Unlock()
	})
	if removeOverflow("/x/flockdeck/baton-20260101-000000-abcdef.md", "20260101-000000-abcdef", time.Second) {
		t.Error("a removal that is stuck was reported done")
	}
}

// A layout of any size is searched for the batons it names, and one is not skipped
// for being large.
func TestALargeLayoutIsStillSearchedForBatons(t *testing.T) {
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	id := oldBaton(t, s, 2)
	big := strings.Repeat("x", 3<<20) + `"baton":"` + id + `"` + strings.Repeat("y", 1<<20)
	if err := os.WriteFile(filepath.Join(root, "layout-big.json"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prune(RetainFor, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if !has(s, id) {
		t.Error("a baton a large layout names was removed")
	}
}

// An id cut by the boundary between two reads is still found.
func TestAnIDOnAReadBoundaryIsFound(t *testing.T) {
	id := "20260101-000000-abcdef"
	for _, at := range []int{(1 << 20) - 5, (1 << 20) - 20, (1 << 20) + 3} {
		data := strings.Repeat("z", at) + id + strings.Repeat("z", 100)
		refs := map[string]bool{}
		scanIDs(strings.NewReader(data), refs)
		if !refs[id] {
			t.Errorf("an id at %d was missed", at)
		}
	}
}
