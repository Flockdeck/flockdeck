package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadGroupsRoundTrip checks a saved grouping reads back exactly as it
// was written, and that no groups ever recorded reads back as none rather
// than as an error -- the ordinary case for a user who has never grouped
// anything.
func TestLoadGroupsRoundTrip(t *testing.T) {
	isolateConfig(t)

	if got, err := LoadGroups(); err != nil || len(got) != 0 {
		t.Fatalf("LoadGroups with nothing saved = %v, %v; want none, no error", got, err)
	}

	want := []ProjectGroup{
		{ID: "g1", Name: "backend", Roots: []string{"/code/api", "/code/worker"}, Primary: "/code/api"},
		{ID: "g2", Roots: []string{"/code/site"}, Primary: "/code/site"},
	}
	if err := SaveGroups(want); err != nil {
		t.Fatalf("save groups: %v", err)
	}
	got, err := LoadGroups()
	if err != nil {
		t.Fatalf("load groups: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("loaded %d groups, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Name != want[i].Name || got[i].Primary != want[i].Primary {
			t.Errorf("group %d = %+v, want %+v", i, got[i], want[i])
		}
		if len(got[i].Roots) != len(want[i].Roots) {
			t.Errorf("group %d roots = %v, want %v", i, got[i].Roots, want[i].Roots)
		}
	}
}

// TestLoadGroupsDamagedFileIsKept checks a groups.json that does not parse
// is quarantined rather than silently dropped, and startup is not failed
// over it -- the same treatment session.json and projects.json get.
func TestLoadGroupsDamagedFileIsKept(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	path := filepath.Join(dir, groupsFile)
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write damaged file: %v", err)
	}

	got, err := LoadGroups()
	if err != nil {
		t.Fatalf("a damaged groups file should not fail startup: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v from a damaged file, want none", got)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("the damaged file should have been moved aside, not left in place")
	}
}

// TestLoadGroupsDropsEmptyEntries checks a group with no id or no members --
// which a hand-edited file could hold -- is dropped rather than handed back
// to a caller that assumes every group names something real.
func TestLoadGroupsDropsEmptyEntries(t *testing.T) {
	isolateConfig(t)
	if err := SaveGroups([]ProjectGroup{
		{ID: "", Roots: []string{"/code/api"}},
		{ID: "g1", Roots: nil},
		{ID: "g2", Roots: []string{"/code/web"}, Primary: "/code/web"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadGroups()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].ID != "g2" {
		t.Fatalf("got %v, want only g2", got)
	}
}
