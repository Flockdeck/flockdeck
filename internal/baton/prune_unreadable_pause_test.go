package baton

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A record that cannot be read (a folder in its place) stops pruning for thirty days, said in
// the log, and then pruning goes on with it taken as empty: nothing is removed inside the
// retention window, and removals resume.
func TestAnUnreadableRecordStopsPruningForThirtyDaysThenItGoesOn(t *testing.T) {
	withClock(t)
	recordSleep = func(time.Duration) {}
	t.Cleanup(func() { recordSleep = time.Sleep })
	logged := capture(t)
	oldTouch := touchNow
	t.Cleanup(func() { touchNow = oldTouch })
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	start := time.Now()
	day := 24 * time.Hour
	used := map[string]time.Time{}
	var all []string
	removedWhileBlocked, removedAfter, wrongly := 0, 0, 0
	for d := 0; d < 330; d++ {
		now := start.Add(time.Duration(d) * day)
		if d%7 == 0 {
			b := sample()
			b.ID = NewID(now)
			if err := s.Save(b); err != nil {
				t.Fatal(err)
			}
			os.Chtimes(s.Path(b.ID), now, now)
			all = append(all, b.ID)
			used[b.ID] = now
		}
		if d == 40 {
			// the record is replaced by a folder
			_ = os.Remove(filepath.Join(s.dir, usedName))
			if err := os.MkdirAll(filepath.Join(s.dir, usedName), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if d%11 == 0 && len(all) > 0 {
			id := all[(d/11)%len(all)]
			touchNow = func() time.Time { return now }
			s.Touch(id)
			if has(s, id) {
				used[id] = now // a use counts from the day it was made, record or not
			}
		}
		removed, err := s.Prune(RetainFor, now, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range removed {
			if now.Sub(used[id]) < RetainFor-day {
				wrongly++
			}
			if d >= 40 && d < 70 {
				removedWhileBlocked++
			} else if d >= 70 {
				removedAfter++
			}
		}
	}
	if removedWhileBlocked != 0 {
		t.Errorf("%d batons were removed in the thirty days after the record became unreadable", removedWhileBlocked)
	}
	if removedAfter == 0 {
		t.Error("pruning never went on")
	}
	if wrongly != 0 {
		t.Errorf("%d batons were removed inside the retention window", wrongly)
	}
	said := 0
	for _, l := range *logged {
		if strings.Contains(l, "could not be read, so no baton") || strings.Contains(l, "unreadable for over") {
			said++
		}
	}
	if said == 0 || said > 400 {
		t.Errorf("said %d times", said)
	}
	if fi, err := os.Stat(filepath.Join(s.dir, usedName)); err != nil || !fi.IsDir() {
		t.Error("the folder in the place of the record was moved or removed")
	}
}

// Using a baton leaves a record that cannot be read where it is.
func TestTouchLeavesARecordThatCannotBeRead(t *testing.T) {
	recordSleep = func(time.Duration) {}
	t.Cleanup(func() { recordSleep = time.Sleep })
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	if err := os.MkdirAll(filepath.Join(s.dir, usedName), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Touch(id)
	if fi, err := os.Stat(filepath.Join(s.dir, usedName)); err != nil || !fi.IsDir() {
		t.Errorf("the record that could not be read was moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, usedName+".bad")); err == nil {
		t.Error("it was kept aside")
	}
}

// A record that is valid JSON of the wrong shape has the copy beside it kept: the sweep and
// the read agree on what a record is.
func TestTheOnlyGoodCopyOfARecordOfTheWrongShapeIsNotSwept(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, usedName), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	cp := filepath.Join(s.dir, ".record-"+usedName+"-5.tmp")
	if err := os.WriteFile(cp, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	ageFile(t, cp, 3*time.Hour)
	s.sweepTemps(time.Now())
	if _, err := os.Stat(cp); err != nil {
		t.Errorf("the only good copy was swept: %v", err)
	}
}
