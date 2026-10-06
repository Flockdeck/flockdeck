package baton

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStoreSaveLoadList(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "batons"))
	old, newer := sample(), sample()
	newer.ID = NewID(time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC))
	for _, b := range []Baton{old, newer} {
		if err := s.Save(b); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Load(old.ID)
	if err != nil || got.Title != old.Title {
		t.Fatalf("Load = %+v, %v", got, err)
	}
	list, err := s.List()
	if err != nil || len(list) != 2 || list[0].ID != newer.ID {
		t.Errorf("List = %v, %v; want newest first", list, err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(s.Dir())
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("folder mode = %v", fi.Mode().Perm())
		}
	}
}

func TestStoreNeverReplacesABaton(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Save(sample()); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(sample().Set(Goal, "other")); !errors.Is(err, ErrExists) {
		t.Errorf("second save = %v, want ErrExists", err)
	}
	got, _ := s.Load(sample().ID)
	if got.Section(Goal) == "other" {
		t.Error("the stored baton was replaced")
	}
}

func TestStoreRefusesNamesThatAreNotIDs(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Load(`..\..\secret`); !errors.Is(err, ErrNotFound) {
		t.Errorf("Load = %v", err)
	}
	if err := s.Save(Baton{ID: "../x"}); err == nil {
		t.Error("saved under a path")
	}
	if p := s.Path("../x"); p != "" {
		t.Errorf("Path = %q", p)
	}
}

func TestReadFileTakesFreeTextAsNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(path, []byte("# my notes\nfinish the thing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := ReadFile(path)
	if err != nil || b.Title != "notes" || b.Section(Standing) == "" {
		t.Errorf("ReadFile = %+v, %v", b, err)
	}
	if _, err := ReadFile(filepath.Join(t.TempDir(), "gone.md")); err == nil {
		t.Error("a missing file was read")
	}
}

// Where a baton came from is kept by the application, outside the baton's own
// text.
func TestStoreRecordsSourcesOutsideTheBatonFile(t *testing.T) {
	s := NewStore(t.TempDir())
	b := sample()
	b.FromAgent = "claimed-in-the-header"
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	if got := s.Source(b.ID); got != "" {
		t.Errorf("a baton with nothing recorded has source %q", got)
	}
	if err := s.SetSource(b.ID, "claude"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSource(NewID(b.Created), "codex"); err != nil {
		t.Fatal(err)
	}
	if got := s.Source(b.ID); got != "claude" {
		t.Errorf("Source = %q", got)
	}
	if err := s.SetSource("../x", "claude"); err == nil {
		t.Error("a source was recorded for a name that is not an id")
	}
	if got := s.Source("../x"); got != "" {
		t.Errorf("Source(../x) = %q", got)
	}
	// The sources file is not a baton, and the list does not show it.
	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Errorf("List = %v, %v", list, err)
	}
	// Loading does not take the header's word for it either.
	loaded, _ := s.Load(b.ID)
	if loaded.FromAgent != "claimed-in-the-header" || s.Source(b.ID) != "claude" {
		t.Errorf("header %q, recorded %q", loaded.FromAgent, s.Source(b.ID))
	}
}

// A sources file that cannot be read is kept, not silently written over.
func TestADamagedSourcesFileIsKeptBeforeItIsReplaced(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	damaged := "{ this is not json"
	if err := os.WriteFile(filepath.Join(s.Dir(), "sources.json"), []byte(damaged), 0o600); err != nil {
		t.Fatal(err)
	}
	b := sample()
	if err := s.SetSource(b.ID, "claude"); err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(filepath.Join(s.Dir(), "sources.json.bad"))
	if err != nil || string(kept) != damaged {
		t.Errorf("sources.json.bad = %q, %v", kept, err)
	}
	if got := s.Source(b.ID); got != "claude" {
		t.Errorf("Source = %q", got)
	}
}

// A second damage does not overwrite the copy of the first.
func TestRepeatedDamageKeepsEveryCopy(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir(), "sources.json")
	for i, text := range []string{"{ first damage", "{ second damage", "{ third damage"} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.SetSource(NewID(sample().Created.Add(time.Duration(i)*time.Second)), "claude"); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]string{"sources.json.bad": "{ first damage", "sources.json.bad.1": "{ second damage", "sources.json.bad.2": "{ third damage"} {
		got, err := os.ReadFile(filepath.Join(s.Dir(), name))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
}

// A sources file that is there and cannot be read is not replaced with one that
// says nothing of what it held.
func TestAnUnreadableSourcesFileIsLeftAlone(t *testing.T) {
	s := NewStore(t.TempDir())
	// A folder where the file should be cannot be read as a file.
	if err := os.MkdirAll(filepath.Join(s.Dir(), "sources.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSource(sample().ID, "claude"); err == nil {
		t.Fatal("the record succeeded over an unreadable file")
	}
	if fi, err := os.Stat(filepath.Join(s.Dir(), "sources.json")); err != nil || !fi.IsDir() {
		t.Errorf("the unreadable file was replaced: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "sources.json.bad")); err == nil {
		t.Error("a copy of a folder was kept as a damaged file")
	}
}
