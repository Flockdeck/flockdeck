package baton

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClock is a wall and a monotonic reading that a test moves by hand.
type fakeClock struct {
	wall time.Time
	mono time.Duration
}

func (c *fakeClock) sample() (time.Time, time.Duration) { return c.wall, c.mono }

// both moves the wall clock and the monotonic one together: time passing.
func (c *fakeClock) both(d time.Duration) { c.wall, c.mono = c.wall.Add(d), c.mono+d }

// withClock puts a fake clock under the watch Prune looks at, and puts the real one back.
func withClock(t *testing.T) *fakeClock {
	t.Helper()
	c := &fakeClock{wall: time.Now().Round(0)}
	old := defaultWatch
	defaultWatch = &clockWatch{sample: c.sample}
	t.Cleanup(func() { defaultWatch = old })
	return c
}

// The real logic that decides a clock was set: the wall clock moved more than the
// monotonic one between two looks. A machine that slept and a clock that was set look
// the same, and the one look that sees either is the one that does nothing.
func TestTheClockWatchSeesAJumpBetweenTwoLooksAndThenStartsAgain(t *testing.T) {
	c := &fakeClock{wall: time.Now().Round(0)}
	w := &clockWatch{sample: c.sample}
	if w.jumped() {
		t.Error("the first look reported a jump")
	}
	c.both(10 * time.Minute)
	if w.jumped() {
		t.Error("ten minutes passing reported a jump")
	}
	// The monotonic clock stops in sleep: eight hours of wall, none of it.
	c.wall = c.wall.Add(8 * time.Hour)
	if !w.jumped() {
		t.Error("eight hours of sleep, or a clock set, was not reported")
	}
	// The baseline is taken again from that look, so it is reported once and not for ever.
	c.both(5 * time.Minute)
	if w.jumped() {
		t.Error("a jump was reported again after the baseline was taken again")
	}
	// A clock set a long way back.
	c.wall = c.wall.Add(-400 * 24 * time.Hour)
	if !w.jumped() {
		t.Error("a clock set back was not reported")
	}
	c.both(time.Minute)
	if w.jumped() {
		t.Error("still reported after a look")
	}
}

