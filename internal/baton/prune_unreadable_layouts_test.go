package baton

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func unreadableLayouts(t *testing.T) {
	t.Helper()
	old := openLayout
	openLayout = func(string) (io.ReadCloser, error) { return nil, errors.New("sharing violation") }
	t.Cleanup(func() { openLayout = old })
}

// A layout that can be read again clears the count.
func TestALayoutReadableAgainResetsTheUnreadableCount(t *testing.T) {
	quietCleanups(t)
	withClock(t)
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	oldBaton(t, s, 2)
	if err := os.WriteFile(filepath.Join(root, "layout-abc.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := openLayout
	openLayout = func(string) (io.ReadCloser, error) { return nil, errors.New("locked") }
	now := time.Now()
	_, _ = s.Prune(RetainFor, now, nil)
	openLayout = old
	if _, err := s.Prune(RetainFor, now.Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	if v := s.pruneKey("unreadable_since"); v != "" {
		t.Errorf("unreadable_since is still %q", v)
	}
	if s.pruneKey("seen") == "" {
		t.Error("the other keys of the record were lost")
	}
}

// A state folder that cannot be listed stops that run, and is said once a day.
func TestAStateFolderThatCannotBeListedStopsTheRunAndIsSaidOnceADay(t *testing.T) {
	quietCleanups(t)
	withClock(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	id := oldBaton(t, s, 2)
	old := readStateDir
	readStateDir = func(string) ([]os.DirEntry, error) { return nil, errors.New("access denied") }
	t.Cleanup(func() { readStateDir = old })
	n := 0
	SetLogf(func(string, ...any) { n++ })
	t.Cleanup(func() { SetLogf(func(string, ...any) {}) })
	now := time.Now()
	for i := 0; i < 5; i++ {
		if removed, err := s.Prune(RetainFor, now.Add(time.Duration(i)*time.Hour), nil); err == nil || len(removed) != 0 {
			t.Fatalf("run %d: removed %v, err %v", i, removed, err)
		}
	}
	if n != 1 {
		t.Errorf("logged %d times in five hours, want once", n)
	}
	if !has(s, id) {
		t.Error("a baton was removed")
	}
}

// Noting what pruning has seen does not drop the other entries of the record.
func TestNotingWhatPruningSawKeepsTheRecordsOtherEntries(t *testing.T) {
	quietCleanups(t)
	withClock(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	oldBaton(t, s, 2)
	s.setPruneKey("readdir_logged", "2026-01-01T00:00:00Z")
	if _, err := s.Prune(RetainFor, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if s.pruneKey("readdir_logged") == "" || s.pruneKey("seen") == "" {
		t.Errorf("the record now holds readdir_logged=%q seen=%q", s.pruneKey("readdir_logged"), s.pruneKey("seen"))
	}
}
