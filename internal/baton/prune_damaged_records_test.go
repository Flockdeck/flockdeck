package baton

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A used.json that cannot be read must not make batons that were used a moment ago look
// unused: the run that finds it removes nothing, nothing is removed for a day after, and the
// record is kept aside and started again.
func TestADamagedUsedRecordStopsPruningForADay(t *testing.T) {
	withClock(t)
	recordSleep = func(time.Duration) {}
	t.Cleanup(func() { recordSleep = time.Sleep })
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	now := time.Now()
	for d := 1; d <= 8; d++ {
		id := oldBaton(t, s, d)
		s.Touch(id)
		ageFile(t, s.Path(id), 90*24*time.Hour) // as if restored from a backup: only used.json knows
	}
	if err := os.WriteFile(filepath.Join(s.dir, usedName), []byte("{\"x\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.Prune(RetainFor, now, nil); err != nil || len(removed) != 0 {
		t.Fatalf("the run that finds it: removed %v, err %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, usedName+".bad")); err != nil {
		t.Errorf("the damaged record was not kept aside: %v", err)
	}
	if removed, err := s.Prune(RetainFor, now.Add(time.Hour), nil); err != nil || len(removed) != 0 {
		t.Errorf("an hour later: removed %v, err %v", removed, err)
	}
	if removed, err := s.Prune(RetainFor, now.Add(23*time.Hour), nil); err != nil || len(removed) != 0 {
		t.Errorf("23 hours later: removed %v, err %v", removed, err)
	}
	if removed, _ := s.Prune(RetainFor, now.Add(26*time.Hour), nil); len(removed) == 0 {
		t.Error("nothing was removed once the day was over")
	}
}

// Using a baton repairs a damaged used.json instead of refusing to write it.
func TestTouchRepairsADamagedUsedRecord(t *testing.T) {
	recordSleep = func(time.Duration) {}
	t.Cleanup(func() { recordSleep = time.Sleep })
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	if err := os.WriteFile(filepath.Join(s.dir, usedName), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Touch(id)
	used, ok := s.readTimes(usedName)
	if !ok || used[id] == "" {
		t.Errorf("the record after a use: %v %v", used, ok)
	}
	if _, err := os.Stat(filepath.Join(s.dir, usedName+".bad")); err != nil {
		t.Errorf("the damaged record was not kept: %v", err)
	}
	if got := s.pruneKey("hold_until"); got == "" {
		t.Error("no hold on pruning was set after the record was lost")
	}
}

// A copy kept aside earlier is never written over.
func TestARecordKeptAsideEarlierIsNotWrittenOver(t *testing.T) {
	recordSleep = func(time.Duration) {}
	t.Cleanup(func() { recordSleep = time.Sleep })
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	for _, content := range []string{"first damage", "second damage"} {
		if err := os.WriteFile(filepath.Join(s.dir, usedName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		s.Touch(id)
	}
	a, _ := os.ReadFile(filepath.Join(s.dir, usedName+".bad"))
	b, _ := os.ReadFile(filepath.Join(s.dir, usedName+".bad.1"))
	if string(a) != "first damage" || string(b) != "second damage" {
		t.Errorf("kept aside: %q and %q", a, b)
	}
}

// A record that stays damaged costs the wait once, not on every call; and a call looks only
// at the record it is about to read.
func TestADamagedRecordCostsTheWaitOnceAndOnlyWhereItIsRead(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	slept := 0
	old := recordSleep
	recordSleep = func(time.Duration) { slept++ }
	t.Cleanup(func() { recordSleep = old })
	damagedSeen.Range(func(k, _ any) bool { damagedSeen.Delete(k); return true })
	// used.json damaged: asking for a source does not touch it.
	if err := os.WriteFile(filepath.Join(s.dir, usedName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for i := 0; i < 20; i++ {
		s.Source("20260101-000000-abcdef")
	}
	if slept != 0 || time.Since(start) > 50*time.Millisecond {
		t.Errorf("20 source lookups: %d waits, %s", slept, time.Since(start))
	}
	// sources.json damaged and staying so: one wait for twenty calls.
	if err := os.WriteFile(filepath.Join(s.dir, sourcesName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		s.Source("20260101-000000-abcdef")
	}
	if slept != 1 {
		t.Errorf("twenty calls with a record that stays damaged waited %d times", slept)
	}
}

// Five runs of Prune with a record that cannot be set aside (so it stays damaged) are quick.
func TestPruneWithARecordThatStaysDamagedIsQuick(t *testing.T) {
	withClock(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	oldBaton(t, s, 2)
	if err := os.WriteFile(filepath.Join(s.dir, usedName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRename := renameFile
	renameFile = func(string, string) error { return os.ErrPermission }
	t.Cleanup(func() { renameFile = oldRename })
	damagedSeen.Range(func(k, _ any) bool { damagedSeen.Delete(k); return true })
	start := time.Now()
	for i := 0; i < 5; i++ {
		if removed, err := s.Prune(RetainFor, time.Now(), nil); err != nil || len(removed) != 0 {
			t.Fatalf("run %d: removed %v, err %v", i, removed, err)
		}
	}
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Errorf("five runs took %s", d)
	}
}

// A record that is empty or cut short is read from the newest complete copy a write left
// beside it, and the sweep keeps that copy; once the record is whole the copy is swept.
func TestACompleteCopyBesideADamagedRecordIsReadAndKept(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, pruneName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cp := filepath.Join(s.dir, ".record-"+pruneName+"-123.tmp")
	if err := os.WriteFile(cp, []byte("{\"seen\": \"2026-01-01T00:00:00Z\"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	ageFile(t, cp, 3*time.Hour)
	if m, ok := s.readTimes(pruneName); !ok || m["seen"] != "2026-01-01T00:00:00Z" {
		t.Errorf("read %v %v", m, ok)
	}
	s.sweepTemps(time.Now())
	if _, err := os.Stat(cp); err != nil {
		t.Errorf("the only complete copy was swept: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, pruneName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.sweepTemps(time.Now())
	if _, err := os.Stat(cp); err == nil {
		t.Error("the copy was kept after the record was whole")
	}
}

// A write that fails partway and had nothing before it leaves no cut-short file.
func TestAFailedFirstWriteLeavesNoPartialFile(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldRename, oldWrite := renameFile, writeInPlace
	renameFile = func(string, string) error { return os.ErrPermission }
	writeInPlace = func(path string, data []byte, perm os.FileMode) error {
		_ = os.WriteFile(path, data[:min(5, len(data))], perm)
		return os.ErrDeadlineExceeded
	}
	t.Cleanup(func() { renameFile, writeInPlace = oldRename, oldWrite })
	if err := s.writeJSON(usedName, map[string]string{"a": strings.Repeat("b", 40)}); err == nil {
		t.Fatal("the write did not fail")
	}
	if data, err := os.ReadFile(filepath.Join(s.dir, usedName)); err == nil && !bytes.Equal(data, nil) {
		t.Errorf("a partial record was left: %q", data)
	}
}
