package baton

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// capture sends the log to a slice for the test.
func capture(t *testing.T) *[]string {
	t.Helper()
	var logged []string
	SetLogf(func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })
	t.Cleanup(func() { SetLogf(func(string, ...any) {}) })
	return &logged
}

// One layout that can never be read must not stop pruning for ever. The file here is really
// unreadable (held open with no sharing on Windows, permissions taken off elsewhere), not
// replaced by a function that says so. It stops pruning for thirty days and after that the
// rest is pruned, with the batons only that layout names no longer protected.
func TestALayoutThatStaysUnreadableStopsPruningForThirtyDaysOnly(t *testing.T) {
	withClock(t)
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	var ids []string
	for d := 1; d <= 4; d++ {
		ids = append(ids, oldBaton(t, s, d))
	}
	layout := filepath.Join(root, "layout-abc.json")
	if err := os.WriteFile(layout, []byte(`{"pane":{"baton":"`+ids[0]+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	makeUnreadable(t, layout)
	now := time.Now()
	if removed, err := s.Prune(RetainFor, now, nil); err == nil || len(removed) != 0 {
		t.Fatalf("day 0: removed %v, err %v", removed, err)
	}
	if removed, err := s.Prune(RetainFor, now.Add(29*24*time.Hour), nil); err == nil || len(removed) != 0 {
		t.Fatalf("day 29: removed %v, err %v", removed, err)
	}
	removed, err := s.Prune(RetainFor, now.Add(31*24*time.Hour), nil)
	if err != nil || len(removed) == 0 {
		t.Fatalf("day 31: removed %v, err %v", removed, err)
	}
	// The oldest first, half of them: the one that only the unreadable layout names is the
	// oldest here, and is not protected any more, since nothing can read what it names.
	if has(s, ids[0]) {
		t.Error("the baton that only the unreadable layout names was kept: it should be prunable now, and the help says so")
	}
}

// A baton a layout names is wanted however old it looks, and does not make the clock look
// wrong. Two batons: one named and years old, one old; if the named one were counted as
// looking too old, half of them would, and the run would not be written down.
func TestAReferencedSixYearOldBatonDoesNotMakeTheClockLookWrong(t *testing.T) {
	withClock(t)
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	b := sample()
	b.ID = NewID(time.Date(2016, 1, 1, 9, 0, 0, 0, time.UTC))
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	ageFile(t, s.Path(b.ID), 8*365*24*time.Hour)
	other := oldBaton(t, s, 2)
	if err := os.WriteFile(filepath.Join(root, "layout-abc.json"), []byte(`{"pane":{"baton":"`+b.ID+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	logged := capture(t)
	removed, err := s.Prune(RetainFor, time.Now(), nil)
	if err != nil || len(removed) != 1 || removed[0] != other {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	if !has(s, b.ID) {
		t.Error("the referenced baton was removed")
	}
	if s.pruneKey("seen") == "" {
		t.Error("the run was not written down: the clock was taken to be wrong because of a baton a layout names")
	}
	if strings.Contains(strings.Join(*logged, " "), "five years") {
		t.Errorf("logged %q", *logged)
	}
}

// A baton that looks over five years old and is named by nothing is skipped by itself:
// the others are pruned, nothing returns ErrClock for it, and it is said once a day.
func TestAnAbsurdlyOldBatonIsLeftAloneAndTheOthersArePruned(t *testing.T) {
	withClock(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	b := sample()
	b.ID = NewID(time.Date(2016, 1, 1, 9, 0, 0, 0, time.UTC))
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	ageFile(t, s.Path(b.ID), 8*365*24*time.Hour)
	oldBaton(t, s, 2)
	oldBaton(t, s, 3)
	logged := capture(t)
	now := time.Now()
	removed, err := s.Prune(RetainFor, now, nil)
	if err != nil || len(removed) != 1 {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	if !has(s, b.ID) {
		t.Error("the baton that looks over five years old was removed")
	}
	if _, err := s.Prune(RetainFor, now.Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range *logged {
		if strings.Contains(l, "five years") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("said %d times in two runs, want once: %q", n, *logged)
	}
}

// A prune record that is empty or damaged is started again, not a reason to stop for good:
// it is kept as prune.json.bad, pruning goes on from the next run.
func TestADamagedOrEmptyPruneRecordIsStartedAgain(t *testing.T) {
	for name, content := range map[string]string{"empty": "", "damaged": "{\"seen\": "} {
		t.Run(name, func(t *testing.T) {
			withClock(t)
			s := NewStore(filepath.Join(t.TempDir(), "batons"))
			id := oldBaton(t, s, 2)
			if err := os.WriteFile(filepath.Join(s.dir, pruneName), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			recordSleep = func(time.Duration) {}
			t.Cleanup(func() { recordSleep = time.Sleep })
			now := time.Now()
			if removed, err := s.Prune(RetainFor, now, nil); err != nil || len(removed) != 0 {
				t.Fatalf("the run that found it: removed %v, err %v", removed, err)
			}
			if _, err := os.Stat(filepath.Join(s.dir, pruneName+".bad")); err != nil {
				t.Errorf("the old record was not kept: %v", err)
			}
			if s.pruneKey("seen") == "" {
				t.Error("a new record was not written")
			}
			removed, err := s.Prune(RetainFor, now.Add(time.Hour), nil)
			if err != nil || len(removed) != 1 || removed[0] != id {
				t.Errorf("the next run: removed %v, err %v", removed, err)
			}
		})
	}
}

// A write in place that fails part of the way leaves the record as it was, not cut short.
func TestAFailedInPlaceWriteRestoresWhatWasThere(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.writeJSON(pruneName, map[string]string{"seen": "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	oldRename, oldWrite := renameFile, writeInPlace
	renameFile = func(string, string) error { return os.ErrPermission }
	writeInPlace = func(path string, data []byte, perm os.FileMode) error {
		if bytes.Contains(data, []byte("2027")) {
			// the new record: cut short, and then the disk says no
			_ = os.WriteFile(path, data[:5], perm)
			return os.ErrDeadlineExceeded
		}
		return os.WriteFile(path, data, perm)
	}
	t.Cleanup(func() { renameFile, writeInPlace = oldRename, oldWrite })
	if err := s.writeJSON(pruneName, map[string]string{"seen": "2027-01-01T00:00:00Z"}); err == nil {
		t.Fatal("the write did not fail")
	}
	data, _ := os.ReadFile(filepath.Join(s.dir, pruneName))
	if !strings.Contains(string(data), "2026-01-01") || !strings.HasSuffix(strings.TrimSpace(string(data)), "}") {
		t.Errorf("the record is %q, want what was there, whole", data)
	}
}

// When half of the batons that nothing names look over five years old the clock is the
// likelier fault: the run still prunes what it can, and is not written down.
func TestWhenHalfTheBatonsLookAbsurdlyOldTheRunIsNotWrittenDown(t *testing.T) {
	withClock(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	b := sample()
	b.ID = NewID(time.Date(2016, 1, 1, 9, 0, 0, 0, time.UTC))
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	ageFile(t, s.Path(b.ID), 8*365*24*time.Hour)
	oldBaton(t, s, 2)
	if _, err := s.Prune(RetainFor, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if got := s.pruneKey("seen"); got != "" {
		t.Errorf("the run was written down as %q", got)
	}
}
