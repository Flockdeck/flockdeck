package baton

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ageFile(t *testing.T, path string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestPruneRemovesOldBatonsTheirSourcesAndOverflowFiles(t *testing.T) {
	quietCleanups(t)
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	oldB, keepB, usedB := sample(), sample(), sample()
	oldB.ID = NewID(time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC))
	keepB.ID = NewID(time.Date(2026, 1, 3, 9, 0, 0, 0, time.UTC))
	usedB.ID = NewID(time.Date(2026, 1, 4, 9, 0, 0, 0, time.UTC))
	overflow := map[string]string{}
	for _, b := range []Baton{oldB, keepB, usedB} {
		if err := s.Save(b); err != nil {
			t.Fatal(err)
		}
		if err := s.SetSource(b.ID, "claude"); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(root, "repo-"+b.ID, ".git", "flockdeck", "baton-"+b.ID+".md")
		if err := WriteOverflow(p, "the full text"); err != nil {
			t.Fatal(err)
		}
		if err := s.NoteOverflow(b.ID, p); err != nil {
			t.Fatal(err)
		}
		overflow[b.ID] = p
	}
	ageFile(t, s.Path(oldB.ID), 40*24*time.Hour)
	ageFile(t, s.Path(usedB.ID), 90*24*time.Hour)
	ageFile(t, s.Path(keepB.ID), 3*24*time.Hour)

	removed, err := s.Prune(RetainFor, time.Now(), func(id string) bool { return id == usedB.ID })
	WaitOverflowCleanup()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != oldB.ID {
		t.Fatalf("removed %v, want only %s", removed, oldB.ID)
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	if exists(s.Path(oldB.ID)) || exists(overflow[oldB.ID]) {
		t.Error("the old baton or its overflow file is still there")
	}
	for _, b := range []Baton{keepB, usedB} {
		if !exists(s.Path(b.ID)) || !exists(overflow[b.ID]) {
			t.Errorf("baton %s, which is recent or in use, lost a file", b.ID)
		}
	}
	if s.Source(oldB.ID) != "" || s.Source(keepB.ID) != "claude" || s.Source(usedB.ID) != "claude" {
		t.Errorf("sources after prune: %q %q %q", s.Source(oldB.ID), s.Source(keepB.ID), s.Source(usedB.ID))
	}
	m, _ := s.readOverflows()
	if _, ok := m[oldB.ID]; ok || len(m) != 2 {
		t.Errorf("overflow record = %v", m)
	}
}

func TestPruneLeavesWhatIsNotAnOverflowFile(t *testing.T) {
	quietCleanups(t)
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "batons"))
	b := sample()
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	// A recorded path that is somebody's file is not removed because it was
	// written into the record.
	precious := filepath.Join(root, "precious.txt")
	if err := os.WriteFile(precious, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.NoteOverflow(b.ID, precious); err != nil {
		t.Fatal(err)
	}
	ageFile(t, s.Path(b.ID), 60*24*time.Hour)
	if _, err := s.Prune(RetainFor, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	WaitOverflowCleanup()
	if _, err := os.Stat(precious); err != nil {
		t.Errorf("a file that is not an overflow file was removed: %v", err)
	}
}

func TestTouchKeepsABatonThatIsStillUsed(t *testing.T) {
	quietCleanups(t)
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	b := sample()
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	ageFile(t, s.Path(b.ID), 60*24*time.Hour)
	s.Touch(b.ID)
	if removed, _ := s.Prune(RetainFor, time.Now(), nil); len(removed) != 0 {
		t.Errorf("removed %v after the baton was used", removed)
	}
}

func TestPruneOnAMissingFolderIsNothing(t *testing.T) {
	quietCleanups(t)
	s := NewStore(filepath.Join(t.TempDir(), "none"))
	if removed, err := s.Prune(RetainFor, time.Now(), nil); err != nil || len(removed) != 0 {
		t.Errorf("Prune = %v, %v", removed, err)
	}
}
