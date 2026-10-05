package baton

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// oldBaton saves a baton whose id and file are both older than the retention.
func oldBaton(t *testing.T, s *Store, day int) string {
	t.Helper()
	b := sample()
	b.ID = NewID(time.Date(2026, 1, day, 9, 0, 0, 0, time.UTC))
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	ageFile(t, s.Path(b.ID), 90*24*time.Hour)
	return b.ID
}

func has(s *Store, id string) bool {
	_, err := os.Stat(s.Path(id))
	return err == nil
}

// A layout that names a baton protects it, for any project, whether or not it is
// open, and a damaged one kept under another name does too.
func TestPruneKeepsWhatAnySavedLayoutNames(t *testing.T) {
	quietCleanups(t)
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	named, damaged, plain := oldBaton(t, s, 2), oldBaton(t, s, 3), oldBaton(t, s, 4)
	if err := os.WriteFile(filepath.Join(root, "layout-abc123.json"), []byte(`{"tabs":[{"pane":{"baton":"`+named+`"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "layout-def456.json.damaged"), []byte(`not json `+damaged), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prune(RetainFor, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if !has(s, named) || !has(s, damaged) {
		t.Error("a baton a saved layout names was removed")
	}
	if has(s, plain) {
		t.Error("a baton nothing names was kept")
	}
}

// A pane another Flockdeck has just started from a baton, not yet in a saved
// layout, protects it for an hour and no longer.
func TestPruneKeepsABatonAPaneWasJustStartedFrom(t *testing.T) {
	quietCleanups(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	fresh, stale := oldBaton(t, s, 2), oldBaton(t, s, 3)
	s.MarkInUse(fresh)
	s.MarkInUse(stale)
	ageFile(t, filepath.Join(s.dir, inuseDir, stale), 3*time.Hour)
	if _, err := s.Prune(RetainFor, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if !has(s, fresh) {
		t.Error("a baton a pane was started from a minute ago was removed")
	}
	if has(s, stale) {
		t.Error("a mark from three hours ago still protected a baton")
	}
}

func TestPruneDoesNothingWhenTheClockWentBackward(t *testing.T) {
	quietCleanups(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	if _, err := s.Prune(RetainFor, time.Now().Add(-48*time.Hour), nil); err != nil && !errors.Is(err, ErrClock) {
		t.Fatal(err)
	}
	// The record now says pruning has seen a later time than this run's.
	// (The first call above removed the baton, so make another.)
	id = oldBaton(t, s, 3)
	removed, err := s.Prune(RetainFor, time.Now().Add(-72*time.Hour), nil)
	if !errors.Is(err, ErrClock) || len(removed) != 0 || !has(s, id) {
		t.Errorf("a clock that went back three days: removed %v, err %v, baton kept %v", removed, err, has(s, id))
	}
}

func TestPruneDoesNothingWhenABatonIsDatedInTheFuture(t *testing.T) {
	quietCleanups(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	old := oldBaton(t, s, 2)
	b := sample()
	b.ID = NewID(time.Now())
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(72 * time.Hour)
	if err := os.Chtimes(s.Path(b.ID), when, when); err != nil {
		t.Fatal(err)
	}
	removed, err := s.Prune(RetainFor, time.Now(), nil)
	if !errors.Is(err, ErrClock) || len(removed) != 0 || !has(s, old) {
		t.Errorf("a baton dated in the future: removed %v, err %v", removed, err)
	}
}

// A machine that saves a baton less often than weekly still prunes: a long gap is
// not a clock that went wrong, and each run takes half of what is old.
func TestPruningGoesOnAfterLongGapsBetweenSaves(t *testing.T) {
	quietCleanups(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	var ids []string
	for d := 2; d <= 7; d++ {
		ids = append(ids, oldBaton(t, s, d))
	}
	now := time.Now()
	left := len(ids)
	for _, gap := range []int{8, 16, 24, 32, 40, 48} {
		removed, err := s.Prune(RetainFor, now.Add(time.Duration(gap)*24*time.Hour), nil)
		if err != nil {
			t.Fatalf("at +%dd: %v", gap, err)
		}
		left -= len(removed)
		if left == 0 {
			break
		}
		if len(removed) == 0 {
			t.Fatalf("at +%dd nothing was removed with %d old batons left", gap, left)
		}
	}
	if left != 0 {
		t.Errorf("%d old batons are left after six runs a week or more apart", left)
	}
}

// Marks that no longer protect anything are removed: old ones, and those of batons
// that are gone.
func TestPruneRemovesStartedFromMarksThatAreStale(t *testing.T) {
	quietCleanups(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	fresh := oldBaton(t, s, 2)
	stale := oldBaton(t, s, 3)
	s.MarkInUse(fresh)
	s.MarkInUse(stale)
	ageFile(t, filepath.Join(s.dir, inuseDir, stale), 3*time.Hour)
	s.MarkInUse("20260101-000000-abcdef") // a baton that is not stored
	if _, err := s.Prune(RetainFor, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	marks := func(id string) bool { _, err := os.Stat(filepath.Join(s.dir, inuseDir, id)); return err == nil }
	if !marks(fresh) {
		t.Error("a fresh mark was removed")
	}
	if marks(stale) || marks("20260101-000000-abcdef") {
		t.Error("a stale mark, or one for a baton that is not stored, was left")
	}
}

// At most half the batons go in one run, the oldest first.
func TestPruneRemovesAtMostHalfInOneRun(t *testing.T) {
	quietCleanups(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	var ids []string
	for d := 2; d <= 9; d++ {
		ids = append(ids, oldBaton(t, s, d))
	}
	removed, err := s.Prune(RetainFor, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 4 {
		t.Fatalf("removed %d of 8, want 4", len(removed))
	}
	for _, id := range ids[:4] {
		if has(s, id) {
			t.Errorf("one of the oldest, %s, was kept", id)
		}
	}
	for _, id := range ids[4:] {
		if !has(s, id) {
			t.Errorf("a newer one, %s, was removed in the first run", id)
		}
	}
}

// A baton restored from a backup has an old modified time. Its id says when it was
// made, and a record says when it was last used: either keeps it.
func TestPruneReadsTheIDAndTheUsedRecordNotOnlyTheModifiedTime(t *testing.T) {
	quietCleanups(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	restored := sample()
	restored.ID = NewID(time.Now().Add(-3 * 24 * time.Hour))
	if err := s.Save(restored); err != nil {
		t.Fatal(err)
	}
	ageFile(t, s.Path(restored.ID), 200*24*time.Hour)
	usedOld := oldBaton(t, s, 5)
	s.Touch(usedOld) // records the use, and the file's time with it
	ageFile(t, s.Path(usedOld), 200*24*time.Hour)
	gone := oldBaton(t, s, 6)
	if _, err := s.Prune(RetainFor, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if !has(s, restored.ID) {
		t.Error("a baton made three days ago was removed for its restored file's old time")
	}
	if !has(s, usedOld) {
		t.Error("a baton used a moment ago was removed for its file's old time")
	}
	if has(s, gone) {
		t.Error("a baton nothing speaks for was kept")
	}
}

func TestTouchRecordsTheUse(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	s.Touch(id)
	m, ok := s.readTimes(usedName)
	if !ok || m[id] == "" {
		t.Errorf("used record = %v, %v", m, ok)
	}
	// A baton that is not stored is not recorded.
	s.Touch("20260101-000000-abcdef")
	if m, _ := s.readTimes(usedName); len(m) != 1 {
		t.Errorf("a missing baton was recorded: %v", m)
	}
}

// A damaged overflow record is kept beside and a new one started, so later
// overflow files are still noted; the run that finds one removes nothing (see
// TestADamagedUsedRecord...) and says what it did.
func TestADamagedOverflowRecordIsRepairedNotLeftToFailEveryTime(t *testing.T) {
	quietCleanups(t)
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, overflowsName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := oldBaton(t, s, 2)
	var logged []string
	SetLogf(func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })
	t.Cleanup(func() { SetLogf(func(format string, args ...any) {}) })
	removed, err := s.Prune(RetainFor, time.Now(), nil)
	WaitOverflowCleanup()
	if err != nil || len(removed) != 0 || len(logged) == 0 {
		t.Errorf("Prune with a damaged record: removed %v, err %v, logged %q, want nothing removed in the run that finds it, and the trouble logged", removed, err, logged)
	}
	p := filepath.Join(root, "repo", ".git", "flockdeck", "baton-"+id+".md")
	if err := s.NoteOverflow(id, p); err != nil {
		t.Fatalf("NoteOverflow with a damaged record: %v", err)
	}
	if m, ok := s.readOverflows(); !ok || len(m[id]) != 1 {
		t.Errorf("the record was not started again: %v %v", m, ok)
	}
	if _, err := os.Stat(filepath.Join(s.dir, overflowsName+".bad")); err != nil {
		t.Errorf("the damaged record was not kept: %v", err)
	}
}

// The lock the records share is not held while the files in checkouts are looked
// at, since one on a share that has stopped answering would hold every save.
func TestPruneDoesNotHoldTheRecordsLockWhileItLooksAtOverflowFiles(t *testing.T) {
	quietCleanups(t)
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	id := oldBaton(t, s, 2)
	p := filepath.Join(root, "repo", ".git", "flockdeck", "baton-"+id+".md")
	if err := WriteOverflow(p, "text"); err != nil {
		t.Fatal(err)
	}
	if err := s.NoteOverflow(id, p); err != nil {
		t.Fatal(err)
	}
	held := true
	old := overflowRemove
	overflowRemove = func(path, id string, within time.Duration) bool {
		if sourcesMu.TryLock() {
			held = false
			sourcesMu.Unlock()
		}
		return old(path, id, within)
	}
	t.Cleanup(func() { overflowRemove = old })
	if _, err := s.Prune(RetainFor, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	WaitOverflowCleanup()
	if held {
		t.Error("the records lock was held while an overflow file was looked at")
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("the overflow file was not removed")
	}
}
