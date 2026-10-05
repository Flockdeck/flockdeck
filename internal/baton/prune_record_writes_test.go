package baton

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The folder goes between making it and the mark in it: the second try makes the mark.
// A version that tries once leaves no mark.
func TestAMarkIsMadeWhenTheFolderGoesBetweenMakingItAndTheFile(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	calls := 0
	old := markBetween
	markBetween = func() {
		calls++
		if calls == 1 {
			_ = os.RemoveAll(filepath.Join(s.dir, inuseDir))
		}
	}
	t.Cleanup(func() { markBetween = old })
	s.MarkInUse(id)
	if _, err := os.Stat(filepath.Join(s.dir, inuseDir, id)); err != nil {
		t.Errorf("no mark after the folder went between the two steps: %v (steps run %d times)", err, calls)
	}
}

// A mark that cannot be made is said once, not every time.
func TestAMarkThatCannotBeMadeIsSaidOnce(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	// A file where the folder should be.
	if err := os.WriteFile(filepath.Join(s.dir, inuseDir), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	markFailedDirs.Delete(s.dir)
	var logged []string
	SetLogf(func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	t.Cleanup(func() { SetLogf(func(string, ...any) {}) })
	for i := 0; i < 4; i++ {
		s.MarkInUse(id)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "could not mark") {
		t.Errorf("logged %q, want one line", logged)
	}
	// Another state folder has its own.
	other := NewStore(filepath.Join(t.TempDir(), "batons"))
	id2 := oldBaton(t, other, 2)
	if err := os.WriteFile(filepath.Join(other.dir, inuseDir), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	markFailedDirs.Delete(other.dir)
	other.MarkInUse(id2)
	other.MarkInUse(id2)
	if len(logged) != 2 {
		t.Errorf("logged %d lines for two folders, want 2: %q", len(logged), logged)
	}
}

// A record that is being written in place when it is asked for is given a moment and read
// again, and not called damaged.
func TestARecordCaughtMidWriteIsReadAgainBeforeItIsCalledDamaged(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	path := filepath.Join(s.dir, pruneName)
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := recordRetryWait
	recordRetryWait = 150 * time.Millisecond
	t.Cleanup(func() { recordRetryWait = old })
	if err := os.WriteFile(path, []byte("{\"seen\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = os.WriteFile(path, []byte("{\"seen\": \"2026-01-01T00:00:00Z\"}"), 0o600)
	}()
	if got := s.pruneKey("seen"); got != "2026-01-01T00:00:00Z" {
		t.Errorf("a record that was being written was read as %q", got)
	}
	// Still damaged after the wait: called damaged.
	if err := os.WriteFile(path, []byte("{\"seen\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	recordRetryWait = time.Millisecond
	if _, ok := s.readTimes(pruneName); ok {
		t.Error("a record that stays damaged was called fine")
	}
}

// The wait for a record that is being written is not made with the records locked: another
// goroutine that wants them is not kept from them for it.
func TestTheWaitForARecordIsNotMadeUnderTheRecordsLock(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, pruneName), []byte("{\"seen\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	slept, held := 0, false
	old := recordSleep
	recordSleep = func(time.Duration) {
		slept++
		if !sourcesMu.TryLock() {
			held = true
			return
		}
		sourcesMu.Unlock()
	}
	t.Cleanup(func() { recordSleep = old })
	s.pruneKey("seen")
	if slept == 0 {
		t.Fatal("no wait was made for a record that could not be read")
	}
	if held {
		t.Error("the wait was made with the records locked")
	}
}

// When the record cannot be renamed into place or written in place, the whole copy is
// still beside it, not gone.
func TestAFailedInPlaceWriteLeavesTheWholeCopyBeside(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A folder where the record should be: neither a rename nor a write can replace it.
	if err := os.MkdirAll(filepath.Join(s.dir, "prune.json", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := renameFile
	renameFile = func(string, string) error { return os.ErrPermission }
	t.Cleanup(func() { renameFile = old })
	if err := s.writeJSON(pruneName, map[string]string{"seen": "2026-01-01T00:00:00Z"}); err == nil {
		t.Fatal("the write did not fail")
	}
	left, _ := filepath.Glob(filepath.Join(s.dir, ".record-*.tmp"))
	if len(left) != 1 {
		t.Fatalf("copies left beside the record: %v", left)
	}
	if data, _ := os.ReadFile(left[0]); !strings.Contains(string(data), "2026-01-01") {
		t.Errorf("the copy is %q", data)
	}
}
