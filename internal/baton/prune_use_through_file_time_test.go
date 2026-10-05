package baton

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// simDay is the time of day d of a simulated run that starts at start.
func simDay(start time.Time, d int) time.Time { return start.Add(time.Duration(d) * 24 * time.Hour) }

// A baton used while the record of uses cannot be read is not forgotten: the use is on the
// file's time, which the age rule reads. Created on day 0, the record a folder from day 10,
// used on day 39, readable again on day 50: it is not removed before day 69 (39 + 30).
func TestABatonUsedWhileTheUsedRecordWasUnreadableIsNotPrunedForThirtyDaysAfter(t *testing.T) {
	withClock(t)
	recordSleep = func(time.Duration) {}
	t.Cleanup(func() { recordSleep = time.Sleep })
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	start := time.Now().Add(-100 * 24 * time.Hour)
	b := sample()
	b.ID = NewID(start)
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(s.Path(b.ID), start, start); err != nil {
		t.Fatal(err)
	}
	oldTouch := touchNow
	t.Cleanup(func() { touchNow = oldTouch })
	removedOn := -1
	for d := 0; d <= 90 && removedOn < 0; d++ {
		now := simDay(start, d)
		switch d {
		case 10:
			if err := os.MkdirAll(filepath.Join(s.dir, usedName), 0o700); err != nil {
				t.Fatal(err)
			}
		case 39:
			touchNow = func() time.Time { return now }
			s.Touch(b.ID)
		case 50:
			if err := os.Remove(filepath.Join(s.dir, usedName)); err != nil {
				t.Fatal(err)
			}
		}
		removed, err := s.Prune(RetainFor, now, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(removed) > 0 {
			removedOn = d
		}
	}
	if removedOn < 69 {
		t.Errorf("the baton, used on day 39, was removed on day %d, want day 69 or later", removedOn)
	}
	if removedOn < 0 {
		t.Error("the baton was never removed")
	}
}

// A record's own time of being unreadable is forgotten as soon as that record reads fine, even
// while another stays unreadable: when it fails again its thirty days start again.
func TestARecordsUnreadableTimeIsForgottenWhenItReadsFineWhateverTheOthersDo(t *testing.T) {
	withClock(t)
	recordSleep = func(time.Duration) {}
	t.Cleanup(func() { recordSleep = time.Sleep })
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	start := time.Now().Add(-100 * 24 * time.Hour)
	// A baton that becomes old enough to remove on day 35, and no earlier.
	b := sample()
	b.ID = NewID(simDay(start, 5))
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(s.Path(b.ID), simDay(start, 5), simDay(start, 5)); err != nil {
		t.Fatal(err)
	}
	a, bRec := filepath.Join(s.dir, usedName), filepath.Join(s.dir, overflowsName)
	var removedOn = -1
	for d := 1; d <= 36 && removedOn < 0; d++ {
		now := simDay(start, d)
		switch d {
		case 1:
			for _, p := range []string{a, bRec} {
				if err := os.MkdirAll(p, 0o700); err != nil {
					t.Fatal(err)
				}
			}
		case 2:
			if err := os.Remove(bRec); err != nil { // B reads fine again (it is not there)
				t.Fatal(err)
			}
		case 35:
			if err := os.MkdirAll(bRec, 0o700); err != nil { // B fails again
				t.Fatal(err)
			}
		}
		removed, err := s.Prune(RetainFor, now, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(removed) > 0 {
			removedOn = d
		}
	}
	if removedOn >= 0 {
		t.Errorf("the baton was removed on day %d, though a record that had read fine failed again that day", removedOn)
	}
	since, _ := time.Parse(time.RFC3339, s.pruneKey(unreadableKey(overflowsName)))
	if since.IsZero() || simDay(start, 35).Sub(since) > time.Hour {
		t.Errorf("the second record's time is %v, want day 35", since)
	}
}