// A clock set while Flockdeck runs stops pruning for that run, through the real
// logic and a clock that is moved: not a stub of the detector.
func TestPruneDoesNothingWhenTheClockWasSetWhileRunning(t *testing.T) {
	c := withClock(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	oldBaton(t, s, 2)
	if removed, err := s.Prune(RetainFor, time.Now(), nil); err != nil || len(removed) != 1 {
		t.Fatalf("a normal run: removed %v, err %v", removed, err)
	}
	id := oldBaton(t, s, 3)
	c.wall = c.wall.Add(3 * time.Hour) // set forward between two runs
	removed, err := s.Prune(RetainFor, time.Now(), nil)
	if !errors.Is(err, ErrClock) || len(removed) != 0 || !has(s, id) {
		t.Errorf("after the clock was set: removed %v, err %v", removed, err)
	}
	// And the next run, the baseline taken again, prunes.
	c.both(time.Minute)
	if removed, err := s.Prune(RetainFor, time.Now(), nil); err != nil || len(removed) != 1 {
		t.Errorf("the run after: removed %v, err %v", removed, err)
	}
}

// A clock seventy years ahead, while Flockdeck was not running, must not wipe the
// store or poison the record of what pruning has seen.
func TestAClockSeventyYearsAheadWipesNothingAndPoisonsNothing(t *testing.T) {
	withClock(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	var ids []string
	for d := 1; d <= 20; d++ {
		ids = append(ids, oldBaton(t, s, d))
	}
	absurd := time.Now().Add(70 * 365 * 24 * time.Hour)
	for i := 0; i < 6; i++ {
		removed, err := s.Prune(RetainFor, absurd.Add(time.Duration(i)*24*time.Hour), nil)
		if !errors.Is(err, ErrClock) || len(removed) != 0 {
			t.Fatalf("run %d with the clock 70 years ahead: removed %v, err %v", i, removed, err)
		}
	}
	for _, id := range ids {
		if !has(s, id) {
			t.Fatalf("baton %s was removed by a clock 70 years ahead", id)
		}
	}
	// Nothing was written down from those runs, so the clock being put right prunes.
	removed, err := s.Prune(RetainFor, time.Now(), nil)
	if err != nil || len(removed) != 10 {
		t.Errorf("after the clock was put right: removed %d, err %v, want half of the 20", len(removed), err)
	}
}

// A time written down that is ahead of now by more than a day is not believed.
func TestARecordedTimeInTheFutureDoesNotStopPruningForEver(t *testing.T) {
	withClock(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	if err := s.writeJSON(pruneName, map[string]string{"seen": time.Now().Add(10 * 365 * 24 * time.Hour).UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	removed, err := s.Prune(RetainFor, time.Now(), nil)
	if err != nil || len(removed) != 1 || has(s, id) {
		t.Errorf("removed %v, err %v", removed, err)
	}
	m, _ := s.readTimes(pruneName)
	if seen, _ := time.Parse(time.RFC3339, m["seen"]); seen.After(time.Now().Add(time.Hour)) {
		t.Errorf("the recorded time was not put right: %v", m)
	}
}

// With fewer than four batons, one goes in a run; with more, half.
func TestPruneTakesOneAtATimeFromAFewAndHalfFromMany(t *testing.T) {
	withClock(t)
	for _, tc := range []struct{ n, first int }{{1, 1}, {2, 1}, {3, 1}, {4, 2}, {5, 2}, {10, 5}} {
		s := NewStore(filepath.Join(t.TempDir(), "batons"))
		for d := 1; d <= tc.n; d++ {
			oldBaton(t, s, d)
		}
		removed, err := s.Prune(RetainFor, time.Now(), nil)
		if err != nil || len(removed) != tc.first {
			t.Errorf("%d batons: removed %d (err %v), want %d", tc.n, len(removed), err, tc.first)
		}
	}
}

// The age is also checked against the folder's own files: a clock a year or more past
// everything in it is wrong, whatever the batons say.
func TestAClockMoreThanAYearPastTheNewestFileIsNotTrusted(t *testing.T) {
	withClock(t)
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	id := oldBaton(t, s, 2)
	recent := filepath.Join(root, "layout-abc.json")
	if err := os.WriteFile(recent, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Everything in the folder is two years old except what a running Flockdeck writes.
	long := time.Now().Add(-800 * 24 * time.Hour)
	_ = os.Chtimes(recent, long, long)
	_ = os.Chtimes(s.Path(id), long, long)
	removed, err := s.Prune(RetainFor, time.Now(), nil)
	if !errors.Is(err, ErrClock) || len(removed) != 0 {
		t.Errorf("removed %v, err %v", removed, err)
	}
}

// A rename the system refuses is tried again, and then the record is written in place.
func TestARecordIsWrittenInPlaceWhenTheRenameIsRefusedForGood(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := renameFile
	t.Cleanup(func() { renameFile = old })

	tries := 0
	renameFile = func(a, b string) error {
		tries++
		if tries < 3 {
			return errors.New("Access is denied")
		}
		return old(a, b)
	}
	if err := s.writeJSON(usedName, map[string]string{"a": "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatalf("a rename that works the third time: %v", err)
	}
	if tries != 3 {
		t.Errorf("renamed %d times, want 3", tries)
	}

	tries = 0
	renameFile = func(a, b string) error { tries++; return errors.New("Access is denied") }
	if err := s.writeJSON(usedName, map[string]string{"b": "2026-01-02T00:00:00Z"}); err != nil {
		t.Fatalf("a rename that never works: %v", err)
	}
	if tries != 5 {
		t.Errorf("tried %d times, want 5", tries)
	}
	m, _ := s.readTimes(usedName)
	if m["b"] != "2026-01-02T00:00:00Z" {
		t.Errorf("the record was not written in place: %v", m)
	}
	if matches, _ := filepath.Glob(filepath.Join(s.dir, ".record-*.tmp")); len(matches) != 0 {
		t.Errorf("a temporary file was left: %v", matches)
	}
}

func TestPruneSweepsStaleTemporaryRecords(t *testing.T) {
	withClock(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	oldBaton(t, s, 2)
	stale, fresh := filepath.Join(s.dir, ".record-123.tmp"), filepath.Join(s.dir, ".record-456.tmp")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ageFile(t, stale, 3*time.Hour)
	if _, err := s.Prune(RetainFor, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("a stale temporary file was left")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a fresh temporary file, which may be in use, was removed")
	}
}

// Marking a baton as in use survives the folder going between making it and the file,
// and sweeping never removes the folder.
func TestMarkInUseSurvivesTheFolderBeingRemovedAndSweepKeepsIt(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	s.MarkInUse(id)
	if err := os.RemoveAll(filepath.Join(s.dir, inuseDir)); err != nil {
		t.Fatal(err)
	}
	s.MarkInUse(id)
	if _, err := os.Stat(filepath.Join(s.dir, inuseDir, id)); err != nil {
		t.Errorf("the mark was not made after the folder went: %v", err)
	}
	// Everything stale and swept: the folder is still there for the next mark.
	ageFile(t, filepath.Join(s.dir, inuseDir, id), 5*time.Hour)
	s.sweepMarks(time.Now())
	if _, err := os.Stat(filepath.Join(s.dir, inuseDir)); err != nil {
		t.Errorf("sweeping removed the folder: %v", err)
	}
}

// A layout that cannot be read may name a baton: nothing is pruned that run.
func TestAnUnreadableLayoutStopsPruningThatRun(t *testing.T) {
	withClock(t)
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	id := oldBaton(t, s, 2)
	if err := os.WriteFile(filepath.Join(root, "layout-abc.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := openLayout
	openLayout = func(string) (io.ReadCloser, error) { return nil, errors.New("sharing violation") }
	t.Cleanup(func() { openLayout = old })
	var logged []string
	SetLogf(func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })
	t.Cleanup(func() { SetLogf(func(string, ...any) {}) })
	removed, err := s.Prune(RetainFor, time.Now(), nil)
	if err == nil || len(removed) != 0 || !has(s, id) {
		t.Errorf("removed %v, err %v", removed, err)
	}
	if len(logged) == 0 || !strings.Contains(strings.Join(logged, " "), "could not be read") {
		t.Errorf("not logged: %q", logged)
	}
}

// A path noted while the cleanup was looking at files is not lost when it writes the
// record back.
func TestAPathNotedDuringACleanupIsNotLost(t *testing.T) {
	withClock(t)
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	id := oldBaton(t, s, 2)
	first := overflowFor(t, root, s, id)
	second := filepath.Join(root, "other", ".git", "flockdeck", "baton-"+id+".md")
	old := overflowRemove
	overflowRemove = func(path, id string, within time.Duration) bool {
		// Another save notes a second file for the same baton as this one is removed.
		_ = s.NoteOverflow(id, second)
		return old(path, id, within)
	}
	t.Cleanup(func() { overflowRemove = old })
	if _, err := s.Prune(RetainFor, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	WaitOverflowCleanup()
	m, _ := s.readOverflows()
	if len(m[id]) != 1 || m[id][0] != second {
		t.Errorf("the record is %v, want only the path noted meanwhile", m)
	}
	if _, err := os.Stat(first); err == nil {
		t.Error("the first overflow file was not removed")
	}
}
