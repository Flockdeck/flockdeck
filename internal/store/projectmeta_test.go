package store

import (
	"fmt"
	"path/filepath"
	"testing"
)

// TestSetProjectNameAndArchivedPersist covers the picker's own rename and
// archive actions: each changes one project's entry without disturbing the
// rest of the list, and an emptied name goes back to none rather than being
// kept as an empty string.
func TestSetProjectNameAndArchivedPersist(t *testing.T) {
	isolateConfig(t)
	base := t.TempDir()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	if err := TouchRecents(a, b); err != nil {
		t.Fatalf("touch: %v", err)
	}

	if err := SetProjectName(a, "  Renamed  "); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if err := SetProjectArchived(a, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	pa, ok := findProject(list, a)
	if !ok {
		t.Fatalf("project a not found in %+v", list)
	}
	// The name is trimmed, the way a name typed with stray whitespace around
	// it should be.
	if pa.Name != "Renamed" {
		t.Errorf("name = %q, want %q", pa.Name, "Renamed")
	}
	if !pa.Archived {
		t.Error("archived was not recorded")
	}
	pb, ok := findProject(list, b)
	if !ok {
		t.Fatalf("project b not found in %+v", list)
	}
	if pb.Name != "" || pb.Archived {
		t.Errorf("project b was disturbed: %+v", pb)
	}

	// An emptied name goes back to none, the way clearing a tab's name does,
	// rather than being kept as an empty string that would still count as
	// "named" to a caller checking Name != "".
	if err := SetProjectName(a, "   "); err != nil {
		t.Fatalf("clear name: %v", err)
	}
	if err := SetProjectArchived(a, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	list, err = Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	pa, ok = findProject(list, a)
	if !ok {
		t.Fatalf("project a not found in %+v", list)
	}
	if pa.Name != "" {
		t.Errorf("name after clearing = %q, want empty", pa.Name)
	}
	if pa.Archived {
		t.Error("still archived after unarchiving")
	}
}

// TestUpdateProjectCreatesAnEntryForAnUnknownRoot covers a project renamed or
// archived before its own TouchRecent has ever run -- a project changed the
// same moment it is opened for the first time. The change must not be
// silently lost for want of an entry to attach it to.
func TestUpdateProjectCreatesAnEntryForAnUnknownRoot(t *testing.T) {
	isolateConfig(t)
	root := filepath.Join(t.TempDir(), "fresh")

	if err := SetProjectName(root, "Fresh"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	p, ok := findProject(list, filepath.Clean(root))
	if !ok {
		t.Fatalf("no entry was created for %s in %+v", root, list)
	}
	if p.Name != "Fresh" {
		t.Errorf("name = %q, want %q", p.Name, "Fresh")
	}
}

// TestReorderProjectsOrdersTheGivenRootsLeavingOthersAlone covers the
// picker's move-up/move-down controls: reordering one section of the list
// (say, Archived) must not disturb the position already chosen for a
// project in another section that reorder call was not given.
func TestReorderProjectsOrdersTheGivenRootsLeavingOthersAlone(t *testing.T) {
	isolateConfig(t)
	base := t.TempDir()
	a, b, c, d := filepath.Join(base, "a"), filepath.Join(base, "b"), filepath.Join(base, "c"), filepath.Join(base, "d")
	if err := TouchRecents(a, b, c, d); err != nil {
		t.Fatalf("touch: %v", err)
	}

	// Only a and b are reordered; c and d were never given an order and
	// should keep sorting by LastUsed, after both of the two that were.
	if err := ReorderProjects([]string{b, a}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	var order []string
	for _, p := range list {
		order = append(order, filepath.Base(p.Root))
	}
	// b before a (as given), both before c and d, which fall back to
	// LastUsed: TouchRecents gave the first of its arguments the most
	// recent timestamp, so among the two left untouched by the reorder, c
	// (touched before d) sorts first.
	if got, want := fmt.Sprint(order), "[b a c d]"; got != want {
		t.Errorf("order = %s, want %s", got, want)
	}

	pb, _ := findProject(list, b)
	pa, _ := findProject(list, a)
	if pb.Order != 1 || pa.Order != 2 {
		t.Errorf("orders = b:%d a:%d, want b:1 a:2", pb.Order, pa.Order)
	}
	pc, _ := findProject(list, c)
	pd, _ := findProject(list, d)
	if pc.Order != 0 || pd.Order != 0 {
		t.Errorf("untouched projects were given an order: c:%d d:%d", pc.Order, pd.Order)
	}

	// Reordering again with a project left out keeps the place it already
	// had rather than losing it.
	if err := ReorderProjects([]string{a}); err != nil {
		t.Fatalf("reorder again: %v", err)
	}
	list, err = Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	pa, _ = findProject(list, a)
	if pa.Order != 1 {
		t.Errorf("a's order after being reordered alone = %d, want 1", pa.Order)
	}
	pb, _ = findProject(list, b)
	if pb.Order != 1 {
		t.Errorf("b's order, left out of the second call, changed to %d, want it to stay 1", pb.Order)
	}
}

// TestAllProjectMetaOmitsProjectsWithNothingChosen covers what the workspace
// caches from the recent list: only a project with a name, an archived flag
// or a position chosen by hand is worth a lookup answering, so the map stays
// small next to a recent list that can hold up to forty entries most of
// which nobody has touched.
func TestAllProjectMetaOmitsProjectsWithNothingChosen(t *testing.T) {
	isolateConfig(t)
	base := t.TempDir()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	if err := TouchRecents(a, b); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := SetProjectName(a, "A"); err != nil {
		t.Fatalf("set name: %v", err)
	}

	meta, err := AllProjectMeta()
	if err != nil {
		t.Fatalf("all project meta: %v", err)
	}
	if len(meta) != 1 {
		t.Fatalf("meta = %+v, want exactly the one project with something chosen", meta)
	}
	got, ok := meta[MetaKey(a)]
	if !ok {
		t.Fatalf("project a missing from %+v", meta)
	}
	if got.Name != "A" {
		t.Errorf("name = %q, want %q", got.Name, "A")
	}
	if _, ok := meta[MetaKey(b)]; ok {
		t.Errorf("project b, with nothing chosen, was cached anyway")
	}
}
